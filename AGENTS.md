# Repository instructions

## Formatting

Use `treefmt` for all formatting and formatting verification in this repository. Run it through the Nix development shell so the repository's `treefmt.toml` configuration is applied:

```bash
nix develop --command treefmt
```

Verify that files are formatted with:

```bash
nix develop --command treefmt --ci
```

Prefer these commands over invoking `gofmt`, `nixfmt`, or other formatters directly.
