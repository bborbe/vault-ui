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
	$(HOME)/Documents/workspaces/go/bin/vault-ui
