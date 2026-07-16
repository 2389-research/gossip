# ABOUTME: Build entry points for GOssip; `check` delegates to scripts/check (the canonical gate).
# ABOUTME: Targets: build, test, check, install, clean.

BINARY := bin/gossip

.PHONY: build test check install clean

build:
	go build -o $(BINARY) ./cmd/gossip

test:
	go test ./...

check:
	./scripts/check

install:
	go install ./cmd/gossip

clean:
	rm -rf bin
