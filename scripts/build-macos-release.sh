#!/bin/bash
# Build, sign and package a macOS arm64 release archive.
#
# Produces exactly the layout release/homebrew/render_formula.py accepts:
#   cercano-<version>-darwin-arm64/{bin/cercano,bin/cercano-cli,LICENSE,README.txt}
#
# Signing is mandatory here; there is no unsigned escape hatch. Use
# scripts/build-macos-unsigned.sh for rehearsal builds. This script does NOT
# notarize: run scripts/notarize-macos-local.py (or the release workflow)
# against the staged bin directory before trusting the archive for
# distribution. A built archive is not a releasable archive.
set -euo pipefail
export LC_ALL=C

fail() { echo "Error: $*" >&2; exit 1; }

[[ "$(uname)" == Darwin && "$(uname -m)" == arm64 ]] || fail 'Darwin arm64 host required'
[[ $# -eq 2 ]] || fail "Usage: $0 <VERSION> <OUTPUT_DIR>"

VERSION="$1"
OUTPUT_DIR="$2"

# Stable releases only: the formula renderer rejects prereleases and the tap
# publishes immutable versioned URLs.
[[ "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
    || fail "Version must be stable X.Y.Z without a leading v or leading zeros: $VERSION"
[[ -n "${CERCANO_CODESIGN_ID:-}" && "${CERCANO_CODESIGN_ID}" != none && "${CERCANO_CODESIGN_ID}" != - ]] \
    || fail 'CERCANO_CODESIGN_ID must select a Developer ID Application identity; release builds are never unsigned'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SIGN_SCRIPT="$SCRIPT_DIR/sign-macos-release.sh"
[[ -x "$SIGN_SCRIPT" ]] || fail "Signing script is missing or not executable: $SIGN_SCRIPT"

# The version must come from a real tag so the archive URL, the formula and the
# source revision agree. CERCANO_ALLOW_UNTAGGED=1 is for local dry runs only.
if [[ "${CERCANO_ALLOW_UNTAGGED:-}" != 1 ]]; then
    TAG_COMMIT="$(git -C "$REPO_ROOT" rev-parse --verify "refs/tags/v$VERSION^{commit}")" \
        || fail "No tag v$VERSION in this repository. Tag the release, or set CERCANO_ALLOW_UNTAGGED=1 for a local dry run."
    [[ "$TAG_COMMIT" == "$(git -C "$REPO_ROOT" rev-parse --verify 'HEAD^{commit}')" ]] \
        || fail "HEAD does not match v$VERSION; refusing to label different source as this release."
fi

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd -P)"
NAME="cercano-${VERSION}-darwin-arm64"
FINAL_ARCHIVE="$OUTPUT_DIR/$NAME.tar.gz"
CHECKSUM_FILE="$FINAL_ARCHIVE.sha256"
# Never silently replace an artifact someone may already have published.
for existing in "$FINAL_ARCHIVE" "$CHECKSUM_FILE"; do
    [[ -e "$existing" ]] && fail "Refusing to overwrite existing artifact: $existing"
done

TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT
STAGE_ROOT="$TEMP_DIR/$NAME"
mkdir -p "$STAGE_ROOT/bin"

# Explicit CGO flags key the Go build cache; the env var alone does not.
export MACOSX_DEPLOYMENT_TARGET=12.0
export CGO_CFLAGS='-O2 -g -mmacosx-version-min=12.0'
export CGO_LDFLAGS='-O2 -g -mmacosx-version-min=12.0'

build() { # module_dir package output
    ( cd "$1" && GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildvcs=false \
        -ldflags "-X main.version=$VERSION" -o "$3" "$2" ) || fail "Build failed: $3"
}
echo "Building agent..."
build "$REPO_ROOT/source/server" ./cmd/cercano "$STAGE_ROOT/bin/cercano"
echo "Building terminal client..."
build "$REPO_ROOT/source/clients/cli" . "$STAGE_ROOT/bin/cercano-cli"

# Verify both binaries before signing either: a wrong architecture, a raised
# deployment floor or a mismatched version must fail before any signature.
for name in cercano cercano-cli; do
    binary="$STAGE_ROOT/bin/$name"
    arch="$(lipo -archs "$binary")" || fail "Cannot inspect $binary"
    minos="$(otool -l "$binary" | awk '/cmd LC_BUILD_VERSION/ { build=1; next } build && $1 == "minos" { print $2; build=0 }')"
    [[ "$arch" == arm64 ]] || fail "$binary: expected arm64 only, got $arch"
    [[ "$minos" == 12.0 ]] || fail "$binary: expected macOS 12.0 build target, got $minos"
    reported="$("$binary" --version 2>/dev/null | tr -d '\r')" || fail "$binary does not report a version"
    [[ "$reported" == *"$VERSION"* ]] || fail "$binary reports '$reported', which does not contain $VERSION"
    echo "Verified arm64, macOS 12.0 target, version $VERSION: $name"
done

cp "$REPO_ROOT/LICENSE" "$STAGE_ROOT/LICENSE"
cat > "$STAGE_ROOT/README.txt" <<EOF
Cercano $VERSION for macOS (Apple Silicon)

Contents:
  bin/cercano      agent; also the Homebrew upgrade entrypoint
  bin/cercano-cli  terminal client

Both binaries are signed with a Developer ID Application identity and built
with a macOS 12.0 deployment target. The deployment target is a compile-time
floor, not a guarantee of tested support on that OS version.

Notarization is performed as a separate step and is not implied by the
presence of this file. Models, runtimes and provider credentials are not
included and are provisioned on first use.

Homepage: https://github.com/cercano-ai/Cercano
EOF
chmod 0644 "$STAGE_ROOT/LICENSE" "$STAGE_ROOT/README.txt"
chmod 0755 "$STAGE_ROOT/bin/cercano" "$STAGE_ROOT/bin/cercano-cli"

echo "Signing staged binaries..."
"$SIGN_SCRIPT" "$STAGE_ROOT/bin" || fail 'Signing failed; no archive was produced'

# Re-verify the exact bytes that will be archived, not an earlier copy.
for name in cercano cercano-cli; do
    codesign --verify --strict "$STAGE_ROOT/bin/$name" || fail "Signature verification failed after staging: $name"
done

# Deterministic member order and ownership; the archive is created in the temp
# directory and moved into place only after it is complete.
TEMP_ARCHIVE="$TEMP_DIR/$NAME.tar.gz"
( cd "$TEMP_DIR" && tar --uid 0 --gid 0 --numeric-owner -czf "$TEMP_ARCHIVE" \
    "$NAME/LICENSE" "$NAME/README.txt" "$NAME/bin/cercano" "$NAME/bin/cercano-cli" ) \
    || fail 'Archive creation failed'
mv "$TEMP_ARCHIVE" "$FINAL_ARCHIVE"
( cd "$OUTPUT_DIR" && shasum -a 256 "$NAME.tar.gz" > "$CHECKSUM_FILE" ) || fail 'Checksum failed'

echo "Archive:  $FINAL_ARCHIVE"
echo "Checksum: $CHECKSUM_FILE"
echo 'Signed but NOT notarized. Notarize before distribution or tap promotion.'
