# `atte.hcl` targets

The HCL detector discovers directory-local target kinds registered in a plugin
registry. The built-in registry provides the `test`, `codegen`, and `lint`
targets; applications can add their own target kinds without changing this
package. Each target may have a `script` and may also have `depends_on` and
`triggered_by` expressions. Declaration-only target discovery permits a missing
script and does not evaluate target bodies; graph construction and execution
reject missing scripts for script-backed target kinds.

A file is evaluated independently. Target expressions cannot read declarations
from an ancestor, sibling, or child `atte.hcl` file. Repository-wide target
dependency resolution is a later graph-assembly concern.

## Registering target kinds

Use `plugin.NewDefaultBuilder` when an application wants the standard detectors, target
kinds, and HCL functions. Register custom HCL blocks on the setup builder
with `plugin.RegisterHCLBlock`. The registration requires a decoder and can
optionally provide graph, execution, configuration, and script projections.
Those optional projections determine which capabilities are available for the
custom target.

```go
builder, err := plugin.NewDefaultBuilder()
if err != nil {
	return err
}

err = plugin.RegisterHCLBlock(builder, "deploy", attehcl.TargetKindSpec{
	Schema:    &deploySchema,
	Decoder:   decodeDeploy,
	Graph:     graphDeploy,
	Execution: executeDeploy,
	Config:    configDeploy,
})
if err != nil {
	return err
}

registry := builder.Compile()
```

`deploySchema` and the projection functions in this example are application
code. See `examples/attehcl-custom-block` for a complete custom target. The
builder returned by `plugin.NewDefaultBuilder` contains the built-in target kinds,
detectors, and HCL functions. Compilation adds the HCL detector with a reference
to the immutable registry, so all subsequent HCL evaluation uses one consistent
capability set. Target kind names must be valid HCL identifiers and cannot be
registered more than once in a builder.

Pass the same registry to the direct `attehcl` APIs that evaluate targets or
build graphs:

```go
targets, err := attehcl.Targets(ctx, repo, registry)
config, err := attehcl.ConfigFor(ctx, repo, relativePath, registry)
graph, err := attehcl.Graph(ctx, repo, registry)
```

When adapting the HCL detector directly, use `attehcl.NewDetector(registry)`.
For CLI execution, provide the registry through `cmd.ExecuteOptions.Detector`;
repository discovery remains unchanged when only the detector registry is
injected:

```go
err := cmd.ExecuteWithOptions(ctx, args, cmd.ExecuteOptions{
	Repository:     repo,
	RepositoryRoot: root,
	Detector:       registry,
})
```

## Registering HCL functions

Applications can add repository- and file-aware HCL functions to the same
setup builder with `Builder.RegisterHCLFunction`. The registration takes a function
name and an `HCLFunctionFactory`. At evaluation time, the factory receives the
context, repository, and `atte.hcl` file being evaluated, and returns a fresh
`cty/function.Function`.

```go
err = builder.RegisterHCLFunction("source_file", func(
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
`source_file()`. Provider functions are available under both their registered
name and the `atte::<name>` namespace, so the same function can also be called
as `atte::source_file()`. The factory can use the repository and context to
construct functions backed by repository state or to return contextual errors.

Built-in functions such as `path`, `gopkg`, and `gopkg_test` are already
registered by `plugin.NewDefaultBuilder`. Custom function names must not conflict with
the built-in HCL functions or another registered function, and a function name
can only be registered once in a builder.

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
      - registered kind
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
order without evaluating locals, target attributes, functions, or registered target
decoders. Use `Targets` or `ConfigFor` when decoded target values are needed; use
`Graph` for repository paths and symbolic target dependencies.

Full evaluation provides the common HCL functions used by HashiCorp configuration
languages, including collection, encoding, crypto, CIDR, UUID, YAML, and filesystem
functions. Filesystem functions resolve relative to the tree containing the HCL
file. Provider functions are merged on top of this base set and must use names
that do not conflict with a base function. Each provider function is available
under both its registered name and the `atte::<name>` namespace. The built-in
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
