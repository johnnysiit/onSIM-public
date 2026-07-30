#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
data_dir=${ONSIM_HOST_DATA:-"$repo_dir/data"}
stock_boot="$data_dir/firmware/oneplus5/boot-stock.img"
fastboot_bin=${FASTBOOT:-fastboot}
serial=${ONSIM_ANDROID_SERIAL:?Set ONSIM_ANDROID_SERIAL to the target device serial}

test -s "$stock_boot"
echo "This restores the verified stock boot image and removes Magisk root."
echo "Target serial: $serial"
"$fastboot_bin" -s "$serial" flash boot "$stock_boot"
"$fastboot_bin" -s "$serial" reboot
