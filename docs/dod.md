# Definition of Done

After completing your implementation, review your own changes against each criterion below. These are quality checks you perform by inspecting your work — not commands to run (linting and tests already ran via `validationCommand`). Report any unmet criterion as a blocker.

## Code Quality

- Functions have type annotations (Python) or clear parameter names (JavaScript)
- Go code follows the bborbe conventions (errors wrapped with github.com/bborbe/errors, no silently ignored error returns, factory `Create*` functions contain no business logic)
- Error handling follows project conventions (no silently ignored errors)
- No debug output (print statements, console.log for debugging) — use structured logging

## Testing

- New code has good test coverage (target >= 80%)
- Changes to existing code have tests covering at least the changed behavior

## Documentation

- README.md is updated if the change affects usage, configuration, or setup
- Documentation is updated if the change affects behavior described in docs/
- CHANGELOG.md has an entry under `## Unreleased`
