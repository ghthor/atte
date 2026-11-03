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

`--base` and `--head` each replace only their own side; the other side keeps its default from the table. `--base HEAD^ --head HEAD` is therefore the committed comparison regardless of the working tree, and `--base origin/main` compares the working tree (if dirty) against `origin/main`. `--working-tree` overlays the working tree on whichever head is used, `--head REV` included. An explicit `--head` is never overlaid by a dirty tree alone, only by `--working-tree`. There is no derived default such as `REV^` for an explicit `--head`: passing `--head` alone pairs it with the default base from the table.

*Uncommitted changes* is whatever git reports. `attegit` runs `git --no-optional-locks status --porcelain -z --untracked-files=normal` in the repository root and treats any output as dirty. That covers the same three sources `WithWorkingTree` reads (unstaged, staged, untracked and not ignored), so untracked files count, and it leaves the question of what is dirty to git instead of re-deriving it. `git status` refreshes stat information before it compares, so a file that was only touched is not reported, unlike `git diff-files`, which lists stat-dirty entries. Two consequences follow, both visible in the stderr line below. A change git calls dirty but the graph does not model, such as a mode-only change, compares `HEAD..working tree` and finds an empty Cone. A change that is staged and then reverted on disk is dirty to git because the index differs, and also finds an empty Cone. When the tree is clean `HEAD` is opened once as the head and `HEAD^` as the base; when it is dirty `HEAD` is opened as the base and `HEAD` with the working tree overlay as the head.

Because the default depends on repository state, the command writes the comparison it made to stderr before the output, for example `cone: HEAD..working tree` or `cone: HEAD^..HEAD` (`cone: HEAD^..feature + working tree` when an explicit head is overlaid), and `--quiet` silences it (stdout stays the tree only). The line is stderr so piped output is unchanged.

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

`Delta.Frontiers()` layers the Cone by how far each Candidate is from the Impulses, which is the Frontier of the glossary at each step of Propagation. Hash comparison finds the Cone without walking, so the layers are derived afterwards from `Delta.Graph`, following only the dependency relations, which `Delta` reads from the head `Calculator` through a new `Dependencies()` accessor in `contenthash`. A dependency on an entity that was removed is not a changed dependency, so an entity that lost its only dependency is in layer 0: the removed entity cannot be listed, and its dependents are the nearest thing to the edit.

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
//
// Diff returns an error naming WithContentHashes for a snapshot without hashes.
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
// The error is for an entity the head calculator does not know, which cannot
// happen for a Delta that Diff built.
func (d *Delta) Frontiers() ([][]graph.EntityID, error)
```

`Diff` forces `Sum` for every head entity and for every base entity that head also has, so after it both calculators are fully memoized. `Delta.Graph` is built with `graph.New` from the changed entities and the filtered relationships, so any consumer can walk it with the normal `graph` API; nothing in `graph` changes.

### Rendering

`atte cone` reuses `printGitGraph` with the head repository and `Delta.Graph`. Three adjustments to `cmd/graph.go`:

- `renderOptions` currently holds a `*contenthash.Calculator` and formats one abbreviated hash. It gains a hash-label hook (a function or small interface) so `atte graph` supplies `<head>` and `atte cone` supplies `<base>..<head>` or `new`. Token lookups (`go version`, external module versions) use the head calculator.
- When the starting tree is not in a subset, `printGitGraph` prints nothing and returns nil. That is the "nothing changed below here" case of `atte cone`. A complete graph still fails with `parent entity %q not found`, so `atte graph` in a directory that does not exist at `--ref` is still an error. The renderer tells the two apart by whether it was given `complete`.
- `hasContainment` decides whether an import is local by looking for `contains` or `source-file` edges leaving the package. In the induced graph a package that changed only because one of *its* imports changed has no changed source files, so those edges are gone and its importers would stop listing it. The renderer is therefore given the complete graph next to the graph it prints (`complete`) and asks that one. The golden test for this case is a two-hop import chain where only the leaf file changes.

### Testing

- `cone` unit tests over hand-built graphs and tokens (`graph.New`, `contenthash.New`): identical snapshots give an empty delta; a token change marks the entity and every dependent; a new entity is `Added` and `Changed`; a missing entity is `Removed` and its container is `Changed`; the induced graph drops relationships whose other end is unchanged; the connectivity property holds on a small tree. `Frontiers` puts added entities and entities with no changed dependency in layer 0, layers the rest by shortest distance, puts a changed cycle with no changed outside dependency in layer 0, and returns every changed entity in exactly one layer.
- `cmd` tests built with `attegittest.GitRepo` (two commits, opened at `HEAD^` and `HEAD`): the worked example above; the two-hop import chain; no change (empty output); `new` labels; default selection with a clean tree (`HEAD^..HEAD`), with an unstaged edit, with a staged-only edit, with an untracked file, with a deleted file, with a staged edit reverted on disk (`HEAD..working tree`, empty Cone), and with a touched-but-identical file (the first and last compare `HEAD^..HEAD`, the rest `HEAD..working tree`); explicit `--base`/`--head` ignoring a dirty tree; `--working-tree` on a clean tree; a subdirectory with nothing changed below it; `--content-hash` showing `base..head` and `new`.
- Error tests: unresolvable `--base` and `--head`; `HEAD^` on a single-commit repository with a clean tree (error) and with a dirty tree (works: base is `HEAD`).

## Cross cutting concerns

- **Determinism.** `Changed`, `Added` and `Removed` are sorted; the renderer is the one `atte graph` uses.
- **Performance.** Two complete graph builds and two full hash computations per run; today `atte graph --content-hash` does one. Each calculator costs the sum of its closure sizes. Acceptable at this repository's scale; the content hash IDR's plan to persist hashes keyed by id and token would make the base side nearly free, and both captures are independent so they can run concurrently.
- **Conservatism.** `atte cone` is a Broad Phase and trades precision for speed: it compares content hashes and never looks at what a change means. The glossary's rule for the Broad Phase applies: false positives are acceptable, false negatives are not. The two have different causes and different remedies.
  - *False positives are by design.* An entity is in the Cone when anything in its dependency closure changed, whether or not the change can affect it: a comment or formatting edit, a change to a function the dependent never calls, code behind a build constraint that is not active (build constraints are ignored). Removing these Echoes takes analysis of the change itself (AST, ABI or build-and-compare), which is Narrow Phase work. It is out of scope for `cone`, and nothing here should be traded against speed to shrink the Cone. A refinement fast enough to run at Broad Phase speed would need its own IDR.
  - *False negatives are defects.* They come only from inputs the graph does not model yet, listed in the content hash IDR: `script` edges, `//go:embed`, `testdata`, file modes, and toolchain upgrades that do not touch `go.mod`. Each is closed by modeling the input, not by accepting it as a trade-off.
  - The help text of `atte cone` states both: the output may include entities the change cannot affect, and it can miss entities affected through the unmodeled inputs, until those are closed.
- **Test injection.** `ExecuteOptions.Repository` injects one `*attegit.Repo` and `openRepository` returns it for every ref, which cannot serve a command that opens two. `atte cone` therefore opens its revisions itself and ignores the injected repository. Its tests run the real command in a temporary Git repository, and the rendering is tested through `printCone` with two opened repositories, as the graph tests do with `printGraph`.
- **CI.** Shallow checkouts need `fetch-depth: 2` for the clean default, or an explicit `--base origin/main`. A CI job whose earlier steps dirty the tree (codegen, formatters) would silently switch to a working-tree comparison, so CI should pass `--base` and `--head` explicitly; the stderr line makes the choice visible in logs.
- **Implicit default.** The default now depends on state the user may not be thinking about. The stderr line and the explicit-flag override are the mitigations; the clean-tree default keeps the command usable after committing.

## Alternatives considered

- **Propagate from token-changed entities with a graph walk over reversed edges (what `propagation.Broad` was meant for).** Finds everything whose closure contains a changed token, but misses closure *membership* changes with no token change: a deleted file changes its tree's hash although no surviving token changed, and a new import of an unchanged package changes the importer's closure. Hash comparison captures both, is already memoized, and agrees with `atte graph --content-hash` by construction.
- **A membership predicate instead of an induced graph.** Simpler for the renderer, but the request is for a graph value that downstream consumers can walk, and `graph.New` already validates it. `Contains` is kept for consumers that only need membership; the renderer is given the complete graph instead.
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
- Command tests that render an `atte.hcl` edit, a `go.mod` bump with `--external-imports`, a deleted file and a mode-only change. See Implementation.
- One strongly-connected-components implementation shared by `cone` and `contenthash`. See Implementation.

## Other reading

- `idr/202611021822-content-sha-summing-for-graph-targets.md`: hash definition, dependency edge kinds, known limitations.
- `GLOSSARY.md`: Signature, Snapshot, Impulse, Cone, Impact.
- `cone/doc.go` and `cone/README.md`: the vocabulary (Cone, Candidate and Impact, and how a Cone is computed from two Snapshots) and what a Cone gets wrong.
- `cmd/graph.go`: the renderer this command reuses.

## Implementation (ephemeral)

Delivered as seven pull requests stacked on this IDR's PR. Each passed the repository's verification targets on its own, and no `atte graph` output changed.

| # | PR | Delivered |
| --- | --- | --- |
| 1 | #9 `propagation: delete the unused package` | Removed `propagation/`. Nothing imported it. |
| 2 | #11 `cone: capture a revision's graph and hashes` | `cone.Snapshot`, `cone.Capture`, `cone.WithContentHashes`. `printGraph` calls `Capture`. |
| 3 | #12 `cone: diff two snapshots` | `cone.Diff`, `cone.Delta`, `cone/doc.go` and `cone/README.md`. |
| 4 | #13 `cmd: let graph rendering show a subset of a graph` | `hashLabel` and `complete` in `renderOptions`, and the subset goldens. |
| 5 | #14 `cmd: add atte cone` | The command with `--base`, `--head`, `--content-hash` and the other presentation flags. |
| 6 | #15 `cmd/cone: default to the working tree when it is dirty` | `--working-tree`, `--quiet`, the stderr line, revision selection, and `attegit.HasUncommittedChanges`. |
| 7 | #16 `cone: add Delta.Frontiers` | `Delta.Frontiers()` and `contenthash.Calculator.Dependencies()`. |

### Where the implementation differs from the first approved design

The sections above were updated to describe what was built. These are the points where it differs from what was first written.

Capture and Diff:

- `printGraph` resolves the scanner before building the graph, where it used to build the Git graph first. Only which error wins when both fail can differ. `Capture` keeps the old error prefixes.
- `Diff` returns an error naming `WithContentHashes` when a snapshot is nil or has no hashes. `Changed`, `Added` and `Removed` are never nil, and `Contains` is backed by an unexported set.
- The vocabulary (Snapshot, Cone, Candidate versus Impact) is in the `cone` package documentation and in `cone/README.md`, the first README in the repository. It is not in the GLOSSARY.

Rendering:

- An absent starting tree prints nothing only for a subset, that is when the renderer has been given `complete`. For a complete graph it is still the error `parent entity ... not found`, so `atte graph` in a directory that does not exist at `--ref` still fails. The first design made every graph print nothing.
- For `hasContainment` the renderer is given the complete graph next to the graph it prints, the first of the two options the design left open. `Contains` is not used for this.
- `hashLabel` replaces the abbreviated hash for every entity that shows one. The `go version` and external import lines still read tokens from `hashes`, which `atte cone` sets to the head calculator.

Command:

- `--base` and `--head` default to the empty string, meaning "not given", so the help describes the defaults in words. An explicit `--head` is compared as committed even when the tree is dirty, and only `--working-tree` overlays it. The stderr line reads `REV + working tree` for that case and `working tree` for `HEAD`.
- Git is asked about uncommitted changes only when the flags leave something for the answer to decide: not with `--working-tree`, and not when both `--base` and `--head` are given.
- The stderr line is written after the comparison is chosen and before either revision is opened, so a revision that does not resolve is preceded by the line that says what was attempted.
- Errors from `Capture` and `Diff` are prefixed `capture base`, `capture head` and `compare revisions`. Run targets are discovered on the head revision.
- The command opens both revisions with `attegit.Open` and ignores `ExecuteOptions.Repository`, which stands for a single revision. Its tests run the real command in a temporary Git repository through `t.Chdir`, so `ExecuteOptions` did not need to change.
- The help lists the limitations inherited from the content hash IDR, including `go.work` files, which are ignored.

Frontiers:

- `Frontiers` returns `([][]graph.EntityID, error)` instead of the bare slice, because `Dependencies` fails for an unknown entity. That cannot happen for a `Delta` built by `Diff`.
- An entity whose only dependency was removed has no changed dependency, so it is in layer 0. The removed entity cannot be listed, and its dependents are the nearest thing to the edit.
- `Dependencies` excludes the entity itself, as `Closure` does.
- The layers in the Frontiers table above are checked by `TestFrontiersOfTheWorkedExample`, which captures the worked example's repository before and after the edit. The other layering tests use hand-built worlds.

### Not done

- The design listed command-level goldens for an `atte.hcl` edit, a `go.mod` bump with `--external-imports`, a deleted file, and a mode-only change. No command test renders the first two or the last. The Cone for them follows from the unit tests over hand-built graphs, but nothing checks the rendered output. A deleted file appears only as a working-tree default-selection case, whose output is the changed parent tree.
- Nothing renders the Frontiers.

### Open from review

- `cone/frontiers.go` has its own eager strongly-connected-components function next to the lazy one in `contenthash`. PR 7's review asked why. The options are to share the eager one through a small helper, to ask the `Calculator` for component membership, or to rebuild `contenthash` on a shared iterator in its own change.
