package domain

import "time"

type CallState string

const (
	CallIncoming CallState = "incoming"
	CallDialing  CallState = "dialing"
	CallAlerting CallState = "alerting"
	CallActive   CallState = "active"
	CallEnding   CallState = "ending"
	CallEnded    CallState = "ended"
	CallFailed   CallState = "failed"
)

type Direction string

const (
	Incoming Direction = "incoming"
	Outgoing Direction = "outgoing"
)

type DeviceStatus struct {
	Mode          string    `json:"mode"`
	GatewayType   string    `json:"gatewayType"`
	ATPort        string    `json:"atPort"`
	AudioPort     string    `json:"audioPort"`
	ATConnected   bool      `json:"atConnected"`
	AudioCapable  bool      `json:"audioCapable"`
	SIMReady      bool      `json:"simReady"`
	Registered    bool      `json:"registered"`
	VoiceReady    bool      `json:"voiceRegistered"`
	Operator      string    `json:"operator"`
	AccessTech    string    `json:"accessTechnology"`
	Signal        int       `json:"signal"`
	SignalDBm     int       `json:"signalDbm"`
	TelegramOK    bool      `json:"telegramOk"`
	SIPStatus     string    `json:"sipStatus"`
	SIPPending    int       `json:"sipPendingMessages"`
	DiskUsedPct   float64   `json:"diskUsedPct"`
	Degraded      []string  `json:"degraded"`
	LastCheckedAt time.Time `json:"lastCheckedAt"`
}

type SIMInfo struct {
	Ready       bool   `json:"ready"`
	PhoneNumber string `json:"phoneNumber"`
	ICCID       string `json:"iccid"`
	IMSI        string `json:"imsi"`
}

type NetworkInfo struct {
	Registered       bool   `json:"registered"`
	VoiceRegistered  bool   `json:"voiceRegistered"`
	Operator         string `json:"operator"`
	AccessTechnology string `json:"accessTechnology"`
	Signal           int    `json:"signal"`
	SignalDBm        int    `json:"signalDbm"`
}

type ModemInfo struct {
	Connected    bool   `json:"connected"`
	AudioCapable bool   `json:"audioCapable"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	IMEI         string `json:"imei"`
	Firmware     string `json:"firmware"`
	SubVersion   string `json:"subVersion"`
	QCN          string `json:"qcn"`
	VoLTEControl bool   `json:"volteControl"`
	ATPort       string `json:"atPort"`
	AudioPort    string `json:"audioPort"`
}

type GatewayInfo struct {
	ID                  string                `json:"id"`
	Type                string                `json:"type"`
	Connected           bool                  `json:"connected"`
	Transport           string                `json:"transport"`
	AudioCapable        bool                  `json:"audioCapable"`
	ADBState            string                `json:"adbState,omitempty"`
	Manufacturer        string                `json:"manufacturer,omitempty"`
	Model               string                `json:"model,omitempty"`
	AndroidVersion      string                `json:"androidVersion,omitempty"`
	BuildID             string                `json:"buildId,omitempty"`
	SecurityPatch       string                `json:"securityPatch,omitempty"`
	BasebandVersion     string                `json:"basebandVersion,omitempty"`
	BatteryLevel        int                   `json:"batteryLevel,omitempty"`
	BatteryCharging     bool                  `json:"batteryCharging,omitempty"`
	SubscriptionID      int                   `json:"subscriptionId,omitempty"`
	SIMSlot             int                   `json:"simSlot,omitempty"`
	IMEI                string                `json:"imei,omitempty"`
	IMSRegistered       bool                  `json:"imsRegistered,omitempty"`
	VoLTE               bool                  `json:"volte,omitempty"`
	CompanionVersion    string                `json:"companionVersion,omitempty"`
	ProtocolVersion     int                   `json:"protocolVersion,omitempty"`
	AudioDownlinkOK     bool                  `json:"audioDownlinkOk,omitempty"`
	AudioUplinkOK       bool                  `json:"audioUplinkOk,omitempty"`
	AudioDownlinkFrames int64                 `json:"audioDownlinkFrames,omitempty"`
	AudioUplinkFrames   int64                 `json:"audioUplinkFrames,omitempty"`
	AudioUplinkBytes    int64                 `json:"audioUplinkBytes,omitempty"`
	LastError           string                `json:"lastError,omitempty"`
	Subscriptions       []GatewaySubscription `json:"subscriptions,omitempty"`
}

type GatewaySubscription struct {
	ID          int    `json:"id"`
	SIMSlot     int    `json:"simSlot"`
	DisplayName string `json:"displayName,omitempty"`
	CarrierName string `json:"carrierName,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	IMEI        string `json:"imei,omitempty"`
	Ready       bool   `json:"ready"`
}

type RuntimeInfo struct {
	Version       string    `json:"version"`
	Revision      string    `json:"revision"`
	BuildTime     string    `json:"buildTime"`
	StartedAt     time.Time `json:"startedAt"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
}

type SystemInfo struct {
	SIM           SIMInfo       `json:"sim"`
	Network       NetworkInfo   `json:"network"`
	Modem         ModemInfo     `json:"modem"`
	Gateway       GatewayInfo   `json:"gateway"`
	Gateways      []GatewayInfo `json:"gateways"`
	Runtime       RuntimeInfo   `json:"runtime"`
	LastCheckedAt time.Time     `json:"lastCheckedAt"`
}

type Call struct {
	ID             string     `json:"id"`
	Version        int64      `json:"version"`
	Direction      Direction  `json:"direction"`
	Number         string     `json:"number"`
	DisplayName    string     `json:"displayName,omitempty"`
	State          CallState  `json:"state"`
	Filter         Decision   `json:"filter"`
	StartedAt      time.Time  `json:"startedAt"`
	ConnectedAt    *time.Time `json:"connectedAt,omitempty"`
	EndedAt        *time.Time `json:"endedAt,omitempty"`
	EndReason      string     `json:"endReason,omitempty"`
	Muted          bool       `json:"muted"`
	SpeakerMuted   bool       `json:"speakerMuted"`
	Recording      bool       `json:"recording"`
	RecordingID    string     `json:"recordingId,omitempty"`
	MediaOwner     string     `json:"mediaOwner,omitempty"`
	Held           bool       `json:"held"`
	GatewayID      string     `json:"gatewayId,omitempty"`
	SubscriptionID int        `json:"subscriptionId,omitempty"`
	TelegramMsgIDs []int64    `json:"-"`
}

type Message struct {
	ID             string       `json:"id"`
	Version        int64        `json:"version"`
	Conversation   string       `json:"conversationId"`
	Direction      Direction    `json:"direction"`
	Number         string       `json:"number"`
	Body           string       `json:"body,omitempty"`
	Status         string       `json:"status"`
	Unread         bool         `json:"unread"`
	Filtered       bool         `json:"filtered"`
	Deleted        bool         `json:"deleted"`
	Filter         Decision     `json:"filter"`
	ModemIndex     int          `json:"modemIndex,omitempty"`
	ProviderID     string       `json:"providerId,omitempty"`
	GatewayID      string       `json:"gatewayId,omitempty"`
	SubscriptionID int          `json:"subscriptionId,omitempty"`
	SIPDelivery    *SIPDelivery `json:"sipDelivery,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
}

type SIPDelivery struct {
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	NextAt    *time.Time `json:"nextAttemptAt,omitempty"`
	ExpiresAt time.Time  `json:"expiresAt"`
	LastError string     `json:"lastError,omitempty"`
}

type Conversation struct {
	ID          string    `json:"id"`
	Number      string    `json:"number"`
	DisplayName string    `json:"displayName,omitempty"`
	LastBody    string    `json:"lastBody,omitempty"`
	LastAt      time.Time `json:"lastAt"`
	Unread      int       `json:"unread"`
	Filtered    bool      `json:"filtered"`
}

type FilterRule struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Pattern   string    `json:"pattern"`
	Label     string    `json:"label"`
	Category  string    `json:"category"`
	Action    string    `json:"action"`
	Scope     string    `json:"scope"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

type Decision struct {
	Action     string  `json:"action"`
	Label      string  `json:"label,omitempty"`
	Category   string  `json:"category,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Source     string  `json:"source,omitempty"`
	RuleID     string  `json:"ruleId,omitempty"`
	Reason     string  `json:"reason,omitempty"`
}

type Recording struct {
	ID        string    `json:"id"`
	CallID    string    `json:"callId"`
	Path      string    `json:"-"`
	FileName  string    `json:"fileName"`
	Duration  int64     `json:"durationSeconds"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"createdAt"`
	Kind      string    `json:"kind,omitempty"`
}

type Settings struct {
	SMS              bool     `json:"smsEnabled"`
	Calls            bool     `json:"callsEnabled"`
	ShowTestTone     bool     `json:"showTestTone"`
	VoicemailEnabled bool     `json:"voicemailEnabled"`
	VoicemailTimeout int      `json:"voicemailTimeoutSeconds"`
	TelegramEnabled  bool     `json:"telegramEnabled"`
	TelegramChatID   int64    `json:"telegramChatId"`
	TelegramToken    string   `json:"telegramToken,omitempty"`
	SIPEnabled       bool     `json:"sipEnabled"`
	SIPPassword      string   `json:"sipPassword,omitempty"`
	SIPPasswordSeen  bool     `json:"sipPasswordSeen,omitempty"`
	ProviderURL      string   `json:"providerUrl,omitempty"`
	ProviderAPIKey   string   `json:"providerApiKey,omitempty"`
	AutoBlock        []string `json:"autoBlockCategories"`
	Country          string   `json:"country"`
}

type Snapshot struct {
	Sequence      int64           `json:"sequence"`
	Initialized   bool            `json:"initialized"`
	Device        DeviceStatus    `json:"device"`
	ActiveCall    *Call           `json:"activeCall,omitempty"`
	Calls         []*Call         `json:"calls"`
	Messages      []*Message      `json:"messages"`
	Conversations []*Conversation `json:"conversations"`
	Rules         []*FilterRule   `json:"rules"`
	Recordings    []*Recording    `json:"recordings"`
	Settings      Settings        `json:"settings"`
}

type Event struct {
	Sequence  int64     `json:"sequence"`
	Type      string    `json:"type"`
	EntityID  string    `json:"entityId"`
	Version   int64     `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload"`
	Raw       []byte    `json:"-"`
}
