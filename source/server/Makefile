.PHONY: all agent mcp clean test

all: agent mcp

agent:
	go build -o bin/agent ./cmd/agent

mcp:
	go build -o bin/cercano-mcp ./cmd/mcp

clean:
	rm -f bin/agent bin/cercano-mcp

test:
	go test ./...
