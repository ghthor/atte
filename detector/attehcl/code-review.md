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

The main remaining opportunity is algorithmic simplification, especially deterministic local evaluation.

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

### 2. `ConfigFor` contains redundant filtering

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

### 3. Dead or low-value abstractions are accumulating

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

### 4. Registry snapshots are more expensive than necessary

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

### 5. Diagnostic context rereads files for every diagnostic

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

1. Remove unused `declarationIndex.byID`.
2. Remove or inline `evaluateDeclaration`.
3. Remove redundant `ConfigFor` filtering.
4. Add regression tests for the behavioral changes.

### Phase 2: Remaining maintainability cleanup

1. Add a registry lookup helper.
2. Centralize target identity construction.

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
               - common identity and         - repository-wide target lookup
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
repository-wide declaration index, and then resolves symbolic target dependencies
while invoking graph projections.

The best remaining refactoring is to make local evaluation deterministic.

A large rewrite of the capability registry is not recommended. The registry and projection interfaces are relatively clean and appear to be the intended extensibility boundary.
