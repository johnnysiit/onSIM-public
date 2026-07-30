#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
data_dir=${ONSIM_HOST_DATA:-"$repo_dir/data"}
firmware_dir="$data_dir/firmware/oneplus5"
adb_bin=${ADB:-adb}
fastboot_bin=${FASTBOOT:-fastboot}
serial=${ONSIM_ANDROID_SERIAL:?Set ONSIM_ANDROID_SERIAL to the target device serial}
command=${1:-help}

stock="$firmware_dir/boot-stock.img"
patched="$firmware_dir/boot-magisk.img"

wait_for_boot() {
  timeout 180 "$adb_bin" -s "$serial" wait-for-device
  deadline=$((SECONDS + 180))
  while [[ "$("$adb_bin" -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" != "1" ]]; do
    if (( SECONDS >= deadline )); then
      echo "Android did not finish booting within 180 seconds." >&2
      return 1
    fi
    sleep 2
  done
}

case "$command" in
  manager)
    test -s "$stock"
    test -s "$firmware_dir/Magisk-v30.7.apk"
    "$adb_bin" -s "$serial" install -r "$firmware_dir/Magisk-v30.7.apk"
    "$adb_bin" -s "$serial" push "$stock" /sdcard/Download/onsim-boot-stock.img
    "$adb_bin" -s "$serial" shell monkey -p com.topjohnwu.magisk \
      -c android.intent.category.LAUNCHER 1
    echo "In Magisk choose Install -> Select and Patch a File -> onsim-boot-stock.img"
    ;;
  pull)
    remote=$("$adb_bin" -s "$serial" shell \
      'ls -1t /sdcard/Download/magisk_patched*.img 2>/dev/null | head -1' | tr -d '\r')
    test -n "$remote"
    "$adb_bin" -s "$serial" pull "$remote" "$patched"
    test "$(stat -c %s "$patched")" -gt 10000000
    if cmp -s "$stock" "$patched"; then
      echo "Patched image is identical to stock boot; refusing to continue." >&2
      exit 1
    fi
    sha256sum "$stock" "$patched" >"$firmware_dir/BOOT-SHA256SUMS"
    echo "Patched boot validated at $patched"
    ;;
  test)
    test -s "$patched"
    "$adb_bin" -s "$serial" reboot bootloader
    "$fastboot_bin" -s "$serial" boot "$patched"
    wait_for_boot
    "$adb_bin" -s "$serial" shell su -c id | grep -q 'uid=0'
    echo "Temporary Magisk boot succeeded. Run '$0 flash' to persist it."
    ;;
  flash)
    test -s "$patched"
    "$adb_bin" -s "$serial" shell su -c id | grep -q 'uid=0'
    "$adb_bin" -s "$serial" reboot bootloader
    "$fastboot_bin" -s "$serial" flash boot "$patched"
    "$fastboot_bin" -s "$serial" reboot
    wait_for_boot
    "$adb_bin" -s "$serial" shell su -c id | grep -q 'uid=0'
    echo "Persistent Magisk boot verified."
    ;;
  *)
    echo "Usage: $0 manager|pull|test|flash"
    echo "Recovery: scripts/recover-oneplus5.sh"
    ;;
esac
