# vde build, test and lint. CGO is disabled for every binary; `go test -race` keeps the
# toolchain default because the race detector needs cgo on Linux.

MODULE  := github.com/kaanemec/vde
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"

.PHONY: build test lint check cross clean

build:
	$(GOBUILD) -o bin/vde ./cmd/vde

test:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

check: lint test build

cross:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/vde-linux-amd64 ./cmd/vde
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/vde-linux-arm64 ./cmd/vde

clean:
	rm -rf bin
