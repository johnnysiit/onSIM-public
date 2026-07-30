package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"onsim/internal/app"
	"onsim/internal/auth"
	"onsim/internal/domain"
	"onsim/internal/filter"
	"onsim/internal/media"
	"onsim/internal/modem"
	"onsim/internal/store"
	"onsim/webui"
)

type Server struct {
	state         *store.State
	auth          *auth.Manager
	app           *app.Service
	media         *media.Manager
	log           *slog.Logger
	mux           *http.ServeMux
	loginMu       sync.Mutex
	idemMu        sync.Mutex
	loginAttempts map[string][]time.Time
	sip           sipManager
}

type sipManager interface {
	RevealCredentials() (map[string]string, error)
	ResetCredentials() (map[string]string, error)
	ApplySettings() error
}

func New(state *store.State, am *auth.Manager, service *app.Service, mediaManager *media.Manager, log *slog.Logger, sipManagers ...sipManager) *Server {
	s := &Server{state: state, auth: am, app: service, media: mediaManager, log: log, mux: http.NewServeMux(), loginAttempts: map[string][]time.Time{}}
	if len(sipManagers) > 0 {
		s.sip = sipManagers[0]
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return securityHeaders(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	s.mux.HandleFunc("POST /api/v1/auth/setup", s.setup)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.require(s.logout))
	s.mux.HandleFunc("GET /api/v1/state", s.require(s.snapshot))
	s.mux.HandleFunc("GET /api/v1/info", s.require(s.systemInfo))
	s.mux.HandleFunc("GET /api/v1/events", s.require(s.events))
	s.mux.HandleFunc("POST /api/v1/calls", s.require(s.dial))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/answer", s.require(s.answer))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/hold", s.require(s.hold))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/resume", s.require(s.resume))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/voicemail", s.require(s.voicemail))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/hangup", s.require(s.hangup))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/dtmf", s.require(s.dtmf))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/mute", s.require(s.mute))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/media", s.require(s.mediaOffer))
	s.mux.HandleFunc("GET /api/v1/calls/{id}/media/status", s.require(s.mediaStatus))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/recording/start", s.require(s.recordingStart))
	s.mux.HandleFunc("POST /api/v1/calls/{id}/recording/stop", s.require(s.recordingStop))
	s.mux.HandleFunc("POST /api/v1/messages", s.require(s.sendSMS))
	s.mux.HandleFunc("POST /api/v1/messages/read-all", s.require(s.readAllMessages))
	s.mux.HandleFunc("POST /api/v1/messages/{id}/read", s.require(s.readMessage))
	s.mux.HandleFunc("DELETE /api/v1/messages/{id}", s.require(s.deleteMessage))
	s.mux.HandleFunc("POST /api/v1/rules", s.require(s.upsertRule))
	s.mux.HandleFunc("DELETE /api/v1/rules/{id}", s.require(s.deleteRule))
	s.mux.HandleFunc("PUT /api/v1/settings", s.require(s.settings))
	s.mux.HandleFunc("POST /api/v1/sip/credentials/reveal", s.require(s.sipCredentialsReveal))
	s.mux.HandleFunc("POST /api/v1/sip/credentials/reset", s.require(s.sipCredentialsReset))
	s.mux.HandleFunc("GET /api/v1/recordings/{id}", s.require(s.recordingFile))
	s.mux.HandleFunc("GET /api/v1/recordings/{id}/play", s.require(s.recordingPlay))
	s.mux.HandleFunc("DELETE /api/v1/recordings/{id}", s.require(s.recordingDelete))
	s.mux.HandleFunc("GET /api/v1/settings/voicemail-greeting", s.require(s.greetingInfo))
	s.mux.HandleFunc("GET /api/v1/settings/voicemail-greeting/play", s.require(s.greetingPlay))
	s.mux.HandleFunc("POST /api/v1/settings/voicemail-greeting", s.require(s.greetingSave))
	s.mux.HandleFunc("DELETE /api/v1/settings/voicemail-greeting", s.require(s.greetingReset))
	s.mux.HandleFunc("GET /t/call/{token}", s.temporaryCall)
	s.mux.HandleFunc("GET /api/v1/temp/call", s.tempState)
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/answer", s.tempRequire(s.answer))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/hold", s.tempRequire(s.hold))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/resume", s.tempRequire(s.resume))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/voicemail", s.tempRequire(s.voicemail))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/hangup", s.tempRequire(s.hangup))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/dtmf", s.tempRequire(s.dtmf))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/media", s.tempRequire(s.mediaOffer))
	s.mux.HandleFunc("GET /api/v1/temp/call/{id}/media/status", s.tempRequire(s.mediaStatus))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/recording/start", s.tempRequire(s.recordingStart))
	s.mux.HandleFunc("POST /api/v1/temp/call/{id}/recording/stop", s.tempRequire(s.recordingStop))
	s.mux.HandleFunc("GET /api/v1/temp/call/{id}/capabilities", s.tempRequire(s.tempCapabilities))
	s.mux.Handle("/", spaHandler())
}

func (s *Server) require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("onsim_session")
		if err != nil || !s.auth.Validate(r.Context(), c.Value) {
			fail(w, http.StatusUnauthorized, "AUTH_REQUIRED")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && !sameOrigin(r) {
			fail(w, http.StatusForbidden, "BAD_ORIGIN")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			s.idempotent(w, r, next)
			return
		}
		next(w, r)
	}
}
func (s *Server) tempRequire(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("onsim_temp")
		if err != nil {
			fail(w, 401, "TEMP_AUTH_REQUIRED")
			return
		}
		entity, ok := s.auth.ValidateActionToken(r.Context(), c.Value, "temp_session")
		if !ok || entity != r.PathValue("id") {
			fail(w, 403, "TEMP_SCOPE_DENIED")
			return
		}
		if !sameOrigin(r) {
			fail(w, http.StatusForbidden, "BAD_ORIGIN")
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next(w, r)
			return
		}
		s.idempotent(w, r, next)
	}
}

func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		fail(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED")
		return
	}
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	var code int
	var body []byte
	var expires string
	if err := s.state.DB().QueryRow(`SELECT response_code,response,expires_at FROM idempotency WHERE key=?`, key).Scan(&code, &body, &expires); err == nil {
		if exp, e := time.Parse(time.RFC3339Nano, expires); e == nil && time.Now().Before(exp) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write(body)
			return
		}
		_, _ = s.state.DB().Exec(`DELETE FROM idempotency WHERE key=?`, key)
	}
	rec := &responseCapture{header: http.Header{}}
	next(rec, r)
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	if rec.code < 500 {
		_, _ = s.state.DB().Exec(`INSERT OR REPLACE INTO idempotency(key,response_code,response,expires_at) VALUES(?,?,?,?)`,
			key, rec.code, rec.body.Bytes(), time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339Nano))
	}
	for k, values := range rec.header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}

type responseCapture struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *responseCapture) Header() http.Header { return r.header }
func (r *responseCapture) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}
func (r *responseCapture) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(p)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.state.IntegrityCheck(r.Context()); err != nil {
		fail(w, 503, "DATABASE_UNHEALTHY")
		return
	}
	jsonOut(w, 200, map[string]string{"status": "ok"})
}
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	logged := false
	if c, e := r.Cookie("onsim_session"); e == nil {
		logged = s.auth.Validate(r.Context(), c.Value)
	}
	jsonOut(w, 200, map[string]bool{"initialized": s.auth.Initialized(r.Context()), "authenticated": logged})
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.auth.Setup(r.Context(), in.Password); err != nil {
		failErr(w, err)
		return
	}
	token, exp, err := s.auth.Login(r.Context(), in.Password)
	if err != nil {
		failErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "onsim_session", Value: token, Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteStrictMode, Expires: exp})
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.allowLogin(ip) {
		fail(w, 429, "LOGIN_RATE_LIMITED")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, exp, err := s.auth.Login(r.Context(), in.Password)
	if err != nil {
		time.Sleep(300 * time.Millisecond)
		fail(w, 401, "INVALID_CREDENTIALS")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "onsim_session", Value: token, Path: "/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteStrictMode, Expires: exp})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("onsim_session"); e == nil {
		s.auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "onsim_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteStrictMode})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.state.Snapshot(s.auth.Initialized(r.Context())))
}
func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, s.app.SystemInfo())
}

func (s *Server) dial(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Number         string `json:"number"`
		GatewayID      string `json:"gatewayId"`
		SubscriptionID int    `json:"subscriptionId"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.app.DialRoute(r.Context(), in.Number, "web", modem.Route{GatewayID: in.GatewayID, SubscriptionID: in.SubscriptionID})
	result(w, v, e)
}
func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.Answer(r.Context(), r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) hold(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.Hold(r.Context(), r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.Resume(r.Context(), r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) voicemail(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.TransferToVoicemail(r.Context(), r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) hangup(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.Hangup(r.Context(), r.PathValue("id"), "")
	result(w, v, e)
}
func (s *Server) dtmf(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	e := s.app.DTMF(r.Context(), r.PathValue("id"), in.Key)
	result(w, map[string]bool{"ok": e == nil}, e)
}
func (s *Server) mute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Muted bool `json:"muted"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.app.Mute(r.Context(), r.PathValue("id"), in.Muted)
	result(w, v, e)
}
func (s *Server) mediaOffer(w http.ResponseWriter, r *http.Request) {
	var in media.Offer
	if !decode(w, r, &in) {
		return
	}
	v, e := s.media.Offer(r.Context(), r.PathValue("id"), in)
	result(w, v, e)
}
func (s *Server) mediaStatus(w http.ResponseWriter, r *http.Request) {
	result(w, s.media.MediaStatus(r.PathValue("id")), nil)
}
func (s *Server) tempCapabilities(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, map[string]bool{"voicemailEnabled": s.state.Settings().VoicemailEnabled})
}
func (s *Server) recordingStart(w http.ResponseWriter, r *http.Request) {
	v, e := s.media.StartRecording(r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) recordingStop(w http.ResponseWriter, r *http.Request) {
	v, e := s.media.StopRecording(r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) recordingFile(w http.ResponseWriter, r *http.Request) {
	rec := s.state.Recording(r.PathValue("id"))
	if rec == nil {
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, rec.FileName))
	w.Header().Set("Content-Type", "audio/ogg")
	file, err := s.media.OriginalPath(rec.ID)
	if err != nil {
		failErr(w, err)
		return
	}
	http.ServeFile(w, r, file)
}
func (s *Server) recordingPlay(w http.ResponseWriter, r *http.Request) {
	rec := s.state.Recording(r.PathValue("id"))
	if rec == nil {
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	file, err := s.media.PlaybackPath(rec.ID)
	if err != nil {
		failErr(w, err)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.wav"`, strings.TrimSuffix(rec.FileName, path.Ext(rec.FileName))))
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeFile(w, r, file)
}
func (s *Server) recordingDelete(w http.ResponseWriter, r *http.Request) {
	err := s.media.DeleteRecording(r.PathValue("id"))
	result(w, map[string]bool{"ok": err == nil}, err)
}
func (s *Server) greetingInfo(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, s.media.Greeting())
}
func (s *Server) greetingPlay(w http.ResponseWriter, r *http.Request) {
	raw, err := s.media.GreetingAudio()
	if err != nil {
		fail(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	w.Header().Set("Content-Disposition", `inline; filename="voicemail-greeting.wav"`)
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "voicemail-greeting.wav", time.Time{}, bytes.NewReader(raw))
}
func (s *Server) greetingSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusBadRequest, "VOICEMAIL_GREETING_TOO_LARGE")
		return
	}
	info, err := s.media.SaveGreeting(raw)
	result(w, info, err)
}
func (s *Server) greetingReset(w http.ResponseWriter, r *http.Request) {
	err := s.media.ResetGreeting()
	result(w, map[string]bool{"ok": err == nil}, err)
}
func (s *Server) sendSMS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Number, Body, GatewayID string
		SubscriptionID          int
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.app.SendSMSRoute(r.Context(), in.Number, in.Body, modem.Route{GatewayID: in.GatewayID, SubscriptionID: in.SubscriptionID})
	result(w, v, e)
}
func (s *Server) readMessage(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.MarkRead(r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) readAllMessages(w http.ResponseWriter, r *http.Request) {
	updated, err := s.app.MarkAllRead()
	result(w, map[string]int{"updated": updated}, err)
}
func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	v, e := s.app.DeleteMessage(r.Context(), r.PathValue("id"))
	result(w, v, e)
}
func (s *Server) upsertRule(w http.ResponseWriter, r *http.Request) {
	var v domain.FilterRule
	if !decode(w, r, &v) {
		return
	}
	if v.ID == "" {
		v.ID = domain.NewID("rule")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Kind != "exact" && v.Kind != "prefix" && v.Kind != "keyword" && v.Kind != "regex" {
		fail(w, 400, "INVALID_RULE")
		return
	}
	if v.Action != "allow" && v.Action != "block" && v.Action != "label" {
		fail(w, 400, "INVALID_RULE")
		return
	}
	if v.Kind == "exact" {
		n, e := filter.Normalize(v.Pattern, s.state.Settings().Country)
		if e != nil {
			failErr(w, e)
			return
		}
		v.Pattern = n
	}
	_, e := s.state.UpsertRule(&v)
	result(w, v, e)
}
func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	_, e := s.state.DeleteRule(r.PathValue("id"))
	result(w, map[string]bool{"ok": e == nil}, e)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var v domain.Settings
	if !decode(w, r, &v) {
		return
	}
	old := s.state.Settings()
	if v.TelegramToken == "" {
		v.TelegramToken = old.TelegramToken
	}
	if v.ProviderAPIKey == "" {
		v.ProviderAPIKey = old.ProviderAPIKey
	}
	v.SIPPassword = old.SIPPassword
	v.SIPPasswordSeen = old.SIPPasswordSeen
	if v.VoicemailTimeout == 0 {
		v.VoicemailTimeout = 30
	}
	if v.VoicemailTimeout < 10 || v.VoicemailTimeout > 120 {
		fail(w, 400, "INVALID_VOICEMAIL_TIMEOUT")
		return
	}
	if v.ProviderURL != "" && !strings.HasPrefix(v.ProviderURL, "https://") {
		fail(w, 400, "PROVIDER_HTTPS_REQUIRED")
		return
	}
	_, e := s.state.UpdateSettings(v)
	if e != nil {
		failErr(w, e)
		return
	}
	if s.sip != nil {
		if e = s.sip.ApplySettings(); e != nil {
			failErr(w, e)
			return
		}
	}
	safe := v
	safe.TelegramToken = ""
	safe.ProviderAPIKey = ""
	safe.SIPPassword = ""
	jsonOut(w, 200, safe)
}

func (s *Server) sipCredentialsReveal(w http.ResponseWriter, r *http.Request) {
	if s.sip == nil {
		fail(w, http.StatusServiceUnavailable, "SIP_UNAVAILABLE")
		return
	}
	value, err := s.sip.RevealCredentials()
	result(w, value, err)
}

func (s *Server) sipCredentialsReset(w http.ResponseWriter, r *http.Request) {
	if s.sip == nil {
		fail(w, http.StatusServiceUnavailable, "SIP_UNAVAILABLE")
		return
	}
	value, err := s.sip.ResetCredentials()
	result(w, value, err)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{r.Host}, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ch, cancel := s.state.Subscribe()
	defer cancel()
	for {
		select {
		case e := <-ch:
			raw, _ := json.Marshal(e)
			if err = conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) temporaryCall(w http.ResponseWriter, r *http.Request) {
	callID, err := s.auth.ConsumeActionToken(r.Context(), r.PathValue("token"), "temp_call")
	if err != nil {
		http.Error(w, "链接无效或已过期", http.StatusForbidden)
		return
	}
	call := s.state.Call(callID)
	if call == nil || (call.State != domain.CallIncoming && call.State != domain.CallAlerting && call.State != domain.CallActive) {
		http.Error(w, "来电已经结束", http.StatusGone)
		return
	}
	session, err := s.auth.CreateActionToken(r.Context(), "temp_session", callID, 3*time.Hour)
	if err != nil {
		http.Error(w, "无法创建临时会话", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "onsim_temp", Value: session, Path: "/api/v1/temp/", HttpOnly: true, Secure: isSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: 10800})
	http.Redirect(w, r, "/temp-call?id="+callID, http.StatusFound)
}
func (s *Server) tempState(w http.ResponseWriter, r *http.Request) {
	c, e := r.Cookie("onsim_temp")
	if e != nil {
		fail(w, 401, "TEMP_AUTH_REQUIRED")
		return
	}
	id, ok := s.auth.ValidateActionToken(r.Context(), c.Value, "temp_session")
	if !ok {
		fail(w, 401, "TEMP_AUTH_REQUIRED")
		return
	}
	call := s.state.Call(id)
	if call == nil {
		fail(w, 404, "NOT_FOUND")
		return
	}
	jsonOut(w, 200, call)
}

func (s *Server) allowLogin(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	cut := now.Add(-10 * time.Minute)
	kept := s.loginAttempts[ip][:0]
	for _, t := range s.loginAttempts[ip] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= 10 {
		s.loginAttempts[ip] = kept
		return false
	}
	s.loginAttempts[ip] = append(kept, now)
	return true
}

func spaHandler() http.Handler {
	sub, _ := fs.Sub(webui.Files, "dist")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			r.URL.Path = "/"
		} else {
			r.URL.Path = "/" + p
		}
		files.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "INVALID_JSON")
		return false
	}
	return true
}
func result(w http.ResponseWriter, v any, e error) {
	if e != nil {
		failErr(w, e)
		return
	}
	jsonOut(w, 200, v)
}
func failErr(w http.ResponseWriter, e error) {
	code := 400
	if errors.Is(e, context.DeadlineExceeded) {
		code = 504
	}
	if e.Error() == "MODEM_OFFLINE" || e.Error() == "VOICE_NETWORK_UNAVAILABLE" {
		code = 503
	}
	if e.Error() == "NOT_FOUND" {
		code = 404
	}
	if strings.Contains(e.Error(), "CONFLICT") {
		code = 409
	}
	fail(w, code, e.Error())
}
func fail(w http.ResponseWriter, status int, code string) {
	jsonOut(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func clientIP(r *http.Request) string { h, _, _ := strings.Cut(r.RemoteAddr, ":"); return h }
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || strings.Contains(o, "://"+r.Host)
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "microphone=(self)")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' wss:; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' blob:")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/" || r.URL.Path == "/sw.js" || path.Ext(r.URL.Path) == "" {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}

var _ = fmt.Sprintf
var _ = strconv.Itoa
