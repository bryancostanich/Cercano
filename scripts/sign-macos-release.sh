#!/bin/bash
# Sign disposable staging copies only. Failure may leave staging partially signed.
set -euo pipefail
export LC_ALL=C
fail() { echo "Error: $*" >&2; exit 1; }
[[ $# -eq 1 ]] || fail "Usage: $0 <STAGING_BIN_DIR>"
[[ "$(uname)" == Darwin && "$(uname -m)" == arm64 ]] || fail 'Darwin arm64 host required'
[[ -d "$1" ]] || fail 'Staging directory does not exist'
stage="$(cd "$1" && pwd -P)"
identity="${CERCANO_CODESIGN_ID:-}"
[[ -n "$identity" && "$identity" != none && "$identity" != - ]] || fail 'CERCANO_CODESIGN_ID must explicitly select a Developer ID Application identity'

# Check both binaries before modifying either. Never sign live installed binaries.
for name in cercano cercano-cli; do
    binary="$stage/$name"
    [[ -f "$binary" && -x "$binary" && ! -L "$binary" ]] || fail "Expected regular executable, not symlink: $binary"
    arch="$(lipo -archs "$binary")" || fail "Cannot inspect $binary"
    [[ "$arch" == arm64 ]] || fail "Expected arm64 only: $binary ($arch)"
done

# Arrays preserve keychain paths without eval or shell expansion of user input.
lookup=(security find-identity -v -p codesigning)
if [[ -n "${CERCANO_CODESIGN_KEYCHAIN:-}" ]]; then
    lookup+=("$CERCANO_CODESIGN_KEYCHAIN")
fi
identities="$("${lookup[@]}")" || fail 'Cannot enumerate valid signing identities'
pattern='^[[:space:]]*[0-9]+\)[[:space:]]+([[:xdigit:]]{40})[[:space:]]+"(Developer ID Application: [^"]+)"[[:space:]]*$'
resolved=''
matches=0
while IFS= read -r line; do
    if [[ "$line" =~ $pattern ]]; then
        sha="${BASH_REMATCH[1]}"
        name="${BASH_REMATCH[2]}"
        match=false
        if [[ "$identity" == "$name" ]]; then
            match=true
        elif [[ "$identity" =~ ^[[:xdigit:]]{40}$ ]]; then
            shopt -s nocasematch
            [[ "$identity" == "$sha" ]] && match=true
            shopt -u nocasematch
        fi
        if [[ "$match" == true ]]; then
            matches=$((matches + 1))
            resolved="$sha"
        fi
    fi
done <<< "$identities"
[[ "$matches" -eq 1 ]] || fail "Expected exactly one valid Developer ID Application identity; found $matches. Use its full name or SHA-1 fingerprint."

for name in cercano cercano-cli; do
    binary="$stage/$name"
    sign=(codesign --force --sign "$resolved" --options runtime --timestamp)
    if [[ -n "${CERCANO_CODESIGN_KEYCHAIN:-}" ]]; then
        sign+=(--keychain "$CERCANO_CODESIGN_KEYCHAIN")
    fi
    # Default basename identifier matches the existing development signing helper.
    "${sign[@]}" "$binary" || fail "Signing failed: $name"
    codesign --verify --strict "$binary" || fail "Signature verification failed: $name"
    details="$(codesign -d --verbose=4 "$binary" 2>&1)" || fail "Cannot inspect signature: $name"
    authority=false; runtime=false; timestamp=false
    while IFS= read -r line; do
        [[ "$line" == 'Authority=Developer ID Application: '* ]] && authority=true
        if [[ "$line" == CodeDirectory* && "$line" =~ flags=0x[[:xdigit:]]+\([^\)]*runtime[^\)]*\) ]]; then
            runtime=true
        fi
        [[ "$line" == Timestamp=?* && "$line" != 'Timestamp=none' ]] && timestamp=true
    done <<< "$details"
    [[ "$authority" == true && "$runtime" == true && "$timestamp" == true ]] || fail "Missing Developer ID authority, hardened runtime, or secure timestamp: $name"
    echo "Verified signed staging binary: $binary"
done
echo 'Signing complete. NOT notarized or release-ready; do not modify signed bytes.'
