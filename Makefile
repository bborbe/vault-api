DOCKER_REGISTRY ?= docker.io
IMAGE ?= bborbe/vault-api
BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD)
DIRS += $(shell find */* -maxdepth 0 -name Makefile -exec dirname "{}" \;)
ifeq ($(VERSION),)
	VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)
endif

include tools.env

# Package + publish the Helm chart in helm/ to OCI. Chart version comes from
# helm/Chart.yaml (independent of the binary VERSION). Requires a prior
# `helm registry login registry-1.docker.io`. Native Helm 3.8+ OCI push (NOT the
# chartmuseum cm-push plugin) — same as the sibling bborbe/agent and
# bborbe/recurring-task-creator charts.
CHART_OCI ?= oci://registry-1.docker.io/bborbe
.PHONY: helm-publish
helm-publish:
	@helm lint helm/
	@helm template smoke helm/ --set vaults[0].name=smoke --set vaults[0].repoUrl=git@example.com:smoke.git --set vaults[0].existingSecret=smoke >/dev/null || exit 1
	@helm package helm/ -d /tmp
	@helm push /tmp/git-rest-$$(awk '/^version:/{print $$2; exit}' helm/Chart.yaml).tgz $(CHART_OCI)

.PHONY: default
default: precommit

.PHONY: run
run:
	@go run -mod=mod main.go

.PHONY: precommit
precommit: ensure format generate test check addlicense
	@echo "ready to commit"

.PHONY: ensure
ensure:
	go mod tidy -e
	go mod verify
	rm -rf vendor

.PHONY: format
format:
	find . -type f -name 'go.mod' -not -path './vendor/*' -exec go run github.com/shoenig/go-modtool@$(GO_MODTOOL_VERSION) -w fmt "{}" \;
	go run github.com/incu6us/goimports-reviser/v3@$(GOIMPORTS_REVISER_VERSION) -project-name github.com/bborbe/vault-api -format -excludes vendor ./...
	find . -type d -name vendor -prune -o -type f -name '*.go' -print0 | xargs -0 -n 10 go run github.com/segmentio/golines@$(GOLINES_VERSION) --max-len=100 -w
	# golines last, then gofmt last so its wrapping is normalized and the gofmt lint check passes
	find . -type f -name '*.go' -not -path './vendor/*' -exec gofmt -w "{}" +

.PHONY: generate
generate:
	rm -rf mocks avro
	mkdir -p mocks
	echo "package mocks" > mocks/mocks.go
	go generate -mod=mod ./...

# -race=true catches data races but flakes on some CI runners (rare SIGSEGV
# during gexec.Build in cmd/*-style binary smoke tests). Default off; opt in
# via ENABLE_RACE=true for nightly/manual hardening runs.
TESTFLAGS_RACE = -race=false
ifdef ENABLE_RACE
	TESTFLAGS_RACE = -race=true
endif

.PHONY: test
test:
	go test -mod=mod -p=$${GO_TEST_PARALLEL:-1} -cover $(TESTFLAGS_RACE) $(shell go list -mod=mod ./... | grep -v /vendor/)

.PHONY: check
check: lint vet vulncheck osv-scanner trivy

.PHONY: lint
lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run --allow-parallel-runners --config .golangci.yml ./...

.PHONY: vet
vet:
	go vet -mod=mod $(shell go list -mod=mod ./... | grep -v /vendor/)


# GO-2026-5037/5038/5039 are Go 1.26.3 stdlib vulns (crypto/x509, mime, net/textproto),
# fixed in 1.26.4 — remove from this list once all CI runners and base images are on 1.26.4+.
VULNCHECK_IGNORE ?= GO-2026-4923 GO-2026-4514 GO-2022-0470 GO-2026-4772 GO-2026-4771 \
                    GO-2026-5037 GO-2026-5038 GO-2026-5039 GO-2026-6599 GO-2026-6600 GO-2026-6603 GO-2026-6604 GO-2026-6605 GO-2026-6607 GO-2026-6608 GO-2026-6609 GO-2026-6610 GO-2026-6611 GO-2026-6612 GO-2026-6613 GO-2026-6617 \
                    GO-2026-5932
# GO-2026-5932 is golang.org/x/crypto/openpgp — unmaintained, unsafe by design, and
# unfixed (govulncheck reports no fixed version). It reaches the module graph through
# github.com/bborbe/agent, which the change-event publisher needs for lib.Task; this
# service never imports openpgp. Revisit 2027-04-23 (mirrored in .trivyignore).

# Known-benign govulncheck failure modes we swallow. golang.org/x/tools v0.46.0
# panics on packages containing generic *types.TypeParam during SSA analysis
# (govulncheck v1.3.0+ surface via RuntimeTypes/AllFunctions). We treat that as
# "no findings" because the panic happens AFTER the package scan; any real
# vulnerabilities would have been emitted as JSON on stdout before the panic.
# Any OTHER govulncheck failure (network, bad args, permissions) is surfaced.
.PHONY: vulncheck
vulncheck:
	@PKGS="$(shell go list -mod=mod ./... | grep -v /vendor/)"; \
	IGNORE_JSON=$$(printf '%s\n' $(VULNCHECK_IGNORE) | jq -R . | jq -s .); \
	ERR=$$(mktemp); \
	trap 'rm -f "$$ERR"' EXIT INT TERM; \
	OUT=$$(go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) -format json $$PKGS 2>$$ERR); \
	RC=$$?; \
	if [ $$RC -ne 0 ] && ! grep -qE "typesinternal\.ForEachElement|ForEachElement called on type containing" "$$ERR"; then \
		echo "govulncheck failed (exit $$RC):" >&2; \
		cat "$$ERR" >&2; \
		exit $$RC; \
	fi; \
	REMAIN=$$(printf '%s' "$$OUT" | jq -rs --argjson ignore "$$IGNORE_JSON" \
		'(map(select(.osv != null)) | map({key: .osv.id, value: (.osv.summary // "")}) | from_entries) as $$sum | \
		 map(select(.finding != null) | .finding) | \
		 map(select(.osv as $$o | $$ignore | index($$o) | not)) | \
		 map("\(.osv)\t\(.trace[-1].module)@\(.trace[-1].version) -> \(.fixed_version)\t\($$sum[.osv] // "")") | \
		 unique | .[]'); \
	if [ -n "$$REMAIN" ]; then \
		echo "Unexpected vulnerabilities (ignored: $(VULNCHECK_IGNORE)):"; \
		printf '%s\n' "$$REMAIN" | column -t -s "$$(printf '\t')"; \
		exit 1; \
	else \
		echo "No unignored vulnerabilities found"; \
	fi

.PHONY: osv-scanner
osv-scanner:
	@if [ -f .osv-scanner.toml ]; then \
		echo "Using .osv-scanner.toml"; \
		go run github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION) --config .osv-scanner.toml --recursive .; \
	else \
		echo "No config found, running default scan"; \
		go run github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION) --recursive .; \
	fi

.PHONY: trivy
trivy:
	trivy fs \
	--db-repository ghcr.io/aquasecurity/trivy-db \
	--scanners vuln,secret \
	--quiet \
	--no-progress \
	--disable-telemetry \
	--exit-code 1 .

.PHONY: addlicense
addlicense:
	go run github.com/google/addlicense@$(ADDLICENSE_VERSION) -c "Benjamin Borbe" -y $$(date +'%Y') -l bsd $$(find . -name "*.go" -not -path './vendor/*')

.PHONY: buca
buca: build upload clean apply


# BUILD_TARGET selects where `make build` runs. `local` (the default) uses the
# local Docker daemon; any other value names a cluster alias (sun / nukedev /
# nukeprod) and builds inside that cluster as a Job rendered from
# hack/build-job.yaml. The cluster arm needs no Docker on this machine.
BUILD_TARGET ?= local
BUILD_TIMEOUT ?= 3600
BUILD_REPO_URL ?= https://github.com/$(IMAGE).git
BUILD_DOCKERFILE ?= Dockerfile
BUILD_PUSH_SECRET ?= buildkit-push
# vault-api is private, so the Job mints a GitHub App installation token from
# this secret before cloning. The mint image is pinned by digest.
BUILD_GIT_SECRET ?= git-app
BUILD_MINT_IMAGE ?= docker.prod.nuke.benjamin-borbe.de:443/scripts-mint-iat@sha256:dceb875286c07f3547096591a2824b0e1dbe9646c3c681107af0f617f8931be4
BUILD_MINT_PULL_SECRET ?= buildkit-push-nuke

.PHONY: build
build: check-version-tag check-go-mod
ifeq ($(BUILD_TARGET),local)
	DOCKER_BUILDKIT=1 \
	docker build \
	--no-cache \
	--rm=true \
	--platform=linux/amd64 \
	--build-arg DOCKER_REGISTRY=$(DOCKER_REGISTRY) \
	--build-arg BRANCH=$(BRANCH) \
	--build-arg BUILD_GIT_VERSION=$$(git describe --tags --always --dirty) \
	--build-arg BUILD_GIT_COMMIT=$$(git rev-parse --short HEAD) \
	--build-arg BUILD_DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
	-t $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION) \
	-f Dockerfile .
else
# The cluster arm publishes at VERSION and cannot use check-version-tag: that
# guard compares the local HEAD's tag, which is meaningless when the Job clones
# an arbitrary REF. It would also silently pass a VERSION that is merely the
# Makefile's default — `git describe --tags $(git rev-list --tags --max-count=1)`
# resolves to the newest release tag, so a bare `make build BUILD_TARGET=sun`
# would push over that released image. Require the caller to name the tag:
#   make build BUILD_TARGET=<cluster> REF=<sha> VERSION=<tag>
	@if [ "$(origin VERSION)" = "file" ] && [ -z "$(ALLOW_DEFAULT_VERSION)" ]; then \
		echo "ERROR: the cluster arm needs an explicit VERSION." >&2; \
		echo "  VERSION was defaulted to $(VERSION), which is the newest release tag." >&2; \
		echo "  Building over it would replace a released image." >&2; \
		echo "  Pass VERSION=<scratch-tag>, or set ALLOW_DEFAULT_VERSION=1 to build the default deliberately." >&2; \
		exit 1; \
	fi
	BUILD_TARGET='$(BUILD_TARGET)' \
	REPO_URL='$(BUILD_REPO_URL)' \
	REF='$(REF)' \
	TAG='$(VERSION)' \
	DOCKERFILE='$(BUILD_DOCKERFILE)' \
	IMAGE='$(DOCKER_REGISTRY)/$(IMAGE)' \
	DOCKER_REGISTRY='$(DOCKER_REGISTRY)' \
	PUSH='true' \
	PUSH_SECRET='$(BUILD_PUSH_SECRET)' \
	GIT_SECRET='$(BUILD_GIT_SECRET)' \
	MINT_IMAGE='$(BUILD_MINT_IMAGE)' \
	MINT_PULL_SECRET='$(BUILD_MINT_PULL_SECRET)' \
	TIMEOUT='$(BUILD_TIMEOUT)' \
	hack/build-cluster.sh
endif

.PHONY: check-version-tag
check-version-tag:
ifeq ($(BUILD_TARGET),local)
	@if [ -n "$(ALLOW_UNTAGGED_BUILD)" ]; then \
		echo "ALLOW_UNTAGGED_BUILD set — skipping version/tag check"; \
	else \
		head_tag=$$(git describe --tags --exact-match HEAD 2>/dev/null); \
		if [ "$$head_tag" != "$(VERSION)" ]; then \
			echo "ERROR: refusing to build $(VERSION) from this tree." >&2; \
			echo "  HEAD is at tag: $${head_tag:-<untagged>}" >&2; \
			echo "  building as:    $(VERSION)" >&2; \
			echo "  An image stamped vX.Y.Z must be built from the vX.Y.Z tag." >&2; \
			echo "  Fix: git checkout $(VERSION)   (or set ALLOW_UNTAGGED_BUILD=1 for a scratch build)" >&2; \
			exit 1; \
		fi; \
	fi
endif

.PHONY: check-go-mod
check-go-mod:
ifeq ($(BUILD_TARGET),local)
	@if [ -f "go.mod" ]; then \
		echo "go.mod found, running go mod vendor..."; \
		go mod vendor; \
	else \
		echo "go.mod not found, skipping go mod vendor."; \
	fi
endif


.PHONY: upload
upload:
ifeq ($(BUILD_TARGET),local)
	docker push $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION)
else
	@echo "upload: nothing to do — the cluster arm already pushed the image"
endif

.PHONY: clean
clean:
ifeq ($(BUILD_TARGET),local)
	docker rmi $(DOCKER_REGISTRY)/$(IMAGE):$(VERSION) || true
	# docker builder prune -a -f
	docker builder prune --max-used-space 21474836480 -f || true
	rm -rf vendor
else
	@echo "clean: nothing to do — the cluster arm leaves nothing on this host"
endif

.PHONY: apply
apply:
	@for i in $(DIRS); do \
		cd $$i; \
		echo "apply $${i}"; \
		make apply; \
		cd ..; \
	done

.PHONY: fix
fix:
	@for dir in $$(find $(CURDIR) -type d -name vendor -prune -o -name go.mod -exec dirname "{}" \; | grep -v '^$$'); do \
		cd $${dir}; \
		echo "fix $${dir}"; \
		go get github.com/go-git/go-git/v5@latest; \
		go get github.com/containerd/containerd@latest; \
		go get golang.org/x/crypto@latest; \
		go get golang.org/x/net@latest; \
	done