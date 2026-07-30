# Production installation and operations

This guide installs onSIM as two rootless Podman containers:

- `onsim`: Web/API, providers, media, Telegram, and persistence
- `onsim-asterisk`: optional SIP bridge

The host runs only Podman user services, udev rules, and USB permissions.

## 1. Requirements

- Debian 12/13, Raspberry Pi OS, or another system with rootless Podman
- `podman`, `crun`, `git`, `curl`, `openssl`, and `systemd --user`
- A user allowed to run lingering services
- For Android: USB debugging, a compatible rooted phone, and the Companion
- For SIM7600: recognized serial/PCM interfaces and adequate USB power

Do not run the application as root and do not use a privileged container.

## 2. Clone and inspect

```sh
git clone https://github.com/johnnysiit/onSIM-public.git onSIM
cd onSIM
git status --short
```

Review scripts before running them. Production state must remain outside the
repository.

## 3. Select the provider

Android:

```sh
adb devices -l
export ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
```

Then follow [ANDROID_GATEWAY.md](ANDROID_GATEWAY.md) before expecting a healthy
audio status.

SIM7600:

```sh
export ONSIM_GATEWAY_MODE=sim7600
```

Then complete the hardware gate in [SIM7600.md](SIM7600.md).

## 4. Install

Run as the target desktop/login user, not with `sudo`:

```sh
scripts/install-containers.sh
```

The script uses `sudo` only for udev rules and group membership. It creates:

```text
~/.config/onsim/onsim.env
~/.config/containers/systemd/onsim.container
~/.config/containers/systemd/onsim-asterisk.container
~/.local/share/onsim/
```

Set the provider and serial in the private environment file:

```sh
chmod 600 ~/.config/onsim/onsim.env
editor ~/.config/onsim/onsim.env
systemctl --user daemon-reload
systemctl --user restart onsim-asterisk.service onsim.service
```

For a single Android phone:

```dotenv
ONSIM_GATEWAY_MODE=android
ONSIM_ANDROID_SERIAL=REPLACE_WITH_ADB_SERIAL
ONSIM_ANDROID_SUBSCRIPTION_ID=auto
```

For more than one phone, see the JSON example in
[CONFIGURATION.md](CONFIGURATION.md).

## 5. First login and HTTPS

Port `8989` provides health, CA download, and HTTPS redirection. Port `9443`
serves the application.

1. Download `http://<host-ip>:8989/onsim-ca.crt`.
2. Install the CA only on devices you control.
3. Open `https://<host-ip>:9443`.
4. Create an administrator password of at least 10 characters.
5. Grant microphone and notification permissions when required.

For internet access, prefer a VPN or terminate TLS with a publicly trusted
certificate. Set `ONSIM_PUBLIC_URL` to the exact trusted HTTPS URL used by
Telegram. A private CA must be installed in the phone's trust store; a hostname
mismatch cannot be bypassed safely.

## 6. Install the PWA

- Android Chrome: **Settings → Mobile app → Install onSIM**
- iOS Safari: **Share → Add to Home Screen**

The Web app requires a secure context for microphone access. Telegram's in-app
browser should hand the temporary page to the system browser.

## 7. Verify

```sh
systemctl --user status onsim.service onsim-asterisk.service
podman ps
curl -fsS http://127.0.0.1:8989/healthz
curl -kfsS https://127.0.0.1:9443/healthz
journalctl --user -u onsim.service -n 100 --no-pager
```

Android:

```sh
podman exec onsim adb -P 5038 devices -l
podman exec onsim adb -P 5038 forward --list
```

Do not declare production ready until outbound/inbound SMS and a bidirectional
30-second call have been tested with a number you own.

## 8. Upgrade

Stop placing calls, verify that no call is active, then:

```sh
git pull --ff-only
scripts/install-containers.sh
```

The image is replaced; `~/.local/share/onsim` is preserved. Keep the Android
APK signing key so Companion upgrades retain the same signature.

## 9. Backup and restore

Back up together:

- SQLite database and WAL files
- recordings
- `master.key`
- Android signing key and signing environment
- TLS CA if clients trust it

Never restore encrypted database settings without the matching `master.key`.
Stop the service or use `scripts/backup.sh` to obtain a consistent SQLite
backup.

## 10. Uninstall

Stop the user services first:

```sh
systemctl --user disable --now onsim.service onsim-asterisk.service
```

Removing the containers or image does not remove `~/.local/share/onsim`.
Delete persistent data only after making and verifying a backup.
