# 2026-09-13: add go_test target configuration

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

We want to configure some of the behavior of targets generated in `@detector/attego/`. For example, a `go_test` target should allow its command to be customized:

```atte.hcl
go_test {
  command = "go test -race -count=1 {{.Package}}"
}
```

### Context (as needed)

The detector API centers on `detector.Builder` and its compiled, immutable `detector.Scanner`. A Builder attaches Sensors and their optional capabilities. `GraphSensor`, `TargetSensor`, and `EntityDecodingSensor` describe graph construction, runnable target discovery/presentation/execution, and entity-ID validation.

`detector.NewDefaultBuilder` attaches the built-in `attegit`, `attego`, and `attehcl` Sensors. Compilation sorts Sensors by namespace for graph and target aggregation. That ordering is deterministic but is not a dependency graph: `Scanner.Graph` and `Scanner.Targets` call Sensors independently, and there is no result handoff between Sensors. Current command paths likewise make separate graph/target queries.

Sensors can provide `HCLFunctions()` and `HCLBlocks()`; the Builder collects these into scanner-scoped capabilities, and the HCL Sensor is created at compile time with the immutable Scanner. `detector.AttachHCLBlock` can also attach a target kind directly. The HCL evaluator reads target-kind specs and HCL functions from these capabilities. A Go-owned `go_test` block can be contributed by `attego.Detector` before HCL evaluation. `attego` currently provides HCL functions, but does not yet provide an HCL block; its Sensor can contribute the Go-specific schema, decoder, and projections through `HCLBlocks()`.

Sensors can optionally implement `DecodeID`; `Scanner.DecodeID` dispatches to the appropriate Sensor by namespace. HCL graph construction uses the Scanner capability to decode dependency IDs.

`attehcl.Target.Decoded` remains public, but the Scanner does not expose a repository evaluation result or pass decoded values to other Sensors. The Go Sensor still discovers package-test targets and executes the fixed `go test -v` command; it has no access to the decoded HCL blocks.

### Goals

1. Define an explicit, deterministic detector execution order in which repository data is available first, HCL is evaluated next, and detectors such as attego can consume earlier detector results.
2. Make evaluated HCL target values available to later detectors without reparsing or independently reevaluating every atte.hcl file.
3. Support a built-in go_test HCL target configuration whose decoded value can configure generated Go package-test targets, including the example command template.
4. Make the result-passing mechanism usable by future detectors rather than hard-coding an attehcl-to-attego special case.
5. Preserve deterministic graph assembly, target identity and selector behavior, graph options, context cancellation, and the existing HCL target-kind attachment extension point.

### Non-Goals

* Do not reintroduce inherited HCL scope or allow arbitrary expressions in one atte.hcl file to read another file's locals.
* Do not make the Go detector execute tests while it is detecting or constructing the graph.
* Do not make every detector depend on every other detector or use lexical namespace ordering as an implicit dependency mechanism.
* Do not replace the existing graph and target APIs with an untyped global configuration map.
* Do not change ordinary repository path, entity-ID, target selector, or graph relationship semantics except where the new phase boundary requires an explicit input.

### Proposed Solution

Extend the existing `detector.Builder`/`detector.Scanner` lifecycle. Builder attachments should continue to collect sensor capabilities, while a repository scan should provide an explicit phase/result handoff: repository/Git input, HCL parsing and decoding, then Go target generation using decoded HCL configuration. The required order and dependencies should be explicit, not inferred from the Scanner's namespace-sorted aggregation. A scan should expose typed results and publish no partial result on error; missing dependencies and cycles should be reported if the phase model permits custom dependencies.

Contribute the Go-owned `go_test` target kind through the existing HCL block attachment API (`SensorProvidingHCLBlocks` or `AttachHCLBlock`) before `Compile` creates its immutable Scanner. Have the HCL evaluation produce a typed result containing decoded target values, including `attehcl.Target.Decoded`, and make that result available to the Go target-generation phase. Other Sensors should be able to consume declared prior results through the same mechanism.

Use the compiled Scanner's entity decoder capability for HCL dependency resolution. Keep direct package APIs where useful, but add a single-run API that shares HCL evaluation and its decoded result between target discovery and graph construction.

## Detailed Design (as needed)

### Existing public API and the required boundary

The current `detector.Scanner` boundary is not sufficient for dependent Sensors: its `Graph` and `Targets` methods accept a repository, and aggregate each Sensor's results independently. `graphset.Options` carries graph attachment, function-provider, and entity-decoder configuration, not prior Sensor results. Extend the Builder/Scanner model with a separate scan input/result concept instead of overloading `graphset.Option` with mutable state.

A conceptual result should retain at least:

* the detector namespace;
* the detector graph and discovered target identities, when supplied; and
* an immutable, typed payload that a dependent detector can request by namespace.

The payload should be exposed through a typed accessor or a namespaced result handle. A raw map[string]any would make dependency contracts implicit and move failures into unchecked type assertions. The scanner should prevent a Sensor from reading undeclared predecessors, if dependencies are declared per Sensor.

### HCL result and go_test

The HCL phase should evaluate all relevant files once and expose a repository-wide result. It can build on the existing evaluator phases and Targets API, preserving file-local locals, provider functions, scanner-scoped target-kind specifications, declaration identities, and Decoded values. The go_test target specification belongs to the Go detector because its decoded value and projections are Go-specific; `attego.Detector` can contribute its schema and decoder through `SensorProvidingHCLBlocks` (or `AttachHCLBlock`) before `Builder.Compile`. Its schema should include command and define the default behavior when the attribute is absent.

attego should consume the decoded go_test values while scanning packages. For each generated package-test target it should resolve the applicable HCL configuration, render {{.Package}} from the target's Go package identity, and produce the existing executable/graph representation. The default with no configuration should remain the current go test -v behavior unless the approved design deliberately changes it. The IDR must define whether the template is shell text or an argv template, what .Package contains (import path, package directory, or another stable value), and how quoting/escaping works.

### Builder and Scanner integration

Entity decoding is an optional Sensor capability, gathered into `detector.Builder` and dispatched by the compiled `detector.Scanner`. The HCL Sensor receives the Scanner as its capability provider. Preserve this per-Scanner behavior when adding cross-Sensor configuration handoff.

`SensorProvidingHCLBlocks` lets an attached Sensor contribute target-kind specifications, while `AttachHCLBlock` supports direct attachment. `Compile` freezes the block specifications and constructs the HCL Sensor with the compiled Scanner. Use this mechanism for `go_test`.

The remaining issue is runtime ordering and data flow: the compiled Scanner sorts Sensors by namespace for aggregate graph and target calls, but has no dependency schedule or typed result handoff. In particular, this order does not guarantee that HCL evaluation runs before Go target production with decoded values shared between them.

### Compatibility surface

The package APIs (`attehcl.Targets`, `attehcl.ConfigFor`, `attehcl.Graph`, `attego.Graph`, and `attego.Targets`) accept the relevant Scanner capabilities where needed. Retain them where practical or make their required inputs explicit, while command callers use the single-run API.

## Cross cutting concerns (as needed)

* Determinism: dependency validation, topological execution, detector result ordering, and graph merging must not depend on map iteration or incidental namespace order.
* Evaluation cost: the phase runner should share the HCL result between target discovery, graph construction, and Go detection rather than calling attehcl.Targets once per consumer.
* Failure behavior: HCL parse, decode, and provider errors must prevent dependent phases from running. A detector must not observe a partially published result.
* Cycles: dependency cycles should be rejected with a diagnostic that identifies the participating namespaces.
* Concurrency: detectors in the same independent phase may be parallelized only if result publication, graph merging, and target ordering remain deterministic. The initial implementation may execute topologically and serially.
* Security: command templates are user-controlled input. The design must specify shell versus argv execution, package-value escaping, and whether placeholders are allowed anywhere in the command. Command construction must preserve safe argv semantics and avoid injection-prone shell interpolation.
* Extensibility: typed phase outputs should support future Sensors without special-case coupling in `attehcl` or dispatch logic hard-coded into `detector.Scanner`.

## Alternatives considered (as needed)

* Have attego call attehcl.ConfigFor directly. This is simple for the first integration, but couples detectors, duplicates HCL evaluation, does not provide a reusable mechanism for future detectors, and makes package-level execution order implicit.
* Rely on namespace sorting alone for phase order. Scanner sorting is deterministic for aggregation, but it is not an execution dependency contract and cannot pass decoded values between Sensors.
* Put decoded HCL values in the merged graph. Configuration values are not graph entities and this would conflate graph topology with detector input, complicating typing and lifecycle.
* Use a process-global configuration cache. A cache could avoid duplicate evaluation but would make repository/ref/function-provider scope and invalidation implicit, and would not provide dependency validation.

## Future plans (as needed)

<!--
Things that may be added in the future
-->

## Other reading (as needed)

* [Detector interfaces](../detector/interfaces.go) — current `Sensor`, `GraphSensor`, `TargetSensor`, entity-decoding, and HCL capability contracts.
* [Detector Builder and Scanner](../detector/registry.go) — attachment, compilation, namespace-sorted aggregation, graph/target calls, and entity-decoder dispatch.
* [Built-in detector wiring](../detector/builtin.go) — `NewDefaultBuilder` setup for `attegit`, `attego`, and `attehcl`.
* [HCL target API](../detector/attehcl/api.go) — `Targets`, `ConfigFor`, `DeclaredTargets`, and decoded target values.
* [HCL target-kind capabilities](../detector/attehcl/target_registry.go) — target schemas, decoders, graph projections, execution projections, and config projections.
* [HCL graph assembly](../detector/attehcl/graph.go) — graph construction through the `attehcl.Capabilities` interface implemented by the compiled Scanner.
* [Go detector](../detector/attego/module.go) — package graph and package-test target generation.
* [Go HCL functions](../detector/attego/hcl_plugin.go) — Go-related HCL functions.
* [HCL detector README](../detector/attehcl/README.md) — parse, declaration, local, decode, and graph phase boundaries.
* [Separate HCL phases IDR](202609121644-separate-hcl-target-declarations-from-local-evaluation-and-graph-assembly.md) — prior decisions about HCL evaluation isolation and decoded target values.

## Implementation (ephemeral)

Research findings:

* `detector.NewDefaultBuilder` attaches `attegit`, `attego`, and `attehcl`; `Builder.Compile` snapshots capabilities into an immutable Scanner and constructs the HCL Sensor with that Scanner.
* `SensorProvidingHCLFunctions` and `SensorProvidingHCLBlocks` let Sensors contribute functions and target kinds. `attegit` and `attego` currently provide HCL functions; only `attehcl` provides built-in HCL target kinds. A Go `go_test` block can use the existing block-contribution capability, but this has not yet been added.
* `Scanner.Targets` and `Scanner.Graph` independently aggregate Sensors in namespace order. They do not share per-repository execution results, expose decoded HCL values to other Sensors, or express dependency ordering.
* `attehcl.Target.Decoded` is public. The evaluator consumes Scanner-provided target-kind and function capabilities, but no Scanner API returns and shares one evaluated HCL result for Go target generation.
* `attego.Detector` still generates package-test targets and its execution capability returns the fixed `go test -v` argv. It does not consume HCL target values.
* Entity decoding is an optional per-Sensor capability dispatched by `Scanner.DecodeID`; target-kind specifications are attached to the Builder and included in its compiled Scanner.
* Command operations use the Scanner API. `cmd/graph.go` separately calls `Scanner.Graph` and `Scanner.Targets`; `cmd/run.go` separately discovers targets and requests each target's execution. These are consumers to consider for sharing evaluated HCL results in a single scan.

Open decisions to resolve before implementation:

- the exact scan phase/result interfaces and whether existing `Scanner.Graph`/`Scanner.Targets` methods remain as adapters;
- whether dependencies are named namespaces, explicit phases, or both;
- the public shape and ownership of the evaluated HCL result;
- the exact `go_test` target specification and whether it is contributed by `attego.Detector.HCLBlocks()` or attached by built-in Builder wiring;
- the meaning and execution model of {{.Package}}, including quoting and escaping;
- how a package directory selects an applicable go_test block when multiple atte.hcl files or multiple blocks are present;
- whether the initial runner is serial and whether independent detector phases may later run concurrently;

Implementation should add tests for topological ordering, missing and cyclic dependencies if dependencies are configurable, result isolation and reuse, HCL-before-Go execution, `go_test` block attachment and decoding, command rendering, default command compatibility, malformed configuration, and preservation of per-Scanner entity decoder dispatch and errors.
