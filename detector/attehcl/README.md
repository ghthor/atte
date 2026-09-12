# `atte.hcl` targets

The HCL detector discovers directory-local `test`, `codegen`, and `lint`
targets in `atte.hcl` files. Each target may have a `script` and may also have
`depends_on` and `triggered_by` expressions. Declaration-only target discovery
permits a missing script and does not evaluate target bodies; graph construction
and execution reject missing scripts for script-backed target kinds.

A file is evaluated independently. Target expressions cannot read declarations
from an ancestor, sibling, or child `atte.hcl` file. Repository-wide target
dependency resolution is a later graph-assembly concern.

`DeclaredTargets` returns `graphtarget.ID` values ordered by file path and source
order without evaluating locals, target attributes, functions, or registered target
decoders. Use `Targets` or `ConfigFor` when decoded target values are needed; use
`Graph` for repository paths and symbolic same-file target dependencies.

```hcl
test "service" {
  script = path("./service.sh")
  depends_on = ["./go.mod"]
}

codegen "schema" {
  script = "go generate ./..."
}
```

`script` accepts an inline string or a repository-relative `path(...)` value.
Dependency collections may contain repository paths, detector references
provided by supported HCL functions, and literal target traversals. Ordinary
expressions are evaluated while the file's targets are decoded and must
resolve to known values. Literal target traversals are preserved symbolically
for a later graph phase; target enumeration does not resolve them.

`locals` may be used as file-local target input. Multiple `locals` blocks are
merged and duplicate names are rejected. Locals may reference other locals in
the same file. The `global` namespace is unavailable, and locals do not
propagate to other files.
