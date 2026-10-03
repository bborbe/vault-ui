# Development targets

.PHONY: sync
sync:
	uv sync --all-extras

.PHONY: build
build:
	go build -o bin/vault-ui .

.PHONY: go-format
go-format:
	go fmt ./...

.PHONY: go-vet
go-vet:
	go vet ./...

.PHONY: go-test
go-test:
	go test -race ./...

.PHONY: format
format: go-format
	uv run ruff format .
	uv run ruff check --fix . || true

.PHONY: lint
lint:
	uv run ruff check .

.PHONY: typecheck
typecheck:
	uv run mypy src

.PHONY: check
check: lint typecheck

.PHONY: test
test: sync go-test
	uv run pytest || test $$? -eq 5

.PHONY: test-integration
test-integration:
	uv run pytest -m integration -v

.PHONY: precommit
precommit: sync format go-vet test check
	@echo "✓ All precommit checks passed"

# Run server
.PHONY: run
run: sync
	uv run vault-ui

# Run server with auto-reload on code changes
.PHONY: watch
watch: sync
	uv run uvicorn vault_ui.__main__:app --reload --host 127.0.0.1 --port 8000
