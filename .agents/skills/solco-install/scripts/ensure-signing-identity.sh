#!/usr/bin/env bash
#
# Idempotently make sure the login keychain holds the local self-signed
# code-signing identity "Solco Local Dev". install.sh signs the installed
# Solco.app with it so the bundle's designated requirement stays
# `identifier "com.whstudio.solco" and certificate leaf = <this cert>` across
# rebuilds — macOS then keeps the Microphone (and other TCC) grants instead of
# asking again after every ad-hoc re-sign.
#
# Local only: the certificate is not trusted system-wide, never leaves this
# Mac and is not used for releases (release.yml signs with its own identity).
# Prints the identity name on stdout when it is usable, nothing otherwise.
set -euo pipefail

NAME="${SOLCO_SIGN_IDENTITY:-Solco Local Dev}"
KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"

has_identity() {
  security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null | grep -q "\"$NAME\""
}

if ! has_identity; then
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  cat >"$TMP/openssl.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -config "$TMP/openssl.cnf" \
    -keyout "$TMP/key.pem" -out "$TMP/cert.pem" >/dev/null 2>&1
  # OpenSSL 3 needs -legacy so the macOS keychain can read the p12; LibreSSL has no such flag.
  legacy=""; openssl pkcs12 -help 2>&1 | grep -q -- -legacy && legacy="-legacy"
  openssl pkcs12 -export $legacy -inkey "$TMP/key.pem" -in "$TMP/cert.pem" -name "$NAME" \
    -passout pass:solco -out "$TMP/id.p12" >/dev/null 2>&1
  security import "$TMP/id.p12" -k "$KEYCHAIN" -P solco -T /usr/bin/codesign >/dev/null 2>&1 || exit 0
fi

has_identity && printf '%s\n' "$NAME"
exit 0
