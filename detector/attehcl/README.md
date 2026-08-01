# `atte.hcl` configuration

The HCL detector discovers `test`, `codegen`, and `lint` blocks in every
`atte.hcl` file. Each block has a required `script` and may also have
`depends_on` and `triggered_by` expressions. ## Globals and locals

Configuration values can be shared with directory-scoped globals:

```hcl
# atte.hcl at the repository root
globals {
  go_ver = "1.26"
  script = "./root.sh"
}

# service/atte.hcl
locals {
  script  = "./service.sh"
  version = global.go_ver
}

test "service" {
  script = local.script
}
```

A `globals` block is inherited by `atte.hcl` files in descendant directories.
Definitions are overlaid from the repository root toward the consuming file,
so a nearer definition overrides an inherited value while unrelated values
remain available. Missing intermediate configuration files do not interrupt
inheritance.

Multiple `globals` blocks in one file are merged. Duplicate attribute names
are rejected. Globals are accessed with the `global.<name>` namespace.

A `locals` block is visible only in the file that declares it. Multiple `locals`
blocks in one file are merged, and duplicate names are rejected. Locals may
reference effective `global.<name>` values and other locals in the same file;
locals never propagate to parent, sibling, or child files. Local values are
accessed with the `local.<name>` namespace.

Expressions are evaluated while configuration is decoded and must resolve to
known values. The `gopkg_test("module/path")` function can be used in target
expressions, including `depends_on` and `triggered_by`, to refer to a Go
package test.
