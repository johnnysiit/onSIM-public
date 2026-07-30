# Development and testing

## Native dependencies

```sh
sudo apt-get install -y golang-go nodejs npm gcc pkg-config \
  libsqlite3-dev libopus-dev libopusfile-dev opus-tools
```

## Build and test

```sh
make web
make build
make test
```

`make web` creates `webui/dist`, which is intentionally not committed.
The production Containerfile performs the frontend build and runs all Go tests.

## Mock mode

```sh
ONSIM_GATEWAY_MODE=mock \
ONSIM_MODEM_MODE=mock \
ONSIM_LISTEN=127.0.0.1:8080 \
ONSIM_DATA_DIR="$(mktemp -d)" \
go run ./cmd/onsim
```

Use fictional test numbers such as `+8613800138000`. Tests must never contact a
real carrier, Telegram chat, or production database.

## Frontend

```sh
cd web
npm ci
npm run dev
npm run build
```

Vite proxies API requests to the local backend. Verify both a narrow mobile
viewport and desktop layout. PWA installation requires a production build and
a secure context (localhost also qualifies).

## Test expectations

- Go packages: `go test -tags libsqlite3 ./...`
- Frontend: `npm run build` (includes Vue/TypeScript checking)
- Formatting: `gofmt` and `git diff --check`
- Provider behavior: mock/fake transport; no real calls or SMS
- Telegram: local mock Bot API
- Media: deterministic PCM frames and state transitions

Hardware acceptance tests are separate from automated tests and must use
numbers/accounts owned by the operator.

## Pull requests

Keep provider-specific behavior behind the gateway abstraction. Add tests for
protocol changes, state transitions, reconnect handling, and migrations.
Never include captured production logs without redacting phone/SIM/device
identifiers and tokens.
