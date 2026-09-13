# `atte.hcl` targets

The HCL detector discovers directory-local `test`, `codegen`, and `lint`
targets in `atte.hcl` files. Each target may have a `script` and may also have
`depends_on` and `triggered_by` expressions. Declaration-only target discovery
permits a missing script and does not evaluate target bodies; graph construction
and execution reject missing scripts for script-backed target kinds.

A file is evaluated independently. Target expressions cannot read declarations
from an ancestor, sibling, or child `atte.hcl` file. Repository-wide target
dependency resolution is a later graph-assembly concern.

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
               - stable target ID             - same-file kind/name lookup
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
loading, target decoding, and projection construction. The graph branch uses
all decoded targets to build its same-file lookup before resolving symbolic
target dependencies.

`DeclaredTargets` returns `graphtarget.ID` values ordered by file path and source
order without evaluating locals, target attributes, functions, or registered target
decoders. Use `Targets` or `ConfigFor` when decoded target values are needed; use
`Graph` for repository paths and symbolic same-file target dependencies.

Full evaluation provides the common HCL functions used by HashiCorp configuration
languages, including collection, encoding, crypto, CIDR, UUID, YAML, and filesystem
functions. Filesystem functions resolve relative to the tree containing the HCL
file. Provider functions are merged on top of this base set and must use names
that do not conflict with a base function.

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
