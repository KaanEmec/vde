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
# The Go release that builds images and release binaries: the toolchain line of go.mod.
GO_TOOLCHAIN := $(shell sed -n 's/^toolchain //p' go.mod)

.PHONY: build test lint check scenarios eval live cross clean image image-multiarch conformance \
	ci tidy test-race vulncheck fuzz storage deps hooks

build:
	$(GOBUILD) -o bin/vde ./cmd/vde

test:
	go test -race ./...

lint: # golangci-lint's standard set includes govet
	golangci-lint run ./...

check: lint test build

# The end-to-end scenarios of testdata/scenarios with the fake provider, verbosely.
scenarios:
	go test ./cmd/vde -run TestScenarios -count=1 -v

# The evaluation harness mechanics (docs/operator/evaluation.md) on testdata/eval with the
# fake provider, once per judge policy (docs/operator/judge-policy.md), so no key is needed.
# always and never replay testdata/eval/fake-script.yaml (a schema violation, then the
# ground truth): always ends every case accepted by the judge, never ends it extracted
# with no judge call. auto replays testdata/eval/fake-script-grounded.yaml (the ground
# truth first): cases whose values the document prints end grounded without a judge, the
# others go to the judge. A case the harness cannot run fails the target. Real runs are
# manual and recorded in docs/quality.md.
EVAL_OUT ?= eval-reports
EVAL     := go run ./cmd/vde eval --corpus testdata/eval --config testdata/scenarios/config/vde.yaml \
	--concurrency 4 --out $(EVAL_OUT)
eval:
	$(EVAL) --judge always --provider-script testdata/eval/fake-script.yaml
	$(EVAL) --judge never --provider-script testdata/eval/fake-script.yaml
	$(EVAL) --judge auto --provider-script testdata/eval/fake-script-grounded.yaml

# The opt-in smoke test against every real provider whose key is set (see CONTRIBUTING.md).
live:
	VDE_LIVE=1 go test ./cmd/vde -run TestLiveProviders -count=1 -v -timeout 60m

# Static linux/amd64 and linux/arm64 binaries into $(CROSS_OUT), checked to be statically linked.
CROSS_OUT ?= bin
cross:
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(CROSS_OUT)/vde-linux-amd64 ./cmd/vde
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o $(CROSS_OUT)/vde-linux-arm64 ./cmd/vde
	@for f in $(CROSS_OUT)/vde-linux-amd64 $(CROSS_OUT)/vde-linux-arm64; do \
		file $$f | grep -q 'statically linked' || { echo "$$f is not statically linked" >&2; exit 1; }; \
	done

clean:
	rm -rf bin

# Container image. `image` builds for the local architecture and loads it into the local
# Docker; `image-multiarch` builds linux/amd64 and linux/arm64 with buildx (set PUSH=1 to
# push, which needs a registry login; without it the multi-arch result stays in the cache).
IMAGE     ?= ghcr.io/kaanemec/vde
IMAGE_TAG ?= $(VERSION)
IMAGEARGS := --build-arg GO_VERSION=$(GO_TOOLCHAIN:go%=%) \
	--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE)

image:
	docker build $(IMAGEARGS) -t $(IMAGE):$(IMAGE_TAG) .

image-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 $(IMAGEARGS) \
		-t $(IMAGE):$(IMAGE_TAG) $(if $(PUSH),--push,) .

# The black-box conformance suite for the TravelInbox contract (docs/conformance/travelinbox-v1.md):
# builds vde, runs serve against a fake OpenAI-compatible provider, walks contract §14 items 1-9.
conformance:
	go test ./test/conformance/ -count=1

# Local CI: every check, in order, stopping at the first failure, with one summary line per
# step (CONTRIBUTING.md, "Checks"). Nothing is built to bin/; scratch output goes to $(CI_TMP).
CI_TMP       := $(patsubst %/,%,$(or $(TMPDIR),/tmp))/vde-ci

# step runs `make <target> <vars>` and records "<step> ok|FAILED (<seconds>s)".
define step
	@s=$$(date +%s); if $(MAKE) --no-print-directory $(1) $(2); then r=ok; else r=FAILED; fi; \
	echo "ci: $(strip $(1) $(2)) $$r ($$(( $$(date +%s) - s ))s)" | tee -a $(CI_TMP)/summary; test $$r = ok
endef

ci:
	@rm -rf $(CI_TMP) && mkdir -p $(CI_TMP)
	$(call step,tidy)
	$(call step,lint)
	$(call step,test-race,GOTOOLCHAIN=$(GO_TOOLCHAIN))
	$(call step,test-race,GOTOOLCHAIN=local)
	$(call step,vulncheck)
	$(call step,fuzz)
	$(call step,cross,CROSS_OUT=$(CI_TMP)/cross)
	$(call step,conformance)
	$(call step,eval,EVAL_OUT=$(CI_TMP)/eval-reports)
	$(call step,storage)
	@echo "ci: all steps passed"; cat $(CI_TMP)/summary

tidy:
	go mod tidy -diff

# The race suite in random order; `make ci` runs it with the go.mod toolchain and the local Go.
test-race:
	go version
	go test -race -shuffle=on ./...

# Known vulnerabilities in the modules and in the standard library of the go.mod toolchain,
# which builds the images and release binaries.
vulncheck:
	@command -v govulncheck >/dev/null || { echo "govulncheck is not on PATH: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2; exit 1; }
	GOTOOLCHAIN=$(GO_TOOLCHAIN) govulncheck ./...

# Every fuzz target for FUZZTIME each (go test runs one target per invocation). A failure
# leaves the input under the package's testdata/fuzz/; keep it there as a regression seed.
FUZZTIME ?= 20s
fuzz:
	@set -e; for target in \
		"FuzzReadSubmission ./internal/api" \
		"FuzzNew ./internal/document" \
		"FuzzHTMLText ./internal/document" \
		"FuzzParseJSON ./internal/extract" \
		"FuzzParseVerdict ./internal/extract" \
		"FuzzNormalize ./internal/ground" \
		"FuzzOpen ./internal/pdf"; do \
		set -- $$target; echo "fuzz: $$1 $$2"; \
		go test -run='^$$' -fuzz="^$$1\$$" -fuzztime=$(FUZZTIME) "$$2"; \
	done

# The storage drivers on PostgreSQL 17 and an S3-compatible store (RustFS), each in a
# throwaway container on a free local port, removed on exit. Runs the job and blob suites,
# the worker scaling tests and the Postgres/S3 tests of cmd/vde and internal/api. Docker
# must be running: without it the step fails, it never passes silently.
PG_IMAGE ?= postgres:17-alpine
S3_IMAGE ?= rustfs/rustfs:1.0.1
storage:
	@docker info >/dev/null 2>&1 || { echo "storage: Docker is not running; start it and rerun" >&2; exit 1; }
	@set -e; pg=vde-ci-postgres-$$$$; s3=vde-ci-s3-$$$$; \
	trap 'docker rm -f $$pg $$s3 >/dev/null 2>&1 || true' EXIT; trap 'exit 130' INT TERM; \
	docker run -d --rm --name $$pg -e POSTGRES_PASSWORD=vde -p 127.0.0.1::5432 $(PG_IMAGE) >/dev/null; \
	docker run -d --rm --name $$s3 -e RUSTFS_ACCESS_KEY=minio -e RUSTFS_SECRET_KEY=minio123 \
		-e RUSTFS_SSE_S3_MASTER_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
		-p 127.0.0.1::9000 $(S3_IMAGE) >/dev/null; \
	pgaddr=$$(docker port $$pg 5432/tcp | head -n 1); s3addr=$$(docker port $$s3 9000/tcp | head -n 1); \
	for i in $$(seq 60); do docker exec $$pg pg_isready -q -h 127.0.0.1 -U postgres && break; \
		test $$i -lt 60 || { echo "storage: PostgreSQL not ready" >&2; exit 1; }; sleep 1; done; \
	for i in $$(seq 60); do curl -s -o /dev/null http://$$s3addr && break; \
		test $$i -lt 60 || { echo "storage: S3 endpoint not ready" >&2; exit 1; }; sleep 1; done; \
	export VDE_TEST_POSTGRES="postgres://postgres:vde@$$pgaddr/postgres?sslmode=disable" \
		VDE_TEST_S3_ENDPOINT=http://$$s3addr VDE_TEST_S3_SSE=AES256 \
		AWS_ACCESS_KEY_ID=minio AWS_SECRET_ACCESS_KEY=minio123; \
	unset AWS_SESSION_TOKEN AWS_PROFILE; \
	go test -race -count=1 ./internal/job/... ./internal/blob/... ./internal/worker/...; \
	go test -race -count=1 -run 'Postgres|S3|Shared' ./cmd/vde/... ./internal/api/...

# Available module updates, run by hand before a release (govulncheck in `make ci` covers security).
deps:
	go list -m -u all | grep '\[' || echo "deps: every module is current"

# Use the versioned hooks in .githooks (pre-push runs `make check`).
hooks:
	git config core.hooksPath .githooks
