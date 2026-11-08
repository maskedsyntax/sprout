BINARY  := sprout
PKG     := github.com/maskedsyntax/sprout
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: all build install test test-race integration lint fmt vet tidy snapshot clean

all: fmt vet lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/sprout

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/sprout

test:
	go test ./...

test-race:
	go test -race ./...

# Talks to the real https://start.spring.io (override with SPROUT_IT_SERVER).
integration:
	go test -tags integration -run Live -count=1 ./...

lint:
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...
	go vet -tags integration ./...

tidy:
	go mod tidy

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist
