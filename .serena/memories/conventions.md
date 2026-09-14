# Code Conventions

- Prefer structural/LSP navigation before Go edits; use Serena for every repository edit.
- Go tests use shoenig/test behavioral assertions and must for setup failures.
- Table-driven expectations use test assertions, not must; setup operations use must.NoError.
- Multiline test strings are raw literals with strings.TrimLeft.
- Build slices with make and capacity when appending.
- Struct literals with more than two fields use one field per line.
- Keep detector APIs capability-based and preserve deterministic graph ordering.