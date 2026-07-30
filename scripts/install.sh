#!/bin/sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: sudo scripts/install.sh" >&2
  exit 1
fi

apt-get update
apt-get install -y libsqlite3-0 libopus0 libopusfile0 opus-tools sqlite3

install -d -m 0750 -o root -g root /etc/onsim
getent group onsim >/dev/null 2>&1 || groupadd --system onsim
id onsim >/dev/null 2>&1 || useradd --system --gid onsim --home-dir /var/lib/onsim --shell /usr/sbin/nologin onsim
usermod -a -G dialout onsim
install -d -m 0750 -o onsim -g onsim /var/lib/onsim /var/lib/onsim/recordings
if [ ! -f /etc/onsim/master.key ]; then
  dd if=/dev/urandom of=/etc/onsim/master.key bs=32 count=1 status=none
fi
chown onsim:onsim /etc/onsim/master.key
chmod 0600 /etc/onsim/master.key
install -m 0755 onsim /usr/local/bin/onsim
install -m 0644 deploy/99-onsim.rules /etc/udev/rules.d/99-onsim.rules
install -m 0644 deploy/onsim.service /etc/systemd/system/onsim.service
install -m 0755 scripts/backup.sh /usr/local/libexec/onsim-backup
install -m 0644 deploy/onsim-backup.service /etc/systemd/system/onsim-backup.service
install -m 0644 deploy/onsim-backup.timer /etc/systemd/system/onsim-backup.timer
if [ ! -f /etc/onsim/onsim.env ]; then install -m 0600 deploy/onsim.env.example /etc/onsim/onsim.env; fi
udevadm control --reload-rules
udevadm trigger
systemctl daemon-reload
systemctl enable --now onsim onsim-backup.timer
echo "Installed. Edit /etc/onsim/onsim.env, then run: systemctl restart onsim"
