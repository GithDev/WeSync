#!/usr/bin/env bash
# Generate a self-signed Authenticode code-signing certificate for WeSync.
#
# Usage (from repo root):
#   bash platform/windows/gen-codesign-cert.sh
#
# Output: platform/windows/codesign.pfx  (gitignored — never commit)
#
# After running, add two secrets to GitHub (Settings → Secrets → environment "Secrets"):
#   WINDOWS_CERT_PFX_BASE64  — the base64 string printed below
#   WINDOWS_CERT_PASSWORD    — the password you enter below
#
# NOTE: self-signed certs ARE NOT trusted by Windows SmartScreen. Windows will
# show "Unknown Publisher" on first install. For public distribution, obtain an
# OV or EV code-signing certificate from a trusted CA (e.g. Sectigo, DigiCert).

set -euo pipefail

OUT="platform/windows"
PFX="$OUT/codesign.pfx"
KEY="$OUT/codesign.key"
CRT="$OUT/codesign.crt"
CFG="$OUT/codesign.cnf"

if [ -f "$PFX" ]; then
    read -rp "codesign.pfx already exists. Overwrite? [y/N] " yn
    [[ "$yn" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }
fi

read -rsp "Certificate password (used for WINDOWS_CERT_PASSWORD secret): " PASS
echo
read -rsp "Confirm password: " PASS2
echo
if [ "$PASS" != "$PASS2" ]; then
    echo "Passwords do not match. Aborted." >&2
    exit 1
fi

# OpenSSL config with the codeSigning EKU that Authenticode requires.
cat > "$CFG" <<EOF
[req]
distinguished_name = dn
prompt             = no
x509_extensions    = ext

[dn]
CN = WeSync
O  = WeSync
C  = SE

[ext]
basicConstraints     = CA:FALSE
keyUsage             = critical,digitalSignature
extendedKeyUsage     = codeSigning
subjectKeyIdentifier = hash
EOF

echo "[gen] generating 4096-bit RSA key..."
openssl genrsa -out "$KEY" 4096 2>/dev/null

echo "[gen] creating self-signed certificate (10-year validity)..."
openssl req -new -x509 \
    -key "$KEY" \
    -out "$CRT" \
    -days 3650 \
    -config "$CFG"

echo "[gen] packaging as PFX..."
openssl pkcs12 -export \
    -in "$CRT" \
    -inkey "$KEY" \
    -out "$PFX" \
    -passout "pass:$PASS"

rm -f "$KEY" "$CRT" "$CFG"

echo ""
echo "================================================================"
echo " WINDOWS_CERT_PFX_BASE64  (paste this into GitHub Secrets)"
echo "================================================================"
base64 -w0 "$PFX"
echo ""
echo ""
echo "Add these two secrets to GitHub (environment: Secrets):"
echo "  WINDOWS_CERT_PFX_BASE64  = <the base64 string above>"
echo "  WINDOWS_CERT_PASSWORD    = <the password you entered>"
echo ""
echo "Certificate saved to: $PFX  (gitignored)"
