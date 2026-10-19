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

.PHONY: build test lint check scenarios eval live cross clean image conformance \
	ci tidy test-race vulncheck fuzz storage deps hooks release release-check dist image-release publish

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
	rm -rf bin dist

# Container image for the local architecture, loaded into the local Docker. The multi-arch
# release image is `image-release` below.
IMAGE     ?= ghcr.io/kaanemec/vde
IMAGE_TAG ?= $(VERSION)
IMAGEARGS := --build-arg GO_VERSION=$(GO_TOOLCHAIN:go%=%) \
	--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE)

image:
	docker build $(IMAGEARGS) -t $(IMAGE):$(IMAGE_TAG) .

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

# Release (CONTRIBUTING.md, "Releasing"): `make release VERSION=vX.Y.Z` checks the tree, runs
# `make ci`, builds the archives into dist/, builds and pushes the multi-arch image, then tags
# and creates the GitHub release. DRY_RUN=1 does everything but push, tag and publish, and
# may run on a branch other than main. Credentials stay with gh and docker; none is read here.
DIST        := dist
REL_VERSION := $(VERSION:v%=%)
REL_LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(REL_VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)
REL_PLATFORMS := linux_amd64 linux_arm64 darwin_amd64 darwin_arm64
REL_STABLE    := $(if $(findstring -,$(REL_VERSION)),,yes)
REL_TAGS      := -t $(IMAGE):$(REL_VERSION) $(if $(REL_STABLE),\
	-t $(IMAGE):$(shell echo $(REL_VERSION) | cut -d. -f1-2) -t $(IMAGE):latest)

release: release-check
	$(MAKE) --no-print-directory ci
	$(MAKE) --no-print-directory dist
	$(MAKE) --no-print-directory image-release
	@if [ -n "$(DRY_RUN)" ]; then echo "release: dry run of $(VERSION) done; nothing was tagged, pushed or published"; \
	else $(MAKE) --no-print-directory publish; fi

release-check:
	@test "$(origin VERSION)" = "command line" && echo "$(VERSION)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$' \
		|| { echo "release: set VERSION=vX.Y.Z (or vX.Y.Z-rc.N) on the command line" >&2; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "release: the working tree is not clean" >&2; exit 1; }
	@test -n "$(DRY_RUN)" || test "$$(git branch --show-current)" = main \
		|| { echo "release: not on main (DRY_RUN=1 allows another branch)" >&2; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(VERSION)" >/dev/null || { echo "release: tag $(VERSION) exists locally" >&2; exit 1; }
	@! git ls-remote --exit-code --tags origin "refs/tags/$(VERSION)" >/dev/null || { echo "release: tag $(VERSION) exists on origin" >&2; exit 1; }
	@grep -Fq '## [$(REL_VERSION)]' CHANGELOG.md || { \
		if [ -n "$(DRY_RUN)" ]; then echo "release: WARNING CHANGELOG.md has no section for $(REL_VERSION) (dry run continues)" >&2; \
		else echo "release: CHANGELOG.md has no section '## [$(REL_VERSION)]'" >&2; exit 1; fi; }
	@test -n "$(DRY_RUN)" || gh auth status >/dev/null 2>&1 || { echo "release: gh is not logged in" >&2; exit 1; }

# Four static binaries built with the go.mod toolchain, one tar.gz each (binary, LICENSE,
# README.md, CHANGELOG.md, tracked examples/), checksums.txt and the release notes, verified:
# checksums, toolchain, static linking and `vde version` (linux binaries in Docker).
dist:
	rm -rf $(DIST) && mkdir -p $(DIST)
	@set -e; for p in $(REL_PLATFORMS); do \
		os=$${p%_*}; arch=$${p#*_}; dir=$(DIST)/vde_$(REL_VERSION)_$$p; \
		echo "dist: $$os/$$arch"; \
		GOTOOLCHAIN=$(GO_TOOLCHAIN) CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(REL_LDFLAGS)" -o $$dir/vde ./cmd/vde; \
		COPYFILE_DISABLE=1 tar -czf $$dir.tar.gz -C $$dir vde -C $(CURDIR) \
			LICENSE README.md CHANGELOG.md $$(git ls-files examples); \
	done
	cd $(DIST) && shasum -a 256 *.tar.gz > checksums.txt && shasum -a 256 -c checksums.txt
	@set -e; for p in $(REL_PLATFORMS); do \
		bin=$(DIST)/vde_$(REL_VERSION)_$$p/vde; \
		go version $$bin | grep -q ' $(GO_TOOLCHAIN)$$' || { echo "dist: $$bin not built with $(GO_TOOLCHAIN)" >&2; exit 1; }; \
		case $$p in linux_*) \
			file $$bin | grep -q 'statically linked' || { echo "dist: $$bin is not statically linked" >&2; exit 1; }; \
			out=$$(docker run -q --rm --platform linux/$${p#*_} -v $(CURDIR)/$$bin:/vde:ro --entrypoint /vde \
				gcr.io/distroless/static:nonroot version);; \
		*) out=$$(test "$$p" != "$$(go env GOHOSTOS)_$$(go env GOHOSTARCH)" || $$bin version);; esac; \
		test -z "$$out" || { echo "$$out" | grep -Fq "vde $(REL_VERSION) " || { echo "dist: $$bin prints '$$out'" >&2; exit 1; }; \
			echo "dist: $$p: $$out"; }; \
	done
	awk -v h='## [$(REL_VERSION)]' 'index($$0, h) == 1 { f = 1; next } /^## \[/ { f = 0 } f' CHANGELOG.md > $(DIST)/notes.md
	printf '\nContainer image: `$(IMAGE):$(REL_VERSION)`\n' >> $(DIST)/notes.md

# The linux/amd64 and linux/arm64 image with buildx, tagged X.Y.Z (and X.Y and latest for a
# stable version), pushed (loaded locally with DRY_RUN=1), then `vde version` on both platforms.
image-release:
	@if ! docker buildx version >/dev/null 2>&1; then \
		if [ -n "$(DRY_RUN)" ]; then echo "image-release: WARNING docker buildx is not installed, image skipped in this dry run (brew install docker-buildx)" >&2; exit 0; fi; \
		echo "image-release: docker buildx is required (brew install docker-buildx)" >&2; exit 1; \
	fi; set -e; \
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg GO_VERSION=$(GO_TOOLCHAIN:go%=%) \
		--build-arg VERSION=$(REL_VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) \
		$(REL_TAGS) $(if $(DRY_RUN),--load,--push) .; \
	for p in linux/amd64 linux/arm64; do docker run --rm --platform $$p $(IMAGE):$(REL_VERSION) version; done

# The annotated tag and the GitHub release (pre-release for a suffixed version).
publish:
	git tag -a $(VERSION) -m $(VERSION)
	git push origin $(VERSION)
	gh release create $(VERSION) $(DIST)/*.tar.gz $(DIST)/checksums.txt --verify-tag --title $(VERSION) \
		--notes-file $(DIST)/notes.md $(if $(REL_STABLE),,--prerelease)
