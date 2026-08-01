---
name: conventions
description: Stable coding and test conventions
metadata:
  type: project
---

- Go tests must use `github.com/shoenig/test/must` for assertions and setup failures; avoid ad hoc test failure calls.
- Repeated multi-step test setup should be moved into small fixture types with convenience methods while retaining low-level script helpers.
- Multiline test data uses raw strings with leading whitespace removed via `strings.TrimLeft`.
- Prefer `make` with capacity when constructing append-only slices.
- Use Serena structural tools before editing Go; use Serena editing tools rather than direct file edits.
