# Commands

- Inspect repository state and source: `git status --short`; `rg -n 'pattern' --glob '*.go'`.
- Discover CLI selectors/targets: `nix develop --command atte run --help`.
- Build/test/lint: `nix develop --command atte run test.build`; `nix develop --command atte run test.go`; `nix develop --command atte run lint.go`.
- Generated validation/formatting: `nix develop --command atte run codegen.go`; `nix develop --command atte run codegen.fmt`; `nix develop --command atte run codegen.rendered`.
- Use Serena LSP tools for structural Go navigation and Serena editing tools for repository changes, per `AGENTS.md`.