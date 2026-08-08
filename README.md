# onSIM

[简体中文](README.zh-CN.md)

onSIM is a self-hosted phone and SMS gateway. It connects an Android phone or
a supported SIM7600 USB modem to a browser, Telegram, and an optional SIP
client—without moving call history, messages, or recordings to a hosted
service.

> This project controls real telephone and SMS services. Read the
> [security guide](docs/SECURITY.md) before exposing it outside a trusted
> network. It is not an emergency-calling or life-safety system.

## Gallary 
<img width="1865" height="785" alt="image" src="https://github.com/user-attachments/assets/66a14d48-638e-4b39-a614-4ebf0f4ccd54" />
<img width="1766" height="1317" alt="image" src="https://github.com/user-attachments/assets/47e6a36a-5d15-433c-abf6-70034f4a3b2c" />
<img width="1315" height="1295" alt="image" src="https://github.com/user-attachments/assets/97b5d2eb-3c38-4d66-93b7-7ac7f49da94e" />
<img width="1074" height="1021" alt="image" src="https://github.com/user-attachments/assets/c746ba5a-4151-4310-8a02-018b35806ebe" />
<img width="526" height="461" alt="image" src="https://github.com/user-attachments/assets/1ed32b6f-5dac-44db-bc56-5a6e48a471b9" />
<img width="1772" height="900" alt="image" src="https://github.com/user-attachments/assets/3b335637-0560-4dbb-90cb-86cb1c2a41ba" />



## Highlights

- Responsive installable PWA for calls, SMS, voicemail, device status, and settings
- Incoming/outgoing calls, DTMF, mute, hold, manual recording, and call history
- Browser audio over WebRTC with a persistent cellular audio bridge
- SMS conversations, Unicode/long-message support, delivery state, and bulk read
- Configurable voicemail with a bilingual prompt, custom recording, playback, and deletion
- Telegram call/SMS workflows, confirmations, temporary call pages, and inline controls
- Optional Asterisk/PJSIP integration for SIP softphones
- Dual-SIM and multi-phone route selection
- Local allow/block rules and an optional HTTPS caller-identification provider
- Rootless Podman deployment, encrypted credentials, SQLite persistence, and health checks
- `mock` provider for development without telephony hardware

## Gateway support

| Provider | Status | Notes |
| --- | --- | --- |
| Android | Primary target | Requires USB debugging, root/Magisk, the onSIM Companion, and a compatible in-call audio route |
| SIM7600 | Hardware-dependent | Calls/SMS depend on modem firmware, carrier registration, VoLTE/fallback support, and USB PCM |
| Mock | Development | Exercises the Web/API without placing real calls or messages |

The Android implementation was developed against a OnePlus 5 running its
matching Android 10 firmware. Other phones may require device-specific audio
policy work. Do not flash images from a different build.

## Quick start with mock mode

The fastest safe way to explore onSIM is a local mock container:

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
podman build -t localhost/onsim:dev .
mkdir -p .local-data
podman run --rm --name onsim-dev \
  --network host \
  -e ONSIM_GATEWAY_MODE=mock \
  -e ONSIM_LISTEN=127.0.0.1:8989 \
  -e ONSIM_DATA_DIR=/var/lib/onsim \
  -v "$PWD/.local-data:/var/lib/onsim:Z" \
  localhost/onsim:dev
```

Open `http://127.0.0.1:8989`, create the administrator password, and use only
fictional numbers. Mock mode does not contact a carrier.

## Production installation

The recommended deployment is rootless Podman with Quadlet:

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
scripts/install-containers.sh
```

The installer builds one immutable image and runs onSIM and Asterisk as
separate user containers. Persistent state is stored outside the repository:

- `~/.config/onsim/onsim.env` — deployment configuration
- `~/.local/share/onsim` — database, recordings, certificates, ADB keys, and secrets

Continue with the guide for your provider:

- [Production installation](docs/INSTALL.md)
- [Android gateway setup](docs/ANDROID_GATEWAY.md)
- [SIM7600 setup](docs/SIM7600.md)

After installation, import the local CA from
`http://<host-ip>:8989/onsim-ca.crt`, then open
`https://<host-ip>:9443`. A publicly exposed installation should use a
publicly trusted HTTPS certificate or a trusted private network such as a VPN.

## Mobile app

onSIM is a Progressive Web App:

- Android Chrome: open **Settings → Mobile app → Install onSIM**
- iPhone/iPad Safari: choose **Share → Add to Home Screen**
- Telegram in-app browser: open the temporary call page in the system browser

The standalone app includes shortcuts for dialing, messages, and settings.
Microphone access still follows the browser/OS permission model.

## Documentation

- [Installation and operations](docs/INSTALL.md)
- [Android gateway](docs/ANDROID_GATEWAY.md)
- [SIM7600 gateway](docs/SIM7600.md)
- [Configuration reference](docs/CONFIGURATION.md)
- [Security and privacy](docs/SECURITY.md)
- [Development and testing](docs/DEVELOPMENT.md)
- [Contributing](CONTRIBUTING.md)
- [Security reports](SECURITY.md)

## Architecture

```text
Browser PWA ── HTTPS/WebRTC ─┐
Telegram Bot ── Bot API ─────┼── onSIM (Go)
SIP client ──── Asterisk ────┘      │
                                    ├── SQLite + recordings
                                    └── Gateway provider
                                         ├── Android Companion over private ADB forwards
                                         ├── SIM7600 AT + USB PCM
                                         └── Mock provider
```

The Go service owns call state, media ownership, SMS state, filtering,
authentication, Telegram interactions, and persistence. The Vue frontend uses
REST commands plus WebSocket invalidation events. Sensitive settings are
encrypted with a local master key and are never returned by the normal state
API.

## Development

```sh
sudo apt-get install -y golang-go nodejs npm gcc pkg-config \
  libsqlite3-dev libopus-dev libopusfile-dev opus-tools
make build
make test
```

The Containerfile also runs the complete Go test suite while building the
production image. See [development.md](docs/DEVELOPMENT.md) for mock mode,
frontend development, and test conventions.

## Project status and limitations

- Cellular behavior varies by phone firmware, modem firmware, carrier, and SIM subscription.
- Android call audio requires privileged system integration; a normal unrooted app is insufficient.
- Temporary Telegram pages require trusted HTTPS for microphone access.
- Browser autoplay and microphone permissions cannot be bypassed by application code.
- Call recording and message retention may be regulated in your jurisdiction.

No firmware, boot images, SIM credentials, APK signing keys, ADB keys, database,
recordings, Telegram tokens, or TLS private keys belong in this repository.

## License

onSIM is available under the [MIT License](LICENSE).
