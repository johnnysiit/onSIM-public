FROM node:22-bookworm AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.24-bookworm AS go-build
RUN apt-get update && apt-get install -y --no-install-recommends espeak-ng gcc pkg-config libsqlite3-dev libopus-dev libopusfile-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist /src/webui/dist
ARG ONSIM_VERSION=dev
ARG ONSIM_REVISION=unknown
ARG ONSIM_BUILD_TIME=unknown
RUN CGO_ENABLED=1 go test -tags libsqlite3 ./...
RUN CGO_ENABLED=1 go build -tags libsqlite3 -trimpath \
    -ldflags="-s -w -X onsim/internal/buildinfo.Version=${ONSIM_VERSION} -X onsim/internal/buildinfo.Revision=${ONSIM_REVISION} -X onsim/internal/buildinfo.BuildTime=${ONSIM_BUILD_TIME}" \
    -o /out/onsim ./cmd/onsim
RUN CGO_ENABLED=1 go build -tags libsqlite3 -trimpath -ldflags="-s -w" \
    -o /out/onsim-media-probe ./cmd/onsim-media-probe

FROM debian:bookworm AS asterisk-build
ARG ASTERISK_VERSION=22.10.1
ARG ASTERISK_SHA256=0953564c44fa49827f3c9d70ca6e80db83828c9848440852c6be44c961855353
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential ca-certificates curl pkg-config libedit-dev libjansson-dev \
    libsqlite3-dev libssl-dev libxml2-dev libncurses-dev uuid-dev && \
    rm -rf /var/lib/apt/lists/*
WORKDIR /build
RUN curl -fL --retry 5 "https://downloads.asterisk.org/pub/telephony/asterisk/releases/asterisk-${ASTERISK_VERSION}.tar.gz" -o asterisk.tar.gz && \
    echo "${ASTERISK_SHA256}  asterisk.tar.gz" | sha256sum -c - && \
    tar -xzf asterisk.tar.gz
WORKDIR /build/asterisk-22.10.1
RUN ./configure --with-pjproject-bundled --with-jansson-bundled --disable-xmldoc && \
    make menuselect.makeopts && \
    menuselect/menuselect --disable BUILD_NATIVE menuselect.makeopts && \
    make -j2 && make DESTDIR=/asterisk-root install

FROM debian:bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
    adb ca-certificates curl espeak-ng libcap2 libedit2 libjansson4 libsqlite3-0 libssl3 \
    libxml2 libncurses6 libuuid1 libopus0 opus-tools tini util-linux && \
    rm -rf /var/lib/apt/lists/*
COPY --from=asterisk-build /asterisk-root/ /
COPY --from=go-build /out/onsim /usr/local/bin/onsim
COPY --from=go-build /out/onsim-media-probe /usr/local/bin/onsim-media-probe
COPY deploy/asterisk/asterisk-container.conf /etc/asterisk/asterisk.conf
COPY deploy/asterisk/pjsip.conf deploy/asterisk/extensions.conf deploy/asterisk/rtp.conf deploy/asterisk/modules.conf /etc/asterisk/
COPY deploy/containers/entrypoint.sh /usr/local/bin/onsim-entrypoint
RUN chmod 0755 /usr/local/bin/onsim-entrypoint && \
    mkdir -p /var/lib/onsim/asterisk /var/lib/asterisk /var/log/asterisk /var/spool/asterisk /run/asterisk
ENTRYPOINT ["/usr/bin/tini","--","/usr/local/bin/onsim-entrypoint"]
CMD ["onsim"]
