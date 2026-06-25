# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL 						= /usr/bin/env bash -o pipefail
.SHELLFLAGS 				= -ec
export REPO                 := ghcr.io/stackitcloud
VERSION 					?= $(shell git describe --dirty --tags --match='v*' 2>/dev/null || git rev-parse --short HEAD)
export TAG                  := $(VERSION)
IS_DEV                      ?= true
ifeq ($(IS_DEV),true)
REPO_POSTFIX                := -dev
endif

.PHONY: all
all: verify

##@ Tools

include ./hack/tools.mk

export PUSH ?= false

.PHONY: images
images: $(KO)
	KO_DOCKER_REPO=$(REPO)$(REPO_POSTFIX) $(KO) build --push=$(PUSH) \
		--image-label org.opencontainers.image.source="https://github.com/stackitcloud/application-load-balancer-controller" \
		--sbom none -t $(TAG) --base-import-paths \
		--platform linux/amd64,linux/arm64 \
		./cmd/application-load-balancer-controller

.PHONY: clean-tools-bin
clean-tools-bin: ## Empty the tools binary directory.
	rm -rf $(TOOLS_BIN_DIR)/* $(TOOLS_BIN_DIR)/.version_*

.PHONY: fmt
fmt: $(GOIMPORTS_REVISER) ## Run go fmt against code.
	go fmt ./...
	$(GOIMPORTS_REVISER) .

.PHONY: modules
modules: ## Runs go mod to ensure modules are up to date.
	go mod tidy

.PHONY: test
test: ## Run tests.
	./hack/test.sh ./cmd/... ./pkg/...

.PHONY: test-cover
test-cover: ## Run tests with coverage.
	go test -coverprofile cover.out ./...
	go tool cover -html cover.out -o cover.html

##@ Verification

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint against code.
	$(GOLANGCI_LINT) run ./...

.PHONY: check
check: lint test ## Check everything (lint + test).

.PHONY: verify-fmt
verify-fmt: fmt ## Verify go code is formatted.
	@if !(git diff --quiet HEAD); then \
		echo "unformatted files detected, please run 'make fmt'"; exit 1; \
	fi

.PHONY: verify-modules
verify-modules: modules ## Verify go module files are up to date.
	@if !(git diff --quiet HEAD -- go.sum go.mod); then \
		echo "go module files are out of date, please run 'make modules'"; exit 1; \
	fi

.PHONY: verify-generate
verify-generate: generate ## Verify go module files are up to date.
	@if !(git diff --quiet HEAD); then \
		echo "generate created a diff, please run 'make generate'"; exit 1; \
	fi

.PHONY: verify
verify: verify-fmt verify-modules verify-generate check

.PHONY: generate
generate:
	go generate ./...
