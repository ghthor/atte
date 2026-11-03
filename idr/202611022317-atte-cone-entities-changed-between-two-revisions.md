# 2026-11-02: atte cone: entities changed between two revisions

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

`atte graph --content-hash` fingerprints every entity at one revision, but nothing compares two revisions. To learn what a commit or branch affected, a user has to run `atte graph` twice and diff the output by hand. This IDR adds `atte cone`, which computes the Cone of that change: it builds the graph at a base and a head revision, computes content hashes for both, subtracts the base hashes from the head, and prints the head graph pruned to the entities whose content hash changed. By default it compares `HEAD` with the working tree, staged changes included, when there are uncommitted changes, and `HEAD^` with `HEAD` otherwise.

### Context

- The content hash of an entity (`detector/contenthash`, IDR 202611021822) covers the entity's id and token and the id and token of every entity in its flattened dependency closure. It is a property of the complete graph at one revision: it does not depend on the working directory or on what is printed.
- `contains` is followed parent -> child, so a tree's closure includes everything beneath it. When a blob, package or target changes, every tree containing it changes too, up to the repository root `attegit:`. The changed set is therefore always connected to the root through `contains` edges, which is what lets the existing tree renderer print it without gaps.
- `attegit.Open(root, ref)` reads a revision with `git ls-tree` and reads blobs through go-git, so a `*attegit.Repo` can be built for any resolvable revision without touching the working tree. Every built-in Sensor reads repository content through `*attegit.Repo` (`Show`, `Memoize`), never from the filesystem, so a graph built for a base revision reflects that revision, including its own `atte.hcl` settings. `WithWorkingTree` overlays the working tree on top of a ref: every path reported by `git diff-files` (unstaged), `git diff --cached` (staged) or `git ls-files --others --exclude-standard` (untracked, not ignored) takes the bytes on disk, and deleted paths disappear. Staged and unstaged changes are therefore one state, "the working tree"; the index is not modeled separately, so a change that is staged and then reverted on disk is not a change.
- `cmd/graph.go` (`printGraph`) assembles the complete graph (`repo.Graph()` absorbed with `Scanner.Graph(ctx, repo, graphset.WithAttachToTree())`) and, for `--content-hash`, a `*contenthash.Calculator` from `Scanner.ContentHashes`. The renderer (`printGitGraph` and its helpers) walks `contains` edges from the working directory's tree and prints packages, targets and their dependency lines.
- The root `propagation` package (`Impulse`, `Cone`, `Broad`) is an unused Broad Phase: nothing in the repository imports it and it has no tests. `Broad` walks `Out` edges, which point from a dependent to its dependency, the opposite of the direction change flows. This IDR replaces it: a changed content hash already means "something in my closure changed", so comparing hashes is the propagation.
- GLOSSARY: a content hash is a Signature, a Snapshot is a recorded state of the Universe, and a Cone is the conservative region of the Universe an Impulse could have affected. A content hash covers an entity's whole dependency closure, so it changes whenever anything in that closure changes, including edits with no observable effect. Entities whose hash changed between two Snapshots are therefore Candidates, not confirmed Impacts, which the glossary reserves for the result of refinement. The content hash IDR listed "compare hashes between two refs to produce Impulses and the Cone" as a future plan; this is that work.

### Goals

- `atte cone` prints the entities whose content hash at the head revision differs from the base revision, or which do not exist at the base, rendered as the same tree `atte graph` prints and pruned to those entities.
- With no flags, `atte cone` answers "what does my current work affect?": it compares `HEAD` with the working tree when that has uncommitted changes, and otherwise "what did the last commit affect?", comparing `HEAD^` with `HEAD`.
- Base and head are each overridable with a flag that accepts any git revision expression, so a script or CI job can pin the comparison and never depend on the state of the working tree.
- The command says which comparison it made.
- A reusable abstraction, independent of Cobra, that captures one revision as a graph plus content hashes and subtracts one capture from another, producing a graph that contains only the changed entities. Its result, `cone.Delta`, is the Broad Phase output: the Cone of the change between two Snapshots. `atte cone` is its first consumer; running the affected targets later should reuse it unchanged.
- `Delta.Frontiers()` layers the Cone by distance from its Impulses, so a consumer can tell what changed first from what changed as a consequence.
- The `cone` package replaces the unused `propagation` package, which is deleted.
- Deterministic output, and no output when nothing changed.

### Non-Goals

- Narrow Phase refinement (AST, ABI or build-and-compare). Content hash inequality is the only signal.
- Rendering the Frontiers in `atte cone` output. `Delta.Frontiers()` is API only in this IDR, and rendering it is future work. The Cone itself is computed by comparing hashes, not by walking outward, which already includes propagation.
- Executing anything. `atte cone` prints; running the affected targets is a follow-up.
- Listing removed entities, distinguishing staged from unstaged changes, and a non-zero exit status when something changed. See Future plans.
- Making a target's script an input of the target (the `script` edge). It is handled separately.
- Comparing against a persisted snapshot or across repositories. Both revisions must resolve in the current repository.
- Changing `atte graph` output or the content hash encoding.
- Explaining *why* an entity changed beyond what the pruned tree already shows (its changed children and changed dependencies).

### Proposed Solution

Add a package `cone` with two operations and one result:

1. `Capture(ctx, scanner, repo)` builds the complete graph for one `*attegit.Repo` and, when asked, its `*contenthash.Calculator`, returning a `Snapshot`. This is the assembly `printGraph` performs today, moved so both commands share it.
2. `Diff(base, head)` compares `Sum(id)` for every entity in head against base. An entity is *changed* when base has no entity with that id or when the two sums differ. The result, a `Delta`, holds the sorted changed ids, the subset that is new, the base ids missing from head, and the subgraph of the head graph induced by the changed set: changed entities plus every head relationship whose both ends are changed.

The `cone` package is the Broad Phase, and `Delta` is its output. `Changed` are the Candidates the glossary describes; the Cone is intentionally an over-approximation, and Narrow Phase refinement would consume it later.

`atte cone [--base REV] [--head REV]` resolves the two sides (see Revision selection), opens two repositories, captures both, diffs them and renders the induced graph with the `atte graph` renderer, starting at the working directory's tree. When that tree is not in the delta nothing is printed. `--content-hash` appends `<base>..<head>` abbreviated hashes (or `new`) to each node, and the other `atte graph` presentation flags are accepted with the same meaning.

## Detailed Design

### Command

```text
atte cone [flags]

  --base REV          base revision (default: "HEAD" when the working tree has
                      uncommitted changes or --working-tree is given, otherwise "HEAD^")
  --head REV          head revision (default: "HEAD" plus the working tree when it has
                      uncommitted changes or --working-tree is given, otherwise "HEAD")
  --working-tree      overlay the working tree on the head revision, even if clean
  --quiet             do not print the comparison that was made to stderr
  --content-hash      show <base>..<head> abbreviated content hashes, or "new"
  --external-imports, --go-files, --run-targets, --go-versions
                      as for atte graph
```

Revisions are passed verbatim to `attegit.Open`, so anything `git ls-tree` accepts works: `HEAD~3`, `origin/main`, a tag, a sha. Errors name the side and the revision, for example `resolve base revision "HEAD^": ...` on an initial commit or in a shallow clone. On a merge commit `HEAD^` is the first parent, so a clean default shows the Cone of the merge relative to the mainline.

### Revision selection

The two sides are resolved before anything is captured. The defaults for both sides are chosen together by the state of the working tree:

| working tree | default base | default head |
| --- | --- | --- |
| has uncommitted changes, or `--working-tree` is given | `HEAD` | `HEAD` + working tree |
| clean | `HEAD^` | `HEAD` |

`--base` and `--head` each replace only their own side; the other side keeps its default from the table. `--base HEAD^ --head HEAD` is therefore the committed comparison regardless of the working tree, and `--base origin/main` compares the working tree (if dirty) against `origin/main`. `--working-tree` overlays the working tree on whichever head is used, `--head REV` included. There is no derived default such as `REV^` for an explicit `--head`: passing `--head` alone pairs it with the default base from the table.

*Uncommitted changes* is whatever git reports. `attegit` runs `git --no-optional-locks status --porcelain -z --untracked-files=normal` in the repository root and treats any output as dirty. That covers the same three sources `WithWorkingTree` reads (unstaged, staged, untracked and not ignored), so untracked files count, and it leaves the question of what is dirty to git instead of re-deriving it. `git status` refreshes stat information before it compares, so a file that was only touched is not reported, unlike `git diff-files`, which lists stat-dirty entries. Two consequences follow, both visible in the stderr line below. A change git calls dirty but the graph does not model, such as a mode-only change, compares `HEAD..working tree` and finds an empty Cone. A change that is staged and then reverted on disk is dirty to git because the index differs, and also finds an empty Cone. When the tree is clean `HEAD` is opened once as the head and `HEAD^` as the base; when it is dirty `HEAD` is opened as the base and `HEAD` with the working tree overlay as the head.

Because the default depends on repository state, the command writes the comparison it made to stderr before the output, for example `cone: HEAD..working tree` or `cone: HEAD^..HEAD`, and `--quiet` silences it (stdout stays the tree only). The line is stderr so piped output is unchanged.

Like `atte graph`, output is scoped to the working directory: the renderer starts at the working directory's tree in the induced graph. Hashes are still computed over both complete graphs, so the same entity shows the same result from any directory.

### Changed set

For head entity ids `H`, base entity ids `B`, and `sum_x(id)` the content hash in snapshot x:

```text
changed = { id in H : id not in B  or  sum_base(id) != sum_head(id) }
added   = { id in H : id not in B }           (subset of changed)
removed = { id in B : id not in H }
```

Consequences, all following from how hashes are defined:

- A deleted file is `removed`; it is not in the head graph and cannot be printed. Its parent tree is `changed` because its closure lost an entry, so the tree appears, possibly with no children listed.
- A rename is one `removed` and one `added`; both parent trees are changed.
- A bumped `require` in `go.mod` changes the token of every `attego:package-external` entity from that module, so every importer and every tree above an importer changes. With `--external-imports` the external import line appears under its importers.
- A `go` directive bump changes `attego:go-version`, so every package in the module changes.
- An `atte.hcl` blob whose target depends on something that changed is itself `changed` even though its git blob hash is identical, because the blob contains its targets (content hash IDR). The blob and the target both appear.
- Editing a target's script does **not** change the target: the `script` edge is not a dependency edge yet (content hash IDR, non-goals). Fixing that is deliberately left to a separate change.

**Connectivity.** If `X` is changed and tree `T` contains `X`, then `closure(T)` contains `X` and `closure(X)`. `X` changed means either `X` is new (so `T`'s input gained an entry) or some entry in `X`'s input differs between the snapshots; every such entry is also in `T`'s input, so `T` is changed. By induction every ancestor up to `attegit:` is changed, which is why walking `contains` edges from the root over the induced graph reaches every changed entity. The root is changed whenever anything is.

### Frontiers

`Delta.Frontiers()` layers the Cone by how far each Candidate is from the Impulses, which is the Frontier of the glossary at each step of Propagation. Hash comparison finds the Cone without walking, so the layers are derived afterwards from `Delta.Graph`, following only the dependency relations, which `Delta` reads from the head `Calculator` through a new `Dependencies()` accessor in `contenthash`.

- **Layer 0, the Impulses.** Every added entity, and every changed entity that has no changed dependency. A tree token is itself a Merkle hash, so "its own token changed" would also select every tree above a change; "no changed dependency" selects only entities whose change is not explained by something they depend on. A changed cycle of entities that depends on nothing else that changed is layer 0 as a whole, since the cycle has no entry point of its own.
- **Layer k.** Changed entities whose nearest changed dependency is in layer k-1, that is `1 + min` over their changed dependencies. Cycles are condensed first so this is well defined.
- Every entity in `Changed` appears in exactly one layer, each layer is sorted bytewise, and the layers concatenated are a permutation of `Changed`. The last layer is the outermost boundary the change reached. Because trees depend on what they contain, trees ride along in the layers; a consumer that only wants runnable targets filters by kind.

For the worked example below:

```text
0  attegit:internal/value/value.go
1  tree internal/value, package internal/value
2  tree internal, package cmd
3  root, tree cmd, package main (root), target codegen go
4  atte.hcl
```

`Frontiers()[0]` is the set of Impulses, which is the answer to "what did I actually edit".

### Worked example

The repository from `TestPrintGraphGolden` (`cmd/testdata/graph/default.golden`): `main` imports `cmd`, `cmd` imports `internal/value`, and `atte.hcl` declares `codegen "go" { depends_on = [gopkg("./cmd")] }` and `test "default"`. `HEAD` edits `internal/value/value.go`.

Changed: `attegit:internal/value/value.go`, the `internal/value` package, trees `internal/value`, `internal`, the `cmd` package (imports `value`), tree `cmd`, the `main` package (imports `cmd`), target `codegen go` (depends on `cmd`), blob `atte.hcl` (contains `codegen go`), and the root. Unchanged and therefore absent: `README.md`, `go.mod`, `main.go`, `cmd/cmd.go`, everything under `pkg/`, `test.sh`, `test default`, and the `go-version` entity.

```text
.
├── atte.hcl
├── cmd/
│   └── go package example.com/root/cmd
│       └── import example.com/root/internal/value
├── internal/
│   └── value/
│       ├── value.go
│       └── go package example.com/root/internal/value
├── go package example.com/root
│   └── import example.com/root/cmd
└── codegen attehcl:codegen:atte.hcl:go
    └── go package example.com/root/cmd
        └── import example.com/root/internal/value
```

Only changed dependencies are listed under a package or target, because unchanged ones are not in the induced graph. That is the "why" the output offers: each listed dependency is a path the change travelled along. With `--content-hash`, `value.go 36f627c487a2..9a1b2c3d4e5f` and so on; a new file shows `new`.

### Package shape

```go
package cone

// Snapshot is the complete graph of one revision and its content hashes.
type Snapshot struct {
    Graph *graph.Graph
    // Hashes is nil unless Capture was given WithContentHashes.
    Hashes *contenthash.Calculator
}

// Capture builds the complete graph for repo. Content hashes are computed only
// with WithContentHashes: content tokens are strict, so a repository with an
// unresolvable external import has a graph but no hashes, and plain atte graph
// must keep working on it. atte cone always asks for hashes.
func Capture(ctx context.Context, scanner detector.Scanner, repo *attegit.Repo, options ...CaptureOption) (*Snapshot, error)

// Delta is the Broad Phase output: the Cone of the change from base to head,
// found by subtracting the base content hashes from the head's.
type Delta struct {
    Base, Head *Snapshot
    // Changed is every head entity whose content hash differs from base or
    // which base does not have, sorted bytewise.
    Changed []graph.EntityID
    // Added is the subset of Changed that base does not have.
    Added []graph.EntityID
    // Removed is every base entity that head does not have, sorted bytewise.
    Removed []graph.EntityID
    // Graph is the subgraph of Head.Graph induced by Changed.
    Graph *graph.Graph
}

func Diff(base, head *Snapshot) (*Delta, error)
func (d *Delta) Contains(id graph.EntityID) bool

// Frontiers returns Changed layered by distance from the Impulses; see Frontiers.
func (d *Delta) Frontiers() [][]graph.EntityID
```

`Diff` forces `Sum` for every head entity and for every base entity that head also has, so after it both calculators are fully memoized. `Delta.Graph` is built with `graph.New` from the changed entities and the filtered relationships, so any consumer can walk it with the normal `graph` API; nothing in `graph` changes.

### Rendering

`atte cone` reuses `printGitGraph` with the head repository and `Delta.Graph`. Three adjustments to `cmd/graph.go`:

- `renderOptions` currently holds a `*contenthash.Calculator` and formats one abbreviated hash. It gains a hash-label hook (a function or small interface) so `atte graph` supplies `<head>` and `atte cone` supplies `<base>..<head>` or `new`. Token lookups (`go version`, external module versions) use the head calculator.
- When the starting tree is not in the graph, `printGitGraph` prints nothing and returns nil instead of `parent entity %q not found`. For `atte graph` this cannot happen (the complete graph has every tree); for `atte cone` it is the "nothing changed below here" case.
- `hasContainment` decides whether an import is local by looking for `contains` or `source-file` edges leaving the package. In the induced graph a package that changed only because one of *its* imports changed has no changed source files, so those edges are gone and its importers would stop listing it. The check must use the entity kind and the head graph, not the induced graph, or the renderer must be given a membership predicate alongside the full graph. Which of the two is a detail for implementation, but the golden test for this case (a two-hop import chain where only the leaf file changes) must exist.

### Testing

- `cone` unit tests over hand-built graphs and tokens (`graph.New`, `contenthash.New`): identical snapshots give an empty delta; a token change marks the entity and every dependent; a new entity is `Added` and `Changed`; a missing entity is `Removed` and its container is `Changed`; the induced graph drops relationships whose other end is unchanged; the connectivity property holds on a small tree. `Frontiers` puts added entities and entities with no changed dependency in layer 0, layers the rest by shortest distance, puts a changed cycle with no changed outside dependency in layer 0, and returns every changed entity in exactly one layer.
- `cmd` golden tests built with `attegittest.GitRepo` (two commits, opened at `HEAD^` and `HEAD`): the worked example above; the two-hop import chain; an `atte.hcl` edit; a `go.mod` version bump with `--external-imports`; a deleted file; no change (empty output); default selection with a clean tree (`HEAD^..HEAD`), with an unstaged edit, with a staged-only edit, with an untracked file, with a deleted file, with a staged edit reverted on disk (`HEAD..working tree`, empty Cone), and with a touched-but-identical file (the first and last compare `HEAD^..HEAD`, the rest `HEAD..working tree`); explicit `--base`/`--head` ignoring a dirty tree; `--working-tree` on a clean tree; a subdirectory with nothing changed below it; `--content-hash` showing `base..head` and `new`.
- Error tests: unresolvable `--base` and `--head`; `HEAD^` on a single-commit repository with a clean tree (error) and with a dirty tree (works: base is `HEAD`).

## Cross cutting concerns

- **Determinism.** `Changed`, `Added` and `Removed` are sorted; the renderer is the one `atte graph` uses.
- **Performance.** Two complete graph builds and two full hash computations per run; today `atte graph --content-hash` does one. Each calculator costs the sum of its closure sizes. Acceptable at this repository's scale; the content hash IDR's plan to persist hashes keyed by id and token would make the base side nearly free, and both captures are independent so they can run concurrently.
- **Conservatism.** `atte cone` is a Broad Phase and trades precision for speed: it compares content hashes and never looks at what a change means. The glossary's rule for the Broad Phase applies: false positives are acceptable, false negatives are not. The two have different causes and different remedies.
  - *False positives are by design.* An entity is in the Cone when anything in its dependency closure changed, whether or not the change can affect it: a comment or formatting edit, a change to a function the dependent never calls, code behind a build constraint that is not active (build constraints are ignored). Removing these Echoes takes analysis of the change itself (AST, ABI or build-and-compare), which is Narrow Phase work. It is out of scope for `cone`, and nothing here should be traded against speed to shrink the Cone. A refinement fast enough to run at Broad Phase speed would need its own IDR.
  - *False negatives are defects.* They come only from inputs the graph does not model yet, listed in the content hash IDR: `script` edges, `//go:embed`, `testdata`, file modes, and toolchain upgrades that do not touch `go.mod`. Each is closed by modeling the input, not by accepting it as a trade-off.
  - The help text of `atte cone` states both: the output may include entities the change cannot affect, and it can miss entities affected through the unmodeled inputs, until those are closed.
- **Test injection.** `ExecuteOptions.Repository` injects one `*attegit.Repo` and `openRepository` returns it for every ref, which cannot serve a command that opens two. Command tests call the internal print function with two opened repositories, as the graph tests do with `printGraph`; if end-to-end Cobra tests are wanted, `ExecuteOptions` needs a way to inject a repository root for discovery without injecting a repository.
- **CI.** Shallow checkouts need `fetch-depth: 2` for the clean default, or an explicit `--base origin/main`. A CI job whose earlier steps dirty the tree (codegen, formatters) would silently switch to a working-tree comparison, so CI should pass `--base` and `--head` explicitly; the stderr line makes the choice visible in logs.
- **Implicit default.** The default now depends on state the user may not be thinking about. The stderr line and the explicit-flag override are the mitigations; the clean-tree default keeps the command usable after committing.

## Alternatives considered

- **Propagate from token-changed entities with a graph walk over reversed edges (what `propagation.Broad` was meant for).** Finds everything whose closure contains a changed token, but misses closure *membership* changes with no token change: a deleted file changes its tree's hash although no surviving token changed, and a new import of an unchanged package changes the importer's closure. Hash comparison captures both, is already memoized, and agrees with `atte graph --content-hash` by construction.
- **A membership predicate instead of an induced graph.** Simpler for the renderer, but the request is for a graph value that downstream consumers can walk, and `graph.New` already validates it. The predicate is kept too (`Contains`) for the `hasContainment` case.
- **`atte graph --diff REV` instead of a new command.** Fewer commands, but the output is a different thing (a delta, not a view), it needs two repositories, and `cone` is the vocabulary the rest of Atte uses for this stage.
- **A positional `BASE..HEAD` range like git.** Familiar, but flags were asked for and are easier to default independently.

## Future plans

- `atte run --in-cone` (or `atte cone --run`): execute the run targets in the Cone. Without refinement this is a conservative Work Set.
- `atte impact`: the command name is reserved for the output of the Narrow Phase, the Cone with Echoes attenuated away, once a Refiner exists.
- A machine-readable output (`--format ids` or JSON) for CI consumers.
- Persist base hashes so the base capture is a lookup.
- List removed entities, for example in a trailing section or behind a flag. `Delta.Removed` is already computed.
- Compare `HEAD` with the index separately (a `--staged` flag, like `git diff --cached`), so a pre-commit hook sees exactly what will be committed. Until then the working tree is the combination of staged and unstaged changes.
- `--exit-code`: exit non-zero when the Cone is not empty, like `git diff --exit-code`. The default is exit 0.
- Color the `<base>..<head>` hashes in `--content-hash` output.
- Render `Delta.Frontiers()`: mark Impulses in the tree or print the layer next to each node. Layer 0 alone cannot be rendered with the tree renderer because it lacks the ancestors that connect it to the root, so this needs the full `Delta.Graph` with markers.
- Derive the default base from an explicit `--head` (`<head>^`) instead of pairing it with the default base.
- Follow the `script` edge so target scripts are inputs (content hash IDR follow-up), addressed specifically and separately from this IDR.

## Other reading

- `idr/202611021822-content-sha-summing-for-graph-targets.md`: hash definition, dependency edge kinds, known limitations.
- `GLOSSARY.md`: Signature, Snapshot, Impulse, Cone, Impact.
- `cmd/graph.go`: the renderer this command reuses.

## Decisions from review

Answers given in review. They are already reflected in the sections above and are kept here as a record.

- **Removed entities** are not listed in v1. They are visible only through their changed parent tree. `Delta.Removed` is computed, and listing them is future work.
- **Staged and unstaged changes** are not distinguished. "The working tree" is their combination, as `WithWorkingTree` already defines it. A separate index comparison is future work.
- **Exit status** is 0 when nothing changed. `--exit-code` is future work.
- **Working directory scope** is the same as `atte graph`: output starts at the working directory's tree.
- **Revision syntax** is flags only. There is no positional `BASE..HEAD`.
- **Package location** is the root package `cone`.
- **The `script` edge** is out of this IDR and its PR plan. It will be addressed specifically later; until then, editing a target's script does not change the target.
- **Untracked files** count as uncommitted changes, as they do for `atte graph --working-tree`.
- **Dirtiness** is decided by `git status`, not by comparing blobs or by `git diff-files`.
- **The comparison is announced** on stderr, silenced by `--quiet`.
- **No derived default base** for an explicit `--head`. It pairs with the default base. Deriving one is future work.
- **`--content-hash` format** is `<base>..<head>` abbreviated hashes, with `new` for added entities. Coloring of this output is future work.
- **Vocabulary** (Cone, Candidate and Impact, and how a Cone is computed from two Snapshots) is documented in the `cone` package documentation and README, not in the GLOSSARY.
- **The `propagation` package** is deleted. `cone.Delta` is the Broad Phase output that `atte cone` consumes.
- **`Delta.Frontiers()`** is an unused API at the top of the stack. Rendering it is future work.

## Implementation (ephemeral)

Stacked PRs on top of this IDR's PR, bottom to top. Every PR passes the repository's verification targets on its own, keeps `atte graph` output unchanged unless stated, and carries its own tests. The order puts what the goals need first and what they need least last.

| # | PR | Contents | Notes |
| --- | --- | --- | --- |
| 1 | `propagation: delete the unused package` | Remove `propagation/`. | No importers or tests, so a pure removal. Independent of everything above it. |
| 2 | `cone: capture a revision's graph and hashes` | `cone.Snapshot` and `cone.Capture`, moved out of `printGraph`, which now calls them. | Pure refactor: existing `atte graph` goldens must not change. Hashes are optional, see the hints. |
| 3 | `cone: diff two snapshots` | `cone.Diff` and `cone.Delta` (`Changed`, `Added`, `Removed`, `Graph`, `Contains`), with the unit tests over hand-built graphs, and the package documentation and README that define the vocabulary: Snapshot, Cone, Candidate versus Impact, and how a Cone is computed from two Snapshots. | The Broad Phase output. No command yet. |
| 4 | `cmd: let graph rendering show a subset of a graph` | Hash-label hook in `renderOptions`, an empty starting tree prints nothing, and `hasContainment` judged on the head graph. | No change for `atte graph`. Tests render a `Delta.Graph`, including the two-hop import chain. |
| 5 | `cmd: add atte cone` | The command with `--base` (default `HEAD^`), `--head` (default `HEAD`), `--content-hash` and the other presentation flags, two-repository open, golden and error tests, and help text listing the inherited limitations. | Smallest useful `atte cone`: always the committed comparison. |
| 6 | `cmd/cone: default to the working tree when it is dirty` | `--working-tree`, `--quiet`, the stderr line, revision selection as a pure function with its table test, and the `git status` check in `attegit` that decides dirtiness. | Changes the default of the command from PR 5 to what Revision selection describes. Golden tests for staged, unstaged, untracked, deleted and touched-only files. |
| 7 | `cone: add Delta.Frontiers` | `Delta.Frontiers()`, `contenthash.Calculator.Dependencies()`, and the Frontiers unit tests. | Top of the stack on purpose: no consumer, and none of the goals depend on it. Can be dropped or merged later without affecting the rest. |

Progress, recorded by the PR that did the work:

- PR 1 (`propagation-delete`): removed `propagation/`. A search for the package name found no importers, no `atte.hcl` targets and no documentation references other than unrelated uses of the word "propagation" in GLOSSARY.md and a comment in `detector/attegit/graph.go`; those stay. No design change.
- PR 2 (`cone-capture`): added `cone.Snapshot`, `cone.Capture` and `cone.WithContentHashes`, and made `printGraph` call `Capture`. Notes on how it differs from the sketch above:
  - `Capture` keeps `printGraph`'s error prefixes ("build Git graph", "build detector graph", "merge detector graph", "compute content hashes"), so existing error assertions are unchanged. `printGraph` returns them as they are.
  - `printGraph` now resolves the scanner before building the graph, where it used to build the Git graph first. Only the error that wins when both fail could differ.
  - The `cone` package documentation is a short placeholder. The vocabulary (Snapshot, Cone, Candidate versus Impact) arrives with `Diff` in PR 3, as planned.
  - Tests are black-box (`cone_test`) against the default scanner and `attegittest`: the graph holds Git tree and detector entities, `Hashes` is nil without the option, hashes follow file content, and an unresolved external import fails only with `WithContentHashes`.
  - No `atte graph` golden changed.
- PR 3 (`cone-diff`): added `cone.Diff` and `cone.Delta` (`Changed`, `Added`, `Removed`, `Graph`, `Contains`), the package documentation in `cone/doc.go`, and `cone/README.md`. Notes:
  - `Diff` returns an error when either snapshot lacks `Hashes`, or is nil, so a caller that forgot `WithContentHashes` fails with that name rather than on a nil pointer.
  - `Changed`, `Added` and `Removed` are never nil, so an empty Cone compares equal to an empty list. `Contains` is backed by an unexported set.
  - The README is `cone/README.md`; no other package has one, so it is the first and is kept short. The package documentation carries the vocabulary and the README repeats it as a table with an example.
  - Tests build worlds with `graph.New` and `contenthash.New`: identical snapshots, an edited token reaching dependents, a non-dependency relationship that does not propagate, an added entity, a removed entity, a rename, the induced graph, connectivity through `contains`, missing hashes, and a pair of real captured repositories.
- PR 4 (`graph-subset`): `renderOptions` gained `hashLabel` and `complete`; `atte graph` sets neither, so its output is unchanged. Notes on how it differs from the sketch above:
  - The missing-start-tree case is tied to `complete`. Only a subset prints nothing when its starting tree is absent; a complete graph still fails with `parent entity ... not found`, so `atte graph` in a directory that does not exist at `--ref` is still an error. The sketch had every graph print nothing.
  - Of the two options for `hasContainment`, the renderer is given the complete graph (`complete`) next to the graph it prints. `packageImports` asks that graph whether an imported package has source files or contents.
  - `hashLabel` replaces the abbreviated hash for every entity that shows one. Go version and external import lines still read tokens from `hashes`, which `atte cone` will set to the head calculator.
  - Only changed dependencies are listed, so a package in the Cone does not show a `go version` line when the version entity did not change.
  - Tests live in `cmd/graph_subset_test.go` and render a real `Delta.Graph` from a two-commit repository: the worked example with and without `base..head` hashes (goldens `subset-edit` and `subset-edit-content-hash`, the first containing the two-hop import chain), the imports of packages with no changed files, `new` labels, an unchanged subdirectory, no change, and the unchanged error for a complete graph. The two-commit fixture is reusable by the command's tests in PR 5.
- PR 5 (`cone-command`): added `atte cone` in `cmd/cone.go`, with `--base` (`HEAD^`), `--head` (`HEAD`), `--content-hash`, `--external-imports`, `--go-files`, `--run-targets` and `--go-versions`. Notes:
  - `printCone(ctx, w, base, head, relativePath, options)` does the work and the Cobra function only opens the two revisions, so the rendering is testable with repositories and not with flags. `rangeHashLabel` produces `<base>..<head>` or `new`. The PR 4 fixture's `render` now calls `printCone` instead of repeating its setup.
  - Each revision is opened with `attegit.Open` directly. The `ExecuteOptions.Repository` injection stands for a single revision and is ignored by this command, so its tests run the real command in a temporary Git repository through `t.Chdir`.
  - Errors name the side and the revision: `resolve base revision "HEAD^": ...`. Errors from `Capture` and `Diff` are prefixed `capture base`, `capture head` and `compare revisions`.
  - As planned, the command always compares committed revisions: uncommitted changes are ignored. The help says so. `--head` alone pairs with the default base `HEAD^`, as Revision selection says, and a test records it.
  - The help lists the limitations inherited from the content hash IDR: the `script` edge, `//go:embed`, `.s`/`.c`/`.h` files and `testdata`, the toolchain, indirectly reached module upgrades, and `go.work`.
  - Run targets are discovered on the head revision.
  - The subset goldens from PR 4 are reused by the command tests, so the command and the renderer tests cannot drift apart. No new golden files.

Order notes:

- PRs 2 and 3 precede the command so the abstraction is reviewable on its own, apart from Cobra and rendering.
- PR 6 is split from PR 5 so the command can land with a simple, stateless default first. `attegit` gains the dirty check only when something uses it.
- `Dependencies()` lands with PR 7 because only `Frontiers` reads it.
- Following the `script` edge is not part of this stack.

Hints:

- `openRepository` already takes a ref; call it twice. `attegit.WithWorkingTree` only on the head open of the dirty pair. The dirty check is a small exported function in `attegit` that runs `git --no-optional-locks status --porcelain -z --untracked-files=normal` and reports whether it printed anything, so it can be tested with `attegittest.GitRepo` independent of the command.
- The default resolution is a pure function of the flags and a dirty bool; unit test it as a table of the cases in Revision selection before wiring it to git.
- `attegit.Open` runs `git ls-tree -r -t --full-tree REV`; its stderr is what the revision error should carry.
- `printGraph` is the only caller of the graph assembly today; after `Capture` exists it should call it rather than duplicate it. It computes hashes only under `--content-hash` or `--go-versions` (`Scanner.ContentHashes` fails on an import that matches no `require`), so PR 2 keeps that behaviour by passing `WithContentHashes` only in those cases.
