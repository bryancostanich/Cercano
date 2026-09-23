#!/bin/bash
set -euo pipefail

# Validate Darwin arm64 host
if [[ "$(uname)" != "Darwin" ]] || [[ "$(uname -m)" != "arm64" ]]; then
    echo "Error: This script requires Darwin arm64 host" >&2
    exit 1
fi

# Validate arguments
if [[ $# -ne 2 ]]; then
    echo "Usage: $0 <VERSION> <OUTPUT_DIR>" >&2
    exit 1
fi

VERSION="$1"
OUTPUT_DIR="$2"

# Validate version format
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]; then
    echo "Error: Invalid version format: $VERSION" >&2
    echo "Expected format: X.Y.Z[-suffix]" >&2
    exit 1
fi

# Resolve repo root from script location
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Create absolute output directory
mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"

# Check for existing final archive or checksum
FINAL_ARCHIVE="$OUTPUT_DIR/cercano-${VERSION}-darwin-arm64-unsigned.tar.gz"
CHECKSUM_FILE="$FINAL_ARCHIVE.sha256"

if [[ -f "$FINAL_ARCHIVE" ]] || [[ -f "$CHECKSUM_FILE" ]]; then
    echo "Error: Final archive or checksum already exists:" >&2
    echo "  $FINAL_ARCHIVE" >&2
    echo "  $CHECKSUM_FILE" >&2
    exit 1
fi

# Create temp directory with cleanup trap
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

# Stage root directory
STAGE_ROOT="$TEMP_DIR/cercano-${VERSION}-darwin-arm64-unsigned"
mkdir -p "$STAGE_ROOT/bin"

# Explicit CGO flags key the Go cache; deployment-target env alone does not.
export MACOSX_DEPLOYMENT_TARGET=12.0
export CGO_CFLAGS='-O2 -g -mmacosx-version-min=12.0'
export CGO_LDFLAGS='-O2 -g -mmacosx-version-min=12.0'

# Build Go modules
cd "$REPO_ROOT/source/server"

echo "Building server..."
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildvcs=false \
    -ldflags "-X main.version=$VERSION" \
    -o "$STAGE_ROOT/bin/cercano" \
    ./cmd/cercano

echo "Building CLI..."
cd "$REPO_ROOT/source/clients/cli"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -buildvcs=false \
    -ldflags "-X main.version=$VERSION" \
    -o "$STAGE_ROOT/bin/cercano-cli" \
    .

# Verify exact target metadata; a higher minos would silently raise the floor.
for binary in "$STAGE_ROOT/bin/cercano" "$STAGE_ROOT/bin/cercano-cli"; do
    arch="$(lipo -archs "$binary")"
    minos="$(otool -l "$binary" | awk '/cmd LC_BUILD_VERSION/ { build=1; next } build && $1 == "minos" { print $2; build=0 }')"
    if [[ "$arch" != arm64 || "$minos" != 12.0 ]]; then
        echo "Error: $binary: expected arm64/minos 12.0, got $arch/$minos" >&2
        exit 1
    fi
    echo "Verified arm64, macOS 12.0 build target: $binary"
done

# Copy LICENSE
cp "$REPO_ROOT/LICENSE" "$STAGE_ROOT/"

# Write README.txt
cat > "$STAGE_ROOT/README.txt" << 'EOF'
THIS IS AN UNSIGNED LOCAL REHEARSAL BUILD - NOT FOR PUBLISHING

This is a local build of Cercano for macOS arm64. It is intended for
local testing and development purposes only.

Build target: macOS 12.0. This is verified Mach-O metadata, NOT proof of
runtime support on macOS 12.0; that still requires an actual OS test.

Entrypoints:
- cercano-cli: Command line interface
- cercano agent (or bare cercano): Agent service

This build does not include:
- Models or third-party runtimes
- Code signing
- Verification of minimum OS requirements
- Full startup validation

Use at your own risk. This build is not suitable for production deployment.
EOF

# Create temporary archive
TEMP_ARCHIVE="$TEMP_DIR/cercano-${VERSION}-darwin-arm64-unsigned.tar.gz"
echo "Creating archive..."
tar -czf "$TEMP_ARCHIVE" -C "$TEMP_DIR" "cercano-${VERSION}-darwin-arm64-unsigned"

# Move final archive to output directory
mv "$TEMP_ARCHIVE" "$FINAL_ARCHIVE"

# Create checksum
echo "Creating checksum..."
cd "$OUTPUT_DIR"
shasum -a 256 "$(basename "$FINAL_ARCHIVE")" > "$CHECKSUM_FILE"

# Print paths
echo "Build complete:"
echo "  Archive: $FINAL_ARCHIVE"
echo "  Checksum: $CHECKSUM_FILE"