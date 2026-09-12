# gocritic lint policy and fixes

The project enables all gocritic checks through `.golangci.yml`, but intentionally disables these checks:

- `unnamedResult`
- `hugeParam`
- `importShadow`
- `rangeValCopy`

Do not reintroduce refactors for those checks unless the project decision changes. Existing range-loop improvements were retained even after `rangeValCopy` was disabled.

The remaining gocritic cleanup used behavior-preserving changes: combine consecutive `append` calls, simplify string concatenation, combine adjacent same-typed parameters, remove commented-out code, register direct function values instead of equivalent wrappers, and move `os.Exit` outside the scope containing deferred cleanup.

The detailed task plan is tracked in `gocritic-fixes.md`. After lint-related changes, verify with `atte run lint.go`; the expected result for the current tree is `0 issues`.
