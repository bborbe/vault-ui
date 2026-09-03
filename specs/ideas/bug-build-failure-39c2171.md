---
status: idea
kind: bug
---

# Build Failure: bborbe/vault-ui

Filed automatically by the build-fix agent for the CI episode `39c21719cca188696f0f494c5cb97f13d2420824`.

## Summary

The default-branch build for `bborbe/vault-ui` is failing; the build-fix diagnosis classified this as a code/test bug (verdict `file_spec`).

## Reproduction

Failing workflow(s): test

Episode SHA: `39c21719cca188696f0f494c5cb97f13d2420824`

Log evidence:

```text
| Workflow | Job | Failed Step | Run |
|---|---|---|---|
| CI | test | Run linter | [Run](https://github.com/bborbe/vault-ui/actions/runs/33718347845) |
```

## Expected vs Actual

**Expected:** green CI on the default branch.
**Actual:** `The linter (ruff) found 2 code defects in src/vault_ui/api/tasks.py: an unused variable 'vault_config' (F841) and a missing 'raise ... from err' in an except clause (B904). These are code bugs, not dependency or configuration issues.`

## Why this is a bug

The default-branch build is the repository's quality gate; a red build blocks merges. Diagnosis: `The linter (ruff) found 2 code defects in src/vault_ui/api/tasks.py: an unused variable 'vault_config' (F841) and a missing 'raise ... from err' in an except clause (B904). These are code bugs, not dependency or configuration issues.`
