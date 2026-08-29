# vera — build and local install. Two binaries, like rook/rookd:
# `vera` is what you type, `verad` is what stays up.

BINDIR ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test test-mac install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/vera ./cmd/vera
	go build -ldflags "$(LDFLAGS)" -o bin/verad ./cmd/verad

test:
	go test ./...

# The Mac app is one target and one scheme, with no test bundle in it.
# What is worth holding — what a Return means in the ask panel — was
# written to be reachable without one, so this compiles the real source
# beside a main that asserts it. Needs the Swift toolchain, which is why
# it is not part of `test`.
test-mac:
	@mkdir -p bin
	@swiftc -o bin/askpanel-tests macos/Vera/AskPanel.swift macos/Tests/AskReturnTests.swift
	@./bin/askpanel-tests

install: build test
	@mkdir -p $(BINDIR)
	@rm -f $(BINDIR)/vera $(BINDIR)/verad
	install -m 0755 bin/vera $(BINDIR)/vera
	install -m 0755 bin/verad $(BINDIR)/verad
	@echo "vera: installed $(BINDIR)/vera and $(BINDIR)/verad"
	@echo "vera: a running verad keeps the old binary — \`vera restart\` when ready"
