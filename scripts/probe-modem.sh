#!/bin/sh
set -eu

echo "USB devices:"
lsusb | grep -Ei 'simcom|qualcomm|1e0e' || true
echo
echo "Candidate serial ports:"
for dev in /dev/ttyUSB* /dev/onsim-*; do
  [ -e "$dev" ] || continue
  printf '%s -> ' "$dev"
  udevadm info -q property -n "$dev" 2>/dev/null | sed -n 's/^ID_USB_INTERFACE_NUM=/interface /p' | head -1
done
echo
echo "Stable links:"
ls -l /dev/onsim-at /dev/onsim-audio 2>/dev/null || true
