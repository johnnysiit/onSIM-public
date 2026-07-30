#!/bin/sh
set -eu

VERSION=22.10.1
ARCHIVE="asterisk-${VERSION}.tar.gz"
SHA256=0953564c44fa49827f3c9d70ca6e80db83828c9848440852c6be44c961855353
BASE_URL=https://downloads.asterisk.org/pub/telephony/asterisk/releases
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
RUNTIME_USER=${ONSIM_RUNTIME_USER:-${SUDO_USER:-onsim}}

if [ "$(id -u)" -ne 0 ]; then
  echo "Run with sudo: sudo scripts/install-asterisk.sh" >&2
  exit 1
fi
if [ ! -d "$PROJECT_DIR/deploy/asterisk" ]; then
  echo "Asterisk templates not found" >&2
  exit 1
fi

apt-get update
apt-get install -y build-essential curl ca-certificates pkg-config \
  libedit-dev libjansson-dev libsqlite3-dev libssl-dev libxml2-dev \
  libncurses-dev uuid-dev

if ! id asterisk >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/asterisk --create-home \
    --shell /usr/sbin/nologin asterisk
fi

if ! command -v asterisk >/dev/null 2>&1; then
  install -d -o root -g root -m 0755 /var/cache/onsim
  BUILD_DIR="/var/cache/onsim/build-asterisk-$VERSION"
  curl -fL --retry 10 --retry-delay 3 --retry-all-errors -C - \
    "$BASE_URL/$ARCHIVE" -o "/var/cache/onsim/$ARCHIVE"
  echo "$SHA256  /var/cache/onsim/$ARCHIVE" | sha256sum -c -
  if [ ! -d "$BUILD_DIR/asterisk-$VERSION" ]; then
    install -d -o root -g root -m 0755 "$BUILD_DIR"
    tar -xzf "/var/cache/onsim/$ARCHIVE" -C "$BUILD_DIR"
  fi
  cd "$BUILD_DIR/asterisk-$VERSION"
  if [ ! -f makeopts ]; then
    ./configure --with-pjproject-bundled --with-jansson-bundled --disable-xmldoc
  fi
  make menuselect.makeopts
  menuselect/menuselect --disable BUILD_NATIVE menuselect.makeopts
  make -j1
  make install
fi

install -d -o asterisk -g asterisk -m 0750 \
  /var/lib/asterisk /var/log/asterisk /var/spool/asterisk /run/asterisk
install -d -o "$RUNTIME_USER" -g asterisk -m 2750 /var/lib/onsim/asterisk
if [ ! -e /var/lib/onsim/asterisk/pjsip-generated.conf ]; then
  install -o "$RUNTIME_USER" -g asterisk -m 0640 /dev/null /var/lib/onsim/asterisk/pjsip-generated.conf
fi
install -d -o root -g asterisk -m 0755 /etc/asterisk
install -o root -g asterisk -m 0640 "$PROJECT_DIR/deploy/asterisk/asterisk.conf" /etc/asterisk/asterisk.conf
install -o root -g asterisk -m 0640 "$PROJECT_DIR/deploy/asterisk/pjsip.conf" /etc/asterisk/pjsip.conf
install -o root -g asterisk -m 0640 "$PROJECT_DIR/deploy/asterisk/extensions.conf" /etc/asterisk/extensions.conf
install -o root -g asterisk -m 0640 "$PROJECT_DIR/deploy/asterisk/rtp.conf" /etc/asterisk/rtp.conf
install -o root -g asterisk -m 0640 "$PROJECT_DIR/deploy/asterisk/modules.conf" /etc/asterisk/modules.conf
install -o root -g root -m 0644 "$PROJECT_DIR/deploy/asterisk/asterisk.service" /etc/systemd/system/asterisk.service

if id "$RUNTIME_USER" >/dev/null 2>&1; then
  usermod -a -G asterisk "$RUNTIME_USER"
fi
systemctl daemon-reload
systemctl enable asterisk
systemctl restart asterisk

echo "Asterisk $VERSION installed."
echo "Set ONSIM_ASTERISK_CONFIG=/var/lib/onsim/asterisk/pjsip-generated.conf and restart onSIM."
