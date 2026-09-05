# Koffr build targets. CGO is disabled everywhere: the project must produce a
# single statically linked binary (ENF-030), which rules out any C-backed
# dependency for SQLite or compression.
BINARY  := koffr
PKG     := github.com/Gu1llaum-3/koffr
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

# Tools live either on PATH or under mise (see mise.toml). Resolving both means
# `make ci` behaves the same whether or not mise is activated in this shell.
#
# A missing tool is a hard failure, not a skip. A security check that reports
# success because it did not run is the same failure mode as a backup that
# reports success because it never started.
define tool
	@if command -v $(1) >/dev/null 2>&1; then \
		$(1) $(2); \
	elif command -v mise >/dev/null 2>&1 && mise which $(1) >/dev/null 2>&1; then \
		mise exec -- $(1) $(2); \
	else \
		echo "$(1) is not installed. Run 'mise install' (see mise.toml)." >&2; \
		exit 1; \
	fi
endef

.PHONY: all build test cover lint lint-only vet vuln cross clean tidy hooks ci

all: lint test build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/koffr

test:
	go test -race ./...

cover:
	go test -race -cover ./...

vet:
	go vet ./...

# Linted for the platform Koffr ships on as well as the one it is written on.
#
# Build-tagged files are invisible to a linter running under another GOOS, and
# CI found a Linux-only finding this cannot see otherwise. Discovering that
# there costs a round trip; discovering it here costs two seconds.
lint: vet
	$(call tool,golangci-lint,run)
	@echo "linting for linux/amd64"
	@GOOS=linux GOARCH=amd64 $(MAKE) --no-print-directory lint-only
	@echo "linting for linux/arm64"
	@GOOS=linux GOARCH=arm64 $(MAKE) --no-print-directory lint-only

lint-only:
	$(call tool,golangci-lint,run)

vuln:
	$(call tool,govulncheck,./...)

# Cross-compilation targets Koffr actually supports (ENF-030).
cross:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-amd64 ./cmd/koffr
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-arm64 ./cmd/koffr

tidy:
	go mod tidy

# Everything the GitHub workflow runs, locally. Nothing in the pipeline needs
# GitHub, so a red build should never be a surprise.
ci: lint test cross vuln
	@echo "all CI checks passed locally"

# Install the git hooks (see lefthook.yml). Run once per clone.
hooks:
	$(call tool,lefthook,install)

clean:
	rm -rf bin
