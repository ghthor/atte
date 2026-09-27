# 2026-09-26: make target selector capabilities Scanner-scoped

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

Target selectors are currently split across independently managed pieces. Target discovery belongs to a compiled `detector.Scanner`, but rendering and matching are registered in the process-global `reference/selector` mapping. Built-in Sensors install those mappings from package `init` functions, outside the lifetime and configuration of any Builder or Scanner. The run command then combines those global mappings with namespace-specific execution logic.

This split makes the compiled Scanner an incomplete description of the Sensors attached to it. A process cannot safely compile two Scanners that attach different selector behavior to the same namespace, and custom Sensors cannot make their targets fully selectable and executable without participating in global registration and command-specific special cases.

### Context

`reference/selector` has accumulated both pure selector utilities and repository- or Sensor-aware behavior. The pure layer includes selector values, parsing, path resolution, aliases, and generic matching. The mapping layer associates a namespace with renderer and matcher callbacks in a global map protected by a mutex. `detector/attehcl` and `detector/attego` register those callbacks from `init`.

The detector Builder already snapshots Sensor capabilities into an immutable compiled Scanner. Target discovery is therefore naturally Scanner-scoped, while selector behavior is not. `cmd/graph.go` and `cmd/run.go` consume the global selector mapping; `cmd/run.go` additionally reconstructs target data and switches on built-in namespaces to decide how to execute a target. That command-specific logic prevents a custom target Sensor from providing a complete run target through the detector contract.

The selector package cannot import `detector.Scanner`: detector imports the built-in Sensors, and those Sensors import `reference/selector`, so the reverse dependency would form an import cycle. The shared selector package must remain below the detector layer.

### Goals

- Make the compiled Scanner the source of truth for discovering, presenting, matching, resolving, and executing targets supplied by its attached Sensors.
- Make target discovery, selector presentation, and execution one complete target capability. Every discovered target must be renderable and executable through the Scanner.
- Allow separately compiled Scanners in one process to use different implementations for a shared namespace without shared mutable selector state.
- Keep the selector package limited to common selector values and pure syntax, path, alias, and matching helpers.
- Preserve built-in HCL and Go canonical selector forms, aliases, path semantics, and command-facing selection behavior.
- Allow custom target Sensors to participate in `atte run` and graph selector rendering without built-in namespace switches or package initialization registration.

### Non-Goals

- Redesigning selector syntax, canonical selector strings, or the existing alias/path matching semantics.
- Moving command policy such as current-directory preference or HCL label fallback into generic Scanner resolution.
- Removing `graphtarget.ID.Aliases` as part of the initial ownership change; it can be reconsidered separately once all callers use Sensor-owned selector values.
- Adding arbitrary custom selector grammars or independent Sensor matcher/renderer callbacks beyond the shared `selector.Target` model.
- Adding a Scanner API for listing rendered selectors when callers can discover IDs and render them individually.

### Proposed Solution

Move all behavior that depends on an attached Sensor or a repository onto `detector.Scanner`. The Scanner dispatches target operations by `graphtarget.ID.Namespace`; it does not rely on process-global registration. The selector package remains a shared dependency of Sensors and detector internals, and does not import detector or repository packages.

A method-based target Sensor implements one complete capability:

```go
type TargetSensor interface {
    Sensor
    Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    TargetSelector(graphtarget.ID) selector.Target
    ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
}
```

Function-based `SensorSpec` values provide the equivalent `Targets`, `TargetSelector`, and `ExecuteTarget` functions together or omit all three. A partially configured target capability is rejected with an error naming the namespace and missing operation. Sensors that only provide graph or entity-decoding capabilities remain valid. `AttachSensor` adapts a method-based Sensor as a target Sensor only when it implements the complete interface.

The compiled Scanner exposes the corresponding operations:

- `TargetSelector(id) (selector.Target, bool)` dispatches to the Sensor for the target namespace. It reports false when the namespace is missing or the attached Sensor lacks the complete target capability.
- `TargetString(id) (string, bool)` renders the Sensor-owned value's canonical selector and reports false when selector presentation is unavailable.
- `TargetMatches(id, input, relative) bool` applies generic `selector.Target.Matches` semantics to the Sensor-owned presentation and returns false when selector presentation is unavailable.
- `ResolveTarget(ctx, repo, input, relative) (graphtarget.ID, error)` discovers targets through the same Scanner, matches them, and returns exactly one target ID.
- `ExecuteTarget(ctx, repo, root, id) (graphtarget.Execution, error)` delegates command construction to the Sensor that discovered the target.

`ResolveTarget` returns distinguishable no-match and ambiguity errors. Ambiguity candidates include target IDs and canonical selectors and are sorted deterministically by selector, then target ID. Discovery errors retain their cause when wrapped with context. Resolution observes context cancellation before discovery and while examining candidates. A discovered target without a selector capability is an explicit error rather than a silently skipped candidate.

The selector package retains `selector.Target` and its generic rendering and matching operations, parser and path helpers, aliases, and Sensor-independent no-match/ambiguity error types. Rename the path-relative pure `Resolve` operation to an explicit name such as `ResolveRelative` so it cannot be confused with Scanner target discovery. Remove the global mapping type, callbacks, mutex, registration function, and package-level mapping-based render/match operations. Do not add a Scanner-shaped interface to `reference/selector` to work around the import-cycle constraint.

Built-in Sensors produce their selector presentation directly from their native target identity. The HCL Sensor continues to supply the same behavior when constructed during Scanner compilation. The Go Sensor preserves its package-test canonical selector and aliases. Every ID returned by a target-capable Sensor must be executable through that Sensor's `ExecuteTarget` capability. `cmd/run.go` must not impose HCL-only runnable filtering or a hard-coded namespace allow-list; each Sensor owns the set of IDs it publishes, and the command dispatches execution through the Scanner. This replaces command-layer namespace-specific filtering and the `attehcl`/`attego` execution switch.

Command callers pass the Scanner to target rendering and matching. `cmd/run.go` retains its own current-directory preference and HCL label fallback because these are run-command selection policies, not universal selector semantics. It keeps the original target ID and presentation/execution data rather than rebuilding identity from split selector strings. Shell completion continues to use pure parsing after selectors have been rendered.

Keep aliases on `graphtarget.ID` during this migration to limit scope. The Sensor-owned `TargetSelector` becomes the behavior source; whether aliases remain neutral target metadata or move entirely to presentation data is a separate follow-up decision.

## Cross cutting concerns

### Dependency direction

`detector` may import `reference/selector` to use the common `Target` value and pure helpers. `reference/selector` must not import `detector`, `detector/attegit`, or concrete Sensors. Repository discovery and Scanner dispatch belong to the detector layer, which avoids the existing Sensor import cycle.

### Capability completeness

Target discovery must not be attachable without presentation and execution. This prevents the Scanner from publishing IDs that the CLI cannot render or run. The same all-or-nothing rule applies to method-based Sensor adaptation and function-based SensorSpec registration.

### Compatibility and policy boundaries

Existing HCL and Go canonical selector strings, aliases, path matching, and target IDs remain the compatibility baseline. Scanner resolution is generic and applies the same target matching contract to all attached Sensors. The run command may still apply its local-target preference and HCL label fallback around matching; those policies must not be silently incorporated into generic resolution. If a policy is later shown to be universal, it should be promoted explicitly with tests or resolver options.

### Determinism and errors

Target discovery follows the Scanner's deterministic Sensor ordering. Ambiguity candidate ordering is stable even when two targets render to the same selector. Shared error types carry resolution results without introducing repository dependencies in the selector package. Wrapped discovery failures preserve `errors.As` behavior for those types.

### Identity metadata

`graphtarget.ID.Aliases` remains transitional selector-facing metadata. Keeping it during the initial refactor avoids mixing a target identity model change with removal of the global registry. A later cleanup should first ensure every presentation consumer obtains aliases from `TargetSelector`.

`graphtarget.ID.Label` also carries the optional HCL block label needed by `cmd/run`'s label-fallback policy. It is kept separate from aliases so generic `selector.Target.Matches` and Scanner resolution do not treat that command-specific fallback as a normal selector alias. Revisit whether this belongs in a future presentation value when identity metadata is next cleaned up.

## Alternatives considered

### Keep the process-global mapping and pass the Scanner only for discovery

Rejected. Discovery and selector behavior would still have different ownership and lifetime. A second Scanner using the same namespace could not safely provide different mapping behavior, and custom Sensors would continue to require global initialization.

### Import `detector.Scanner` from `reference/selector`

Rejected. Built-in Sensors import `reference/selector` and detector imports those Sensors, so this reverses the dependency and introduces an import cycle. It would also put repository discovery in a generic selector package.

### Put a Scanner-like interface in `reference/selector`

Rejected. An interface that mirrors Scanner operations would preserve the cycle-avoidance mechanically but leave repository-aware resolution owned by the wrong package and create a duplicate public abstraction. The detector layer already owns the compiled Scanner and should own its methods.

### Keep separate renderer and matcher callbacks on each Sensor

Rejected. The existing built-ins already fit the shared `selector.Target` value, including path, kind, name, index, and aliases. A single structured presentation lets generic matching stay shared and eliminates another pair of Sensor-specific callbacks. Arbitrary selector grammars are outside this refactor.

### Treat target discovery, selector presentation, and execution as independent capabilities

Rejected. An attached Sensor could then publish a target that cannot be rendered or run, or expose only one side of the contract through method adaptation. These operations jointly define an executable target for Scanner consumers and are therefore one all-or-nothing capability.

### Move run-command preference and HCL label fallback into Scanner resolution

Rejected for the initial API. The Scanner resolver should provide generic, deterministic target matching. Current-directory preference and label fallback are command policies and must remain in `cmd/run.go` unless they are proven to be common selector semantics.

### Preserve built-in namespace switches in the run command

Rejected. Namespace switches duplicate Sensor knowledge in command code and make custom Sensors non-executable. Execution data and command construction belong to the target-capable Sensor, dispatched by the Scanner.

## Future plans

- Revisit `graphtarget.ID.Aliases` once all presentation callers use `TargetSelector`; remove it if it is no longer neutral identity metadata.
- Consider additional presentation capabilities only if a concrete Sensor cannot represent its selector with `selector.Target`; arbitrary syntax is not part of this decision.
- Add higher-level selector listing or resolver policy options only when multiple callers need identical behavior.

## Implementation constraints and verification plan

### Migration sequence

1. Document in `detector/interfaces.go` and `detector/GLOSSARY.md` that target discovery, selector presentation, and execution are one Sensor capability.
2. Add the complete capability and registration validation with tests. If needed to keep intermediate changes compiling, retain the old mapping calls temporarily; do not treat them as the final ownership model.
3. Add Scanner dispatch, rendering, matching, generic resolution, and execution. Resolution must use the same Scanner for discovery and presentation.
4. Migrate `attehcl` and `attego`, including the special HCL Sensor created during `Builder.Compile`, then migrate command callers.
5. Keep run-specific current-directory preference and HCL label fallback in `cmd/run.go`. Do not call `ResolveTarget` from `run` unless its candidate universe and policy are compatible. Preserve the original `graphtarget.ID` and its presentation/execution data through matching; do not reconstruct identity from selector strings. `cmd/runcomp` continues to use pure `Parse` after selectors have been rendered.
6. Replace mapping tests with Scanner tests, remove all direct global `selector.String`/`selector.Matches` and `selector.Register` calls, then delete the mapping types, callbacks, map, mutex, and global operations. Do not add a Scanner interface to `reference/selector`.
7. Keep `graphtarget.ID.Aliases` during this migration unless a separately scoped cleanup is justified. Update package comments and any notes that describe selector registration as independent of Sensor attachment.

### Required behavioral tests

- **Registry and Scanner:** reject a `SensorSpec` that supplies only part of the target capability with an error naming the namespace and missing function; attach non-target Sensors without target functions; adapt method-based Sensors only when they implement the complete target interface; verify capability snapshots after Builder mutation, namespace dispatch, missing namespace/capability behavior, and fail-closed rendering/matching.
- **Resolution:** cover distinguishable no-match and ambiguity errors; include candidate IDs and canonical selectors; verify deterministic ordering, including the target-ID tie-break when selectors are equal; preserve discovery error causes and context; check cancellation before discovery and while filtering; report a discovered target without selector capability clearly.
- **Scanner isolation:** prove two separately compiled Scanners can use different selector implementations for the same namespace without global-state collision. Keep Scanner dispatch/resolution tests in detector tests, not behind a fake Scanner interface in `reference/selector`.
- **Pure selector helpers:** retain parser, path, and alias table coverage. Verify `Target.Matches` for relative and root-qualified paths, HCL containing-directory behavior, aliases, and pathless targets.
- **Built-ins and commands:** assert HCL and Go canonical selectors and aliases remain compatible; retain run matching, ambiguity, local-preference, completion, and graph rendering coverage; add a custom executable Sensor end-to-end test showing `atte run` and graph selector rendering use Scanner capabilities without package-init registration. Also verify that every ID published by a target-capable Sensor has the execution data required by `atte run`.

Run race tests if the repository's standard test target supports them. Before considering the refactor complete, run:

```sh
nix develop --command atte run test.build
nix develop --command atte run test.go
nix develop --command atte run codegen.go
nix develop --command atte run codegen.fmt
nix develop --command atte run codegen.rendered
nix develop --command atte run lint.go
```

### Edge-case contract

Methods on a real compiled Scanner should not require callers to handle a nil Scanner. If nil must be tolerated at a call site, fail closed or return a clear error according to that operation's return type. Pure selector helpers remain deterministic and independent of repository discovery. No Scanner-level listing API is required initially; callers may discover IDs and render them individually.

## Other reading

- `reference/selector/selector.go` — shared selector values, parser, path operations, aliases, and generic matching.
- The historical process-global selector mapping and its tests were removed by this refactor.
- `detector/interfaces.go` — Sensor capability interfaces.
- `detector/registry.go` — SensorSpec, Builder adaptation, compiled Scanner dispatch, target resolution, and execution.
- `detector/attehcl/detector.go` — built-in HCL target presentation and execution.
- `detector/attego/module.go` — built-in Go target presentation and execution.
- `cmd/run.go` — run-specific matching policy and Scanner-backed target handling.
- `cmd/graph.go` — graph selector rendering.
- `detector/GLOSSARY.md` — detector capability terminology.
- `idr/202608052235-formalize-the-selector-spec.md` — earlier selector grammar and behavior record, whose mapping-specific historical design is superseded by this Scanner ownership decision.

## Implementation and verification

The refactor is implemented in the working tree:

- `TargetSensor` and `SensorSpec` couple target discovery, `selector.Target` presentation, and execution; Builder validation rejects partial function-based capabilities, and method-based Sensors are adapted only when the complete interface is implemented.
- The compiled Scanner dispatches selector presentation, canonical rendering, matching, resolution, and command construction through the attached Sensor snapshot. Resolution preserves discovery error causes, reports deterministic ambiguity candidates, and observes cancellation.
- HCL and Go Sensors provide selector values and executable commands without package-init registration. HCL publishes only runnable targets; its block label is preserved separately for the run command's existing fallback policy.
- `cmd/run.go` keeps local-target preference and label fallback while dispatching matching and execution through the Scanner. `cmd/graph.go` renders built-in and custom run targets through Scanner capabilities.
- The global selector mapping implementation and its empty source/test stubs have been removed. Scanner isolation, built-in selector compatibility, command matching, and custom Sensor end-to-end behavior are covered by tests.

All required verification commands passed:

- `nix develop --command atte run test.build`
- `nix develop --command atte run test.go`
- `nix develop --command atte run codegen.go`
- `nix develop --command atte run codegen.fmt`
- `nix develop --command atte run codegen.rendered`
- `nix develop --command atte run lint.go`

`git diff --check` also passed. The standard `test.go` target runs `go test ./...` and the example package tests without `-race`; no race-specific standard target is declared in `atte.hcl`, so race tests were not run.
