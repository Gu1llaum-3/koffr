# Koffr build targets. CGO is disabled everywhere: the project must produce a
# single statically linked binary (ENF-030), which rules out any C-backed
# dependency for SQLite or compression.
BINARY  := koffr
PKG     := github.com/Gu1llaum-3/koffr
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build test cover lint vet vuln cross clean tidy

all: lint test build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/koffr

test:
	go test -race ./...

# Coverage is scoped to internal/: the auto-downloaded Go toolchain module ships
# without the covdata tool, which -cover needs for packages that have no tests.
cover:
	go test -race -cover ./internal/...

vet:
	go vet ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint not installed, skipping (see .golangci.yml)"

vuln:
	@command -v govulncheck >/dev/null 2>&1 \
		&& govulncheck ./... \
		|| echo "govulncheck not installed, skipping"

# Cross-compilation targets Koffr actually supports (ENF-030).
cross:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-amd64 ./cmd/koffr
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY)-linux-arm64 ./cmd/koffr

tidy:
	go mod tidy

clean:
	rm -rf bin
