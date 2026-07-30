package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"onsim/internal/app"
	"onsim/internal/auth"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/modem"
	"onsim/internal/store"
)

type Bot struct {
	state           *store.State
	app             *app.Service
	auth            *auth.Manager
	publicURL       string
	log             *slog.Logger
	client          *http.Client
	apiBase         string
	mu              sync.Mutex
	notificationMu  sync.Mutex
	pending         map[string]pendingAction
	replies         map[int64]replyPrompt
	configuredToken string
}

type pendingAction struct {
	Kind, Number, Body string
	Route              modem.Route
	RouteLabel         string
	Expires            time.Time
}
type replyPrompt struct{ Kind, Step, Number, Body string }

func New(state *store.State, service *app.Service, am *auth.Manager, publicURL string, log *slog.Logger) *Bot {
	return &Bot{state: state, app: service, auth: am, publicURL: strings.TrimRight(publicURL, "/"), log: log,
		client: &http.Client{Timeout: 65 * time.Second}, apiBase: "https://api.telegram.org/bot",
		pending: map[string]pendingAction{}, replies: map[int64]replyPrompt{}}
}

func (b *Bot) Start(ctx context.Context) {
	go b.pollLoop(ctx)
	if call := b.state.ActiveCall(); call != nil &&
		(call.State == domain.CallIncoming || call.State == domain.CallAlerting || call.Held) {
		b.IncomingCall(call)
	}
}

func (b *Bot) IncomingSMS(msg *domain.Message) {
	s := b.state.Settings()
	if !s.TelegramEnabled || s.TelegramToken == "" || s.TelegramChatID == 0 {
		return
	}
	text := fmt.Sprintf("📩 新短信\n来自：%s\n\n%s", msg.Number, msg.Body)
	kb := inline([][]button{{{Text: "Reply", Data: "sms_reply:" + msg.ID}, {Text: "Delete", Data: "sms_delete:" + msg.ID}, {Text: "Block", Data: "sms_block:" + msg.ID}}})
	_, _ = b.send(context.Background(), s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": kb})
}

func (b *Bot) IncomingCall(call *domain.Call) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		deadline := time.NewTimer(3 * time.Minute)
		defer deadline.Stop()
		for {
			current := b.state.Call(call.ID)
			if current == nil {
				return
			}
			b.upsertCallNotification(current)
			if current.State == domain.CallEnded || current.State == domain.CallFailed ||
				(current.State == domain.CallActive && !current.Held) || current.MediaOwner == "voicemail" {
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				return
			}
		}
	}()
}

func (b *Bot) upsertCallNotification(call *domain.Call) {
	b.notificationMu.Lock()
	defer b.notificationMu.Unlock()
	s := b.state.Settings()
	if !s.TelegramEnabled || s.TelegramToken == "" || s.TelegramChatID == 0 {
		return
	}
	var messageID int64
	var token string
	_ = b.state.DB().QueryRow(`SELECT message_id,action_token FROM telegram_call_messages WHERE call_id=?`, call.ID).Scan(&messageID, &token)
	if token == "" {
		var err error
		token, err = b.auth.CreateActionToken(context.Background(), "temp_call", call.ID, 3*time.Hour)
		if err != nil {
			return
		}
	}
	status := "来电振铃"
	if call.Held {
		status = "已接通并挂起"
	}
	if call.MediaOwner == "voicemail" {
		status = "已转入语音信箱"
	}
	if call.State == domain.CallActive && !call.Held && call.MediaOwner != "voicemail" {
		status = "已接听"
	}
	if call.State == domain.CallEnded {
		status = "通话已结束"
	}
	elapsed := time.Since(call.StartedAt).Round(time.Second)
	text := fmt.Sprintf("📞 %s\n%s\n持续 %s", status, call.Number, elapsed)
	var rows [][]button
	if call.State != domain.CallEnded && call.State != domain.CallFailed && call.MediaOwner != "voicemail" {
		holdLabel := "挂起"
		if call.Held {
			holdLabel = "恢复"
		}
		callActions := []button{
			{Text: "打开通话页", URL: b.publicURL + "/t/call/" + token},
			{Text: holdLabel, Data: "call_hold:" + call.ID},
		}
		if s.VoicemailEnabled {
			callActions = append(callActions, button{Text: "转留言", Data: "call_voicemail:" + call.ID})
		}
		rows = [][]button{
			callActions,
			{{Text: "拒接/挂断", Data: "call_reject:" + call.ID}, {Text: "拉黑", Data: "call_block:" + call.ID}, {Text: "短信回复", Data: "call_reply:" + call.ID}},
		}
	}
	payload := map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": inline(rows)}
	if messageID == 0 {
		res, err := b.send(context.Background(), s, "sendMessage", payload)
		if err != nil {
			return
		}
		messageID = res.Result.MessageID
		_, _ = b.state.DB().Exec(`INSERT OR REPLACE INTO telegram_call_messages(call_id,chat_id,message_id,action_token,updated_at) VALUES(?,?,?,?,?)`,
			call.ID, s.TelegramChatID, messageID, token, time.Now().UTC().Format(time.RFC3339Nano))
		return
	}
	payload["message_id"] = messageID
	_, _ = b.send(context.Background(), s, "editMessageText", payload)
	_, _ = b.state.DB().Exec(`UPDATE telegram_call_messages SET updated_at=? WHERE call_id=?`, time.Now().UTC().Format(time.RFC3339Nano), call.ID)
}

func (b *Bot) CallEnded(call *domain.Call) {
	b.upsertCallNotification(call)
	_, _ = b.state.DB().Exec(`DELETE FROM telegram_call_messages WHERE call_id=?`, call.ID)
}

func (b *Bot) pollLoop(ctx context.Context) {
	var offset int64
	_ = b.state.DB().QueryRow(`SELECT update_offset FROM telegram_state WHERE id=1`).Scan(&offset)
	for ctx.Err() == nil {
		s := b.state.Settings()
		if !s.TelegramEnabled || s.TelegramToken == "" || s.TelegramChatID == 0 {
			time.Sleep(2 * time.Second)
			continue
		}
		b.ensureCommands(ctx, s)
		var updates struct {
			OK     bool     `json:"ok"`
			Result []update `json:"result"`
		}
		err := b.call(ctx, s, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		if err != nil {
			b.setHealth(false)
			time.Sleep(3 * time.Second)
			continue
		}
		b.setHealth(true)
		for _, u := range updates.Result {
			if u.ID >= offset {
				offset = u.ID + 1
			}
			b.handleUpdate(ctx, s, u)
			_, _ = b.state.DB().Exec(`UPDATE telegram_state SET update_offset=? WHERE id=1`, offset)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, s domain.Settings, u update) {
	if u.Callback != nil {
		if u.Callback.Message.Chat.ID != s.TelegramChatID {
			return
		}
		_, _ = b.send(ctx, s, "answerCallbackQuery", map[string]any{"callback_query_id": u.Callback.ID})
		b.handleCallback(ctx, s, u.Callback)
		return
	}
	if u.Message == nil || u.Message.Chat.ID != s.TelegramChatID {
		return
	}
	if u.Message.ReplyTo != nil {
		b.mu.Lock()
		prompt, ok := b.replies[u.Message.ReplyTo.MessageID]
		if ok {
			delete(b.replies, u.Message.ReplyTo.MessageID)
		}
		b.mu.Unlock()
		if ok {
			b.handleReply(ctx, s, prompt, strings.TrimSpace(u.Message.Text))
			return
		}
	}
	parts := strings.Fields(u.Message.Text)
	if len(parts) == 0 {
		return
	}
	command := strings.SplitN(parts[0], "@", 2)[0]
	switch command {
	case "/start":
		b.sendWelcome(ctx, s)
	case "/help":
		b.sendHelp(ctx, s)
	case "/call":
		if len(parts) >= 2 {
			b.createConfirmation(ctx, s, "call", parts[1], "")
		} else {
			b.ask(ctx, s, "请输入要拨打的电话号码：", replyPrompt{Kind: "call", Step: "number"})
		}
	case "/text":
		if len(parts) >= 3 {
			b.createConfirmation(ctx, s, "text", parts[1], strings.Join(parts[2:], " "))
		} else if len(parts) == 2 {
			b.ask(ctx, s, "请输入短信内容：", replyPrompt{Kind: "text", Step: "body", Number: parts[1]})
		} else {
			b.ask(ctx, s, "请输入收信号码：", replyPrompt{Kind: "text", Step: "number"})
		}
	case "/block":
		if len(parts) == 2 {
			b.createConfirmation(ctx, s, "block", parts[1], "")
		} else {
			b.ask(ctx, s, "请输入要拉黑的号码：", replyPrompt{Kind: "block", Step: "number"})
		}
	case "/status":
		b.sendStatus(ctx, s)
	default:
		switch strings.TrimSpace(u.Message.Text) {
		case "📞 打电话":
			b.ask(ctx, s, "请输入要拨打的电话号码：", replyPrompt{Kind: "call", Step: "number"})
		case "✉️ 发短信":
			b.ask(ctx, s, "请输入收信号码：", replyPrompt{Kind: "text", Step: "number"})
		case "📶 设备状态":
			b.sendStatus(ctx, s)
		case "🚫 拉黑号码":
			b.ask(ctx, s, "请输入要拉黑的号码：", replyPrompt{Kind: "block", Step: "number"})
		case "❓ 帮助":
			b.sendHelp(ctx, s)
		default:
			b.note(ctx, s, "无法识别该命令。发送 /help 查看完整用法。")
		}
	}
}

func (b *Bot) handleReply(ctx context.Context, s domain.Settings, p replyPrompt, text string) {
	switch p.Step {
	case "number":
		number, err := filter.Normalize(text, s.Country)
		if err != nil {
			b.note(ctx, s, "号码格式无效，请重新操作。")
			return
		}
		if p.Kind == "text" {
			b.ask(ctx, s, "请输入要发送给 "+number+" 的短信内容：", replyPrompt{Kind: "text", Step: "body", Number: number})
			return
		}
		b.createConfirmation(ctx, s, p.Kind, number, "")
	case "body":
		if text == "" {
			b.note(ctx, s, "短信内容不能为空。")
			return
		}
		b.createConfirmation(ctx, s, "text", p.Number, text)
	}
}

func (b *Bot) createConfirmation(ctx context.Context, s domain.Settings, kind, raw, body string) {
	number, err := filter.Normalize(raw, s.Country)
	if err != nil {
		b.note(ctx, s, "号码格式无效")
		return
	}
	id := domain.NewID("confirm")
	routes := b.availableRoutes()
	pending := pendingAction{Kind: kind, Number: number, Body: body, Expires: time.Now().Add(2 * time.Minute)}
	if len(routes) == 1 {
		pending.Route = routes[0].Route
		pending.RouteLabel = routes[0].Label
	}
	b.mu.Lock()
	b.pending[id] = pending
	b.mu.Unlock()
	if (kind == "call" || kind == "text") && len(routes) > 1 {
		rows := make([][]button, 0, len(routes))
		for i, route := range routes {
			rows = append(rows, []button{{Text: route.Label, Data: fmt.Sprintf("route:%s:%d", id, i)}})
		}
		_, _ = b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": "请选择本次使用的号码：", "reply_markup": inline(rows)})
		return
	}
	b.sendConfirmation(ctx, s, id, pending)
}

func (b *Bot) sendConfirmation(ctx context.Context, s domain.Settings, id string, p pendingAction) {
	labels := map[string]string{"call": "拨打", "text": "发送短信到", "block": "拉黑"}
	text := fmt.Sprintf("请确认%s：%s", labels[p.Kind], p.Number)
	if p.Body != "" {
		text += "\n\n" + p.Body
	}
	if p.RouteLabel != "" {
		text += "\n\n使用号码：" + p.RouteLabel
	}
	kb := inline([][]button{{{Text: "确认", Data: "confirm:" + id}, {Text: "取消", Data: "cancel:" + id}}})
	_, _ = b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": kb})
}

func (b *Bot) handleCallback(ctx context.Context, s domain.Settings, c *callback) {
	if strings.HasPrefix(c.Data, "route:") {
		routeParts := strings.Split(c.Data, ":")
		if len(routeParts) != 3 {
			b.note(ctx, s, "路由选择无效")
			return
		}
		index, err := strconv.Atoi(routeParts[2])
		routes := b.availableRoutes()
		b.mu.Lock()
		p, ok := b.pending[routeParts[1]]
		if err == nil && index >= 0 && index < len(routes) && ok {
			p.Route = routes[index].Route
			p.RouteLabel = routes[index].Label
			b.pending[routeParts[1]] = p
		}
		b.mu.Unlock()
		if err != nil || index < 0 || index >= len(routes) || !ok {
			b.note(ctx, s, "路由选择已失效，请重新操作。")
			return
		}
		b.sendConfirmation(ctx, s, routeParts[1], p)
		return
	}
	parts := strings.SplitN(c.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	action, id := parts[0], parts[1]
	switch action {
	case "confirm":
		b.mu.Lock()
		p, ok := b.pending[id]
		delete(b.pending, id)
		b.mu.Unlock()
		if !ok || time.Now().After(p.Expires) {
			b.note(ctx, s, "确认已过期")
			return
		}
		var err error
		switch p.Kind {
		case "call":
			var call *domain.Call
			call, err = b.app.DialRoute(ctx, p.Number, "web", p.Route)
			if err == nil {
				err = b.sendOutgoingCallLink(ctx, s, call)
			}
		case "text":
			_, err = b.app.SendSMSRoute(ctx, p.Number, p.Body, p.Route)
		case "block":
			_, err = b.addBlock(p.Number, "both")
		}
		if err != nil {
			b.note(ctx, s, "操作失败："+err.Error())
		} else if p.Kind != "call" {
			b.note(ctx, s, "操作已执行")
		}
	case "cancel":
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		b.note(ctx, s, "已取消")
	case "call_reject":
		if _, err := b.app.Hangup(ctx, id, "rejected"); err != nil {
			b.note(ctx, s, "挂断失败："+err.Error())
		}
	case "call_hold":
		var err error
		if call := b.state.Call(id); call != nil && call.Held {
			_, err = b.app.Resume(ctx, id)
		} else {
			_, err = b.app.Hold(ctx, id)
		}
		if err != nil {
			b.note(ctx, s, "挂起/恢复失败："+err.Error())
		}
	case "call_voicemail":
		if _, err := b.app.TransferToVoicemail(ctx, id); err != nil {
			b.note(ctx, s, "转入语音信箱失败："+err.Error())
		}
	case "call_block":
		if call := b.state.Call(id); call != nil {
			_, _ = b.addBlock(call.Number, "call")
			_, _ = b.app.Hangup(ctx, id, "blocked")
		}
	case "call_reply":
		if call := b.state.Call(id); call != nil {
			b.forceReply(ctx, s, call.Number)
		}
	case "sms_reply":
		if msg := b.state.Message(id); msg != nil {
			b.forceReply(ctx, s, msg.Number)
		}
	case "sms_delete":
		_, _ = b.app.DeleteMessage(ctx, id)
	case "sms_block":
		if msg := b.state.Message(id); msg != nil {
			_, _ = b.addBlock(msg.Number, "sms")
		}
	}
}

func (b *Bot) sendOutgoingCallLink(ctx context.Context, s domain.Settings, call *domain.Call) error {
	if call == nil || b.auth == nil {
		return errors.New("CALL_LINK_UNAVAILABLE")
	}
	token, err := b.auth.CreateActionToken(ctx, "temp_call", call.ID, 3*time.Hour)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("📞 正在拨打 %s\n\n点击下方按钮打开通话页面。", call.Number)
	_, err = b.send(ctx, s, "sendMessage", map[string]any{
		"chat_id": s.TelegramChatID,
		"text":    text,
		"reply_markup": inline([][]button{{
			{Text: "打开通话页", URL: b.publicURL + "/t/call/" + token},
		}}),
	})
	return err
}

func (b *Bot) forceReply(ctx context.Context, s domain.Settings, number string) {
	b.ask(ctx, s, "请输入要发送给 "+number+" 的短信：", replyPrompt{Kind: "text", Step: "body", Number: number})
}

func (b *Bot) ask(ctx context.Context, s domain.Settings, text string, prompt replyPrompt) {
	res, err := b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": map[string]any{"force_reply": true, "selective": true}})
	if err == nil {
		b.mu.Lock()
		b.replies[res.Result.MessageID] = prompt
		b.mu.Unlock()
	}
}

type routeChoice struct {
	Label string
	Route modem.Route
}

func (b *Bot) availableRoutes() []routeChoice {
	info := b.app.SystemInfo()
	out := []routeChoice{}
	for _, gateway := range info.Gateways {
		if !gateway.Connected {
			continue
		}
		if len(gateway.Subscriptions) == 0 {
			label := gateway.Model
			if label == "" {
				label = "默认号码"
			}
			out = append(out, routeChoice{Label: label, Route: modem.Route{GatewayID: gateway.ID, SubscriptionID: gateway.SubscriptionID}})
			continue
		}
		for _, sub := range gateway.Subscriptions {
			if !sub.Ready {
				continue
			}
			label := "卡 " + strconv.Itoa(sub.SIMSlot+1)
			if sub.PhoneNumber != "" {
				label = sub.PhoneNumber
			}
			if sub.CarrierName != "" {
				label += " · " + sub.CarrierName
			}
			out = append(out, routeChoice{Label: label, Route: modem.Route{GatewayID: gateway.ID, SubscriptionID: sub.ID}})
		}
	}
	return out
}

func replyKeyboard() map[string]any {
	return map[string]any{"keyboard": [][]map[string]string{
		{{"text": "📞 打电话"}, {"text": "✉️ 发短信"}},
		{{"text": "📶 设备状态"}, {"text": "🚫 拉黑号码"}, {"text": "❓ 帮助"}},
	}, "resize_keyboard": true, "is_persistent": true}
}
func (b *Bot) sendWelcome(ctx context.Context, s domain.Settings) {
	info := b.app.SystemInfo()
	connected := 0
	for _, g := range info.Gateways {
		if g.Connected {
			connected++
		}
	}
	voiceStatus := "不可用"
	if info.Network.VoiceRegistered || info.Gateway.IMSRegistered {
		voiceStatus = "可用"
	}
	text := fmt.Sprintf("欢迎使用 onSIM 电话与短信助手。\n\n可用设备：%d/%d\n运营商：%s\n电话服务：%s\n\n你可以直接使用下方按钮拨打电话、发送短信、查看状态或管理黑名单。执行重要操作前会再次请你确认。", connected, len(info.Gateways), info.Network.Operator, voiceStatus)
	_, _ = b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": replyKeyboard()})
}
func (b *Bot) sendHelp(ctx context.Context, s domain.Settings) {
	text := "onSIM 命令帮助\n\n/call [号码] — 拨打电话；不带号码时会询问号码\n/text [号码] [内容] — 发送短信；信息不完整时会逐步询问\n/block [号码] — 拉黑电话和短信\n/status — 查看设备、电话卡、信号和通话状态\n/help — 显示本帮助\n\n拨号、发短信和拉黑前都需要确认，确认在 2 分钟后失效。有多个可用号码时，会先请你选择本次使用的号码。来电页面只可控制当前通话。"
	_, _ = b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text, "reply_markup": replyKeyboard()})
}
func (b *Bot) sendStatus(ctx context.Context, s domain.Settings) {
	info := b.app.SystemInfo()
	deviceStatus, cardStatus, callStatus, audioStatus := "离线", "未就绪", "不可用", "不可用"
	if info.Gateway.Connected {
		deviceStatus = "在线"
	}
	if info.SIM.Ready {
		cardStatus = "已就绪"
	}
	if info.Network.VoiceRegistered || info.Gateway.IMSRegistered {
		callStatus = "可用"
	}
	if info.Gateway.AudioCapable {
		audioStatus = "可用"
	}
	text := fmt.Sprintf("📶 设备状态\n设备：%s\n电话卡：%s\n运营商：%s\n网络：%s\n信号：%d dBm\n电话服务：%s\n通话语音：%s", deviceStatus, cardStatus, info.Network.Operator, info.Network.AccessTechnology, info.Network.SignalDBm, callStatus, audioStatus)
	b.note(ctx, s, text)
}
func (b *Bot) ensureCommands(ctx context.Context, s domain.Settings) {
	b.mu.Lock()
	if b.configuredToken == s.TelegramToken {
		b.mu.Unlock()
		return
	}
	b.configuredToken = s.TelegramToken
	b.mu.Unlock()
	commands := []map[string]string{{"command": "start", "description": "打开 onSIM 菜单"}, {"command": "call", "description": "拨打电话"}, {"command": "text", "description": "发送短信"}, {"command": "block", "description": "拉黑号码"}, {"command": "status", "description": "设备状态"}, {"command": "help", "description": "完整帮助"}}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := b.call(ctx, s, "setMyCommands", map[string]any{"commands": commands}, &result); err != nil || !result.OK {
		b.mu.Lock()
		b.configuredToken = ""
		b.mu.Unlock()
	}
}

func (b *Bot) addBlock(number, scope string) (*domain.FilterRule, error) {
	r := &domain.FilterRule{ID: domain.NewID("rule"), Kind: "exact", Pattern: number, Label: "手动黑名单", Category: "manual", Action: "block", Scope: scope, Enabled: true, CreatedAt: time.Now().UTC()}
	_, err := b.state.UpsertRule(r)
	return r, err
}
func (b *Bot) note(ctx context.Context, s domain.Settings, text string) {
	_, _ = b.send(ctx, s, "sendMessage", map[string]any{"chat_id": s.TelegramChatID, "text": text})
}
func (b *Bot) setHealth(ok bool) {
	snap := b.state.Snapshot(false)
	d := snap.Device
	if d.TelegramOK == ok {
		return
	}
	d.TelegramOK = ok
	_, _ = b.state.UpdateDevice(d)
}

type button struct{ Text, Data, URL string }

func inline(rows [][]button) map[string]any {
	out := [][]map[string]string{}
	for _, row := range rows {
		r := []map[string]string{}
		for _, x := range row {
			v := map[string]string{"text": x.Text}
			if x.URL != "" {
				v["url"] = x.URL
			} else {
				v["callback_data"] = x.Data
			}
			r = append(r, v)
		}
		out = append(out, r)
	}
	return map[string]any{"inline_keyboard": out}
}

type update struct {
	ID       int64     `json:"update_id"`
	Message  *message  `json:"message"`
	Callback *callback `json:"callback_query"`
}
type chat struct {
	ID int64 `json:"id"`
}
type message struct {
	MessageID int64    `json:"message_id"`
	Chat      chat     `json:"chat"`
	Text      string   `json:"text"`
	ReplyTo   *message `json:"reply_to_message"`
}
type callback struct {
	ID      string  `json:"id"`
	Data    string  `json:"data"`
	Message message `json:"message"`
}
type apiResult struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

func (b *Bot) send(ctx context.Context, s domain.Settings, method string, payload any) (apiResult, error) {
	var out apiResult
	err := b.call(ctx, s, method, payload, &out)
	return out, err
}
func (b *Bot) call(ctx context.Context, s domain.Settings, method string, payload, out any) error {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.apiBase+s.TelegramToken+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("telegram %d: %s", resp.StatusCode, body)
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	if r, ok := out.(*apiResult); ok && !r.OK {
		return fmt.Errorf("telegram: %s", r.Description)
	}
	return nil
}

func ParseChatID(raw string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(raw), 10, 64) }
