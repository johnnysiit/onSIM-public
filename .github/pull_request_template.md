## Summary

<!-- What changed and why? -->

## Verification

- [ ] `go test -tags libsqlite3 ./...`
- [ ] `cd web && npm ci && npm run build`
- [ ] Mobile and desktop UI checked when applicable
- [ ] No real calls/SMS were made without explicit authorization

## Privacy and security

- [ ] No phone/SIM/device identifiers, IP addresses, credentials, private logs,
      messages, recordings, firmware, databases, or signing material are included
- [ ] New configuration uses placeholders and is documented
- [ ] Security impact and migration/rollback needs are described
