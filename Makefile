# SPDX-License-Identifier: MIT
.DEFAULT_GOAL := check
SHELL := /bin/bash

GO_FILES := $(shell find . -name '*.go' -not -path './node_modules/*' -not -path './.claude/*' 2>/dev/null)

.PHONY: fmt lint test gen check-gen check coverage

## fmt: format Go and web files in place
fmt:
	@if [ -n "$(GO_FILES)" ]; then gofmt -w $(GO_FILES); fi
	pnpm format

## lint: run Go and web linters
lint:
	@if [ -n "$(GO_FILES)" ]; then golangci-lint run ./...; else echo "no Go files, skipping golangci-lint"; fi
	pnpm lint

## test: run Go and web tests
test:
	@if [ -n "$(GO_FILES)" ]; then go test -race ./...; else echo "no Go files, skipping go test"; fi
	pnpm test

## gen: regenerate code from the contract (runs scripts/gen.sh when present)
gen:
	@if [ -x scripts/gen.sh ]; then scripts/gen.sh; else echo "scripts/gen.sh not present yet, nothing to generate"; fi

## check-gen: fail when generated code is out of date
check-gen: gen
	@test -z "$$(git status --porcelain -- .)" || { echo "generated files are out of date, run 'make gen' and commit"; git status --short -- .; exit 1; }

## check: narrow local checks (format check, SPDX, vet, typecheck)
check:
	scripts/check-spdx.sh
	@if [ -n "$(GO_FILES)" ]; then test -z "$$(gofmt -l $(GO_FILES))" && go vet ./...; else echo "no Go files, skipping go checks"; fi
	pnpm format:check
	pnpm typecheck
	scripts/test-check-coverage.sh

## coverage: race tests with the per-layer coverage floors from TESTING.md
coverage:
	mkdir -p coverage
	scripts/heavy go test -race -coverprofile=coverage/cover.out ./...
	scripts/check-coverage.sh coverage/cover.out
