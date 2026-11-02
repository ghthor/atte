# 2026-11-02: Content hash summing for graph targets

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

The graph says what depends on what, but nothing says whether an entity, or anything it depends on, has changed. Without a deterministic fingerprint per entity, Atte cannot compare two snapshots of the Universe to find which entities an Impulse reached, and users cannot cheaply tell whether a target's inputs changed. This IDR adds a deterministic sha256 *content hash* for every graph entity that covers the entity and its full transitive dependencies, and exposes it through `atte graph`.

### Context

- The graph (`detector/graph`) holds uniquely identified entities and directed relationships. Detectors contribute entities: `attegit` (trees, blobs), `attego` (package, package-test, stdlib, external) and `attehcl` (targets with `depends-on` edges).
- `attegit.Repo.Obj` already carries the git object hash for every blob and tree, including hashes computed for working-tree overlays (`--working-tree`).
- Git object ids are SHA-1 (or SHA-256 in sha256 repositories). They are used here as opaque input, never as the result.
- Relationship kinds today: `contains`, `imports`, `source-file`, `depends-on`. `contains` is not directionally consistent: `attegit` emits child -> parent (`detector/attegit/graph.go`), while `attego` (tree -> package, tree -> package-test) and `attehcl` (tree -> target) emit parent -> child. `attego` also emits package-test -> package as `contains`, which is neither.
- External Go packages are leaf entities whose id encodes the requiring `go.mod` path and the import path. No version is recorded: `attego` reads `go.mod` only for the module name and local `replace` directives.
- `GLOSSARY.md` lists "Content hash" as a *Signature*. This is the first one Atte computes. Impulse and Impact calculation will later compare Signatures between snapshots.

### Goals

- Every entity gets a sha256 that changes whenever its own identity or the content of anything in its transitive dependency set changes, for the inputs the graph models. Inputs that are not modeled yet (see Known limitations) do not affect the hash, and the set of modeled inputs will keep growing.
- The hash depends on the *set* of dependencies, not on the shape of the graph: restructuring edges without changing the reachable set (for example adding a direct import of a package that was already imported transitively) does not change the hash of any importer.
- Cycles are allowed. An entity's dependency set is the set of entities reachable from it, excluding itself; a cycle adds nothing to the set and never makes the computation loop.
- Hashes are available for intermediate entities (a single Go package, for example), not only for roots, and the flattened dependency set of an entity is computed once and reused.
- Git blob and tree hashes are the content input where they exist. Other entities define their own input (external Go packages use their module version).
- A hash is a property of the whole graph, not of a view of it: it does not depend on the subdirectory `atte graph` runs from, on which entities are printed, or on absolute paths.
- `atte graph` can show the hash next to each entity.

### Non-Goals

- Dirty-tree handling for git *tree* hashes. A tree token is always the hash from the selected revision, never one recomputed from the working tree or index. Blob hashes are used as-is, so with `--working-tree` dirty blob content does flow into hashes (and into the hashes of every tree that contains it, through `contains` edges).
- Hashing a target's own declaration or the script/file it runs. That is addressed separately by making the source script or file a dependency of the target. The `script` edge from a target to its script blob already exists but is not followed, so following it is the likely first step.
- Hashing `go.sum`, module zips, or the source of external modules. External packages stay leaves in the graph: their imports are never resolved to source, and the `module@version` token plus the package path is the whole input.
- `go.work` files. They are always ignored, even when one is committed; hashes are computed as if `GOWORK=off`. Honoring `go.work` (and so removing it from the ignored set) is a non-goal here and would need its own IDR.
- Vendored dependencies (`vendor/`, `vendor/modules.txt`). Supporting them needs its own IDR.
- Minimal version selection. The version in the requiring `go.mod`'s `require` directive is used as the module version, even when another module in the build would select a higher one. `exclude` directives are ignored.
- A Merkle-style hash that reuses a dependency's final hash as input. Only flattened dependency sets are reused.
- Persisting hashes, comparing them between refs, or computing Impulses and Impacts. Those consume this feature later.
- Changing selectors, `atte run`, or any graph output when the new flag is absent.

### Proposed Solution

Add a content hash calculator over a completed `graph.Graph`. For an entity E:

1. Flatten: collect every entity reachable from E over *dependency edges*, excluding E itself, and sort the result bytewise by entity id.
2. Hash: sha256 over a versioned, length-prefixed encoding of E's entry, followed by the sorted entries. An entry is `(entity id, content token)`.

A *content token* is the entity's own content identifier, supplied by the detector that owns the entity: a git blob or tree hash, `module@version` for an external Go package, the declared Go version for a module's Go version entity, and empty for entities with no content of their own (HCL targets, Go packages, which get their content through their dependencies).

Reachable sets are computed per strongly connected component and memoized, so every importer of a package reuses that package's set. `atte graph --content-hash` prints the hash next to each entity-backed node.

## Detailed Design

### Dependency edges

An edge `From -> To` means From depends on To. Edge directions are corrected so that this holds for every kind that is followed:

- `contains` is always parent -> child (tree -> blob, tree -> subtree, tree -> package, tree -> target). A tree depends on what it contains; a blob or package does *not* depend on the tree it lives in, so depending on a blob never balloons into a whole git tree. `attegit.Repo.Graph` is changed to emit parent -> child, matching the other detectors. `cmd/graph.go` (`addGraphChildren`), which currently handles both directions, is simplified accordingly.
- `imports`, `source-file` (package -> blob) and `depends-on` already point at the dependency.
- The `atte.hcl` file -> target relationship is currently a `source-file` edge pointing target -> file, which is backwards. It becomes a `contains` edge, file -> target (the `atte.hcl` blob contains its targets), in `hclblock` (`detector/attehcl/hclblock/target.go`). A target therefore no longer depends on its declaring file; that gap is the separate follow-up under Non-Goals. The reverse is intended: because `contains` is followed, an `atte.hcl` blob depends on the targets it declares, so its closure includes those targets and everything they `depends_on`. The hash of an `atte.hcl` blob changes when any dependency of a target it declares changes, even though its own token (the git blob hash) does not. A file that declares targets is no longer a leaf.
- A new `tests` relationship (package-test -> package) replaces the `contains` edge `attego` emits between them today. An external test package (`package foo_test`) additionally gets an `imports` edge to the package under test through its test imports, so it has both.
- A new `go-version` relationship runs from every `attego` package entity (package, package-test, package-stdlib, package-external) to a new `attego:go-version` entity, one per `go.mod`. The entity stands for the Go version the module declares. Packages depend on it instead of on the `go.mod` blob, so an unrelated `require` edit does not change every package's hash while a bump of the `go` or `toolchain` directive does. Its id has the usual `attego` shape with an empty import-path segment: `attego:go-version:go.mod:`.

All of `contains`, `imports`, `source-file`, `depends-on`, `tests` and `go-version` are followed. A consequence is intended: a tree's closure includes the packages attached to it and everything those packages import, so anything contained in a tree path changing (including a Go package's imports) changes the tree's hash. Each detector exports the relationship kinds it contributes as dependency edges, keeping `graph` language-agnostic.

`source-file` edges, the tree -> package and tree -> target `contains` edges, and the `atte.hcl` file -> target edge are only emitted under `graphset.WithAttachToTree`. The `tests` edge is not gated: package-test -> package is always part of the `attego` graph. `atte graph` is the only non-test caller of `Graph` and it already passes that option, so content hashes are computed over a graph built with it and no option or default output changes. The calculator documents that it requires a graph built that way.

### Content tokens

| Entity kind | Token |
| --- | --- |
| `attegit:blob`, `attegit:tree` | `git-blob:<hex>` / `git-tree:<hex>` from `Repo.Obj` |
| `attego:package-external` | `go-module:<module>@<version>`; the import path is already in the entity id |
| `attego:go-version` | `go-version:go=<go directive>`, plus `,toolchain=<toolchain directive>` when the file has one |
| `attego:package`, `attego:package-test`, `attego:package-stdlib`, `attehcl:*` | empty |

External resolution: find the module that owns the import path by longest-prefix match against the `require` directives of the requiring `go.mod`. If a `replace` directive whose right-hand side carries a version matches that module (an unversioned left-hand side matches every version; a versioned one matches only that version), the token uses the *replacement* path and version, so `replace github.com/foo/bar v1.2.0 => github.com/fork/bar v1.2.1-patched` yields `go-module:github.com/fork/bar@v1.2.1-patched`. Replacements to a local path are already resolved to in-repository packages and never produce a token. `attego` has to start reading `require` and versioned `replace` entries. An external import that matches no `require` is an error, not an empty token.

The token prefix names the input kind so identical bytes from different kinds can never collide.

### Token examples

Each row is the `(entity id, token)` pair that enters the hash input. Hashes are real values from this repository at the time of writing; ids are copied from `atte graph` / `attego.EntityID`. The base64 segment of an `attego` id is the unpadded URL-safe encoding of the import path (decoded in the second column).

| Entity | Entity id | Token |
| --- | --- | --- |
| Git blob | `attegit:cmd/graph.go` | `git-blob:292da617d48b9a177bf83a4801a9c10e845f7b4f` |
| Git tree | `attegit:cmd` | `git-tree:1117ce0298cd79b90915fead4bab7ebd0430e45a` |
| Repository root tree | `attegit:` | empty (`Repo.Obj` has no entry for the root today) |
| Directory that only exists in the working tree (`--working-tree`) | `attegit:scratch` | empty (synthesized tree, zero hash) |
| Local Go package `github.com/ghthor/atte/detector/graph` | `attego:package:go.mod:Z2l0aHViLmNvbS9naHRob3IvYXR0ZS9kZXRlY3Rvci9ncmFwaA` | empty |
| Its test package | `attego:package-test:go.mod:Z2l0aHViLmNvbS9naHRob3IvYXR0ZS9kZXRlY3Rvci9ncmFwaA` | empty |
| Package in a nested module | `attego:package:examples/attehcl-custom-block/go.mod:<base64 import path>` | empty; the module's `go.mod` path is part of the id |
| External package `github.com/spf13/cobra` | `attego:package-external:go.mod:Z2l0aHViLmNvbS9zcGYxMy9jb2JyYQ` | `go-module:github.com/spf13/cobra@v1.10.2` |
| External package in a `/v2` module, `github.com/hashicorp/hcl/v2/hclsyntax` | `attego:package-external:go.mod:Z2l0aHViLmNvbS9oYXNoaWNvcnAvaGNsL3YyL2hjbHN5bnRheA` | `go-module:github.com/hashicorp/hcl/v2@v2.24.0` (the module, never the package) |
| External package replaced by a fork, `github.com/foo/bar/x` with `replace github.com/foo/bar v1.2.0 => github.com/fork/bar v1.2.1-patched` | `attego:package-external:go.mod:Z2l0aHViLmNvbS9mb28vYmFyL3g` | `go-module:github.com/fork/bar@v1.2.1-patched` (the id keeps the import path the code wrote) |
| Go version of the root module (`go 1.26.0`, no `toolchain`) | `attego:go-version:go.mod:` | `go-version:go=1.26.0` |
| Same, `go.mod` also has `toolchain go1.26.9` | `attego:go-version:go.mod:` | `go-version:go=1.26.0,toolchain=go1.26.9` |
| Go version of a nested module | `attego:go-version:examples/attehcl-custom-block/go.mod:` | `go-version:go=1.26.5` |
| Stdlib package `fmt` | `attego:package-stdlib:go.mod:Zm10` | empty; its Go version arrives through the `go-version` edge |
| HCL target `test "go"` in the root `atte.hcl` | `attehcl:test:atte.hcl:go` | empty |
| HCL target in a nested file | `attehcl:codegen:.github/workflows/atte.hcl:actions-lock` | empty |

Two packages from the same external module share a token and differ only in id. The same external package imported from two `go.mod` files in the repository has two ids, and two tokens when the files require different versions. The `go` and `toolchain` values are used exactly as written in `go.mod`; the special `toolchain` values `default` and `local` are treated as absent, and a `go.mod` with no `go` directive is `go=1.16`, the version the go command assumes.

### Worked examples

**A target with a single file dependency.** `atte.hcl` declares `test "go" { depends_on = ["//go.mod"] ... }`.

```text
E       = (attehcl:test:atte.hcl:go, "")
closure = [ (attegit:go.mod, git-blob:8651a061beeab615ecdb1cedf34c1c8312392590) ]
```

The `script = path("./atte_test.sh")` attribute is a `script` edge from the target to the script blob. `script` is not one of the followed dependency kinds, so editing `atte_test.sh` changes nothing in this hash. Following it is the follow-up described under non-goals.

**A package with direct and transitive dependencies.** Simplified: `cmd` imports `detector/graph` and cobra; `detector/graph` imports `fmt`; each has one source file and, like every package, depends on its module's Go version entity.

```text
E = (attego:package:go.mod:Z2l0aHViLmNvbS9naHRob3IvYXR0ZS9jbWQ, "")    // cmd

closure, sorted bytewise by id (E excluded):
  attegit:cmd/graph.go                    git-blob:292da617d48b9a177bf83a4801a9c10e845f7b4f
  attegit:detector/graph/graph.go         git-blob:b6838d7fd5c5c84bb4693c46e4675ad73ebcaabe
  attego:go-version:go.mod:               go-version:go=1.26.0
  attego:package-external:go.mod:<cobra>  go-module:github.com/spf13/cobra@v1.10.2
  attego:package-stdlib:go.mod:Zm10       ""    // fmt, reached only through detector/graph
  attego:package:go.mod:<detector/graph>  ""
```

The list is ordered by id, not by kind (`-` sorts before `:`, so `package-external` and `package-stdlib` precede `package:`). If `cmd` later adds a direct `import "fmt"`, `fmt` is already in the set, so the hash of `cmd` and of everything importing it is unchanged. Upgrading cobra to another version changes the cobra token and therefore the hash of `cmd` and of every importer of `cmd`.

**A cycle.** Two targets `a` and `b` that depend on each other: closure(a) = [b], closure(b) = [a]. Each hash input is its own entry followed by the other's, whichever target is evaluated first.

A byte-exact golden vector for the final encoding is added to the tests once the encoding is settled.

### Hash encoding

```text
sha256( "atte-content-sha\x00v1"
        || entry(E)
        || uvarint(len(closure))
        || entry(d) for d in closure sorted bytewise by id )

entry(x) = uvarint(len(x.id)) || x.id || uvarint(len(x.token)) || x.token
```

E is excluded from its own closure even when it is in a cycle, so cycle membership does not make E's input differ from an equivalent acyclic entity. Ids are unique, so the bytewise sort (Go string comparison) is total. The version prefix lets the encoding change without silently reusing old hashes.

Entity ids contain repository paths, so renaming or moving an entity changes its hash even when its content is identical. This is intended: the id is part of the entity's identity.

### Flatten and reuse

1. Tarjan's algorithm over dependency edges yields strongly connected components in reverse topological order.
2. For each component, closure(C) = members(C) union closure(S) for every successor component S. Memoize per component.
3. For an entity E in C, the flattened list is closure(C) minus E.

Memoizing per *component* rather than per node is required for cycles. A plain memoized DFS that stops at already-visited nodes caches an incomplete set for any node reached from inside a cycle, and the result then depends on the walk's starting point.

`graph.Graph.EntityKeys` is already sorted, so closures stored as sorted slices of entity indexes iterate in id order and need no separate sort.

### Package and API shape

A new package `detector/contenthash`:

- `New(g *graph.Graph, dependencies []graph.RelationKind, tokens map[graph.EntityID]string) *Calculator`. Only relationships of the listed kinds are followed, and an entity with no entry in `tokens` has an empty token.
- `(*Calculator).Sum(id) (Sum, error)` computes lazily and memoizes closures per component. `Sum` is a `[32]byte` with a lowercase-hex `String` method.
- `(*Calculator).Closure(id)` exposes the flattened list so Impulse and Impact code can reuse it, and `Token(id)` the token.

A new optional Sensor capability in `detector/interfaces.go`:

```go
type ContentSensor interface {
    Sensor
    DependencyRelations() []graph.RelationKind
    ContentTokens(context.Context, *attegit.Repo, *graph.Graph) (map[graph.EntityID]string, error)
}
```

`ContentTokens` receives the complete graph so a Sensor can read what it needs (`attego` shares one parse of every `go.mod` between building the graph and computing tokens, through `attegit.Memoize`) and returns tokens only for entities in its own namespace. It returns an error, instead of an empty token, when an entity's content cannot be identified. `Scanner.ContentHashes(ctx, repo, g)` merges the relation kinds and tokens of every attached `ContentSensor` into one `Calculator`. `attegit`, `attego` and `attehcl` each implement the capability.

### `atte graph` output

`--content-hash` appends the first 12 hex characters of the hash, in the style of git abbreviation, to every entity-backed node: tree, blob, package, package-test and HCL target. Lines for what a node refers to rather than is (`import ...`, `std import ...`, `file ...`, a target's dependencies) show no hash. With `--external-imports`, external import lines instead show the module and version from the token, for example `external import github.com/spf13/cobra (github.com/spf13/cobra@v1.10.2)`, since external packages are leaves and that token is their whole content.

`--go-versions` adds a `go version go=1.26.0` line, taken from the `attego:go-version` token, as the first child of each package node. Without it `go-version` entities are not printed, so output without the new flags is unchanged. Combined with `--content-hash`, the line also shows the hash of the `go-version` entity. The line is read from the `go-version` content token, so `--go-versions` needs the same content tokens as `--content-hash`.

Hashes are computed over the whole graph, including entities outside the printed subtree and the working-directory filter, so an entity prints the same hash from any directory. A repository with an external import that matches no `require` directive, or that a `replace` directive redirects outside the repository, makes `atte graph --content-hash` and `atte graph --go-versions` fail immediately, before any output is written, with an error naming the import and its `go.mod`. There is no fallback token and no partial output: a graph whose inputs cannot all be identified has no trustworthy hashes, and `--go-versions` does not hide that by skipping the check.

## Cross cutting concerns (as needed)

- **Determinism:** inputs are ids, git hashes and module versions only; no map iteration order, timestamps or absolute paths reach the hash.
- **Performance:** a closure is a sorted slice of entity indexes per component, so memory grows with the sum of the closure sizes. Most blobs have empty closures; an `atte.hcl` blob's closure is the closure of the targets it declares. The calculator is lazy, and hashing streams each entry into sha256 without building the encoding in memory.
- **Conservatism:** within the modeled inputs, the graph over-approximates dependencies (it ignores build constraints), so hashes change more often than necessary for those inputs. Inputs the graph does not model yet are listed below and do not affect hashes.
- **Known limitation, by design:** external packages are leaves, so upgrading a module that is only reached indirectly (imported by an external package but never by this repository) changes no hash. Changing the version of any module this repository imports directly changes the hash of every importer.
- **Known limitations (documented, to be improved by separate `attego` parser work):** `//go:embed` files, `.s`/`.c`/`.h` files and `testdata` are not package inputs, so changing them does not change a package's hash. The Go version comes from `go.mod`, so a toolchain upgrade that does not touch `go.mod` does not change hashes.
- **Follow-up improvements and bug fixes:**
  - A blob token is the git blob hash, which does not include the file mode, so a change to the executable bit changes no hash. Follow-up: include the mode in the blob token.
  - With `--working-tree`, blobs are hashed from raw working-tree bytes, while git hashes the content after clean filters (`core.autocrlf`, `.gitattributes`). The same logical content can therefore hash differently in a working tree and in a commit. Follow-up: apply the same filters or document the divergence in graph output.
  - `attego` classifies any import whose first path element has no dot as stdlib, so cgo's `import "C"` becomes a stdlib entity. Bug fix: model it as its own pseudo-package or skip it.

## Alternatives considered (as needed)

- **Merkle hash (hash of direct dependency hashes):** reuses hashes directly, but any change to graph structure that leaves the reachable set unchanged changes every importer's hash. Rejected on purpose.
- **Packages depend on the module's `go.mod` blob:** catches `go`-directive bumps for packages that import nothing from stdlib, but every `go.mod` edit, including adding an unrelated `require`, then changes every package's hash in the module. A `go-version` entity carries only the declared Go version.
- **`go.sum` hashes for external modules:** captures module content, but `go.sum` already guarantees one content per `module@version`, so the version is sufficient input and cheaper to produce.

## Future plans (as needed)

- Compare hashes between two refs to produce Impulses and the Cone.
- Persist hashes keyed by id and token to skip recomputation.
- Handle dirty trees explicitly if a consumer needs them.

## Other reading (as needed)

## Implementation (ephemeral)

Three stacked PRs. The first corrects the existing graph so the second can rely on it, and the third tidies what the second left; none changes `atte graph` output when `--content-hash` and `--go-versions` are absent.

1. **Graph corrections.** Fixes to what the graph already says, each useful on its own.
   - Flip `contains` to parent -> child in `attegit.Repo.Graph` (`detector/attegit/graph.go`) and simplify `addGraphChildren` in `cmd/graph.go`. Update the other `contains` consumers and tests (`detector/attego/module.go`, `cmd/execution_test.go`, `detector/attehcl/attehcl_test.go`, `detector/attego/module_test.go`).
   - Reverse the `atte.hcl` file -> target edge: replace the target -> file `source-file` edge with a file -> target `contains` edge (still under `WithAttachToTree`). `cmd/graph.go` does not otherwise consume the old edge: `hasContainment` is only asked about packages.
   - Add the `tests` relationship (package-test -> package) in place of the `contains` edge `attego` emits today, always emitted regardless of options. An external test package keeps the `imports` edge it already gets when its test files import the package under test.
   - Fix `import "C"` being classified as stdlib by skipping it: the cgo pseudo-package has no source and is not an entity.
2. **Content hashes.** The feature itself, built on the corrected graph.
   - `attego`: read `require`, versioned `replace`, `go` and `toolchain` from `go.mod`; add the `attego:go-version` entity and `go-version` relationship.
   - The new optional content capability on Sensors, implemented by `attegit`, `attego` and `attehcl`, and aggregated by the Scanner.
   - `detector/contenthash`: tokens, Tarjan per-component closures, hash encoding, with the golden vector test.
   - `atte graph --content-hash`, and `--go-versions` to show the `go-version` entities, which are hidden by default.
3. **Follow-ups from reviewing the second PR.** Tightening that does not change behavior.
   - Parse each `go.mod` once per command. `attegit.Memoize` remembers a value on a `Repo`, and `attego` uses it so building the graph and computing content tokens share one parse. Failed computations are not remembered.
   - Validate content capabilities when a Sensor is attached: `DependencyRelations` without `ContentTokens` is an error, and a Sensor with tokens and no relations is accepted. `Scanner.ContentHashes` only reports a missing content Sensor when none is attached.

