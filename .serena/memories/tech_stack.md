---
name: tech_stack
description: Language and build toolchain
metadata:
  type: project
---
- Go 1.26.0 (`go.mod`).
- Nix flake provides development shell and pinned tooling; `treefmt` uses gofmt/nixfmt according to `treefmt.toml`.
- Tests use `github.com/shoenig/test/must`; CLI uses Cobra.
- Shell tooling includes Bash scripts under `tools/`, packaged into the Nix development shell.
