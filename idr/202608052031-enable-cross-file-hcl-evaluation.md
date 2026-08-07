# 2026-08-05: Rejected — enable cross-file HCL evaluation

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

Atte currently evaluates each `atte.hcl` file with a context containing only
that file's `global` and `local` values. It has no repository-wide HCL
identifier scope, so an expression in one file cannot refer to a declaration
in another file.

This design is rejected. Cross-file HCL evaluation is not part of the target-centric
HCL model; target evaluation remains file-local and cross-file references are
not supported by this design. The remainder of this document is retained as
archival rejected design material.

The HCL detector should support relationships such as:

```hcl
# job/example/atte.hcl
docker_bake {
  image "name" {}
}

# job2/other/atte.hcl
test {
  depends_on = ["//job/example".docker_bake.image.name]
}
```

This is an HCL evaluation problem. The declarations, identifier namespace, and
reference values all belong to the HCL detector; they do not need to be
provided by or resolved through another detector.

### Context

The `attehcl` package already parses every repository `atte.hcl` file before
resolving globals, locals, and runnable blocks. Its evaluator creates an HCL
evaluation context for each file. The current context contains `global`,
`local`, and per-file HCL functions, but no values representing blocks in
other files.

The HCL address has an explicit rooted path portion followed by an HCL
traversal. For `job/example/atte.hcl`, the path portion is written
`"//job/example"`; `atte.hcl` is omitted. The remaining components come from
HCL block types and labels. An unlabeled block contributes only its type; a
labeled block contributes its type followed by its label. Therefore the
example's image is addressed as
`"//job/example".docker_bake.image.name`.

The rooted path and traversal are deliberately distinct. The initial form is
repository-absolute and does not support a relative path prefix. A future
relative form may be added after its HCL parsing and scope semantics are
specified.

The evaluator must discover declaration names before evaluating attributes such
as `depends_on`. It cannot discover them by evaluating those same attributes,
and it must not recursively invoke graph construction to resolve a reference.

### Goals

- Build one repository-wide identifier index as part of the HCL detector's
  existing evaluation pipeline.
- Make declarations from every parsed `atte.hcl` file available to HCL
  traversals in every other parsed file.
- Support nested blocks, optional labels, and repository-directory namespace
  components.
- Represent a resolved declaration as an HCL value that preserves its HCL
  detector entity ID and graph kind when used in `depends_on` or
  `triggered_by`.
- Discover declarations from parsed HCL syntax before evaluating declaration
  and target expressions.
- Keep the index deterministic, immutable during evaluation, and scoped to one
  repository evaluation request.
- Preserve current global/local semantics, per-file HCL functions, target IDs,
  selectors, and graph output for existing configurations.

### Non-Goals

- Implementing Docker Bake behavior. This IDR only establishes the HCL
  namespace and evaluation mechanism that a future Docker Bake block can use.
- Creating a cross-detector identifier-provider API or changing detector
  registration and registry graph aggregation. Cross-file identifiers are
  owned entirely by `detector/attehcl`.
- Making `local` values or arbitrary computed HCL values visible across files.
  `global` remains ancestor-inherited and `local` remains file-scoped.
- Changing the user-facing selector grammar or existing entity IDs for
  `test`, `codegen`, and `lint` blocks.
- Supporting arbitrary imports, aliases, dynamic block labels, or identifier
  escaping for names that are not valid HCL identifiers in the first version.
- Evaluating a referenced declaration's attributes on demand. The first
  version exposes declaration identity, not arbitrary cross-file computed
  values.

## Proposed Solution

Extend the HCL evaluator with a syntax-level declaration discovery phase. Once
all `atte.hcl` files have been parsed, walk their HCL block trees and build a
repository-wide address index. The index maps an address such as
`"//job/example".docker_bake.image.name` to an immutable nested cty object
tree.

Intermediate address components evaluate to cty objects. A referenceable
leaf evaluates to an HCL detector entity-reference capsule containing the
entity ID and kind. The evaluator adds the index's repository-directory roots
to the existing per-file context alongside `global` and `local`.

When decoding `depends_on` or `triggered_by`, the HCL detector recognizes the
entity-reference capsule and records its entity ID and kind directly. Strings
and repository paths retain their existing meaning. Graph construction then
uses the typed reference and does not need to infer a kind or call another
detector.

The index is built once by `newEvaluator` for each `Targets`, `ConfigFor`, or
`Graph` request and reused by all files and all expression evaluations in that
request. No registry or external detector API is involved.

## Detailed Design

### Declaration discovery

Add an HCL-detector-owned discovery model to the parsed `hclFile` data. The
model records, for each addressable block:

- source `atte.hcl` file;
- block type;
- literal labels in source order;
- parent declaration, if nested; and
- the HCL detector graph entity ID and kind represented by the declaration.

Discovery walks `hclsyntax.Body.Blocks` recursively and does not evaluate
attributes. `globals` and `locals` are evaluator declarations, not exported
identifier namespaces, so they are excluded from the cross-file declaration
index. Other HCL blocks are eligible for indexing according to the HCL
detector's block-kind rules.

Existing runnable blocks retain their current entity ID format and kind. New
addressable HCL block kinds use an HCL-detector-owned stable ID derived from
the declaring file and declaration path; the exact encoding must be documented
and validated by the HCL package. Entity IDs must not depend on map iteration
order or evaluation order.

A future `docker_bake` implementation can therefore discover this tree:

```text
job
└── example
    └── docker_bake
        └── image
            └── name  -> HCL entity reference
```

A labeled outer block is represented as:

```text
"//job/example".docker_bake.<outer-label>.image.name
```

An unlabeled block omits the label. The first version treats a block with
children as a namespace object and its leaf child blocks as referenceable
terminals. This avoids making one cty path simultaneously an object and an
entity value. If parent-block references are needed later, a separate explicit
terminal such as `self` can be added without changing child addresses.

### Address rules

- The address starts with a quoted repository-root path of the form
  `"//<repository-path>"`.
- The path identifies the containing directory's `atte.hcl`; `atte.hcl` is not
  included in the address.
- The path portion is repository-absolute in the first version; relative HCL
  address paths are not accepted.
- Repository directory components precede HCL block components.
- `atte.hcl` is not included in an address.
- A block type is always included.
- Each literal label follows its block type.
- Every component must be non-empty and a valid HCL identifier.
- `global` and `local` are reserved roots and cannot be directory roots in the
  exported index.
- Duplicate terminal addresses are errors, including duplicate unlabeled
  declarations.
- A path cannot be both a terminal entity and an intermediate namespace.
- Shared intermediate namespaces are allowed.
- Numeric indexes are not invented for duplicate or unlabeled declarations.
- Discovery errors identify the source file and block location whenever HCL
  source ranges are available.

The repository path is quoted and explicitly delimited from the HCL
traversal. Supporting directories or labels containing `-`, `.`, or other
non-identifier characters still requires a future escaping or bracket-address
design and is outside this IDR. Relative path prefixes are also outside the
first version.

### HCL evaluation value

Define an HCL-detector-owned cty capsule type for an entity reference. It
contains at least:

```go
type EntityReference struct {
    ID   graph.EntityID
    Kind string
}
```

The capsule must have the cty operations required to place it in object values
and extract it while decoding. It is distinct from `cty.String` and from
`attegit.RepositoryPathType`.

The address index converts its flat declarations into nested cty object values:

- intermediate nodes are object values;
- referenceable declarations are entity-reference capsule values; and
- the root is a map of repository directory components to object values.

The index must be immutable after construction. The same cty values can be
used by every file context in the evaluator. A missing root or traversal is
reported through HCL's normal unknown-variable/attribute diagnostics.

### Evaluation lifecycle

For each HCL evaluator request:

1. Parse all repository `atte.hcl` files as today.
2. Resolve the syntax-level HCL declaration index from the parsed block trees.
3. Validate addresses, entity IDs, kinds, and collisions.
4. Construct the immutable nested cty object tree.
5. Resolve inherited globals and file-local locals using the existing
   evaluation behavior.
6. Construct each file's HCL context with `global`, `local`, the identifier
   roots, and that file's HCL functions.
7. Decode target attributes and graph dependencies from evaluated values.

The declaration index is independent of globals and locals. A declaration
cannot be created by an expression, and evaluating a target cannot mutate the
index. The index therefore does not introduce a cross-file evaluation cycle.

`newEvaluator`, `evalContext`, declaration evaluation, and all paths that
construct an `hcl.EvalContext` must use the same index. The index must not be
rebuilt for each block, declaration retry, or file.

### Dependency decoding

Update `decodeTargetBlock` to accept entity-reference capsule values in
`depends_on` and `triggered_by`:

- extract the entity ID and kind;
- append a typed dependency to the decoded block; and
- preserve existing handling for strings and `path(...)` values.

An entity reference in `script` is rejected with a clear type error. An
intermediate object used as a dependency value is rejected as a non-terminal
reference. Existing encoded `attehcl-id:` strings may remain readable for
compatibility, but newly evaluated identifiers use the typed capsule and do
not rely on string prefixes.

Change graph assembly so typed dependencies use their stored kind directly.
The HCL detector does not inspect another detector's graph, call
`attego.Graph`, or classify a referenced ID by namespace. HCL entity
references point only to HCL-detector entities, so the HCL graph can add the
entity and relationship during its own graph build.

### Compatibility

Existing configurations continue to work:

- `global.<name>` and `local.<name>` retain their current scope;
- ordinary string scripts and dependencies remain strings;
- `path(...)` continues to produce repository path values;
- existing `test`, `codegen`, and `lint` IDs, labels, aliases, and selectors
  remain stable; and
- per-file custom HCL functions remain per-file functions.

The new identifier roots are additive. A configuration that does not declare
cross-file references sees no behavior change except for the up-front syntax
index construction.

## Cross cutting concerns

### Determinism

Sort files, discovered declarations, address components, and duplicate-error
reporting deterministically. Never construct entity IDs or address values from
Go map iteration order.

### Cycles and re-entrancy

Discovery is syntax-only and precedes evaluation. It must not evaluate target
attributes, call `Graph`, or invoke another detector. Evaluation can therefore
resolve a reference without recursively rebuilding a graph.

The evaluation queue must make progress toward publishing known declaration
namespaces. A cross-file dependency cycle cannot be resolved by retrying
unknown values: each member waits for another member to become known. The
production evaluator must detect this condition, or terminate it through the
caller-provided `context.Context`, rather than retrying indefinitely. The
experimental queue implementation demonstrates the latter safety guard; a
future implementation should prefer a deterministic cycle diagnostic once the
pending dependency set is available.

### Graph consistency

The entity ID and kind are attached to the evaluated reference when the index
is built. If the HCL detector later discovers conflicting declarations or
entity definitions, index construction fails before any graph is returned.

### Repository boundaries and security

Discovery reads only the supplied repository snapshot. Address components are
validated as HCL identifiers and are never treated as filesystem paths by HCL
evaluation.

### Performance and cancellation

Parsing and declaration indexing happen once per request. The immutable index is
reused across all files and expressions. Discovery and evaluation honor the
caller context and must stop promptly when it is canceled.

## Alternatives considered

### Merge all files into one HCL body

Rejected. It loses file provenance and existing global/local scope, does not
naturally create directory namespaces, and makes nested block ownership
ambiguous.

### Resolve references after HCL decoding

Rejected. HCL cannot decode an unknown traversal in `depends_on`, and the
reference must be available while evaluating an expression. Discovery must
precede evaluation.

### Use only strings containing encoded entity IDs

Rejected as the new representation. Encoded strings are ambiguous with user
strings and discard the distinction between paths, strings, and references. A
cty capsule preserves the type. Existing encoded strings can remain a
compatibility path.

### Resolve another file by recursively calling its graph builder

Rejected. It repeats parsing and graph construction, introduces recursion, and
couples HCL evaluation to unrelated detector implementations. The HCL detector
already has all parsed files needed to build its own index.

### Make cross-file declarations global variables

Rejected. `global` already has ancestor-inheritance semantics and is intended
for evaluated configuration values. Mixing declaration identity into it would
make scope and evaluation order ambiguous. Identifier roots are a separate
namespace in the evaluation context.

### Require explicit import/reference functions

Rejected for this feature. It makes the example verbose and prevents natural
HCL traversal syntax. Aliases or imports can be added later if repository-wide
names need customization.

## Future plans

- Implement the `docker_bake` block parser and graph entities using this index.
- Add an explicit way to reference parent blocks if they need to be dependency
  terminals.
- Add bracket or escaping syntax for directories and labels that are not valid
  HCL identifiers.
- Support aliases or an import mechanism if canonical repository addresses are
  insufficient.
- Expose detector-owned computed attributes only after a cycle-safe evaluation
  model exists.

## Other reading

- `detector/attehcl/attehcl.go` — current parse, scope, and block evaluation
  pipeline.
- `detector/attehcl/README.md` — current globals, locals, and dependency
  semantics.
- `detector/graphset/options.go` — existing per-file HCL function provider.
- `detector/graph/graph.go` — graph entity and relationship validation.
- `detector/attehcl/refactor_analysis.md` — existing analysis of the evaluator
  pipeline and repeated setup.

## Implementation (ephemeral)

Status: design plan; experimental evaluation programs have been added, but
production implementation has not started.

### Experimental implementations

The following stand-alone programs record the behavior of the HCL parser and
validate the proposed evaluation lifecycle without changing `detector/attehcl`:

- `idr/202608052031-enable-cross-file-hcl-evaluation/main.go` parses the
  example repository containing `src/go/atte.hcl`, `src/py/atte.hcl`, and
  `src/zig/atte.hcl`. It discovers the complete file list before evaluation,
  creates a path-keyed namespace with an unknown cty object value for every
  rooted path, and evaluates the requested `src/py/atte.hcl` through a FIFO
  queue.
- The queue initially evaluates `src/py/atte.hcl`, observes unknown
  `//src/go` and `//src/zig` namespaces, queues those files, and retries the
  original file after publishing their known declaration namespaces. The
  demonstration resolves the dependencies as `//src/go#test.go` and
  `//src/zig#test.zig`. The experiment also clarifies that starting with
  `src/go/atte.hcl` alone cannot observe an unknown dependency because that
  file is a leaf; a requesting file must contain the cross-file references, or
  the queue must be seeded with the complete evaluation worklist.
- `idr/202608052031-enable-cross-file-hcl-evaluation/cycle/main.go` evaluates
  two files whose declarations depend on one another. Neither namespace can
  become known, so the queue makes no progress and the context deadline
  terminates evaluation with an error. This confirms that this model does not
  support cycles without an explicit cycle-detection or failure policy.

The experiments also confirm that HCL parses a rooted address such as
`"//src/go".test.go` as a string expression followed by a relative traversal.
A normal HCL evaluation context cannot apply the traversal to the string. The
application must evaluate the path expression, use the resulting `//src/go`
value to look up the path namespace, and then apply the remaining traversal to
that namespace. Unknown namespaces need a known cty object shape so traversal
can remain unknown while its path dependency is queued.

These programs are demonstrations, not production APIs. They use simplified
string values for declaration references and do not yet implement the
HCL-detector entity-reference capsule, stable entity IDs, declaration discovery
for nested block kinds, graph integration, or the existing globals/locals
pipeline.

### Verification of experiments

```text
go run ./idr/202608052031-enable-cross-file-hcl-evaluation
go run ./idr/202608052031-enable-cross-file-hcl-evaluation/cycle
go run ./idr/202608052235-formalize-the-selector-spec/hcl-id-test
```

The first command prints the queue trace and publishes `//src/go`,
`//src/zig`, and then `//src/py`. The cycle experiment reports a context
deadline exceeded error after repeated retries. The selector experiment
demonstrates the same path-lookup-plus-relative-traversal technique for a
single address.

Checklist:

1. Add HCL-detector-owned declaration metadata and recursive syntax discovery.
2. Define the HCL entity-reference cty capsule and extraction helpers.
3. Define stable IDs and kinds for newly addressable HCL declarations while
   preserving IDs for existing runnable blocks.
4. Implement deterministic address validation, collision detection, and nested
   cty object construction.
5. Build the index once in `newEvaluator` and thread it through every HCL
   evaluation context.
6. Decode typed references in `depends_on` and `triggered_by`; reject them in
   unsupported attributes.
7. Use the reference's stored entity kind during HCL graph construction.
8. Add fixtures covering `job/example` to `job2/other`, nested blocks, named
   and unnamed outer blocks, missing roots, duplicates, invalid components,
   and intermediate-object misuse.
9. Verify existing global/local, target, selector, and graph tests remain
   unchanged.
10. Update `detector/attehcl/README.md` with the address grammar and example.
11. Run the repository verification commands from `AGENTS.md`.
