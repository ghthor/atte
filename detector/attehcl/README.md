# `atte.hcl` targets

The HCL Sensor discovers directory-local target kinds attached in a Scanner. The built-in Scanner provides the `test`, `codegen`, and `lint`
targets; applications can add their own target kinds without changing this
package. Each target may have a `script` and may also have `depends_on` and
`triggered_by` expressions. Declaration-only target discovery permits a missing
script and does not evaluate target bodies; graph construction and execution
reject missing scripts for script-backed target kinds.

A file is evaluated independently. Target expressions cannot read declarations
from an ancestor, sibling, or child `atte.hcl` file. Repository-wide target
dependency resolution is a later graph-assembly concern.

## Attaching target kinds

Use `detector.NewDefaultBuilder` when an application wants the standard Sensors, target
kinds, and HCL functions. Attach custom HCL target blocks on the setup builder
with `detector.AttachHCLTargetBlock`. The attachment requires a decoder and can
optionally provide graph, execution, configuration, and script projections.
Those optional projections determine which capabilities are available for the
custom target. The shared `Kind`, `KindSpec`, `Target`, and projection types are
provided by `detector/attehcltarget`.

```go
builder, err := detector.NewDefaultBuilder()
if err != nil {
	return err
}

err = detector.AttachHCLTargetBlock(builder, "deploy", attehcltarget.KindSpec{
	Schema:    &deploySchema,
	Decoder:   decodeDeploy,
	Graph:     graphDeploy,
	Execution: executeDeploy,
	Config:    configDeploy,
})
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
builder returned by `detector.NewDefaultBuilder` contains the built-in target kinds,
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
`AttachScanner(scanner)` returns the bound Sensor as `any`; check the error and
assert the result to `*attehcl.Detector`. `Builder.Compile` does this validation
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
attached by `detector.NewDefaultBuilder`. Custom function names must not conflict with
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
[1. Parse HCL files]
    output: hclFiles
      - sorted atte.hcl files
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
