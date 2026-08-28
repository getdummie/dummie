#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
KEYS="$ROOT/demo/keys"
mkdir -p "$KEYS"

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 1; }; }
need ssh-keygen
need openssl

gen_ssh_key() { # comment path
  if [[ ! -f "$2" ]]; then
    ssh-keygen -q -t ed25519 -N "" -C "$1" -f "$2"
    echo "generated $2"
  fi
}

gen_ssh_key dpipe-host   "$KEYS/dpipe_host_ed25519"
gen_ssh_key dpipe-client "$KEYS/dpipe_client_ed25519"
gen_ssh_key alice          "$KEYS/alice"
gen_ssh_key bob            "$KEYS/bob"

if [[ -n "${FORCE_CERTS:-}" ]] ||
   { [[ -f "$KEYS/ca.crt" ]] && ! openssl x509 -in "$KEYS/ca.crt" -noout -text | grep -q "Key Usage"; }; then
  echo "regenerating the demo CA and its certificates"
  rm -f "$KEYS"/ca.crt "$KEYS"/ca.key "$KEYS"/ca.srl "$KEYS"/*.local.crt "$KEYS"/*.local.key
fi

if [[ ! -f "$KEYS/ca.crt" ]]; then
  openssl ecparam -genkey -name prime256v1 -out "$KEYS/ca.key" 2>/dev/null
  openssl req -x509 -new -key "$KEYS/ca.key" -sha256 -days 365 \
    -subj "/CN=dpipe demo CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,digitalSignature,keyCertSign,cRLSign" \
    -out "$KEYS/ca.crt" 2>/dev/null
  echo "generated $KEYS/ca.crt"
fi

gen_cert() { # sni
  local sni="$1"
  [[ -f "$KEYS/$sni.crt" ]] && return 0
  openssl ecparam -genkey -name prime256v1 -out "$KEYS/$sni.key" 2>/dev/null
  openssl req -new -key "$KEYS/$sni.key" -subj "/CN=$sni" -out "$KEYS/$sni.csr" 2>/dev/null
  openssl x509 -req -in "$KEYS/$sni.csr" -CA "$KEYS/ca.crt" -CAkey "$KEYS/ca.key" \
    -CAcreateserial -days 365 -sha256 \
    -extfile <(printf "subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n" "$sni") \
    -out "$KEYS/$sni.crt" 2>/dev/null
  rm -f "$KEYS/$sni.csr"
  echo "generated $KEYS/$sni.crt"
}

gen_cert vm1.local
gen_cert vm2.local
gen_cert default.local

if [[ ! -s "$KEYS/known_hosts" ]] && command -v ssh-keyscan >/dev/null; then
  ssh-keyscan -T 2 -p "${DEMO_SSHD_PORT:-2200}" 127.0.0.1 >"$KEYS/known_hosts" 2>/dev/null || true
fi
touch "$KEYS/known_hosts"

chmod 700 "$KEYS"
find "$KEYS" -type f -name '*.key' -exec chmod 600 {} +
find "$KEYS" -type f ! -name '*.pub' ! -name '*.crt' -exec chmod 600 {} +

echo "demo keys ready in $KEYS"
