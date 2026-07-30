#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
RUNTIME_USER=${SUDO_USER:-$(id -un)}
RUNTIME_UID=$(id -u "$RUNTIME_USER")
RUNTIME_HOME=$(getent passwd "$RUNTIME_USER" | cut -d: -f6)

if [ "$(id -u)" -eq 0 ]; then
  echo "Run this script as the target user; it invokes sudo only for host device setup." >&2
  exit 1
fi
if ! command -v podman >/dev/null 2>&1; then
  echo "Podman is required." >&2
  exit 1
fi
if ! command -v crun >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update
    sudo apt-get install -y crun
  else
    echo "crun is required for secure rootless modem access (keep-groups)." >&2
    exit 1
  fi
fi

sudo install -m 0644 "$PROJECT_DIR/deploy/99-onsim.rules" /etc/udev/rules.d/99-onsim.rules
sudo usermod -a -G plugdev "$RUNTIME_USER"
sudo udevadm control --reload-rules
sudo udevadm trigger --subsystem-match=tty
sudo udevadm trigger --subsystem-match=usb
sudo loginctl enable-linger "$RUNTIME_USER"
# usermod only affects new login sessions. Give the current user's primary
# group temporary access so the first container start works immediately; the
# persistent plugdev membership takes over after the next login/reboot.
if ! id -G "$RUNTIME_USER" | tr ' ' '\n' | grep -qx "$(getent group plugdev | cut -d: -f3)"; then
  for device in /dev/onsim-at /dev/onsim-audio /dev/onsim-control; do
    if [ -e "$device" ]; then
      sudo chgrp "$(id -gn "$RUNTIME_USER")" "$device"
    fi
  done
fi

install -d -m 0700 "$RUNTIME_HOME/.config/onsim" "$RUNTIME_HOME/.local/share/onsim"
"$PROJECT_DIR/scripts/generate-tls.sh" "$RUNTIME_HOME/.local/share/onsim" "$(hostname -I | awk '{print $1}')"
install -d -m 0755 "$RUNTIME_HOME/.config/containers/systemd"
if [ ! -e "$RUNTIME_HOME/.config/onsim/onsim.env" ]; then
  install -m 0600 "$PROJECT_DIR/deploy/containers/onsim.env.example" "$RUNTIME_HOME/.config/onsim/onsim.env"
fi
while IFS= read -r env_line; do
  case "$env_line" in
    ''|\#*) continue ;;
  esac
  env_key=${env_line%%=*}
  if ! grep -q "^${env_key}=" "$RUNTIME_HOME/.config/onsim/onsim.env"; then
    printf '%s\n' "$env_line" >>"$RUNTIME_HOME/.config/onsim/onsim.env"
  fi
done <"$PROJECT_DIR/deploy/containers/onsim.env.example"
LAN_IP=$(hostname -I | awk '{print $1}')
if [ -n "$LAN_IP" ]; then
  sed -i "s|^ONSIM_PUBLIC_URL=.*|ONSIM_PUBLIC_URL=https://$LAN_IP:9443|" "$RUNTIME_HOME/.config/onsim/onsim.env"
fi
install -m 0644 "$PROJECT_DIR/deploy/containers/"*.container "$PROJECT_DIR/deploy/containers/"*.volume "$RUNTIME_HOME/.config/containers/systemd/"

REVISION=$(git -C "$PROJECT_DIR" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
podman build -f "$PROJECT_DIR/Containerfile" \
  --build-arg "ONSIM_VERSION=0.2.0" \
  --build-arg "ONSIM_REVISION=$REVISION" \
  --build-arg "ONSIM_BUILD_TIME=$BUILD_TIME" \
  -t localhost/onsim:latest "$PROJECT_DIR"

# The Android provider owns an isolated ADB daemon on port 5038. Stop the
# conventional host daemon so no gateway control process remains on the host.
if command -v adb >/dev/null 2>&1; then
  ADB_SERVER_SOCKET=tcp:127.0.0.1:5037 adb kill-server >/dev/null 2>&1 || true
fi
systemctl --user daemon-reload
systemctl --user start onsim.service
echo "onSIM containers installed. Import http://$(hostname -I | awk '{print $1}'):8989/onsim-ca.crt"
echo "Then open https://$(hostname -I | awk '{print $1}'):9443"
echo "If device access is denied, log out and back in once so plugdev membership takes effect."
