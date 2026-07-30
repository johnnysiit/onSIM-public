#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
rom=${ONSIM_ONEPLUS_ROM:?Set ONSIM_ONEPLUS_ROM to the matching stock OTA zip}
data_dir=${ONSIM_HOST_DATA:-"$repo_dir/data"}
firmware_dir="$data_dir/firmware/oneplus5"

test -f "$rom"
mkdir -p "$firmware_dir"
metadata=$(unzip -p "$rom" META-INF/com/android/metadata)
post_build=$(sed -n 's/^post-build=//p' <<<"$metadata")
if [[ -z "$post_build" ]]; then
  echo "ROM metadata does not contain post-build" >&2
  exit 1
fi

unzip -p "$rom" boot.img >"$firmware_dir/boot-stock.img"
printf '%s\n' "$metadata" >"$firmware_dir/ota-metadata.txt"
sha256sum "$rom" "$firmware_dir/boot-stock.img" >"$firmware_dir/SHA256SUMS"
printf 'Expected device fingerprint: %s\n' "$post_build"
printf 'Stock boot and recovery metadata: %s\n' "$firmware_dir"
printf 'Next: build the APK/module with scripts/build-android-gateway.sh\n'
