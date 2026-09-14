# Tooling

- Go 1.26 module.
- Nix development shell provides project commands.
- Tests use github.com/shoenig/test and github.com/shoenig/test/must.
- HCL uses hashicorp/hcl/v2; Git access uses go-git.
- Formatting/lint/code generation are orchestrated by atte targets under nix develop.