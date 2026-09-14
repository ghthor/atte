# Project Map

- Go module: github.com/ghthor/atte; detector packages under detector/.
- Detector capability interfaces and graph model: detector/interfaces.go, detector/graph/.
- Runtime capability registration/aggregation: detector/registry/. Built-in wiring: detector/registry/builtin.go.
- Built-in detectors: detector/attegit (repository tree), detector/attego (Go modules), detector/attehcl (HCL targets).
- Shared graph options: detector/graphset/. CLI consumers: cmd/.
- Repository instructions: AGENTS.md; changes must use Serena editing tools.
- Build/task commands are documented in mem:suggested_commands; completion gate in mem:task_completion.