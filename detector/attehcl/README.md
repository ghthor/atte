# `atte.hcl` targets

The HCL Sensor discovers directory-local target kinds attached in a Scanner. The built-in Scanner provides the `test`, `codegen`, and `lint`
targets; applications can add their own target kinds without changing this
package. Each target may have a `script` and may also have `depends_on` and
`triggered_by` expressions. Declaration-only target discovery permits a missing
script and does not evaluate target bodies; graph construction and execution
reject missing scripts for script-backed target kinds.

A file is evaluated independently. Target expressions cannot read declarations
from an ancestor, sibling, or child configuration file. Repository-wide target
dependency resolution is a later graph-assembly concern.

## Attaching target kinds

Use `scanner.NewDefault` from the `detector/scanner` package when an application
wants the standard Sensors, target kinds, and HCL functions. Attach custom HCL target blocks on the setup builder
with `detector.AttachHCLTargetBlock`. The attachment requires a decoder and can
optionally provide graph, execution, configuration, and script projections.
Those optional projections determine which capabilities are available for the
custom target. Build the spec with `hclblock.NewTargetKindSpec`: the decoder
returns your own type, and each projection receives the target together with
that typed value, so no projection asserts the decoded value itself. Shared types such as `TargetKind`, `TargetKindSpec`, `Target`,
`TargetGraph`, and `TargetCommand` are provided by `detector/attehcl/hclblock`.

```go
builder, err := scanner.NewDefault()
if err != nil {
	return err
}

err = detector.AttachHCLTargetBlock(builder, "deploy", hclblock.NewTargetKindSpec(
	&deploySchema,
	decodeDeploy, // func(*hcl.BodyContent, *hcl.EvalContext) (deployTarget, error)
	hclblock.TargetKindOptions[deployTarget]{
		Graph:     graphDeploy,
		Execution: executeDeploy,
		Config:    configDeploy,
	},
))
if err != nil {
	return err
}

scanner, err := builder.Compile()
if err != nil {
	return err
}
```

`deploySchema` and the projection functions in this example are application
code. See `examples/attehcl-custom-block` for a complete custom target. The
builder returned by `scanner.NewDefault` contains the built-in target kinds,
Sensors, and HCL functions. During compilation, the HCL Sensor receives the
immutable Scanner through its `SensorWithScanner` capability, so all subsequent
HCL evaluation uses one consistent capability set. Target kind names must be
valid HCL identifiers and cannot be attached more than once in a builder.

Pass the same scanner to the direct `attehcl` APIs that evaluate targets or
build graphs:

```go
targets, err := attehcl.Targets(ctx, repo, scanner)
config, err := attehcl.ConfigFor(ctx, repo, relativePath, scanner)
graph, err := attehcl.Graph(ctx, repo, scanner)
```

`attehcl.NewDetector()` creates an unattached Sensor. Usually `Builder.AttachSensor`
and `Builder.Compile` perform scanner injection. For manual adaptation,
`AttachScanner(detector.Scanner)` returns the bound Sensor as `detector.Sensor`;
`Builder.Compile` performs this binding and validates the HCL capability subset
for attached Sensors.
For CLI execution, provide the scanner through `cmd.ExecuteOptions.Detector`;
repository discovery remains unchanged when only the Scanner is injected:

```go
err := cmd.ExecuteWithOptions(ctx, args, cmd.ExecuteOptions{
	Repository:     repo,
	RepositoryRoot: root,
	Detector:       scanner,
})
```

## Attaching setting blocks

Setting blocks are unlabeled top-level blocks, such as `settings { ... }`,
that carry file-local settings rather than runnable targets. Attach them with
`detector.AttachHCLSettingBlock` or by implementing
`detector.SensorProvidingHCLSettingBlocks` on a Sensor. A setting block
requires a schema and a decoder and may provide a projection that renders the
decoded value in `atte config show` output. Build one with
`hclblock.NewSettingBlock`; consumers read the decoded value with
`hclblock.DecodedAs[T]`, which reports the expected and actual types on a
mismatch. Target consumers use `hclblock.TargetDecodedAs[T]`.

```go
err = detector.AttachHCLSettingBlock(builder, "settings", hclblock.NewSettingBlock(
	&settingsSchema,
	decodeSettings, // func(*hcl.BodyContent, *hcl.EvalContext) (settings, error)
	configSettings, // func(settings) (map[string]any, error); optional
))
```

A file declares at most one block per setting name, and the block takes
no labels. Setting blocks are decoded with the same evaluation context as
the file's targets, so they may read `local.*` values and call HCL functions.
Setting block names must be valid HCL identifiers, cannot collide with an
attached target kind, and cannot use the reserved `locals`, `target`, or `atte`
names. Target kinds cannot use those names either.

`attehcl.ConfigFor` returns the decoded blocks for the requested file in
`Config.Settings`; `atte config show` renders them under a `settings` key beside
`target`, keyed by `//<file>#<name>`, with the projection's result
under `values`.

The HCL Sensor also serves settings evaluation to other Sensors through
the compiled Scanner's `HCLSettings(ctx, repo, tree)` method (the
`detector.HCLSettingSensor` capability). A Sensor that owns a setting
block, such as the Go Sensor's `go_test` block, attaches the block through
`SensorProvidingHCLSettingBlocks` and reads its decoded values at runtime
through the Scanner rather than importing this package. See
`examples/attehcl-go-test-override` for a runnable repository that overrides
`go_test` at module and package scope:

```hcl
# Overrides the go_test command for this package, or for every package in the
# module when declared beside go.mod.
go_test {
  command = ["go", "test", "-race", "-count=1"]
}
```

## Configuring the configuration filename

Every directory has at most one configuration file. By default it is named
`atte.hcl`. The repository root's `atte.hcl` always keeps that name so it can be
found without configuration, and it may declare the built-in `atte` setting
block to rename the file used by every other directory:

```hcl
# atte.hcl at the repository root
atte {
  filename = "build.hcl"
}
```

With this block the root is configured by `atte.hcl` and `sub/` by
`sub/build.hcl`. The root file is otherwise a normal configuration file: it may
declare `locals`, targets, and other setting blocks.

- `filename` is required and must be a literal string: the block is read before
  any file is evaluated, so it cannot use `local.*` values or functions. It must
  be a bare file name ending in `.hcl`.
- The `atte` block is only valid in the root `atte.hcl`, at most once, and takes
  no labels.
- When the filename differs from `atte.hcl`, a non-root `atte.hcl` or a root file
  with the configured name is an error rather than being ignored.
- `attehcl.LoadLayout(ctx, repo)` returns the repository's `attehcl.Layout`
  (`DefaultLayout` when no block is declared). `Layout.File(dir)` names a
  directory's configuration file. `ConfigFor`, `Targets`, `Graph`, and the
  `target()` function all honor the layout; `atte config show` reports the `atte`
  block under `//atte.hcl#atte`.
- Selectors keep their shape and use the configured name where they would use
  `atte.hcl`: `//sub/build.hcl#test.unit` and `//sub#test.unit` select the same
  target.

In `target(path, name)`, `path` names a directory and resolves to that
directory's configuration file. An empty path refers to the declaring file's own
directory, `"//"` names the repository root (whose file is always `atte.hcl`),
and other paths are relative to the declaring directory. See
`examples/attehcl-custom-filename` for a runnable repository.

## Attaching HCL functions

Applications can add repository- and file-aware HCL functions to the same
setup builder with `Builder.AttachHCLFunction`. The attachment takes a function
name and an `HCLFunctionFactory`. At evaluation time, the factory receives the
context, repository, and `atte.hcl` file being evaluated, and returns a fresh
`cty/function.Function`.

```go
err = builder.AttachHCLFunction("source_file", func(
	_ context.Context,
	_ *attegit.Repo,
	file reference.Blob,
) (function.Function, error) {
	return function.New(&function.Spec{
		Type: function.StaticReturnType(cty.String),
		Impl: func([]cty.Value, cty.Type) (cty.Value, error) {
			return cty.StringVal(file.String()), nil
		},
	}), nil
})
```

This example makes the current `atte.hcl` file available through
`source_file()`. Provider functions are available under both their attached
name and the `atte::<name>` namespace, so the same function can also be called
as `atte::source_file()`. The factory can use the repository and context to
construct functions backed by repository state or to return contextual errors.

Built-in functions such as `path`, `gopkg`, and `gopkg_test` are already
attached by `scanner.NewDefault`. Custom function names must not conflict with
the built-in HCL functions or another attached function, and a function name
can only be attached once in a builder.

## Evaluation phases

Each `atte.hcl` file follows the same parse and evaluation pipeline. The
outputs from the early phases are deliberately different: declaration discovery
can stop before evaluating locals or target bodies, while `Targets`, `ConfigFor`,
and `Graph` continue through decoding.

```text
repository
    |
    v
[0. Load layout]
    output: Layout
      - configuration filename read from the root atte.hcl atte block
    |
    v
[1. Parse HCL files]
    output: hclFiles
      - sorted configuration files (named by the layout)
      - parsed HCL bodies
      - file-local local expressions
    |
    v
[2. Normalize target blocks]
    output: normalizedBlock[]
      - attached kind
      - source label/name
      - kind-local source index
      - source range and body
    |
    +------------------------------+
    |                              |
    v                              v
[Declaration-only path]       [3. Evaluate locals]
    output: targetDeclaration[]     output: hclScope
      - entity ID                    - known local values
      - kind, file, name, index     - base and provider HCL functions are loaded
      - source range                - combined into an hcl.EvalContext
    |                              |
    v                              v
DeclaredTargets              [4. Decode target bodies]
    output: graphtarget.ID[]       output: evaluatedTarget[]
      - ordered identities           - decoded target value
      - no locals or bodies          - target kind specification
        are evaluated                - source identity and range
                                   |
                   +---------------+----------------+
                   |                                |
                   v                                v
             [5. Materialize targets]       [5. Build graph index]
             output: map[Kind][]Target       output: declarationIndex
               - stable target ID             - repository-wide kind/name lookup
               - selector aliases             - target declarations for
               - script/inline values           symbolic traversals
               - graph, execution, and config
                 projections
                   |                                |
                   v                                v
             Targets / ConfigFor          [6. Project target graphs]
             output: evaluated config          output: graph.Graph
                                             - entities and relationships
                                             - script and dependency edges
```

The declaration-only branch shares parsing and block normalization with full
evaluation, but intentionally stops before local evaluation, provider function
loading, target decoding, and projection construction. Full evaluation retains
the normalized blocks and declaration lookup for the later target and graph
phases. The graph branch reuses that declaration lookup when resolving symbolic
target dependencies.

`DeclaredTargets` returns `graphtarget.ID` values ordered by file path and source
order without evaluating locals, target attributes, functions, or attached target
decoders. Use `Targets` or `ConfigFor` when decoded target values are needed; use
`Graph` for repository paths and symbolic target dependencies.

Full evaluation provides the common HCL functions used by HashiCorp configuration
languages, including collection, encoding, crypto, CIDR, UUID, YAML, and filesystem
functions. Filesystem functions resolve relative to the tree containing the HCL
file. Provider functions are merged on top of this base set and must use names
that do not conflict with a base function. Each provider function is available
under both its attached name and the `atte::<name>` namespace. The built-in
`atte::target(path, "kind.name")` function resolves a named target declaration
from another `atte.hcl` file. Its path may be repository-root-relative, such as
`//path1`, or relative to the declaring `atte.hcl` file, such as `../path1`.
Target declarations are indexed before locals and target bodies are
evaluated. Cross-file target reference cycles are an intentional, fully supported
use case: cycles are retained in the graph and pruned when building a dependency
tree for a target that has already been reached.

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
`depends_on` and `triggered_by` accept any expression that evaluates to a
known list of graph dependencies. The list may contain repository paths,
detector references returned by supported HCL functions, and target traversals,
including values assembled through functions such as `concat` or `flatten`.
Target traversals are preserved symbolically for a later graph phase; target
enumeration does not resolve them.

`locals` may be used as file-local target input. Multiple `locals` blocks are
merged and duplicate names are rejected. Locals may reference other locals and
named targets in the same file. Locals do not propagate to other files.
