package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Listen                 string
	HTTPListen             string
	TLSListen              string
	TLSCert                string
	TLSKey                 string
	TLSCA                  string
	DataDir                string
	DatabasePath           string
	MasterKeyPath          string
	Recordings             string
	ATPort                 string
	AudioPort              string
	ControlPort            string
	ModemMode              string
	GatewayMode            string
	AndroidSerial          string
	AndroidSubID           string
	AndroidADB             string
	AndroidADBHome         string
	AndroidADBServerSocket string
	AndroidToken           string
	AndroidControl         string
	AndroidAudio           string
	AndroidGateways        []AndroidGateway
	PublicURL              string
	SessionTTL             time.Duration
	SIPListen              string
	SIPAsterisk            string
	SIPTarget              string
	AsteriskConfig         string
}

type AndroidGateway struct {
	ID             string `json:"id"`
	Serial         string `json:"serial"`
	SubscriptionID string `json:"subscriptionId,omitempty"`
	ControlAddr    string `json:"controlAddr,omitempty"`
	AudioAddr      string `json:"audioAddr,omitempty"`
}

func Load() Config {
	data := env("ONSIM_DATA_DIR", "./data")
	hours, _ := strconv.Atoi(env("ONSIM_SESSION_HOURS", "12"))
	legacyMode := env("ONSIM_MODEM_MODE", "auto")
	cfg := Config{
		Listen:                 env("ONSIM_LISTEN", "127.0.0.1:8080"),
		HTTPListen:             env("ONSIM_HTTP_LISTEN", ""),
		TLSListen:              env("ONSIM_TLS_LISTEN", ""),
		TLSCert:                env("ONSIM_TLS_CERT", filepath.Join(data, "tls", "onsim.crt")),
		TLSKey:                 env("ONSIM_TLS_KEY", filepath.Join(data, "tls", "onsim.key")),
		TLSCA:                  env("ONSIM_TLS_CA", filepath.Join(data, "tls", "onsim-ca.crt")),
		DataDir:                data,
		DatabasePath:           filepath.Join(data, "onsim.db"),
		MasterKeyPath:          env("ONSIM_MASTER_KEY", filepath.Join(data, "master.key")),
		Recordings:             filepath.Join(data, "recordings"),
		ATPort:                 env("ONSIM_AT_PORT", "/dev/onsim-at"),
		AudioPort:              env("ONSIM_AUDIO_PORT", "/dev/onsim-audio"),
		ControlPort:            env("ONSIM_CONTROL_PORT", "/dev/onsim-control"),
		ModemMode:              legacyMode,
		GatewayMode:            env("ONSIM_GATEWAY_MODE", legacyMode),
		AndroidSerial:          env("ONSIM_ANDROID_SERIAL", ""),
		AndroidSubID:           env("ONSIM_ANDROID_SUBSCRIPTION_ID", "auto"),
		AndroidADB:             env("ONSIM_ANDROID_ADB", "adb"),
		AndroidADBHome:         env("ONSIM_ANDROID_ADB_HOME", filepath.Join(data, "adb")),
		AndroidADBServerSocket: env("ONSIM_ANDROID_ADB_SERVER_SOCKET", "tcp:127.0.0.1:5038"),
		AndroidToken:           env("ONSIM_ANDROID_TOKEN", filepath.Join(data, "android.token")),
		AndroidControl:         env("ONSIM_ANDROID_CONTROL_ADDR", "127.0.0.1:47100"),
		AndroidAudio:           env("ONSIM_ANDROID_AUDIO_ADDR", "127.0.0.1:47101"),
		PublicURL:              env("ONSIM_PUBLIC_URL", "https://onsim.local"),
		SessionTTL:             time.Duration(hours) * time.Hour,
		SIPListen:              env("ONSIM_SIP_LISTEN", "127.0.0.1:5062"),
		SIPAsterisk:            env("ONSIM_SIP_ASTERISK", "127.0.0.1:5060"),
		SIPTarget:              env("ONSIM_SIP_TARGET", "1001"),
		AsteriskConfig:         env("ONSIM_ASTERISK_CONFIG", filepath.Join(data, "asterisk", "pjsip-generated.conf")),
	}
	if raw := os.Getenv("ONSIM_ANDROID_GATEWAYS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.AndroidGateways)
	}
	if len(cfg.AndroidGateways) == 0 && cfg.AndroidSerial != "" {
		cfg.AndroidGateways = []AndroidGateway{{ID: cfg.AndroidSerial, Serial: cfg.AndroidSerial, SubscriptionID: cfg.AndroidSubID, ControlAddr: cfg.AndroidControl, AudioAddr: cfg.AndroidAudio}}
	}
	return cfg
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
