# Koffr build targets. CGO is disabled everywhere: the project must produce a
# single statically linked binary (ENF-030), which rules out any C-backed
# dependency for SQLite or compression.
BINARY  := koffr
PKG     := github.com/Gu1llaum-3/koffr
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/Gu1llaum-3/koffr/internal/version.Value=$(VERSION)

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

.PHONY: all build test cover lint lint-only vet vuln cross clean tidy hooks ci verify-milestone

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

# verify-milestone is the manual gate at the end of a milestone. It is not in
# `ci` on purpose: it takes tens of minutes and moves tens of gigabytes, and a
# check that long stops being run if it sits in front of every push.
#
# It covers the exit criteria the fast suite cannot: a 10 GiB database to both
# backend types, the memory ceiling at that size, the absence of any temporary
# file, and the PostgreSQL 14 to 18 matrix. KOFFR_REQUIRE_DOCKER is set so a
# missing runtime fails rather than reporting a pass nobody ran.
verify-milestone: verify-pg-matrix
	@echo "This moves tens of gigabytes and takes tens of minutes."
	KOFFR_MILESTONE=1 KOFFR_REQUIRE_DOCKER=1 \
		go test -tags milestone -timeout 120m -v ./test/milestone/...

# M1 exit criterion 5. CI pins one major to stay under five minutes; this walks
# all of them, because "supports PostgreSQL 14 to 18" is a claim and a claim
# needs a run behind it (CT-001: five client toolchains, five servers).
.PHONY: verify-pg-matrix
verify-pg-matrix:
	@for v in 14 15 16 17 18; do \
		echo "== PostgreSQL $$v =="; \
		KOFFR_REQUIRE_DOCKER=1 KOFFR_PG_IMAGE=postgres:$$v \
			go test -count=1 ./internal/source/postgres/... || exit 1; \
	done
