#!/usr/bin/env bash
# Sign a freshly built binary with this machine's Developer ID identity, when
# one exists. A stable signing identity keeps the macOS Keychain ACL matching
# across rebuilds, so the agent's secret reads (cloud profile API keys, OAuth
# tokens) stop prompting for a password after every rebuild — an unsigned Go
# binary gets a fresh ad-hoc signature each build and looks like a stranger
# to the Keychain.
#
# Usage: codesign-if-available.sh <binary>
#
# Identity: $CERCANO_CODESIGN_ID when set ("none" disables signing entirely);
# otherwise the first "Developer ID Application" identity in the keychain.
# No identity found = silent no-op, so builds on machines without one (CI,
# other platforms) are unaffected.
set -euo pipefail

bin="${1:?usage: codesign-if-available.sh <binary>}"

id="${CERCANO_CODESIGN_ID:-}"
[[ "$id" == "none" ]] && exit 0
if [[ -z "$id" ]]; then
    id=$(security find-identity -v -p codesigning 2>/dev/null \
        | sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)
fi
[[ -z "$id" ]] && exit 0

codesign --force --sign "$id" "$bin"
echo "[codesign] signed $bin as $id" >&2
