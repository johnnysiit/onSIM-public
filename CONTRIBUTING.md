# Contributing

Thank you for improving onSIM.

## Before opening an issue

- Search existing issues.
- Use mock mode to reproduce software-only problems.
- For hardware reports, include the phone/modem model, firmware/build,
  operating system, provider mode, and relevant redacted logs.
- Remove phone numbers, SIM identifiers, device serials, IP addresses, tokens,
  recordings, and message content.

Security vulnerabilities must follow [SECURITY.md](SECURITY.md), not a public
issue.

## Development workflow

1. Fork the repository and create a focused branch.
2. Keep real runtime data outside the checkout.
3. Add or update automated tests.
4. Run:

   ```sh
   gofmt -w <changed-go-files>
   go test -tags libsqlite3 ./...
   cd web && npm ci && npm run build
   git diff --check
   ```

5. Describe user-visible behavior, compatibility, and verification in the PR.

Do not test a contribution by calling or messaging a number you do not own or
have permission to use.

## Design rules

- Keep Android, SIM7600, and mock behavior behind the provider abstraction.
- Treat call/SMS commands as idempotent where retries are possible.
- Preserve explicit degraded states; never report unavailable hardware as healthy.
- Keep secrets write-only in normal APIs and redact identifiers in logs.
- Maintain mobile, desktop, and installed-PWA behavior.
- Avoid destructive database migrations without backup and rollback guidance.

## Generated files

Do not commit `web/dist`, `webui/dist` bundles, APKs, firmware, signing
material, databases, or recordings. The build creates required artifacts.
