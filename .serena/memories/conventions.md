# Code Conventions

- Read `AGENTS.md` for binding repository rules. Use Serena LSP for Go symbols/references before edits and Serena editing tools for all repository modifications.
- Go tests use `github.com/shoenig/test`; use `must` for setup failures, not table-case expectations. Do not use ad hoc test assertions (`t.Fatal`, `t.Error`, `if` checks).
- Multiline test strings are raw literals starting on the next line and trimmed with `strings.TrimLeft`.
- Prefer `make([]T, 0, n)` when accumulating slices; format structs with more than two fields on separate lines.
- Detector composition is capability-based: Builders are mutable setup; compiled Scanners are immutable runtime snapshots. Target Sensors provide discovery, `selector.Target` presentation, and `graphtarget.Execution` as one all-or-nothing capability.
- Selector behavior is Scanner-local; do not reintroduce process-global registration in `reference/selector`. Keep that package limited to shared values and pure syntax/path helpers.