# Complexity and Refactoring Review: detector/attehcl

## Scope

| File | Responsibility |
|---|---|
| parser.go | HCL file discovery and parsing |
| locals.go | File-local expression evaluation |
| evaluator.go | Declaration discovery and target decoding |
| target.go | Target identity, execution, and configuration |
| graph.go | Graph projection and dependency resolution |
| diagnostics.go | HCL diagnostic formatting |
| api.go | Public target and configuration APIs |
| target_registry.go | Target-kind capability definitions and decoding |
| detector.go | Detector adapter and selector integration |
| attehcl_test.go | Package tests |

## Validation

- go test ./... — passed
- go test ./examples/attehcl-custom-block — passed
- nix develop --command atte run test.build — passed
- nix develop --command atte run test.go — passed
- nix develop --command atte run codegen.go — passed
- nix develop --command atte run codegen.fmt — passed
- nix develop --command atte run codegen.rendered — passed
- nix develop --command atte run lint.go — passed
- git diff --check — passed
- Serena diagnostics reported no correctness diagnostics.

## Executive summary

The package has a sound pipeline and good separation between declaration discovery, local evaluation, target decoding, and graph projection. The target capability set is a useful extensibility boundary, and the recent evaluator-construction cleanup has removed the earlier duplication there.

The main remaining opportunity is algorithmic: local evaluation repeatedly retries expressions. The remaining smaller refactorings remove unused state.

## Resolved architectural follow-ups

### Builder and Scanner lifecycle

The detector composition lifecycle has been refactored since this review was written:

- detector.Builder is setup-only and owns Sensor, target-kind, and HCL-function attachment.
- Builder.Compile creates an immutable detector.Scanner read-only interface.
- Runtime graph, target, entity-decoding, and HCL-evaluation operations consume the compiled Scanner rather than the Builder.
- The compiled Scanner snapshots all capabilities together, including target schemas and HCL function factories, and does not require a mutex.
- The built-in HCL Sensor is created against the compiled Scanner, preventing runtime evaluation from retaining the mutable Builder.
- Compiled Scanners are isolated from later Builder attachments.

This resolves the previous concern about combining setup-time mutation with runtime reads. It also makes the capability snapshot coherent across Sensors, target kinds, and HCL functions. The remaining findings below concern HCL evaluation, diagnostics, and graph construction rather than setup/runtime synchronization or lifecycle.

## Findings

### 1. Local evaluation is quadratic and nondeterministic

evaluateLocals repeatedly scans all unresolved locals until one pass makes no progress. For L locals, a dependency chain evaluated in an unfavorable order can require approximately O(L²) expression evaluations and context constructions. The context itself is rebuilt for every pending expression, so the retry loop also repeatedly copies the currently known local and target namespaces.

The result is nondeterministic because expressions and pending are maps. The local name and diagnostics reported for an invalid dependency or cycle can vary between executions:

    var lastName string
    var lastDiags hcl.Diagnostics
    for name, expr := range pending {
        ...
    }

#### Recommendation

Use a dependency-aware resolver:

1. Extract local references from each expression.
2. Build a local dependency graph.
3. Resolve with DFS or Kahn's algorithm.
4. Detect cycles explicitly.
5. Sort names before reporting errors.

This reduces the traversal to approximately O(L + R), where R is the number of local references, and permits deterministic cycle diagnostics such as local.a -> local.b -> local.a. As a minimum improvement, sort pending names and retain a reusable evaluation context for each pass. The dependency-aware resolver is preferable because it also gives better cycle and missing-reference errors.

---

### 2. Dead or low-value abstractions remain

A few constructs add indirection without currently serving a caller.

#### declarationIndex.byID

It is populated by initializeDeclarations and graphFor, but no code reads it. Target resolution uses byReference. Remove it unless entity-ID lookup is actually needed.

#### evaluateDeclaration

This is a one-line wrapper around expression.Value(context) and is called only once. Inline it unless it is intended to become a real policy boundary.

These are low-risk cleanups, but removing them makes the evaluator's data flow easier to follow and reinforces the single-purpose declaration index.

---

### 3. Diagnostic context rereads and resplits the source for every diagnostic

hclDiagnosticContext calls repo.Show(file) and splits the complete file contents for each diagnostic. For D diagnostics and a file of size B, this can approach O(D × B) work and performs repeated repository reads.

The number of diagnostics is usually small, so this is not an urgent bottleneck. It is nevertheless avoidable because parsing already has the source file associated with the evaluator.

#### Recommendation

Retain source contents or precomputed lines in hclFile and pass that data to diagnostic formatting. If retaining the full contents is undesirable, cache the split lines for the duration of one file evaluation.

---

### 4. Graph construction does not canonicalize duplicate relationships

graphScriptTarget emits one relationship for every decoded dependency, and graphFor appends all projected relationships before calling graph.New. graph.New sorts relationships but does not remove equivalent edges. Consequently, a dependency list such as depends_on = [path("x"), path("x")] produces duplicate depends-on relationships. Multiple projectors can produce the same issue.

This is unnecessary output and avoidable work for graph consumers. It is also inconsistent with Graph.Absorb, which explicitly ignores duplicate relationships.

#### Recommendation

Define whether relationships are a set at the graph.New boundary and enforce that invariant there, or deduplicate in graphFor with a map[graph.Relationship]struct{} before constructing the graph. Add a regression test for repeated dependencies.

---

## Complexity summary

| Area | Approximate complexity | Notes |
|---|---:|---|
| HCL file discovery and parsing | O(repository objects + parsed bytes) | Only atte.hcl blobs are parsed |
| Declaration normalization | O(files + blocks) once per evaluator | Normalized blocks are retained for later phases |
| Local evaluation | O(L²) worst case | Map retries and repeated context construction; target is O(L + references) |
| Dependency decoding | O(V + A × dependency data) | Dependency context transformation is shared per target |
| Target decoding | O(blocks + expression evaluation) | Provider function cost is external |
| Graph assembly | O(targets + dependencies + projected entities) | Duplicate relationships can increase output and consumer work |
| Scanner compilation | O(attached capabilities + schema size) | One immutable capability snapshot is created before runtime evaluation |
| Scanner runtime access | O(1) capability lookup | Compiled detector and capability maps are immutable and require no mutex |
| Diagnostic rendering | O(diagnostics × file size) | File contents are reread per diagnostic |

## Recommended implementation order

### Phase 1: Improve algorithmic complexity

1. Replace retry-based local evaluation with dependency-ordered evaluation if configurations can contain many locals.

### Phase 2: Correctness and maintainability

1. Canonicalize duplicate graph relationships and test repeated dependencies.
2. Remove declarationIndex.byID and inline evaluateDeclaration.

### Phase 3: Lower-priority allocation cleanup

1. Cache source lines or source contents for diagnostic rendering.
2. Consider sharing immutable common HCL functions across files while retaining per-file filesystem functions.

## Suggested target architecture

The existing pipeline should be retained. Its important property is that each phase exposes only the data needed by the next phase:

    repository
        |
        v
    [1. Parse HCL files]
        output: hclFiles
          - parsed bodies and local expressions, ordered by file
        |
        v
    [2. Normalize target blocks]
        output: normalizedBlock[] + declarationIndex
          - attached kind, name, index, body, and source range
          - canonical repository-wide target lookup
        |
        +------------------------------+
        |                              |
        v                              v
    [Declaration-only path]       [3. Evaluate locals]
        output: targetDeclaration[]     output: hclScope + HCL functions
          - identity and source          - known file-local values
        |                              |
        v                              v
    DeclaredTargets              [4. Decode target bodies]
        output: graphtarget.ID[]       output: evaluatedTarget[]
                                       - decoded value and kind spec
                                       - identity and source information
                                       |
                       +---------------+----------------+
                       |                                |
                       v                                v
                 [5. Materialize targets]       [5. Project target graphs]
                 output: map[Kind][]Target       output: graph.Graph
                   - common identity and         - entities and relationships
                     projections                 - resolved symbolic dependencies

DeclaredTargets does not require local values, provider functions, attached decoders, or graph projections. Targets and ConfigFor consume decoded output and add common target identity, selector, script, execution, and configuration data. Graph consumes the same declaration index and decoded output while invoking graph projections.

The best remaining algorithmic improvement is deterministic, dependency-ordered local evaluation. The detector composition lifecycle is now explicit: setup uses detector.Builder, runtime evaluation uses an immutable compiled detector.Scanner, and no further composition rewrite is currently indicated. Future work should focus on the local-evaluation, diagnostic, and graph findings above.
