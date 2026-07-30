#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$repo_dir/dist/android"}
data_dir=${ONSIM_HOST_DATA:-"$repo_dir/data"}
signing_dir="$data_dir/android-signing"
signing_env="$signing_dir/signing.env"
build_dir=$(mktemp -d)
trap 'rm -r "$build_dir"' EXIT

mkdir -p "$output_dir" "$signing_dir"
podman build -t localhost/onsim-android-builder:latest -f "$repo_dir/android-gateway/Containerfile" "$repo_dir/android-gateway"
if [[ ! -f "$signing_env" ]]; then
  umask 077
  store_password=$(openssl rand -hex 24)
  key_password=$store_password
  {
    printf 'ONSIM_ANDROID_STORE_PASSWORD=%q\n' "$store_password"
    printf 'ONSIM_ANDROID_KEY_PASSWORD=%q\n' "$key_password"
    printf 'ONSIM_ANDROID_KEY_ALIAS=onsim\n'
  } >"$signing_env"
fi
source "$signing_env"
export ONSIM_ANDROID_STORE_PASSWORD ONSIM_ANDROID_KEY_PASSWORD ONSIM_ANDROID_KEY_ALIAS
if [[ ! -f "$signing_dir/onsim.jks" ]]; then
  podman run --rm \
    -v "$signing_dir:/signing:Z" \
    --entrypoint keytool localhost/onsim-android-builder:latest \
    -genkeypair -keystore /signing/onsim.jks -storepass "$ONSIM_ANDROID_STORE_PASSWORD" \
    -keypass "$ONSIM_ANDROID_KEY_PASSWORD" -alias "$ONSIM_ANDROID_KEY_ALIAS" \
    -keyalg RSA -keysize 3072 -validity 10000 -dname "CN=onSIM Android Gateway,O=onSIM"
fi
podman run --rm \
  -e ONSIM_ANDROID_KEYSTORE=/signing/onsim.jks \
  -e ONSIM_ANDROID_STORE_PASSWORD \
  -e ONSIM_ANDROID_KEY_PASSWORD \
  -e ONSIM_ANDROID_KEY_ALIAS \
  -v "$repo_dir/android-gateway:/src:Z" \
  -v "$signing_dir:/signing:Z" \
  -v onsim-android-gradle-cache:/home/gradle/.gradle \
  localhost/onsim-android-builder:latest :app:assembleRelease
cp "$repo_dir/android-gateway/app/build/outputs/apk/release/app-release.apk" "$output_dir/OnSIMGW.apk"

mkdir -p "$build_dir/module/system/priv-app/OnSIMGW"
mkdir -p "$build_dir/module/system/etc/permissions"
cp "$output_dir/OnSIMGW.apk" "$build_dir/module/system/priv-app/OnSIMGW/OnSIMGW.apk"
cp "$repo_dir/android-gateway/app/src/main/res/xml/privapp_permissions_onsim.xml" \
  "$build_dir/module/system/etc/permissions/privapp-permissions-onsim.xml"
cp "$repo_dir/android-gateway/magisk/module.prop" "$build_dir/module/"
cp "$repo_dir/android-gateway/magisk/customize.sh" "$build_dir/module/"
cp "$repo_dir/android-gateway/magisk/service.sh" "$build_dir/module/"
cp "$repo_dir/android-gateway/magisk/uplink-service.sh" "$build_dir/module/"
cp "$repo_dir/android-gateway/magisk/uplink-handler.sh" "$build_dir/module/"
cp "$repo_dir/android-gateway/magisk/system.prop" "$build_dir/module/"
chmod 0755 "$build_dir/module/customize.sh" "$build_dir/module/service.sh" \
  "$build_dir/module/uplink-service.sh" "$build_dir/module/uplink-handler.sh"
(cd "$build_dir/module" && zip -qr "$output_dir/onsim-android-gateway-magisk.zip" .)
sha256sum "$output_dir/OnSIMGW.apk" "$output_dir/onsim-android-gateway-magisk.zip" >"$output_dir/SHA256SUMS"
echo "Android gateway artifacts: $output_dir"
