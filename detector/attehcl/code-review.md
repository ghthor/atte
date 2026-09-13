# Complexity and Refactoring Review: `detector/attehcl`

## Scope

| File | Responsibility |
|---|---|
| `parser.go` | HCL file discovery and parsing |
| `locals.go` | File-local expression evaluation |
| `evaluator.go` | Declaration discovery and target decoding |
| `target.go` | Target identity, execution, and configuration |
| `graph.go` | Graph projection and dependency resolution |
| `diagnostics.go` | HCL diagnostic formatting |
| `api.go` | Public target and configuration APIs |
| `target_registry.go` | Target-kind registration and decoding |
| `detector.go` | Detector adapter and selector integration |
| `attehcl_test.go` | Package tests |

## Validation

- `go test ./detector/attehcl` — passed
- `golangci-lint run ./detector/attehcl` — passed
- `staticcheck ./detector/attehcl` — passed

## Executive summary

The package has a sound pipeline and good recent separation between declaration discovery, target evaluation, and graph assembly. The target capability registry is also a useful extensibility boundary.

The main remaining opportunities are algorithmic simplification and targeted behavioral fixes. There are two behavioral issues worth addressing:

1. `Graph` can panic when passed a nil context.
2. Local traversals in dependency lists are preserved as target traversals instead of being evaluated through the local scope.

## Findings

### 1. Local evaluation is quadratic and nondeterministic

`evaluateLocals` repeatedly scans all unresolved locals until one pass makes no progress.

For `L` locals, the behavior is approximately:

```text
Best case:    O(L) expression evaluations
Worst case:   O(L²) expression evaluations
```

A dependency chain evaluated in reverse map order can require one successful local per pass.

The result is also nondeterministic because `expressions` and `pending` are maps. The local name and diagnostics reported for an invalid dependency or cycle can vary between executions:

```go
var lastName string
var lastDiags hcl.Diagnostics
for name, expr := range pending {
    ...
}
```

#### Recommendation

Use a dependency-aware resolver:

1. Extract local references from each expression.
2. Build a local dependency graph.
3. Resolve using DFS or Kahn's algorithm.
4. Detect cycles explicitly.
5. Sort names before reporting errors.

This would provide approximately `O(L + R)` traversal complexity, where `R` is the number of local references, along with deterministic cycle diagnostics such as:

```text
local.a -> local.b -> local.a forms a cycle
```

As a minimum improvement, sort pending names before each pass.

---

### 2. Dependency decoding and graph projection duplicate branching

`decodeTargetDependencies` and `graphScriptTarget` both discriminate between:

- Target traversals
- Repository paths
- HCL/entity IDs
- Plain strings

`graphScriptTarget` also handles script projection and all dependency projection in one function. Its structural cyclomatic complexity is approximately in the low teens.

The repeated pattern is:

```go
if dep.traversal != nil { ... }
if dep.entity != "" { ... }
// otherwise resolve dep.value as a repository path
```

#### Recommendation

Represent dependencies as a tagged value rather than three optional fields:

```go
type dependencyKind uint8

const (
    dependencyPath dependencyKind = iota
    dependencyEntity
    dependencyTarget
)

type dependency struct {
    kind      dependencyKind
    path      string
    entity    graph.EntityID
    traversal hcl.Traversal
}
```

Then isolate graph projection in a helper such as:

```go
func projectDependency(
    ctx context.Context,
    repo *attegit.Repo,
    target Target,
    dependency dependency,
    graphContext TargetGraphContext,
) (graph.Entity, graph.Relationship, error)
```

The built-in functions should also be renamed because they are used for all built-in kinds:

```text
decodeTestTarget -> decodeScriptTarget
```

---

### 3. Local traversals in dependency lists are likely mishandled

`decodeTargetDependencies` preserves every `hclsyntax.ScopeTraversalExpr` symbolically before evaluating it:

```go
if traversal, ok := element.(*hclsyntax.ScopeTraversalExpr); ok {
    dependencies = append(dependencies, dependency{traversal: traversal.Traversal})
    continue
}
```

Consequently, this configuration is likely treated incorrectly:

```hcl
locals {
  dependency = "./config.yaml"
}

test {
  script     = "echo test"
  depends_on = [local.dependency]
}
```

`local.dependency` is preserved as though it were a target traversal. During graph assembly it is then resolved as a `kind.name` traversal and rejected.

This is inconsistent with the documented behavior that locals may be used as file-local target input.

#### Recommendation

Only preserve traversals intended to be target references:

- Evaluate traversals rooted at `local`.
- Preserve traversals rooted at registered target kinds.
- Reject or evaluate all other expressions consistently.

Add a focused regression test using a local path in `depends_on` or `triggered_by`.

---

### 4. Public `Graph` can panic with a nil context

`newEvaluator` and `newEvaluatorForFile` normalize nil contexts to `context.Background()`, but `Graph` checks the context first:

```go
func Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
```

Calling `Graph(nil, repo)` panics before reaching the normalization logic.

#### Recommendation

Normalize context at the public API boundary:

```go
func normalizeContext(ctx context.Context) context.Context {
    if ctx == nil {
        return context.Background()
    }
    return ctx
}
```

Use it consistently in `Graph`, `DeclaredTargets`, `ConfigFor`, and `Targets`. Alternatively, remove nil-context support entirely and consistently treat nil as invalid. The current mixed behavior is the problem.

---

### 5. Evaluator construction contains avoidable duplication

`newEvaluator` and `newEvaluatorForFile` both construct the same evaluator fields:

```go
&evaluator{
    ctx:       ctx,
    repo:      repo,
    files:     ...,
    provider:  provider,
    functions: make(map[reference.Blob]map[string]function.Function),
    kindSpecs: targetRegistrySnapshot(),
}
```

Factor this into a helper such as:

```go
func newEvaluatorWithFiles(
    ctx context.Context,
    repo *attegit.Repo,
    files hclFiles,
    provider graphset.FunctionProvider,
) *evaluator
```

There is similar repetition in `declaredTargets` and `evaluatedTargets`, which both select files, check cancellation, retrieve the file, and iterate. A shared file-selection helper would reduce this duplication.

---

### 6. `ConfigFor` contains redundant filtering

`ConfigFor` creates an evaluator containing exactly one file and evaluates only that file:

```go
blocks, err := evaluator.evaluatedTargets(currentBlob)
```

It then filters the resulting targets by tree. Since the evaluator can only contain `currentBlob`, this filtering is redundant:

```go
for kind, kindTargets := range allTargets {
    for _, target := range kindTargets {
        if target.File.Tree() == tree {
            targets[kind] = append(targets[kind], target)
        }
    }
}
```

Once the single-file invariant is retained, this can return `allTargets` directly.

---

### 7. Dead or low-value abstractions are accumulating

A few constructs currently add more indirection than value:

#### `declarationIndex.byID`

It is populated in `graphFor` but never read. Remove it or use it for entity-reference validation.

#### `evaluateDeclaration`

This is a one-line wrapper around `expression.Value(context)` and is only called once. It can be removed unless it is intended as a future abstraction point.

#### `hclScope`

It currently contains only one field:

```go
type hclScope struct {
    local cty.Value
}
```

The scope is immediately converted into an `hcl.EvalContext`. It could be replaced with a direct `cty.Value` unless more namespaces are expected soon.

These are not urgent, but removing them would make the evaluator easier to follow.

---

### 8. Registry snapshots are more expensive than necessary

`targetRegistrySnapshot` copies every registered schema for every evaluator. That is reasonable for evaluator snapshot isolation, but `DecodeEntityID` also calls it for every decoded HCL entity ID:

```go
if _, registered := targetRegistrySnapshot()[Kind(parts[1])]; !registered {
```

For `K` registered kinds and `D` entity lookups, this can result in approximately `O(D × K)` map-copy and schema-copy work.

#### Recommendation

Add a read-only lookup helper:

```go
func registeredKind(kind Kind) bool
```

or:

```go
func lookupTargetKind(kind Kind) (targetKindSpec, bool)
```

Use a complete snapshot only when creating an evaluator. Entity ID validation does not need to copy every registered schema.

---

### 9. Diagnostic context rereads files for every diagnostic

`hclDiagnosticContext` calls `repo.Show(file)` and splits the complete file contents for each diagnostic.

For `D` diagnostics and a file of size `B`, this can approach:

```text
O(D × B)
```

The number of diagnostics is usually small, so this is unlikely to be a practical bottleneck. Nevertheless, the evaluator already owns parsed file information and could retain source contents or precomputed lines:

```go
type hclFile struct {
    file     reference.Blob
    contents []byte
    lines    []string
    body     *hclsyntax.Body
    locals   map[string]hcl.Expression
}
```

This would also avoid repeatedly loading the same blob.

---

## Complexity summary

| Area | Approximate complexity | Notes |
|---|---:|---|
| HCL file discovery | `O(repository objects + parsed bytes)` | Only `atte.hcl` blobs are parsed |
| Declaration normalization | `O(files + blocks)` | Deterministic after file sorting |
| Local evaluation | `O(L²)` worst case | Repeated map scans; should become `O(L + references)` |
| Target decoding | `O(blocks + expression evaluation)` | Provider function cost is external |
| Graph assembly | `O(targets + dependencies + projected entities)` | Capability callbacks can add external cost |
| Registry snapshots | `O(registered kinds × schema size)` | Repeated unnecessarily during entity ID validation |
| Diagnostic rendering | `O(diagnostics × file size)` | File contents are reread per diagnostic |

---

## Recommended implementation order

### Phase 1: Correctness and cheap cleanup

1. Normalize nil contexts in public APIs.
2. Fix `local.*` dependency traversal evaluation.
3. Remove unused `declarationIndex.byID`.
4. Remove or inline `evaluateDeclaration`.
5. Remove redundant `ConfigFor` filtering.
6. Add regression tests for the behavioral changes.

### Phase 2: Reduce duplicated construction

1. Factor evaluator construction.
2. Factor file-selection logic.
3. Add a registry lookup helper.
4. Centralize target identity construction.

### Phase 3: Improve algorithmic complexity

Replace repeated local-evaluation scans with dependency-ordered evaluation and explicit cycle detection. This is worthwhile if HCL files can contain many locals, but it is less urgent than the correctness and maintainability changes above.

---

## Suggested target architecture

The existing pipeline is sound and should be retained. Its important property is
that each phase exposes only the data needed by the next phase:

```text
repository
    |
    v
[1. Parse HCL files]
    output: hclFiles
      - parsed bodies and local expressions, ordered by file
    |
    v
[2. Normalize target blocks]
    output: normalizedBlock[]
      - registered kind, name, index, body, and source range
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
             [5. Materialize targets]       [5. Build graph index]
             output: map[Kind][]Target       output: declarationIndex
               - common identity and         - same-file target lookup
                 projections
                   |                                |
                   v                                v
             Targets / ConfigFor          [6. Project target graphs]
             output: evaluated config          output: graph.Graph
                                             - entities and relationships
```

`DeclaredTargets` therefore does not require local values, provider functions,
registered decoders, or graph projections. `Targets` and `ConfigFor` consume the
full decoded output and add common target identity, selector, script, execution,
and configuration data. `Graph` consumes the same decoded output, builds a
same-file declaration index, and then resolves symbolic target dependencies
while invoking graph projections.

The best remaining refactoring is to reduce representation duplication around `dependency`, `targetDeclaration`, and `evaluatedTarget`.

A large rewrite of the capability registry is not recommended. The registry and projection interfaces are relatively clean and appear to be the intended extensibility boundary.
