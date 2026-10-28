# 2026-10-28: Type-safe decoded values for HCL setting blocks and target kinds

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

`hclblock` extension points move a block-owned decoded value through `any`: decoders return `any`, projections and executors receive it as `any`, and consumers read `Target.Decoded` / `SettingValue.Decoded` and type-assert by hand. Every extension author repeats the assertion and its "invalid decoded value %T" error. A mismatch is only found at runtime.

### Context

Applies to `hclblock.TargetKindSpec` / `Target` (`TargetDecoder`, `TargetGraphProjection`, `TargetExecutionProjection`, `TargetConfigProjection`, `Target.Decoded`) and `hclblock.SettingBlock` / `SettingValue` (`SettingDecoder`, `SettingProjection`, `SettingValue.Decoded`). Current assertion sites: `examples/attehcl-custom-block/main.go`, `detector/attehcl/graph.go`, `detector/attego/setting.go`.

The Scanner stores specs for many different decoded types in one map, so `TargetKindSpec` and `SettingBlock` cannot carry a type parameter.

### Goals

- Authors of target kinds and setting blocks write decoders, projections, and executors against their own concrete type, with no `any` and no hand-written assertion.
- Consumers read decoded values through a checked, typed accessor.
- Confine the type-erased `any` to `hclblock` internals.

### Non-Goals

- Changing the projection result type (`map[string]any`).
- Changing HCL schemas, evaluation order, or `config show` output.
- Making `Builder`, `Scanner`, or the spec map types generic.

### Proposed Solution

Add generic constructors that return the existing erased specs:

```go
func NewSettingBlock[T any](
    schema *hcl.BodySchema,
    decode func(*hcl.BodyContent, *hcl.EvalContext) (T, error),
    project func(T) (map[string]any, error), // optional
) SettingBlock

func NewTargetKindSpec[T any](
    schema *hcl.BodySchema,
    decode func(*hcl.BodyContent, *hcl.EvalContext) (T, error),
    opts TargetKindOptions[T], // Graph, Execution, Config; each optional
) TargetKindSpec

func DecodedAs[T any](SettingValue) (T, error)
func TargetDecodedAs[T any](Target) (T, error)
```

The constructors wrap the typed functions with a single `.(T)` assertion. `SettingBlock` / `TargetKindSpec` fields become unexported, so the erased form can only be built through the constructors. `Decoded` fields become unexported behind the accessors. `DecodedAs` errors name the expected and actual types.

Typed projection callbacks receive the target and its typed value, so executors and graph projections do not assert at all.

Migrate the example, `attehcl`, and `attego` in the same change.

## Alternatives considered

- Leave `any` and document the assertion. Rejected: the boilerplate and runtime-only failures remain.
- Generic `SettingBlock[T]` / `TargetKindSpec[T]`. Rejected: heterogeneous storage in one map forces erasure back to an interface anyway.

## Implementation (ephemeral)

- [x] `hclblock`: `NewSettingBlock[T]`, `NewTargetKindSpec[T]`, `TargetKindOptions[T]`, `DecodedAs[T]`, `TargetDecodedAs[T]`. Spec, block, and `Decoded` fields are unexported.
- [x] Migrated `detector/attego/setting.go`, `detector/attehcl` (graph, evaluator, target registry, tests), `detector/scanner.go` and its tests, `cmd` tests, `examples/attehcl-custom-block`. `examples/attehcl-go-test-override` needed no change.
- [x] Updated `detector/attehcl/README.md` and `detector/GLOSSARY.md`.
- [x] Unit tests in `detector/attehcl/hclblock/hclblock_test.go`: type mismatch error from `DecodedAs` / `TargetDecodedAs`, nil optional projections, typed projection arguments, nil decoder results, schema copying.
- [x] Verification: `test.build`, `codegen.fmt`, `codegen.rendered`, `lint.go` pass. `test.go` / `codegen.go` fail only on two `cmd` tests that also fail without this change (`TestRepositoryContext`, `TestExecuteWithOptionsUsesInjectedDetectorWithoutRepositoryOverride`: macOS `/var` vs `/private/var` temp dirs).

Details beyond the proposal:

- Because the erased fields are unexported, `attehcl` and `detector` go through new methods: `Schema()` (returns a copy), `WithSchema`, `HasDecoder`, `SettingBlock.Decode(name, content, ctx)`, `TargetKindSpec.Decode(kind, content, ctx)`, and `Target.ResolveScript(repo)`. `Decode` returns a `SettingValue` / partly-built `Target` that already carries its projections, so `TargetBind` and `SettingBind` are removed; the caller fills identity, `File`, and `Source`.
- The nil / nil-pointer decoder-result check moved from `attehcl` into `hclblock`'s `Decode`, with the same messages.
- Schemas are copied in the constructors and `WithSchema`, and `Schema()` returns a copy, so `copyBodySchema` was removed from `detector` and `attehcl`, and the Builder and Scanner clone their maps with `maps.Clone`.
- Only `TargetGraphProjection` stays as an exported erased function type, because `Target.GraphProjection()` returns it. `TargetDecoder`, `TargetExecutionProjection`, `TargetConfigProjection`, `TargetScriptProjection`, `SettingDecoder`, and `SettingProjection` are gone; typed callbacks are plain function types in `TargetKindOptions[T]` and the constructor parameters.
- Execution and config callbacks for built-in script targets take an unused `decodedTarget` argument now that the signature carries the typed value.
