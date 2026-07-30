# Configuration reference

[简体中文](CONFIGURATION.zh-CN.md)

Production configuration lives in `~/.config/onsim/onsim.env` with mode `0600`.
Do not create a real `.env` inside the repository.

| Variable | Default | Description |
| --- | --- | --- |
| `ONSIM_HTTP_LISTEN` | empty | Health, CA download, and HTTPS redirect listener |
| `ONSIM_TLS_LISTEN` | empty | Direct HTTPS listener |
| `ONSIM_LISTEN` | `127.0.0.1:8080` | Legacy/single HTTP listener |
| `ONSIM_DATA_DIR` | `./data` | Database, recordings, keys, and runtime state |
| `ONSIM_MASTER_KEY` | `<data>/master.key` | Credential-encryption master key |
| `ONSIM_GATEWAY_MODE` | legacy mode | `android`, `sim7600`, or `mock` |
| `ONSIM_MODEM_MODE` | `auto` | Legacy provider selection; `mock` is development-only |
| `ONSIM_ANDROID_SERIAL` | empty | Single-phone ADB serial |
| `ONSIM_ANDROID_GATEWAYS` | empty | JSON array for multiple Android gateways |
| `ONSIM_ANDROID_SUBSCRIPTION_ID` | `auto` | Legacy single-phone subscription selection |
| `ONSIM_ANDROID_ADB_SERVER_SOCKET` | `tcp:127.0.0.1:5038` | Container-private ADB daemon |
| `ONSIM_ANDROID_CONTROL_ADDR` | `127.0.0.1:47100` | First Companion control forward |
| `ONSIM_ANDROID_AUDIO_ADDR` | `127.0.0.1:47101` | First Companion audio forward |
| `ONSIM_AT_PORT` | `/dev/onsim-at` | SIM7600 AT command port |
| `ONSIM_CONTROL_PORT` | `/dev/onsim-control` | SIM7600 recovery/control port |
| `ONSIM_AUDIO_PORT` | `/dev/onsim-audio` | SIM7600 PCM port |
| `ONSIM_PUBLIC_URL` | `https://onsim.local` | Base URL placed in Telegram call buttons |
| `ONSIM_SESSION_HOURS` | `12` | Administrator session lifetime |
| `ONSIM_SIP_LISTEN` | `127.0.0.1:5062` | onSIM local PJSIP listener |
| `ONSIM_SIP_ASTERISK` | `127.0.0.1:5060` | Asterisk endpoint |
| `ONSIM_SIP_TARGET` | `1001` | Extension rung for cellular calls |
| `ONSIM_ASTERISK_CONFIG` | under data dir | Generated Asterisk credential include |

Example multi-phone configuration:

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_GATEWAYS=[{"id":"office-phone","serial":"REPLACE_WITH_FIRST_ADB_SERIAL"},{"id":"backup-phone","serial":"REPLACE_WITH_SECOND_ADB_SERIAL","subscriptionId":2}]
```

Feature switches, Telegram token/chat ID, SIP credentials, caller-identification
provider, and filter rules are managed in the authenticated Settings page.
Secret values returned to the browser are blanked; leaving a secret input blank
preserves the stored value.

## Public URLs and proxies

`ONSIM_PUBLIC_URL` must be the complete external HTTPS origin, including a
non-default port. Reverse proxies must preserve WebSocket upgrades and should
not cache API responses. Microphone access requires a browser-trusted secure
context.

Do not expose the private ADB daemon, Companion forwards, Asterisk control
socket, SQLite files, or recording directory.
