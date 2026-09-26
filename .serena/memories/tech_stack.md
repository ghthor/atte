# Tooling

- Go module declares Go 1.26.0.
- Nix flake/development shell provides the project command `atte run` and build/test/codegen/lint targets.
- Tests use `github.com/shoenig/test` and `github.com/shoenig/test/must`.
- HCL evaluation uses `hashicorp/hcl/v2`; repository Git access uses go-git.
- CLI uses Cobra; formatting, generated checks, and lint are orchestrated by Nix `atte` targets.