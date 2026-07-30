#!/system/bin/sh

MODDIR=${0%/*}

while true; do
  # Android toybox netcat executes one authenticated handler per local
  # connection and keeps listening after the handler exits.
  nc -s 127.0.0.1 -p 47601 -L "$MODDIR/uplink-handler.sh" -l
  sleep 1
done
