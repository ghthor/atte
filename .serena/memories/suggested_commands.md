# Commands

- Inspect: git status --short; rg -n pattern --glob *.go.
- Build/test/lint through Nix: nix develop --command atte run test.build; nix develop --command atte run test.go; nix develop --command atte run lint.go.
- Generated validation: nix develop --command atte run codegen.go; codegen.fmt; codegen.rendered.
- Discover targets/options: nix develop --command atte run --help.