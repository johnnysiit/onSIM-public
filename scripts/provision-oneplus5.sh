#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
data_dir=${ONSIM_HOST_DATA:-"$repo_dir/data"}
artifact_dir=${ONSIM_ANDROID_ARTIFACTS:-"$repo_dir/dist/android"}
adb_bin=${ADB:-adb}
serial=${ONSIM_ANDROID_SERIAL:?Set ONSIM_ANDROID_SERIAL to the value shown by adb devices}
token_file="$data_dir/android.token"

test -f "$artifact_dir/OnSIMGW.apk"
test -f "$artifact_dir/onsim-android-gateway-magisk.zip"
mkdir -p "$data_dir"
if [[ ! -s "$token_file" ]]; then
  umask 077
  openssl rand -hex 32 >"$token_file"
fi
token=$(tr -d '\r\n' <"$token_file")

"$adb_bin" -s "$serial" get-state
"$adb_bin" -s "$serial" install -r "$artifact_dir/OnSIMGW.apk"
"$adb_bin" -s "$serial" push "$artifact_dir/onsim-android-gateway-magisk.zip" /sdcard/Download/

if ! "$adb_bin" -s "$serial" shell su -c id 2>/dev/null | grep -q 'uid=0'; then
  echo "Magisk root is not active yet." >&2
  echo "Patch data/firmware/oneplus5/boot-stock.img on this phone with the official Magisk app," >&2
  echo "then pull and validate the patched image before flashing it." >&2
  exit 2
fi

"$adb_bin" -s "$serial" shell su -c \
  'magisk --install-module /sdcard/Download/onsim-android-gateway-magisk.zip'
"$adb_bin" -s "$serial" reboot
"$adb_bin" -s "$serial" wait-for-device
sleep 8
"$adb_bin" -s "$serial" shell cmd role add-role-holder --user 0 \
  android.app.role.DIALER io.onsim.gateway 0
"$adb_bin" -s "$serial" shell cmd role add-role-holder --user 0 \
  android.app.role.SMS io.onsim.gateway 0
"$adb_bin" -s "$serial" shell am start \
  -n io.onsim.gateway/.GatewayActivity --es token "$token"
"$adb_bin" -s "$serial" shell dumpsys package io.onsim.gateway | \
  grep -E 'CAPTURE_AUDIO_OUTPUT|READ_PRIVILEGED_PHONE_STATE|granted=true' | head -30
echo "OnePlus 5 companion provisioned. Reboot once more before production acceptance."
