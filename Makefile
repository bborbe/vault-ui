# Development targets

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
# The binary binds the admin port :9090 unconditionally (spec 021 forbids a port
# knob), so this cannot run while the live board is up.
.PHONY: e2e
e2e:
	@if lsof -nP -iTCP:9090 -sTCP:LISTEN >/dev/null 2>&1; then \
		echo "make e2e: port 9090 is held by the live board. Stop it first:"; \
		echo "  launchctl bootout gui/$$(id -u) $(HOME)/Library/LaunchAgents/com.github.bborbe.vault-ui.plist"; \
		echo "  ... run make e2e ..."; \
		echo "  launchctl bootstrap gui/$$(id -u) $(HOME)/Library/LaunchAgents/com.github.bborbe.vault-ui.plist"; \
		exit 1; \
	fi
	go run $(PLAYWRIGHT_GO_MODULE)/cmd/playwright@$(PLAYWRIGHT_GO_VERSION) install chromium
	go test -mod=mod -tags e2e -count=1 -timeout 15m ./e2e/
