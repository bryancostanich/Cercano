.PHONY: all build agent mcp dev clean test

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Unified binary (preferred)
build:
	go build -ldflags "$(LDFLAGS)" -o bin/cercano ./cmd/cercano

# Legacy binaries (kept for backwards compatibility)
all: build agent mcp

agent:
	go build -o bin/agent ./cmd/agent

mcp:
	go build -o bin/cercano-mcp ./cmd/mcp

# Dev workflow: build + restart
dev: build
	@pkill -f 'bin/cercano' 2>/dev/null || true
	@sleep 0.5
	@echo "Starting cercano..."
	@bin/cercano &
	@echo "Ready. Use /mcp to reconnect Claude Code."

clean:
	rm -f bin/agent bin/cercano-mcp bin/cercano

test:
	go test ./...
