# cone

The Broad Phase of Atte: given two revisions of a repository, find the Cone of
the change between them.

```go
base, _ := cone.Capture(ctx, scanner, baseRepo, cone.WithContentHashes())
head, _ := cone.Capture(ctx, scanner, headRepo, cone.WithContentHashes())

delta, _ := cone.Diff(base, head)
delta.Changed // the Cone: every head entity that is new or whose content hash differs
delta.Graph   // the head graph pruned to the Cone
```

## Vocabulary

Terms follow [GLOSSARY.md](../GLOSSARY.md).

| Term | In this package |
| --- | --- |
| Snapshot | One revision: its complete entity graph and, when captured `WithContentHashes`, the content hash of every entity. |
| Cone | `Delta.Changed`: the head entities that base does not have or whose content hash differs from base. |
| Candidate | An entity in the Cone. Potentially affected, not verified. |
| Impact | A Candidate that refinement confirmed. This package never produces one; refinement is Narrow Phase work. |

## How the Cone is found

A content hash covers an entity and everything it depends on. An entity's hash
therefore differs between two revisions exactly when something in its
dependency closure was added, removed or edited, and comparing every head
entity's hash against base finds all the Candidates in one pass. Nothing walks
outward from the edited files, so a deleted file or a new import is found the
same way as an edit.

`Delta.Graph` is the subgraph of the head graph induced by the Cone: the
Candidates and the relationships between them. A tree that contains a Candidate is itself a
Candidate, so the pruned graph stays connected from the repository root.

## What the Cone gets wrong

- **False positives are by design.** A hash sees bytes, not meaning: a comment
  edit, an uncalled function and a build constraint all enter the Cone. The
  trade buys speed, and removing them is Narrow Phase work.
- **False negatives are defects.** They come from inputs the graph does not
  model, such as a target's `script`, embedded files, testdata, file modes and
  the Go toolchain. The fix is to model the input.

## Removed entities

`Delta.Removed` lists base entities that head lacks. They are not in the head
graph, so they are not in `Delta.Graph`; their containers and dependents are
Candidates because their closure lost an entry.

The design is recorded in
[the atte cone IDR](../idr/202611022317-atte-cone-entities-changed-between-two-revisions.md).
