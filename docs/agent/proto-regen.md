# Regenerating the gRPC bindings

The proto surface lives in `source/proto/agent.proto`; the generated Go
bindings are committed at `source/server/pkg/proto/agent.pb.go` and
`agent_grpc.pb.go`. There is **no Makefile target and no `go:generate`
directive** for regeneration — this document is the recorded procedure.

## The command

From the repo root:

```sh
PATH="$HOME/go/bin:$PATH" protoc \
  --proto_path=source/proto \
  --go_out=source/server/pkg/proto --go_opt=paths=source_relative \
  --go-grpc_out=source/server/pkg/proto --go-grpc_opt=paths=source_relative \
  agent.proto
```

The `protoc-gen-go` and `protoc-gen-go-grpc` plugins live in `~/go/bin`,
which is typically not on PATH — hence the prefix. The generated file
headers record the exact plugin versions that produced the committed
bindings; keep new output consistent with them.

## Verify before you edit

Before touching `agent.proto`, run the command above against the
**unmodified** proto and confirm the working tree stays clean:

```sh
git diff --stat -- source/server/pkg/proto/   # must be empty
```

A zero diff proves your protoc and plugin versions match whatever generated
the committed bindings. If it isn't empty, stop — regenerating after your
edit would mix a version-skew diff into your change.

## Two traps that recur

1. **Adding an RPC breaks the mcp mock.** `internal/mcp/server_test.go`'s
   `mockAgentClient` implements `proto.AgentClient`; every new RPC needs a
   stub added there (mirror the existing ones — return an empty response).
   `go build` will NOT catch this; `go vet ./...` and the test build will.

2. **Never trust a rebase's auto-merge of generated files.** When both sides
   of a rebase regenerated `agent.pb.go`, git may textually merge the two —
   including the serialized file-descriptor byte blob, which can be silently
   corrupted even when the merge "succeeds". After any rebase that touched
   the proto: regenerate from the merged `agent.proto` and require a zero
   diff. If there is a diff, the regenerated output is the truth; commit it.
