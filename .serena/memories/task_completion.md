---
name: task-completion
description: Required verification before considering Go changes complete
metadata:
  type: project
---

Run `go build ./...`, `go test ./...`, `nix develop --command treefmt --ci`, and `nix develop --command golangci-lint run`. If unrelated tests fail, compare against an unmodified branch before attribution.
