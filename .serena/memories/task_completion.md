---
name: task-completion
description: Required verification before considering Go changes complete
metadata:
  type: project
---
Run `go build ./...`, `go test ./...`, `nix develop --command treefmt --ci`, and `nix develop --command golangci-lint run`. For repository tasks, also run the project targets:
`nix develop --command atte run test.build`
`nix develop --command atte run test.go`
`nix develop --command atte run codegen.go`
`nix develop --command atte run codegen.fmt`
`nix develop --command atte run codegen.rendered`
`nix develop --command atte run lint.go`
If unrelated tests fail, compare against an unmodified branch before attribution.
