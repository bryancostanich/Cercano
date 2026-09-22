# Build Inputs for macOS Standalone Release

## Build Flags and Versioning

### Server Build (source/server/Makefile)
- **Version Flag**: `LDFLAGS := -X main.version=$(VERSION)`
- **Version Source**: `git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev`
- **Main Build Command**: `go build -ldflags "$(LDFLAGS)" -o bin/cercano ./cmd/cercano`
- **Additional Binaries**: 
  - `agent`: `go build -o bin/agent ./cmd/agent`
  - `mcp`: `go build -o bin/cercano-mcp ./cmd/mcp`

### CLI Build (source/clients/cli/Makefile)
- **Version Flag**: `LDFLAGS := -X main.version=$(VERSION)`
- **Version Source**: Same as server
- **Build Command**: `go build -ldflags "$(LDFLAGS)" -o bin/cercano-cli .`

## Embedded Assets

### Configuration Files
- **tier_recommendations.yaml** (source/server/pkg/config/tierrecs.go:16)
- **prototypes.yaml** (source/server/internal/agent/router.go:18)
- **catalog.json** (multiple locations):
  - source/server/internal/localruntime/mistralrs/catalog.go:20
  - source/server/internal/localruntime/llamaserver/catalog.go:17

### Database and Storage
- **schema.sql** (source/server/internal/conversation/store.go:26)
- **testdata/real_conversation.json** (source/server/internal/compaction/realcorpus.go:24)

### Runtime Assets
- **cl100k_base.tiktoken** (source/server/internal/contextmeter/embedded_bpe.go:28)
- **ddg_search.py** (source/server/internal/web/script.go:17)

### Skills and Catalog
- **catalog** (source/server/internal/skills/skills.go:25)

## Notes
- Both server and CLI use identical versioning via LDFLAGS
- Embedded assets are compiled into binaries using `//go:embed` directive
- CLI binary is designed to co-locate with server binary for auto-launch functionality