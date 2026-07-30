#!/system/bin/sh

MODDIR=${0%/*}
PREFS=/data/data/io.onsim.gateway/shared_prefs/gateway.xml

supplied=
session_log=/data/local/tmp/onsim-uplink.$$.log
IFS= read -r supplied
expected=$(sed -n 's:.*<string name="token">\([^<]*\)</string>.*:\1:p' "$PREFS" 2>/dev/null)
if [ -z "$expected" ] || [ "$supplied" != "$expected" ]; then
  echo AUTH_FAILED
  exit 1
fi

cleanup() {
  rm -f "$session_log"
  tinymix 1729 0 >/dev/null 2>&1
}
trap cleanup EXIT INT TERM

if ! tinymix 1729 1 >/dev/null 2>&1; then
  echo MIXER_FAILED
  exit 1
fi
case "$(tinymix 1729 2>/dev/null)" in
  *"On"*) ;;
  *)
    echo MIXER_NOT_ON
    exit 1
    ;;
esac

# tinyplay opens its input path itself and therefore cannot consume a socket
# through /proc/self/fd/0 (open(2) returns ENXIO). cat converts the socket to
# an anonymous pipe; opening /proc/self/fd/0 from tinyplay then works without
# creating a filesystem FIFO, which Android SELinux forbids even to Magisk.
echo READY
/system/bin/log -p i -t OnSIMUplink "session started: Incall_Music=on"
/system/bin/cat | /system/bin/tinyplay /proc/self/fd/0 -D 0 -d 1 -p 320 -n 4 >"$session_log" 2>&1
status=$?
summary=$(tail -n 1 "$session_log" 2>/dev/null)
/system/bin/log -p i -t OnSIMUplink "session ended: tinyplay_status=$status $summary"
exit "$status"
