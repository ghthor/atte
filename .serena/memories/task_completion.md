# Completion Gate

Run all required checks before considering a repository change complete:

- `nix develop --command atte run test.build`
- `nix develop --command atte run test.go`
- `nix develop --command atte run codegen.go`
- `nix develop --command atte run codegen.fmt`
- `nix develop --command atte run codegen.rendered`
- `nix develop --command atte run lint.go`

Use `nix develop --command atte run --help` for target and selector discovery. See `AGENTS.md` for the editing and Go test requirements.