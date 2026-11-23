# Development targets

# github.com/bborbe/vault-markdown is public and fetches anonymously, but
# proxy.golang.org and sum.golang.org still serve cached "not found" results
# for it from when the repo was private. Fetch this one path straight from
# GitHub and skip the checksum database for it, so every `go` target here works
# on a clean machine. Remove once
# `curl -sI https://proxy.golang.org/github.com/bborbe/vault-markdown/@v/v0.1.0.info`
# returns 200.
export GOPRIVATE ?= github.com/bborbe/vault-markdown

.PHONY: build
build:
	mkdir -p $(HOME)/Documents/workspaces/go/bin
	go build -buildvcs=false -o $(HOME)/Documents/workspaces/go/bin/vault-ui .

.PHONY: format
format:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test:
	go test -race ./...

.PHONY: precommit
precommit: format vet test

.PHONY: run
run: build
	VAULT_UI_LISTEN=127.0.0.1:8000 $(HOME)/Documents/workspaces/go/bin/vault-ui

PLAYWRIGHT_GO_MODULE  := github.com/mxschmitt/playwright-go
PLAYWRIGHT_GO_VERSION := v0.6201.1

# The board scenarios, in a real browser. Deliberately NOT part of precommit.
# The suite binds its own API and admin ports (VAULT_UI_LISTEN and
# VAULT_UI_ADMIN_LISTEN), so it runs alongside the live board and never stops it.
.PHONY: e2e
e2e:
	go run $(PLAYWRIGHT_GO_MODULE)/cmd/playwright@$(PLAYWRIGHT_GO_VERSION) install chromium
	go test -mod=mod -tags e2e -count=1 -timeout 15m ./e2e/
