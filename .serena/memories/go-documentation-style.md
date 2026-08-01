---
name: go-documentation-style
description: Documentation style established during the Go API cleanup session
metadata:
  type: feedback
---
For this repository, Go documentation should improve API discoverability without mechanically commenting every exported declaration. Prefer concise group-level comments for related constants and types; explain non-obvious contracts, invariants, ownership, validation, error behavior, and domain semantics. Avoid repetitive comments that merely restate constant names or literal values, because they hurt readability. In particular, `detector/attego/module.go` uses one compact comment for the namespace/entity-kind group and a focused comment for `ImportsRelation`. The exported `detector/attegittest` package is intentionally a normal `.go` package (not `*_test.go`) because other packages import it for shared test setup.

**Why:** The documentation cleanup showed that blanket Go-doc coverage can make compact declarations harder to scan.

**How to apply:** When documenting future Go APIs, prioritize useful contracts and readable grouping over exhaustive per-symbol commentary. Run the repository's formatter and linter after changes.

Related: [[treefmt-workflow]]