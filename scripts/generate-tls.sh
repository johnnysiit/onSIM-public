#!/usr/bin/env bash
set -euo pipefail

target=${1:?usage: generate-tls.sh DATA_DIR [IP...]}
shift
tls_dir="$target/tls"
mkdir -p "$tls_dir"
umask 077

if [[ ! -s "$tls_dir/onsim-ca.key" ]]; then
  openssl ecparam -name prime256v1 -genkey -noout -out "$tls_dir/onsim-ca.key"
  openssl req -x509 -new -sha256 -days 3650 \
    -key "$tls_dir/onsim-ca.key" -out "$tls_dir/onsim-ca.crt" \
    -subj "/CN=onSIM Local CA/O=onSIM"
fi

san="DNS:onsim.local,DNS:localhost,IP:127.0.0.1"
for address in "$@"; do
  [[ -n "$address" ]] && san="$san,IP:$address"
done
openssl ecparam -name prime256v1 -genkey -noout -out "$tls_dir/onsim.key"
openssl req -new -sha256 -key "$tls_dir/onsim.key" -out "$tls_dir/onsim.csr" \
  -subj "/CN=onsim.local/O=onSIM"
openssl x509 -req -sha256 -days 825 -in "$tls_dir/onsim.csr" \
  -CA "$tls_dir/onsim-ca.crt" -CAkey "$tls_dir/onsim-ca.key" -CAcreateserial \
  -out "$tls_dir/onsim.crt" -extfile <(printf 'subjectAltName=%s\nextendedKeyUsage=serverAuth\n' "$san")
rm -f "$tls_dir/onsim.csr" "$tls_dir/onsim-ca.srl"
chmod 0600 "$tls_dir/onsim.key" "$tls_dir/onsim-ca.key"
chmod 0644 "$tls_dir/onsim.crt" "$tls_dir/onsim-ca.crt"
echo "TLS certificate generated for: $san"
