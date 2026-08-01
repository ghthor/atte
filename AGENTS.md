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

## Go linting

Run `golangci-lint` through the Nix development shell so the repository's pinned tool version is used:

```bash
nix develop --command golangci-lint run
```

Use the repository's root `.golangci.yml` configuration. Do not invoke a host-installed `golangci-lint` when validating changes. `treefmt` remains the canonical formatter and formatting verifier.

## Go tests

Use `github.com/shoenig/test/must` for all assertions in Go tests. Do not use ad hoc `if` checks, `t.Fatal`, `t.Error`, or other assertion libraries for test assertions.

Keep setup operations that can fail under `must.NoError` as well.

## Go style

Prefer `make([]T, 0, <len>)` over a `[]T{}` literal when building up a slice by appending. Use a known or estimated length for the capacity hint.

## Go dependencies

When a Go package is needed as a dependency or for documentation, use `go get` to fetch it and add it to `go.mod`. Do not manually edit dependency entries or fetch packages through another method.
