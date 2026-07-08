.PHONY: build clean test

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Builds the standalone terminal client (cercano-cli). At runtime it dials the
# agent server and auto-launches `cercano agent`, locating the server binary as
# a sibling next to its own executable — co-locate the two when installing.
# Build the server binary with: make -C ../../server build
build:
	go build -ldflags "$(LDFLAGS)" -o bin/cercano-cli .
	@../../server/scripts/codesign-if-available.sh bin/cercano-cli

clean:
	rm -f bin/cercano-cli

test:
	go test ./...
