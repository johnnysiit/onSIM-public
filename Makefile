.PHONY: all web build test run clean

all: build

web:
	cd web && npm ci && npm run build
	rm -rf webui/dist
	cp -a web/dist webui/dist

build: web
	CGO_ENABLED=1 go build -tags libsqlite3 -trimpath -ldflags="-s -w" -o onsim ./cmd/onsim

test:
	go test -tags libsqlite3 ./...
	cd web && npm run typecheck

run:
	ONSIM_MODEM_MODE=mock ONSIM_LISTEN=127.0.0.1:8080 go run ./cmd/onsim

clean:
	rm -rf onsim web/dist webui/dist
