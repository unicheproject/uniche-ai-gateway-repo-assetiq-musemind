#!/bin/bash
#
# Prints a Keycloak access token for the given user, via the dev-only
# `dev-cli` client (password grant). Paste the output into Swagger UI's
# bearerAuth, or use it with curl:
#
#   TOKEN=$(scripts/dev-token.sh)
#   curl -H "Authorization: Bearer $TOKEN" ...
#
# The token goes to stdout; its verification and claims breakdown go to
# stderr, so capturing the token still works. Requires curl, jq, openssl.
#

set -euo pipefail

ISSUER=${OIDC_ISSUER:-https://idp.uniche-eccch.eu/realms/uniche}
CLIENT_ID=${DEV_LOGIN_CLIENT_ID:-dev-cli}
AUDIENCE=uniche-platform

b64url_decode() {
    local s
    s=$(tr '_-' '/+' <<<"$1")
    while (( ${#s} % 4 )); do s+="="; done
    base64 -d <<<"$s"
}

read -rp "Username: " USERNAME >&2
read -rsp "Password: " PASSWORD >&2
echo >&2

RESPONSE=$(curl -s "$ISSUER/protocol/openid-connect/token" \
    -d grant_type=password \
    -d client_id="$CLIENT_ID" \
    --data-urlencode username="$USERNAME" \
    --data-urlencode password="$PASSWORD")

TOKEN=$(jq -r '.access_token // empty' <<<"$RESPONSE")
if [[ -z "$TOKEN" ]]; then
    echo "Login failed: $RESPONSE" >&2
    exit 1
fi

IFS=. read -r HEADER_B64 PAYLOAD_B64 SIG_B64 <<<"$TOKEN"
HEADER=$(b64url_decode "$HEADER_B64")
PAYLOAD=$(b64url_decode "$PAYLOAD_B64")

# Signature: rebuild the RSA public key for the token's kid from the realm
# JWKS, then check RS256 over "header.payload".
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

KID=$(jq -r .kid <<<"$HEADER")
JWK=$(curl -s "$ISSUER/protocol/openid-connect/certs" | jq -c --arg kid "$KID" '.keys[] | select(.kid == $kid)')
if [[ -z "$JWK" ]]; then
    SIG_OK="FAIL (kid $KID not in JWKS)"
else
    N_HEX=$(b64url_decode "$(jq -r .n <<<"$JWK")" | xxd -p | tr -d '\n')
    E_HEX=$(b64url_decode "$(jq -r .e <<<"$JWK")" | xxd -p | tr -d '\n')
    printf 'asn1=SEQUENCE:pubkey\n[pubkey]\nn=INTEGER:0x%s\ne=INTEGER:0x%s\n' "$N_HEX" "$E_HEX" >"$TMP/key.conf"
    openssl asn1parse -genconf "$TMP/key.conf" -out "$TMP/key.der" -noout
    openssl rsa -RSAPublicKey_in -inform DER -in "$TMP/key.der" -pubout -out "$TMP/key.pem" 2>/dev/null
    b64url_decode "$SIG_B64" >"$TMP/sig"
    if printf '%s.%s' "$HEADER_B64" "$PAYLOAD_B64" | openssl dgst -sha256 -verify "$TMP/key.pem" -signature "$TMP/sig" >/dev/null 2>&1; then
        SIG_OK="ok (RS256, kid $KID)"
    else
        SIG_OK="FAIL"
    fi
fi

NOW=$(date +%s)
EXP=$(jq -r .exp <<<"$PAYLOAD")

{
    echo
    echo "Verification"
    echo "  signature: $SIG_OK"
    ISS=$(jq -r .iss <<<"$PAYLOAD")
    if [[ "$ISS" == "$ISSUER" ]]; then echo "  iss:       ok"; else echo "  iss:       FAIL (got $ISS, want $ISSUER)"; fi
    if jq -e --arg a "$AUDIENCE" '[.aud] | flatten | index($a)' <<<"$PAYLOAD" >/dev/null; then
        echo "  aud:       ok ($AUDIENCE)"
    else
        echo "  aud:       FAIL (got $(jq -c .aud <<<"$PAYLOAD"), want $AUDIENCE)"
    fi
    if (( EXP > NOW )); then
        echo "  exp:       ok (expires in $(( EXP - NOW ))s)"
    else
        echo "  exp:       FAIL (expired $(( NOW - EXP ))s ago)"
    fi
    echo
    echo "Claims"
    jq -r 'to_entries[] | "  \(.key): \(.value | if type == "string" then . else tojson end)"' <<<"$PAYLOAD"
    echo
} >&2

echo "$TOKEN"
