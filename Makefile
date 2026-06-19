SHELL := /usr/bin/env bash

GO ?= go
GOOS ?= linux
GOARCH ?= amd64
CGO_ENABLED ?= 0
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || printf dev)

BIN_DIR := dist
BINARY := $(BIN_DIR)/wpcloud-site-git-deploy-$(GOOS)-$(GOARCH)
GO_CACHE_DIR := $(CURDIR)/.cache/go-build
GO_MOD_CACHE_DIR := $(CURDIR)/.cache/go-mod
GO_ENV := GOCACHE=$(GO_CACHE_DIR) GOMODCACHE=$(GO_MOD_CACHE_DIR)

SHELL_FILES := scripts/install.sh scripts/live-e2e.sh tests/*.sh

.PHONY: help clean deps build build-linux test vet syntax shellcheck conformance check vm-go vm-check live-e2e

help:
	@printf '%s\n' 'Targets:'
	@printf '%s\n' '  make deps        Download Go module dependencies into .cache/go-mod'
	@printf '%s\n' '  make build       Build the static Linux amd64 binary into dist/'
	@printf '%s\n' '  make build-linux Same as build, kept for readability in scripts'
	@printf '%s\n' '  make test        Run Go tests'
	@printf '%s\n' '  make vet         Run go vet'
	@printf '%s\n' '  make syntax      Run Bash syntax checks'
	@printf '%s\n' '  make shellcheck  Run shellcheck over maintained shell scripts'
	@printf '%s\n' '  make conformance Run black-box Go binary conformance tests'
	@printf '%s\n' '  make check       Run local static and Go checks'
	@printf '%s\n' '  make vm-go       Ensure the Lima Linux VM has a usable Go compiler'
	@printf '%s\n' '  make vm-check    Run Go checks inside the Lima Linux VM'
	@printf '%s\n' '  make live-e2e    Run the WP Cloud live E2E matrix'
	@printf '%s\n' '  make clean       Remove generated build and cache artifacts'

clean:
	if [[ -d "$(GO_MOD_CACHE_DIR)" ]]; then chmod -R u+w "$(GO_MOD_CACHE_DIR)"; fi
	rm -rf "$(BIN_DIR)" "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)" wpcloud-site-git-deploy

deps:
	mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	$(GO_ENV) $(GO) mod download

build build-linux:
	mkdir -p "$(BIN_DIR)" "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	$(GO_ENV) CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build -ldflags "-X github.com/aipokalyptik/wpcloud-site-git-deploy/internal/cli.Version=$(VERSION)" -o "$(BINARY)" ./cmd/wpcloud-site-git-deploy

test:
	mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	$(GO_ENV) $(GO) test ./...

vet:
	mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	$(GO_ENV) $(GO) vet ./...

syntax:
	bash -n $(SHELL_FILES)

shellcheck:
	shellcheck $(SHELL_FILES)

conformance: build
	tests/go_conformance.sh

check: test vet syntax shellcheck conformance
	bash tests/test_live_e2e_static.sh
	bash tests/test_vm_static.sh
	git diff --check

vm-go:
	scripts/ensure-lima-go.sh

vm-check:
	scripts/vm-check.sh

live-e2e:
	scripts/live-e2e.sh
