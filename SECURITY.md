# Security policy

## Reporting a vulnerability

Do not open a public issue for vulnerabilities involving authentication,
temporary call links, command authorization, secret storage, phone/SMS access,
WebRTC, ADB, or SIP.

Use GitHub's private vulnerability reporting feature when it is enabled for the
repository. If it is unavailable, contact the repository owner privately
through their GitHub profile before sharing details.

Include:

- affected revision/version
- provider mode and deployment type
- impact and prerequisites
- minimal reproduction steps
- suggested mitigation, if known

Do not include real tokens, phone/SIM identifiers, messages, recordings, or
private keys. Use synthetic values.

## Supported versions

Until tagged releases are published, only the latest `main` revision receives
security fixes.

## Deployment responsibility

onSIM controls telephony hardware and stores sensitive communications. Operators
must use trusted HTTPS, restrict network access, secure the Android/modem USB
connection, protect the data directory, and comply with applicable recording
and privacy law. See [docs/SECURITY.md](docs/SECURITY.md).
