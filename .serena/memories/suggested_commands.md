---
name: suggested-commands
description: Repository development commands
metadata:
  type: project
---

- Build: `go build ./...`
- Tests: `go test ./...`
- Format: `nix develop --command treefmt`; verify with `nix develop --command treefmt --ci`.
- Lint: `nix develop --command golangci-lint run`.
- Refresh graph snapshot: `ATTE_CODEGEN=1 go test ./detector/attego/... -run TestGraphvizSnapshot`.
