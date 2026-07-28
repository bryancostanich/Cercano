#!/usr/bin/env bash
#
# Regenerate the Go bindings from source/proto/agent.proto.
#
# This is the single source of truth for how the *.pb.go files under
# source/server/pkg/proto/ are produced. There is deliberately no separate
# documentation: run this script.
#
# It is idempotent and location-independent:
#   - puts the Go plugin bin dir ($GOPATH/bin) on PATH, so protoc can find the
#     protoc-gen-go / protoc-gen-go-grpc plugins even when they are not on the
#     login shell PATH;
#   - installs the *pinned* plugin versions if they are missing, so regenerated
#     output stays byte-identical to what is committed;
#   - writes straight into source/server/pkg/proto/ using paths=source_relative,
#     so output never depends on the current working directory (the module=
#     option strips the go_package prefix and, run from the repo root, silently
#     writes to ./pkg/proto/ instead — this avoids that trap).
#
# Requires: protoc (macOS: `brew install protobuf`) and a Go toolchain.
#
# Usage: source/proto/generate.sh   (from anywhere)

set -euo pipefail

# Versions that produced the committed bindings — see the headers in
# source/server/pkg/proto/agent.pb.go and agent_grpc.pb.go. Bump these together
# with a regenerated commit, never independently.
PROTOC_GEN_GO_VERSION="v1.36.11"
PROTOC_GEN_GO_GRPC_VERSION="v1.6.2"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
proto_dir="$repo_root/source/proto"
out_dir="$repo_root/source/server/pkg/proto"

export PATH="$PATH:$(go env GOPATH)/bin"

if ! command -v protoc >/dev/null 2>&1; then
	echo "error: protoc not found. Install it (macOS: brew install protobuf)." >&2
	exit 1
fi

# Install pinned plugins on demand. `go install …@version` is a no-op reinstall
# when the pinned version is already the one on disk, so this stays cheap.
if ! command -v protoc-gen-go >/dev/null 2>&1; then
	echo "installing protoc-gen-go $PROTOC_GEN_GO_VERSION ..." >&2
	go install "google.golang.org/protobuf/cmd/protoc-gen-go@$PROTOC_GEN_GO_VERSION"
fi
if ! command -v protoc-gen-go-grpc >/dev/null 2>&1; then
	echo "installing protoc-gen-go-grpc $PROTOC_GEN_GO_GRPC_VERSION ..." >&2
	go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@$PROTOC_GEN_GO_GRPC_VERSION"
fi

protoc \
	--proto_path="$proto_dir" \
	--go_out="$out_dir" --go_opt=paths=source_relative \
	--go-grpc_out="$out_dir" --go-grpc_opt=paths=source_relative \
	"$proto_dir/agent.proto"

echo "regenerated Go bindings in $out_dir"
