# 2026-10-29: Configurable HCL filename via a root setting block

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

Every HCL configuration file in a repository must be named `atte.hcl`. The name is hard-coded in two places (`attehcl.Filename` and `selector.HclFilename`, kept in sync by comment) and assumed by discovery, `target()` path resolution, `ConfigFor`, selector matching, and `atte run` alias preference. Repositories that want a different name for their per-directory configuration files (for example `build.hcl`) have no way to say so.

### Context

- Discovery walks `repo.ObjKeys` and keeps blobs whose base name is `attehcl.Filename` (`detector/attehcl/parser.go`).
- `target("path", "kind.name")` appends `Filename` to the path (`detector/attehcl/functions.go`).
- `ConfigFor` opens `tree.Blob(Filename)` for a directory (`detector/attehcl/api.go`), which backs `HCLSettings` and `config show`.
- `reference/selector` uses `HclFilename` in `Selector.HCL`, `ContainingDir`, and therefore `Target.Matches`. It cannot import `attehcl` (import cycle), so the name is duplicated as a constant.
- `cmd/run.go` builds `relative + "/" + attehcl.Filename` to exclude the current directory's own file from alias fallback.
- Setting blocks (`hclblock.SettingBlock`) are attached per Sensor and may appear once per file, in any file. There is no notion of a repository-wide setting today.

### Goals

- A repository can declare, in one place, the filename used for its per-directory HCL configuration files.
- The declaration lives in the repository root `atte.hcl`, which keeps its fixed name so it can be found without configuration.
- The declaration is a setting block, reusing the existing `hclblock.SettingBlock` machinery and `config show` output.
- Discovery, `target()` resolution, `ConfigFor`, selector matching, and `atte run` all honor the configured filename.
- Repositories without the block behave exactly as today (`atte.hcl` everywhere).

### Non-Goals

- Multiple HCL filenames or glob patterns within one repository.
- Per-directory overrides of the filename.
- Reading HCL files from outside the git tree (untracked files, environment variables, CLI flags).
- Changing selector syntax; `//path/<file>#id` keeps working with the configured name in place of `atte.hcl`.

### Proposed Solution

Add a built-in setting block, owned by `attehcl`, to the root `atte.hcl`:

```hcl
atte {
  filename = "build.hcl"
}
```

`filename` is a required literal string naming a bare file (no path separators, not empty) that ends in `.hcl`. It defaults to `atte.hcl`. The block is modeled on Terraform's `terraform {}` block: one repository-wide place for settings about the configuration itself.

Evaluation gains a bootstrap step before discovery: read the root `atte.hcl` if it exists, parse it, and decode the `atte` block with an empty `hcl.EvalContext` (no locals or functions). The result is a repository `Layout` value (`Filename string`) that is threaded through the evaluator, `readHCLFiles`, `targetFileFromPath`, `configForWithCapabilities`, and exposed to callers (`cmd`, `attego`) that currently read the constant.

Rules:

- The `atte` block is only valid in the root `atte.hcl`. It is an error in any other file, including a root file with the configured name when that differs from `atte.hcl`.
- The root `atte.hcl` is always read and is a normal configuration file: it may also declare `locals`, targets, and other setting blocks.
- All other directories are read only through the configured filename. When the configured filename differs from `atte.hcl`, a file named `atte.hcl` outside the root is an error, as is a root file with the configured name. Every directory has at most one configuration file.
- `atte` is added to `reservedHCLBlockNames` so Sensors cannot attach a block with that name.

`reference/selector` stops hard-coding the name: `Selector.HCL`, `ContainingDir`, and `Target.Matches` take the filename (or a `Layout`) from the caller, and `selector.HclFilename` is removed.

## Detailed Design

### Bootstrap

`attehcl.LoadLayout(ctx, repo) (Layout, error)`:

1. If the root tree has no `atte.hcl` blob, return the default layout.
2. Parse the file. Find top-level `atte` blocks; more than one is an error. Labels are an error.
3. Decode with the block schema `{ filename: required string }` and an empty eval context. Any expression that is not a constant is an error.
4. Validate `filename`: non-empty, `path.Base(name) == name`, not `.` or `..`, and `path.Ext(name) == ".hcl"`.

The `atte` block is reported through `ConfigFor` / `config show` for the root like other setting blocks, using `hclblock.NewSettingBlock` so it shares the typed decode path.

### Threading the layout

- `evaluator` carries `layout Layout`; `newEvaluator` and `newEvaluatorForFile` call `LoadLayout` first and pass it to `readHCLFiles`.
- `readHCLFiles` keeps a blob when it is the root `atte.hcl`, or when its base name equals `layout.Filename` and it is not in the root. When `layout.Filename` differs from `atte.hcl`, a blob named `atte.hcl` outside the root or a blob named `layout.Filename` in the root is an error naming the offending path and the configured filename. When `layout.Filename == "atte.hcl"` these are the same rule as today.
- `targetFileFromPath` resolves the `path` argument to a directory, then names the file in it: `atte.hcl` if the resolved directory is the root, `layout.Filename` otherwise. The choice is made on the resolved directory, so `//`, `../` from a first-level directory, and `//sub/..` all reach the root `atte.hcl`.
- An empty `path` (`target("", "kind.name")`) refers to the file the call is declared in, equivalent to `"."`. It is currently an error. To reference the root from anywhere, write `target("//", "kind.name")`; `""` only reaches the root when the declaring file is the root.
- `configForWithCapabilities` opens `atte.hcl` for the root tree and `layout.Filename` elsewhere.
- `Detector` exposes `Layout(ctx, repo)` so `cmd` and `attego` do not re-parse.

### One configuration file per directory

When `filename` is not `atte.hcl`, the root directory's configuration file is `atte.hcl` and every other directory's is `<filename>`. A root blob named `<filename>` and a non-root blob named `atte.hcl` are both errors rather than being ignored: a stray `atte.hcl` almost always means a contributor did not know about the configured name, and silently dropping its targets would hide that mistake.

## Cross cutting concerns

- Error messages must name the file and block range, as other `attehcl` parse errors do.
- Performance: bootstrap reads one extra blob per evaluation; `readHCLFiles` already walks `ObjKeys`.
- The `selector` package stays free of `attehcl` imports; it receives the filename as data.

## Alternatives considered

- CLI flag or environment variable. Rejected: the name belongs to the repository, must be visible to every tool and contributor, and should be versioned with the code.
- Allow the block in any file, applying to that subtree. Rejected: discovery would have to be incremental and order-dependent; a single root declaration keeps discovery one pass.
- Keep the duplicated constant in `selector` and only make `attehcl` configurable. Rejected: `Target.Matches` would mis-strip the directory for the configured name, breaking `atte run` selectors.
- Searching for several candidate filenames. Rejected: ambiguous when more than one exists and slower; a single declared name is explicit.
- Ignore stray `atte.hcl` files outside the root when the name differs. Rejected: silently dropping targets hides a likely mistake; an error is cheap to fix.
- Allow any extension for `filename`. Rejected for now: requiring `.hcl` keeps editor and formatter associations working and leaves room to relax later.

## Future plans

- Other repository-wide settings (for example default relation kinds or function namespaces) can be added as attributes of the `atte` block.

## Other reading

- `idr/202610281851-type-safe-decoded-values-for-hcl-setting-blocks-and-target-kinds.md` for the setting block API this reuses.
- `detector/attehcl/README.md` for the current file layout and evaluation phases.

## Implementation (ephemeral)

Decisions:

- Block name is `atte`, by analogy with Terraform's `terraform {}` block.
- The root `atte.hcl` stays a regular configuration file. An `atte.hcl` and a `<filename>` in the same directory is an error.
- Stray `atte.hcl` files outside the root are an error, not ignored.
- `filename` must end in `.hcl`.

Implemented:

- [x] `detector/attehcl/layout.go`: `Layout{Filename}`, `DefaultLayout`, `Layout.File(dir)`, `Layout.Filenames()`, `LoadLayout(ctx, repo)`, the built-in `atte` setting block (`LayoutBlock`, decoded to `Layout`, projected as `{filename}` in `config show`). `attehcl.Filename` is now `attehcl.RootFilename`.
- [x] `readHCLFiles` takes the layout and errors on a non-root `atte.hcl` or a root file with the configured name (`Layout.owns`).
- [x] `newEvaluator` loads the layout; `newEvaluatorForFile` takes it. `ConfigFor` loads it and opens `layout.File(tree)`.
- [x] `atte` is always present in the evaluator's setting blocks and is rejected outside the root `atte.hcl` in `normalizeBlocks`. Labels and duplicates reuse the existing setting-block checks, and `LoadLayout` repeats them because it runs before normalization.
- [x] `reservedHCLBlockNames` gained `atte`, and target kinds are now checked against the reserved names too (previously only setting blocks were).
- [x] `target()`: `reference.ResolveTreeFromTree` resolves the path to a directory, then `Layout.File` names the file. `""` and `"."` mean the declaring directory, `"//"` and `"../"` reaching the root give `atte.hcl`.
- [x] `reference/selector`: `HclFilename` and `ContainingDir` are removed. `Selector.Tree`, `Selector.HCL`, and `Selector.StringShort` take the HCL filenames as variadic arguments.
- [x] `cmd`: `loadRunTargets` loads the layout; `runcomp.Match` takes the filenames; `preferLocalRunTargets` no longer builds the file name.
- [x] `examples/attehcl-custom-filename` (mock repository, `main_test.go`, wired into `atte_test.sh`).
- [x] Docs: `detector/attehcl/README.md` (new section, reserved names, evaluation phase 0).
- [x] Tests: `detector/attehcl/layout_test.go` (layout loading and validation, discovery, misplaced files, root-only block, `target()` resolution, `ConfigFor`), `reference` (`ResolveTreeFromTree`), `reference/selector` (`Target.Dir`, custom filename matching and `Tree`), `cmd/runcomp` (custom filename completion), `detector` (reserved `atte` for setting blocks and target kinds).
- [x] Verification: `test.build`, `codegen.go`, `codegen.fmt`, `codegen.rendered`, and `lint.go` pass. `test.go` fails only in `atte_test.sh` on `examples/attehcl-custom-block` and `examples/attehcl-go-test-override`: their `go.mod` files are stale after `go: bump go-git modules` (`go: updates to go.mod needed`), which also fails at `HEAD` without this change. All three examples pass in a scratch copy after `go mod tidy`.

Details beyond the proposal:

- `selector.Target` carries a `Dir` field instead of `Target.Matches` receiving the filename. `Matches` is called through `Scanner.TargetMatches`, which has no repository and therefore no layout, but the Sensor that builds the Target always knows the declaring directory. `selector.HCL(filePath, ...)` and `selector.GoTest(dir)` set `Dir`, so `ContainingDir` is gone and `cmd/run.go` reads `presentation.Dir`. Only the string-based `Selector` helpers (used by shell completion) take filenames.
- `Detector.Layout(ctx, repo)` was not added: `cmd` calls `attehcl.LoadLayout` directly and `attego` never read the filename (it reads settings through `Scanner.HCLSettings`, which now honors the layout).
- `preferLocalRunTargets` previously excluded `<relative>/atte.hcl` by comparing it with the canonical selector, which always contains `#<identifier>`; that condition could never be true, so it was removed instead of being ported to the layout.
- `LoadLayout` parses the root file separately from `readHCLFiles`, so the root `atte.hcl` is parsed twice per evaluation.
- `ResolveTreeFromTree` is new in `reference` because `ResolveBlobFromTree` rejects results that are the repository root.
