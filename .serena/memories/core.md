---
name: core
description: Project map and durable invariants
metadata:
  type: project
---

- Go repository `github.com/ghthor/atte`; top-level modules: `graph`, `detector/attegit`, `detector/attego`, `cmd`.
- Tests include reusable Git fixture in `detector/attegittest`; Go graph tests use `newBasicFixture` in `detector/attego/module_test.go`.
- Formatting/lint/test completion conventions live in `mem:conventions`, `mem:suggested_commands`, and `mem:task_completion`.
- Toolchain details: `mem:tech_stack`.
