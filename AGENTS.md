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

Use `github.com/shoenig/test/must` for all assertions in Go tests. Prefer the most specific `must` assertion available; use `must.True` or `must.False` only when no specific assertion function expresses the check. Do not use ad hoc `if` checks, `t.Fatal`, `t.Error`, or other assertion libraries for test assertions.

Keep setup operations that can fail under `must.NoError` as well.

When tests create multiline strings, use raw string literals. Start the string on the following line and use `strings.TrimLeft` to remove the leading whitespace added for readability.

When several tests repeat the same multi-step setup (temp directory, file writes, git init/commit, etc.), introduce a small fixture type with methods rather than copy-pasting the boilerplate at each call site. Keep the fixture's low-level building blocks (e.g. running an arbitrary script) available so tests needing non-standard setup are not forced through a higher-level convenience method.

## Go style

Prefer `make([]T, 0, <len>)` over a `[]T{}` literal when building up a slice by appending. Use a known or estimated length for the capacity hint.

## Code navigation

When working with Go code, always try using the LSP to obtain structural information about the code before editing, such as definitions, references, symbols, types, and call hierarchies.

The LSP is read-only; use the repository editing tools to make changes.

## Go dependencies

When a Go package is needed as a dependency or for documentation, use `go get` to fetch it and add it to `go.mod`. Do not manually edit dependency entries or fetch packages through another method.

## Verifying changes

Before treating a change as complete, run:

```bash
go build ./...
go test ./...
ATTE_CODEGEN=1 go test -count=1 ./...
nix develop --command treefmt --ci
nix develop --command golangci-lint run
```

If a test fails and it is not obviously related to the change being made, check whether it also fails on the unmodified branch (e.g. `git stash` and rerun) before attributing the failure to the change.
