# Repository instructions

## Go tests

Use `github.com/shoenig/test` for behavioral assertions in Go tests. Use `github.com/shoenig/test/must` for test setup operations that can fail, such as creating fixtures, writing files, or running commands; keep those operations under `must.NoError` (or the most specific setup-oriented `must` assertion).

Avoid using `must` assertions for expectations in table-driven tests: their failure context may not include the complete table-case context. Use the assertions from `github.com/shoenig/test` there instead. Do not use ad hoc `if` checks, `t.Fatal`, `t.Error`, or other assertion libraries for test assertions.

When `must.True` or `must.False` is appropriate outside table-driven assertions, include a concise failure description as the final argument, wrapped with `must.Sprint`, for example:

```go
must.True(t, got.Valid(), must.Sprint("resolved selector should be valid"))
must.False(t, got.EscapesRoot(), must.Sprint("selector should remain inside the repository"))
```

Do not pass a raw string as the description: the `must` package expects a `must.Setting`.

When tests create multiline strings, use raw string literals. Start the string on the following line and use `strings.TrimLeft` to remove the leading whitespace added for readability.

When several tests repeat the same multi-step setup (temp directory, file writes, git init/commit, etc.), introduce a small fixture type with methods rather than copy-pasting the boilerplate at each call site. Keep the fixture's low-level building blocks (e.g. running an arbitrary script) available so tests needing non-standard setup are not forced through a higher-level convenience method.

For functional tests or repeated behavior checks, prefer a local helper with a short, descriptive name (for example, `match`, `complete`, or `resolve`) that calls `t.Helper()` and accepts the varying inputs and expected outputs. Invoke the helper directly for each case; use `t.Run` to group cases when they represent distinct error categories or behaviors.

## Go style

Prefer `make([]T, 0, <len>)` over a `[]T{}` literal when building up a slice by appending. Use a known or estimated length for the capacity hint.

## Go package usage

All current usage of the Go packages in this repository is within this repository. When refactoring, do not assume unknown external usages need to be preserved.

## Code navigation

When working with Go code, always try using the LSP to obtain structural information about the code before editing, such as definitions, references, symbols, types, and call hierarchies.

The LSP is read-only; use the repository editing tools to make changes.

## Go dependencies

When a Go package is needed as a dependency or for documentation, use `go get` to fetch it and add it to `go.mod`. Do not manually edit dependency entries or fetch packages through another method.

## Verifying changes

Before treating a change as complete, run:

```bash
nix develop --command atte run test.build
nix develop --command atte run test.go
nix develop --command atte run codegen.go
nix develop --command atte run codegen.fmt
nix develop --command atte run codegen.rendered
nix develop --command atte run lint.go
```

Use `nix develop --command atte run --help` for selector and target-discovery details.
