// Package cone is the Broad Phase of Atte: it finds the Cone of a change by
// comparing two revisions of a repository.
//
// A [Snapshot] is one revision, captured by [Capture] as a complete entity
// graph and the content hash of every entity in it. [Diff] subtracts the base
// Snapshot's hashes from the head's and returns a [Delta].
//
// # Vocabulary
//
// The terms follow GLOSSARY.md; this is what they mean here.
//
//   - The Cone is the set of head entities whose content hash differs from
//     base, or which base does not have. It is [Delta].Changed.
//   - A Candidate is an entity in the Cone. Candidates are potentially
//     affected and have not been verified.
//   - An Impact is a Candidate that refinement confirmed. This package never
//     produces Impacts: refinement is Narrow Phase work, and the Cone is
//     deliberately an over-approximation that the Narrow Phase can consume.
//
// # How the Cone is found
//
// The Cone is not found by walking outward from the edited files. A content
// hash covers an entity and everything it depends on, so an entity's hash
// differs between two revisions exactly when something in its dependency
// closure was added, removed or edited. Comparing hashes therefore finds every
// Candidate in one pass, including those reached by an import that was added
// or a file that was deleted, and agrees with atte graph --content-hash.
//
// # Conservatism
//
// False positives are by design. A hash sees bytes, not meaning, so a comment
// edit, an uncalled function and a build constraint all enter the Cone.
// Removing them is Narrow Phase work.
//
// False negatives are defects. They come from inputs the graph does not model,
// such as a target's script, embedded files, testdata, file modes and the Go
// toolchain; the fix is to model the input.
package cone
