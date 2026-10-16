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

.PHONY: build test lint check scenarios live cross clean image image-multiarch

build:
	$(GOBUILD) -o bin/vde ./cmd/vde

test:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

check: lint test build

# The end-to-end scenarios of testdata/scenarios with the fake provider, verbosely.
scenarios:
	go test ./cmd/vde -run TestScenarios -count=1 -v

# The opt-in smoke test against every real provider whose key is set (see CONTRIBUTING.md).
live:
	VDE_LIVE=1 go test ./cmd/vde -run TestLiveProviders -count=1 -v -timeout 60m

cross:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o bin/vde-linux-amd64 ./cmd/vde
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o bin/vde-linux-arm64 ./cmd/vde

clean:
	rm -rf bin

# Container image. `image` builds for the local architecture and loads it into the local
# Docker; `image-multiarch` builds linux/amd64 and linux/arm64 with buildx (set PUSH=1 to
# push, which needs a registry login; without it the multi-arch result stays in the cache).
IMAGE     ?= ghcr.io/kaanemec/vde
IMAGE_TAG ?= $(VERSION)
IMAGEARGS := --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE)

image:
	docker build $(IMAGEARGS) -t $(IMAGE):$(IMAGE_TAG) .

image-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 $(IMAGEARGS) \
		-t $(IMAGE):$(IMAGE_TAG) $(if $(PUSH),--push,) .
