# Development targets
.PHONY: sync
sync:
	uv sync --all-extras

.PHONY: format
format:
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
test: sync
	uv run pytest || test $$? -eq 5

.PHONY: test-integration
test-integration:
	uv run pytest -m integration -v

.PHONY: precommit
precommit: sync format test check
	@echo "✓ All precommit checks passed"

# Run server
.PHONY: run
run: sync
	uv run vault-ui

# Run server with auto-reload on code changes
.PHONY: watch
watch: sync
	uv run uvicorn vault_ui.__main__:app --reload --host 127.0.0.1 --port 8000
