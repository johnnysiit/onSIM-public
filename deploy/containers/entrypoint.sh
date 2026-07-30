#!/bin/sh
set -eu

mkdir -p /var/lib/onsim/asterisk /var/lib/asterisk /var/log/asterisk \
  /var/spool/asterisk /run/asterisk

case "${1:-onsim}" in
  asterisk)
    if [ ! -e /var/lib/onsim/asterisk/pjsip-generated.conf ]; then
      : > /var/lib/onsim/asterisk/pjsip-generated.conf
      chmod 0600 /var/lib/onsim/asterisk/pjsip-generated.conf
    fi
    exec /usr/sbin/asterisk -f -C /etc/asterisk/asterisk.conf
    ;;
  onsim)
    exec /usr/local/bin/onsim
    ;;
  *)
    exec "$@"
    ;;
esac
