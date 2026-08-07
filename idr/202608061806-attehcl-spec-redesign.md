# 2026-08-06: attehcl spec redesign

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

`atte.hcl` is fundamentally a directory-scoped declaration of executable
targets. The current implementation hard-codes `test`, `codegen`, and `lint`
block kinds and decodes them through one shared schema. That makes it difficult
to add target kinds, prevents a kind from owning its own schema, and obscures
the distinction between a target's stable kind and its user-facing name.

This redesign defines one target model and evaluation boundary: evaluating one
`atte.hcl` file produces the targets declared for that directory, without
requiring evaluation of another `atte.hcl` file. It also establishes a registry
for extensible target kinds and supports a concise block syntax.

### Context

An `atte.hcl` file belongs to the directory containing it. It declares targets
that operate in that directory; it is not an import or repository-wide module.
The existing HCL detector already uses `test`, `codegen`, and `lint` as distinct
identities, even when their executable fields have the same shape.

The initial target schema is intentionally small:

```hcl
test {
  script     = "" # string or repository-relative path
  depends_on = [] # list of target references or paths
}
```

The schema is selected by the target kind label, not by the physical `target`
wrapper. This permits several kinds to share a schema while retaining distinct
kind identities.

### Goals

- Define a directory-local target model and deterministic target identifiers.
- Define caller-supplied decoding for kind-specific target values.
- Select a target's HCL body schema from its registered kind.
- Provide built-in `test`, `codegen`, and `lint` kinds with the test schema while
  preserving each kind in identity, output, and selector behavior.
- Make registering a new kind and schema possible for the package and for
  packages importing it.
- Support both explicit `target` blocks and short-form kind blocks.
- Support anonymous and named targets with deterministic IDs, while documenting
  that named `(kind, name)` pairs are the stable identity form.
- Evaluate a single file into a map of targets without cross-file evaluation.

### Non-Goals

- Resolving dependencies between targets declared in different `atte.hcl`
  files. Cross-file graph assembly is a later phase owned by the repository
  graph builder.
- Defining execution scheduling, dependency ordering, or shell isolation.
- Replacing CLI selectors or changing existing graph entity IDs before a
  compatibility review.
- Adding imports, inheritance, globals, locals, or arbitrary HCL modules to
  the target-spec experiment.
- Redesigning production graph entity IDs, CLI aliases, or selector behavior.
- Defining kind-specific execution behavior beyond caller-supplied decoding.
- Defining cross-file graph assembly or cross-file dependency resolution.
- Defining post-evaluation validation of dependency references.
- Defining production integration with the repository path function/provider.
- Defining registry concurrency guarantees or a production initialization
  lifecycle.

## Proposed Solution

Introduce a registered target-kind table. Each entry maps a kind name to an
HCL body schema. `test`, `codegen`, and `lint` are registered by default. The
latter two use the same schema as `test`, but remain separate kinds; schema
sharing is not identity aliasing.

A target may use either form below:

```hcl
target "test" "go" {
  script = "go test ./..."
}

test "go" {
  script = "go test ./..."
}
```

The wrapper form has one required kind label and one optional name label. The
short form uses the registered kind as its block type and has zero or one name
label. An unknown short-form block such as `go {}` is rejected because `go` is
not a registered kind.

The evaluator first normalizes both forms to `(kind, optional name, body)`,
looks up the kind schema, and decodes the body. It then assigns a zero-based,
deterministic source index independently for each kind. The first, second, and
third target blocks of kind `test` have indices `0`, `1`, and `2`, regardless of
whether a block is named. A named block is identified by `test.go`; an
anonymous block is identified by `test.<index>`. Names must be non-numeric and
`(kind, name)` pairs must be unique within one file. Named IDs are therefore
stable as long as their kind and name do not change; anonymous IDs are only
deterministic for a given file version and may change when declarations are
inserted or removed.

The result is a deterministic `map[Kind][]Target`. `Kind` identifies the
schema and target namespace, while each `Target` carries its optional `Name`,
source `Index`, kind-decoded value, and unresolved dependencies. The `target`
wrapper is intentionally not part of the identity. A display or graph adapter
may render an anonymous target as `<kind>.<index>` and a named target as
`<kind>.<name>`, but the evaluation result does not need to resolve or embed
that display ID.

## Detailed Design

### Target block grammar

```text
target-block = "target" kind [name] body
short-block  = kind [name] body
kind         = registered HCL identifier
name         = HCL block label
```

The parser must reject a wrapper with zero or more than two labels, and a short
block with more than one label. A wrapper kind must be registered. A short block
is recognized only when its block type is registered; an otherwise unknown
block type is an error rather than an ignored declaration. A named target keeps
its source index even though its display identifier uses its name.

The normalized block keeps the source range, kind, optional name, and original
HCL body so schema diagnostics can point at the source declaration. Errors for
unknown kinds, duplicate names, invalid labels, and invalid body attributes
should include the file and block location.

### Registration API

Registration is package-level and process-wide for the initial implementation.
The conceptual API is:

```go
type TargetDecoder func(*hcl.BodyContent, *hcl.EvalContext) (any, error)

func Register(kind string, decoder TargetDecoder, schema ...*hcl.BodySchema) error
```

A kind must be a non-empty valid HCL identifier and may be registered only once.
With no schema, or with a nil schema, the registration uses the `test` schema.
A supplied schema is copied into the registry and owns the attributes accepted
by that kind. Registration also receives a caller-supplied decoder. The decoder
receives the schema-validated body and evaluation context and returns the
kind-specific value stored in `Target`. Registration must not mutate a
caller-owned schema. The registry is read during evaluation and should be
initialized before concurrent target evaluation begins.

The built-ins are equivalent to:

```go
Register("test", testSchema)
Register("codegen") // shares testSchema, remains codegen
Register("lint")    // shares testSchema, remains lint
```

The experiment may use an in-process registry reset or explicit initialization
to keep repeated demonstrations deterministic; production registration should
make duplicate registration errors visible rather than silently replacing a
schema. The experiment demonstrates this with a separate `package` kind that
uses its own schema and decoder, while the built-in kinds share the test
schema and test decoder.

### Initial schema and value decoding

The initial test schema accepts optional `script` and `depends_on` attributes.
`script` accepts a string or a repository-path value produced by the experiment's
local `path(...)` fixture. `depends_on` accepts only a list whose members are
HCL identifier traversals or repository-path values. The empty target is valid;
missing attributes decode to empty values. Other collection forms are outside
this experiment's contract.

A target dependency is written as an HCL identifier, for example:

```hcl
test "go" {
  depends_on = [codegen.go, path("./generated.go")]
}
```

The evaluator preserves `codegen.go` as an HCL traversal. It must not convert
it to a string such as `"codegen.go"` or require it to resolve to a declared
target during enumeration. Any syntactically valid HCL identifier traversal is
accepted, including one whose root is not registered or whose target is not
declared. These references are symbolic values for a later graph phase, not
values resolved by target enumeration. Repository paths are retained as typed
path dependencies for that later phase. Path resolution rules are not part of
this experiment's design. Other attributes are rejected by the selected kind
schema. Future kinds may add attributes and decoding behavior without changing
target block discovery or identity.

### Evaluation boundary

`Evaluate` parses and evaluates exactly one `atte.hcl` body. It discovers all
normalized target blocks before decoding any target so anonymous indices and
kind-specific schema selection are deterministic. It does not read or evaluate
another `atte.hcl` file.

The result is conceptually equivalent to:

```hcl
target "test" {}
target "lint" {}
codegen {}
codegen "go" {}
```

```hcl
# evaluated conceptually
targets = {
  test = [{}]
  lint = [{}]
  codegen = [
    {},
    { name = "go" },
  ]
}
```

```go
targets := map[Kind][]Target{
  KindTest:    {{Index: 0}},
  KindLint:    {{Index: 0}},
  KindCodegen: {{Index: 0}, {Name: "go", Index: 1}},
}
```

Evaluation does not need to resolve `codegen.go` or any other target
identifier to produce this result. Reference validation and dependency graph
assembly happen after the file-local declaration map has been produced.

Map iteration must not determine observable ordering. The experiment prints
IDs in sorted order; production APIs should either return a sorted projection
or document ordering separately from the map.

### Identity and aliases

The kind is the base component of a target identity because `target` is the
fundamental declaration form, not a meaningful target kind. `codegen` therefore
remains `codegen`, even though its schema is shared with `test`. Names are
labels, not kinds. Every target has a kind-local source index; named targets
also have a name. Downstream identity adapters may use `(kind, index)` for
anonymous targets and `(kind, name)` for named targets, while retaining the
source index as target metadata. Numeric names are forbidden so named IDs
cannot collide with anonymous display IDs.

CLI aliases and graph entity encoding are downstream concerns. The production
adapter must preserve `Target.Kind`, `Target.Name`, and `Target.Index`; it must
not collapse schema aliases into `test` or include `target` in the identifier.

## Cross cutting concerns

### Determinism

Target discovery follows source order. Anonymous indices and duplicate checks
are assigned from that order. Registration lookups are by exact kind. Any
serialized or displayed map output sorts IDs rather than relying on Go map
iteration.

### Extensibility and compatibility

Adding a kind should require only registering its schema and adding any
kind-specific execution behavior. A kind can initially share the test schema
by omitting the schema argument. Shared schema does not imply shared selector,
graph, or execution identity.

The production `detector/attehcl` implementation will transition to this
file-local, target-centric model as part of this IDR. The transition must
preserve the existing graph entity IDs, selector aliases, and the distinct
`test`, `codegen`, and `lint` kinds unless a separate compatibility decision
changes them. The migration plan is recorded in `## Implementation
(ephemeral)` below; the executable experiment remains a design reference and
is not itself the production API.

### Diagnostics

Errors should distinguish malformed HCL, unknown kind, invalid label count,
duplicate named target, ID collision, unsupported attribute, invalid script
type, and invalid dependency collection/member. HCL diagnostics should retain
source ranges from the original block body.

### Production transition boundary

The production detector adopts the target evaluator as its source of truth for
HCL target declarations. It remains responsible for adapting evaluated targets
to the existing graph and selector APIs until those APIs are revisited by a
separate decision. In particular, the adapter preserves the current
`attehcl:<kind>:<file>:<name>` entity ID encoding and selector aliases while
using `Target.Kind`, `Target.Name`, and `Target.Index` from the new evaluator.

The transition removes inherited `globals` from the production configuration
model. A target in one `atte.hcl` file is not evaluated with declarations from
an ancestor, sibling, or child file. File-local `locals`, if retained during
this transition, are evaluated only for the file containing the target and are
not a separate repository-wide phase. No target may depend on a global value.
The public configuration surface must not continue to expose effective global
values after this transition; callers needing targets use the target-centric
result instead.

## Alternatives considered

### Keep one hard-coded schema for every block kind

Rejected. It makes adding kinds require editing the evaluator and prevents a
kind from evolving its own body schema.

### Treat `codegen` and `lint` as aliases of `test`

Rejected. They share a schema but must remain distinguishable target kinds for
selectors, graph entities, reporting, and future kind-specific behavior.

### Require the `target` wrapper everywhere

Rejected. The wrapper is useful when the kind is expressed as a label, but the
short form is clearer for common built-in and registered kinds.

### Use the target name as the only identity

Rejected. Anonymous declarations are useful and need stable identity. Their
kind-local indices also make repeated anonymous blocks addressable without
inventing names.

### Evaluate all repository files as one HCL program

Rejected for this phase. It introduces cross-file dependency cycles and makes a
directory-local declaration depend on unrelated files. Repository-wide graph
assembly can resolve file and target dependencies after isolated evaluation.

## Future plans

- Define the production representation of target references versus path
  dependencies and validate symbolic references after enumeration.
- Add repository-wide graph assembly and cross-file dependency resolution as a
  separate phase.
- Reconcile the resulting `Target` representation with the selector and graph
  IDRs.

## Other reading

- `detector/attehcl/attehcl.go` — current hard-coded target discovery and
  target/graph projection.
- `detector/attehcl/attehcl_test.go` — current target kinds, labels, scripts,
  dependency behavior, and graph identity tests.
- `detector/attegit/hcl_plugin.go` — repository-path HCL value and `path`
  function used by the existing detector.
- `idr/202608052031-enable-cross-file-hcl-evaluation.md` — related HCL
  evaluation and cross-file reference work; this IDR deliberately keeps target
  evaluation file-local.
- `idr/202608052235-formalize-the-selector-spec.md` — selector identity and
  alias compatibility constraints.
- `idr/202608061806-attehcl-spec-redesign/target-spec/main.go` — executable
  experiment for registration, block normalization, schema selection, and
  isolated evaluation.

## Implementation (ephemeral)

Status: implemented. Phases 1–5 are complete in production. The production
registry, schema selection, wrapper/short-form normalization, decoder-owned
values, file-local evaluation, grouped target results, typed and symbolic
dependency preservation, compatibility projections, source-aware diagnostics,
cleanup, and documentation now use the target-centric decoding path. The
executable experiment remains at
`idr/202608061806-attehcl-spec-redesign/target-spec/main.go` and is the
behavioral reference for registration, normalization, schema selection, and
file-local evaluation.

The production migration is complete and is centered on the evaluated target
model rather than the existing graph projection. Anonymous core targets
retain an empty Name and expose their kind-local Index; adapters derive the
display name from that index. Target names remain intentionally open pending a
separate grammar decision.

### Completed implementation phases

1. Remove `globals` parsing, inheritance, and evaluation from
   `detector/attehcl`.
   - Delete the `globals` fields from `hclFile` and the `global` field from
     `hclScope`.
   - Remove `declarationContext`, the generic declaration retry loop where it
     exists only to resolve globals, `globalPhase`, `evaluationPhase`, and
     `resolveGlobals`.
   - Stop walking the repository directory tree to construct inherited
     scopes.
   - A `globals` block is no longer a supported target declaration. It must
     not silently become a target or be evaluated as configuration.
2. Remove or update the public global configuration surface. `ConfigFor` must
   not return effective inherited globals after this change. If `Config` is
   retained for callers that need file-local values, it contains only the
   supported local/file metadata and the targets for the requested directory.
3. Remove tests whose behavior is specifically global inheritance,
   global/local ordering, global cycles, or propagation across directories.
   This includes the current global inheritance and global-versus-local test
   cases in `detector/attehcl/attehcl_test.go`. Replace mixed fixtures with
   direct target expressions or explicitly file-local setup. Add one focused
   unsupported-`globals` regression only if rejection is part of the final
   diagnostic contract.
4. Rewrite `detector/attehcl/README.md` to remove the globals section and
   describe targets as directory-local declarations. Any retained `locals`
   behavior must be documented as file-local input to target evaluation, not
   as a repository phase. The cross-file HCL IDR and its README references to
   preserving global semantics must be reconciled before implementation is
   considered complete.

### Phase 2: introduce the target evaluation phase

1. Make target evaluation file-local. Add an evaluator-owned `targetsPhase`
   (or equivalent) whose unit of work is one `atte.hcl` file and whose output
   is the normalized/decoded targets from that file. A repository-wide caller
   may parse or enumerate files to assemble a repository result, but that
   orchestration must not become an input to evaluating one file's targets.
2. Build the HCL evaluation context for a target from only:
   - the registered kind's schema and decoder;
   - the file's permitted file-local values, if locals are retained;
   - the provider functions for that file; and
   - the later target-reference namespace, when that separate graph/index
     phase is introduced.
   It must not contain inherited values from another `atte.hcl` file.
3. Discover and normalize every target block in source order before decoding
   any target in that file. Assign kind-local indices during this discovery
   pass, enforce duplicate `(kind, name)` checks, and select the registered
   schema for each block.
4. Expose a single evaluator method that evaluates one file to
   `map[Kind][]Target`. `Targets` invokes it for files in deterministic
   repository order; `ConfigFor` invokes it only for the requested file; and
   `Graph` projects the same evaluated results rather than decoding blocks a
   second time.
5. Preserve the isolation boundary with tests proving that evaluating a file
   does not read or evaluate an ancestor, sibling, or child `atte.hcl` file.
   Use a provider/function fixture that records the files requested during
   evaluation, so the test detects accidental cross-file setup rather than
   only checking the decoded values. Tests should also prove that target
   discovery order and kind-local indices do not depend on map iteration or
   the order in which other files are evaluated.

### Phase 3: move production decoding to the registered target model

1. Move the registry, normalized-block model, built-in schemas, and target
   decoders from the experiment into `detector/attehcl`. Resolve the concrete
   registration API before implementation: every registered kind must have a
   decoder, built-ins must explicitly register the shared test decoder, and
   each evaluation must use a stable registry snapshot.
2. Deep-copy caller-provided schemas when registering a kind. Validate kind
   identifiers, registration duplicates, label counts, target-name syntax,
   and numeric-name collisions according to the final API contract.
3. Replace `blockKinds`, `blocksOfType`, and the shared production body schema
   with registry lookup and block normalization. Support both
   `target "kind" ["name"] {}` and registered short-form blocks. Keep
   `globals` out of the registered target namespace.
4. Preserve the experiment's typed decoding rules for scripts, paths, and
   unresolved target traversals. A target reference is retained for later
   graph assembly; target enumeration does not resolve it or require the
   referenced target to be declared in the same file.
5. Use a shared evaluated-target projection for all consumers:
   - `Targets` adapts the result to `graphtarget.ID`;
   - `ConfigFor` returns the targets for one directory; and
   - `Graph` adapts the same targets and dependencies to graph entities and
     relationships.

### Phase 4: preserve compatibility at the graph and selector boundary

1. Keep existing `Target` adapter fields needed by callers during the
   migration, but derive them from the new model. In particular, preserve
   `Kind`, `Name`, `Index`, `File`, selector aliases, and the existing graph
   entity ID format. Anonymous target IDs must continue to match the current
   `kind.<index>`/graph-name behavior even though the evaluated model stores
   an empty optional name plus a source index.
2. Keep `test`, `codegen`, and `lint` as distinct production kinds even when
   they share the test schema and decoder. Add compatibility tests proving
   that schema sharing does not collapse graph kinds, selectors, aliases, or
   reporting.
3. Make `Graph`, `Targets`, and `ConfigFor` consume the same evaluated target
   data. Add regression tests comparing scripts, dependencies, labels,
   indices, aliases, and graph IDs across all three projections.
4. Keep repository-path resolution in the existing provider/capsule adapter
   until the production target decoder can use it directly. Do not introduce
   cross-file target resolution in this migration; that remains a later graph
   assembly phase.

### Completed cleanup and verification

1. Delete the old global/evaluation phase code and any duplicate block
   decoding after the new target phase is used by every public entry point.
2. Remove obsolete analysis and comments that describe inherited globals or
   repository-wide configuration evaluation, and update package comments,
   README examples, and error expectations.
3. Add focused tests for malformed blocks, unknown kinds, invalid labels,
   duplicate names, unsupported attributes, invalid scripts, invalid
   dependency collections/members, isolated file evaluation, deterministic
   indices, and target/graph projection consistency.
4. Run the repository verification commands from `AGENTS.md`, plus the target
   experiment, before updating this section with factual implementation notes.

### Final acceptance status

The migration is complete:

- no production code evaluates or exposes inherited `globals`;
- target enumeration for one file can complete without evaluating another
  `atte.hcl` file;
- one normalized/evaluated target representation feeds `Targets`, `ConfigFor`,
  and `Graph`;
- built-in kinds remain distinct while sharing schemas where intended;
- existing graph IDs and selector behavior remain unchanged; and
- tests and documentation describe only the target-centric production model.

### Verification

```text
gofmt -w idr/202608061806-attehcl-spec-redesign/target-spec/main.go
go run ./idr/202608061806-attehcl-spec-redesign/target-spec
```
