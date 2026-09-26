# Project Map

- Go module: `github.com/ghthor/atte`; command entrypoints and CLI implementation under `cmd/`.
- Runtime detector composition is in `detector/registry.go` (`Builder`, `SensorSpec`, immutable `Scanner`); capability interfaces are in `detector/interfaces.go` and summarized in `detector/GLOSSARY.md`.
- Built-in Sensors: `detector/attegit` repository model, `detector/attego` Go modules/packages, `detector/attehcl` HCL targets. Shared graph and target values are in `detector/graph`, `graphset`, and `graphtarget`.
- A target Sensor couples discovery, selector presentation, and execution. Scanner owns selector rendering, matching, resolution, and execution dispatch for its attached Sensor snapshot. `reference/selector` contains shared values and pure syntax/path helpers only; there is no global selector registry.
- CLI consumers are in `cmd/`; HCL target kind contracts and detector vocabulary are documented in `detector/GLOSSARY.md`.
- Repository-specific workflow and editing rules: `AGENTS.md`. Required commands: `mem:suggested_commands` and `mem:task_completion`.