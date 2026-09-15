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

- go test ./detector/attehcl — passed
- go vet ./detector/attehcl — passed
- golangci-lint run ./detector/attehcl — passed
- staticcheck ./detector/attehcl — passed
- Serena diagnostics reported no correctness diagnostics.

## Executive summary

The package has a sound pipeline and good separation between declaration discovery, local evaluation, target decoding, and graph projection. The target capability registry is a useful extensibility boundary, and the recent evaluator-construction cleanup has removed the earlier duplication there.

The main remaining opportunity is algorithmic: local evaluation repeatedly retries expressions. The remaining smaller refactorings remove unused state.

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
| Registry snapshots | O(registered kinds × schema size) | One complete snapshot per evaluator is intentional |
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
          - registered kind, name, index, body, and source range
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

DeclaredTargets does not require local values, provider functions, registered decoders, or graph projections. Targets and ConfigFor consume decoded output and add common target identity, selector, script, execution, and configuration data. Graph consumes the same declaration index and decoded output while invoking graph projections.

The best algorithmic improvement is deterministic, dependency-ordered local evaluation. A large rewrite of the capability registry is not recommended; its snapshot and projection interfaces are relatively clean and appear to be the intended extensibility boundary.

## Refactoring review follow-ups

### 1. Custom HCL target kinds are still omitted from atte graph — high priority

cmd/graph.go still hard-codes the built-in HCL kinds:

- isGraphChildKind only accepts test, codegen, and lint (cmd/graph.go:200).
- addGraphChildren only dispatches those three kinds (cmd/graph.go:281-305).
- graphDependencyLabel likewise only recognizes those kinds (cmd/graph.go:357).

This defeats the new plugin target-kind extension for graph rendering. The custom-block example registers deploy, and its graph contains the deploy entity, but:

```bash
cd examples/attehcl-custom-block
go run . graph
```

prints only the atte.hcl node; the deploy target is absent.

The graph command should recognize arbitrary attehcl:<kind> entities and derive their label through the registry rather than enumerating built-in kinds.
