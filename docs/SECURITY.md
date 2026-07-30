# Security and privacy guide

## Data stored by onSIM

Depending on enabled features, the data directory can contain:

- administrator password hash and sessions
- call history and SMS content
- phone numbers, SIM/device metadata, and filter rules
- Telegram/provider/SIP credentials encrypted with `master.key`
- call recordings and voicemail
- TLS CA/private key, ADB key, Companion token, and Android signing material

Treat the entire data directory as sensitive.

## Repository hygiene

Never commit:

- `.env` or production environment files
- SQLite databases, WAL/SHM files, logs, recordings, or backups
- real phone numbers, IMEI/IMSI/ICCID, device serials, or public deployment IPs
- Telegram tokens, chat IDs, provider keys, SIP passwords, or master keys
- ADB private keys, TLS private keys, APK keystores, firmware, OTAs, or boot images

Before publishing:

```sh
git status --short --ignored
git grep -n -I -E '(BEGIN .*PRIVATE KEY|bot[0-9]+:|/home/[^/]+)'
git log --all --format='%h %an <%ae>'
```

Use a dedicated secret scanner such as Gitleaks on the complete Git history.
Deleting a file in a new commit does not remove it from old commits. Rotate any
credential that ever entered Git.

## Network exposure

- Prefer LAN or VPN access.
- Use browser-trusted HTTPS for WebRTC microphone access.
- Do not publish ADB, Asterisk SIP/RTP, control sockets, or database ports.
- Restrict Telegram to the configured chat ID.
- Use a long unique administrator password.
- Keep temporary call links short-lived and send them only through trusted chats.

A private CA is suitable only for managed devices where it is explicitly
installed. Public users need a publicly trusted certificate and a hostname/IP
included in that certificate.

## Phone and modem

The Android Companion has privileged phone, SMS, and audio access. Dedicate the
phone to the gateway, keep USB debugging physically controlled, and do not
authorize unknown computers. Protect recovery and bootloader access.

Do not alter device identity values to bypass carrier controls. SIM7600 AT
ports expose sensitive capabilities and must not be accessible to untrusted
users or containers.

## Recording and retention

Recording and message retention laws differ by jurisdiction. Provide required
notice/consent, restrict access, define a retention period, and securely delete
exports and backups. onSIM does not make a deployment legally compliant.

## Incident response

If a deployment or repository leaks:

1. Disable external access.
2. Revoke Telegram/provider credentials and reset SIP/admin passwords.
3. Replace `master.key` only with a planned settings migration.
4. Rotate TLS CA, ADB authorization, Companion token, and APK signing key as applicable.
5. Rewrite Git history if sensitive blobs were committed.
6. Invalidate caches/forks where possible and document the incident.
