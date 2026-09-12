# 2026-09-12: Separate HCL target declarations from local evaluation and graph assembly

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

We need to remove the support for globals that perform inheritance across
atte.hcl files.

We want to be able to quickly eval any atte.hcl for what targets it declares
without requiring any other type of evaluation.

We do still want to support evaluating locals within a single files evaluation
scope.

### Context (as needed)

The HCL detector currently reads every `atte.hcl` file when building repository-wide results. The former model evaluated `globals` from the repository root down the directory tree and exposed inherited values through `global.<name>`. A file's `locals` could then depend on that inherited scope, making one file depend on configuration outside its own directory.

The detector has four related consumers with different scopes: `ConfigFor` evaluates one directory, `DeclaredTargets` enumerates declarations, `Targets` returns fully decoded target configuration, and `Graph` assembles scripts, file dependencies, and target relationships. Target declarations should have stable identities after parsing their own file; repository-wide graph assembly can happen later.

The package already has useful pieces of this direction: registered target schemas and decoders, file-specific HCL function providers, symbolic literal target traversals, and single-file evaluation through `ConfigFor`. The remaining design work is to make the evaluation boundaries explicit and ensure graph construction, rather than target enumeration, owns repository-wide dependency resolution.

### Goals

1. remove `globals` support
1. remove `globals` eval phase
1. introduce a targets eval phase
1. introduce a locals eval phase (that depends on the targets phase)
1. introduce a graph eval phase (that depends on the locals phase)
1. introduce a `DeclaredTargets` API for target declarations that does not evaluate target bodies

### Non-Goals

* Preserve `globals` or provide a compatibility mode for inherited `global.<name>` values.
* Allow expressions in one `atte.hcl` file to read locals or declarations from an ancestor, sibling, or child file.
* Change the built-in target kinds, target registry extension point, selector syntax, or target identity rules.
* Change script execution, path resolution, or ordinary file and detector dependency semantics except where they currently depend on globals.
* Make target enumeration validate executable scripts or resolve the complete repository graph; those remain graph and execution concerns.


### Proposed Solution

Parse each `atte.hcl` independently and reject a `globals` block as an unsupported declaration. Replace inherited evaluation with four explicit phases:

1. **Target declarations:** Parse and normalize target blocks, assigning their kind, name, source index, stable identity, and source range without evaluating target bodies.
2. **Locals:** Resolve each file's `locals` using only same-file locals and that file's HCL functions.
3. **Target decoding:** Apply local values to target attributes, decode scripts and dependencies, and invoke custom target decoders.
4. **Graph:** Resolve repository paths and symbolic target traversals, then build entities and relationships.

Introduce `DeclaredTargets` as the public repository-wide API for phase one. It returns a deterministic, path-and-source-ordered list of `TargetDeclaration` values for every target declaration with a recognized kind and valid block shape, including targets whose bodies contain semantically invalid or unresolved expressions and targets without scripts. It does not invoke HCL functions, evaluate locals, validate target schemas, decode target bodies, or require graph assembly. A file that fails HCL parsing or declaration collection, including a duplicate-local declaration rejected at that boundary, still causes `DeclaredTargets` to return an error rather than returning partial declarations from that file. An internal file-scoped declaration helper supports `ConfigFor` and other single-file callers without parsing unrelated files.

Keep `Targets`, `ConfigFor`, and `Graph` as the richer configuration and graph APIs. `ConfigFor` evaluates only the requested file; `Targets` and `Graph` evaluate target bodies as needed. Literal target traversals remain symbolic until graph evaluation, while paths and detector entity references retain their existing dependency types.

## Detailed Design (as needed)

### Target declarations

Add a declaration-only API:

    type TargetDeclaration struct {
        ID     graph.EntityID
        Kind   Kind
        File   reference.Blob
        Name   string
        Index  int
        Source hcl.Range
    }

    func DeclaredTargets(ctx context.Context, repo *attegit.Repo) ([]TargetDeclaration, error)

`DeclaredTargets` parses all repository `atte.hcl` files and returns a deterministic list ordered by repository-relative file path and then source order within each file. The public API is repository-wide. An internal file-scoped declaration helper should support `ConfigFor` and other callers that must not parse unrelated files.

The declaration phase requires valid HCL syntax and a valid target block shape, but does not validate target attributes or evaluate their expressions. It identifies both short forms such as `test "unit" {}` and wrapper forms such as `target "test" "unit" {}`.

The declaration phase assigns anonymous targets a kind-local index and preserves an empty `Name`; named targets retain their label. IDs use the same namespace, file, kind, and display-name rules as fully decoded targets:

    EntityID(Namespace+":"+string(kind), file, displayName(name, index))

Selector aliases are not stored on `TargetDeclaration`; adapters derive them from the declaration's file, kind, name, and index using the existing selector logic. The phase rejects unknown target kinds, invalid label counts, duplicate labels, and numeric labels, because these errors affect target identity. It does not reject a missing `script`, an invalid or unresolved target-body `local` reference, an unknown target-body function, a missing script file, an unknown target-body attribute, or an invalid dependency expression.

A `globals` block remains an immediate file-level error because globals are no longer part of the language. More generally, malformed HCL and errors reported while collecting declarations, including duplicate locals rejected by the existing declaration parser, cause `DeclaredTargets` to return an error; declaration discovery must not silently return partial results for a file that failed parsing or declaration collection. Semantically unresolved expressions in otherwise valid target bodies do not fall into that category and must not prevent the declaration from being returned.

No HCL function provider is needed by `DeclaredTargets`. The API should check context cancellation while processing files and should not invoke providers for unrelated files or for any declaration body. It should not call `body.Content` or a registered target decoder.

### Local evaluation

The locals phase evaluates a file's local expressions with a context containing only `local` and the functions supplied for that file. It may retry pending expressions so locals can refer to other locals declared in the same file. Values needed by target decoding must be known; unresolved references, cycles, unknown functions, and duplicate names produce errors tied to the declaring file. A function provider is called for the file being evaluated and is never used to construct a parent or sibling scope.

The phase runs after declarations have been indexed, but it does not read inherited declarations. The ordering makes the phase graph explicit without making locals depend on another file's target values.

### Target decoding

Target attributes that use `local.<name>` are decoded after the locals phase. Inline scripts remain inline, while `path(...)` values retain their repository-relative path representation. Dependency lists continue to accept repository paths, detector entity IDs, and literal target traversals. Literal traversals are stored symbolically rather than evaluated as HCL variables during target decoding.

The existing `Targets` and `ConfigFor` APIs can continue to return rich `Target` values from this phase. They may report errors from locals or target decoders; callers that only need declaration identity should use `DeclaredTargets` instead. `Targets` should continue to invoke the configured function provider because target attributes may require provider functions. `ConfigFor` must invoke it only for the requested file.

### Graph assembly

The graph phase builds entities for targets and their source files, resolves scripts and ordinary file/entity dependencies using existing relative-path rules, and resolves symbolic target traversals against the declaration index. A bare traversal such as `codegen.generate` refers to a target of kind `codegen` named `generate` in the same `atte.hcl` file as the dependent target. Cross-file target dependencies use the existing explicit HCL entity-ID string form rather than an unqualified traversal. Missing same-file targets are graph-phase errors; target names that are duplicated across different files do not make same-file traversals ambiguous.

A target may be listed by `DeclaredTargets` without a script. Graph construction and execution enforce the script requirement only for target kinds whose execution contract is script-backed; custom target kinds retain control of their own decoded value and execution requirements. Errors retain source file and block range context, and graph evaluation honors context cancellation between files and blocks.

The detector-level `Targets` method should use `DeclaredTargets` when it is asked only to enumerate available target identities. It must not filter out declarations merely because they have no script or because their target body contains a semantic evaluation error.

## Cross cutting concerns (as needed)

* **Compatibility:** Removing `globals` is a configuration-language breaking change. The detector should fail at the declaration site rather than silently treating inherited values as empty or producing a later unknown-variable error. Stale documentation and fixtures using `global.*` should be removed or updated in this repository.
* **Determinism:** Target ordering, anonymous-target indices, duplicate detection, and graph entity IDs must not depend on Go map iteration. `DeclaredTargets` orders by file path and source order; indices are kind-local within each file. A declaration index must exist before symbolic dependencies are resolved.
* **Isolation and performance:** Single-file evaluation must not parse or invoke providers for unrelated files. Repository-wide graph construction may parse all files, but should parse each file once, evaluate each local scope once, and cache file-specific functions and locals where appropriate. `DeclaredTargets` must not invoke providers or target decoders.
* **Diagnostics:** Parsing, local evaluation, target decoding, and graph resolution errors should identify the `atte.hcl` path and source range. Missing-target references should be reported during graph assembly, not as opaque failures while enumerating another file. `DeclaredTargets` reports malformed HCL and declaration-collection errors, but does not report semantic errors from target attributes.
* **Security and execution:** This change does not execute scripts or broaden the HCL function set. Existing path normalization and repository-boundary checks remain in force; only graph construction turns decoded paths into graph entities. Script requirements are enforced only by script-backed target kinds; custom target kinds define their own execution contract.


## Alternatives considered (as needed)

* **Keep inherited globals and optimize the global pass.** This preserves the existing language but cannot make a file independently meaningful and still requires evaluating ancestor configuration before target discovery.
* **Keep globals only for `Graph`.** This would give different semantics to `Targets`, `ConfigFor`, and `Graph`, making selectors and target listings unstable while retaining the coupling in the language.
* **Flatten globals into each file before evaluation.** This hides the same repository-wide dependency behind preprocessing, complicates diagnostics, and prevents truly independent single-file evaluation.
* **Resolve target traversals while decoding each file.** This requires a repository-wide target index during target enumeration and conflates local HCL evaluation with graph assembly. Keeping traversals symbolic gives graph evaluation the right scope.


## Future plans (as needed)

* Add a dedicated graph-assembly API or phase result exposing unresolved and resolved symbolic target dependencies for diagnostics and tooling.
* Consider richer file-local helper functions or explicit imports if shared configuration is needed later; such a feature should define its dependency boundary rather than reintroduce implicit inheritance.


## Other reading (as needed)

* [HCL detector README](../detector/attehcl/README.md) — user-facing target and scope semantics.
* [HCL detector implementation](../detector/attehcl/attehcl.go) — parser, evaluator, target, and graph boundaries.
* [Target registry](../detector/attehcl/target_registry.go) — extensible target schemas and decoders that the phases must preserve.
* [HCL detector tests](../detector/attehcl/attehcl_test.go) — current behavior around globals rejection, local isolation, target identity, and graph output.
* [HCL specification redesign IDR](202608061806-attehcl-spec-redesign.md) — preceding decisions about the target-centric configuration model.


## Implementation (ephemeral)

Code review findings and clarified decisions from `detector/attehcl/`:

* The current source already rejects `globals` in `declarationBlocks`, stores only local expressions on `hclFile`, and covers the behavior in `TestGlobalsAreRejected`.
* `evaluateLocals` already implements same-file local dependency resolution, and `ConfigFor` constructs an evaluator for only the requested `atte.hcl` file.
* `normalizeBlocks` already performs most declaration-phase work: short and wrapper form normalization, registered-kind lookup, label validation, kind-local indices, duplicate named-target detection, and source-range preservation.
* The new declaration API is separate from rich target decoding. `DeclaredTargets` must list a declaration when its valid HCL body references an invalid or unresolved local, unknown function, missing script, unknown body attribute, or invalid graph dependency.
* Malformed HCL and errors reported by declaration collection are different from semantic errors in target attributes. `DeclaredTargets` returns an error for malformed HCL, unsupported `globals`, unknown kinds, invalid labels, duplicate names, and duplicate locals rejected by the existing parser; it must not return partial declarations from a file that failed at that boundary.
* `DeclaredTargets` is repository-wide and deterministically ordered by file path and source order. An internal file-scoped helper should support `ConfigFor` and other single-file callers. It must not call HCL function providers, `body.Content`, or registered target decoders.
* `TargetDeclaration` does not store selector aliases. Adapters derive aliases from its file, kind, name, and index using the existing selector logic, preserving current IDs and selectors.
* The current phase boundary is not yet as explicit as the goals describe: locals are evaluated by `targetsPhase.scopeFor`, and `Graph` calls target evaluation before `addEvaluatedTargetGraph` rather than consuming distinct locals and graph phase results.
* `Detector.Targets` currently calls rich `Targets` and filters out targets without scripts. It must use `DeclaredTargets` and include scriptless declarations.
* `addEvaluatedTargetGraph` currently skips symbolic traversals. The chosen resolution rule is that `kind.name` traversals resolve within the dependent target's file; cross-file target dependencies use explicit HCL entity-ID strings. Missing same-file targets are graph-phase errors.
* Script requirements apply only to script-backed target kinds. Custom target kinds retain control of their decoded values and execution requirements rather than being forced through the built-in `decodedTarget` contract.

Implementation checklist:

- [ ] Add `TargetDeclaration` and repository-wide `DeclaredTargets(ctx, repo)` with deterministic file/source ordering and stable IDs.
- [ ] Extract a file-scoped declaration helper so `ConfigFor` does not parse unrelated files.
- [ ] Ensure `DeclaredTargets` does not evaluate locals, invoke HCL function providers, call `body.Content`, invoke target decoders, validate target attributes, or require executable scripts.
- [ ] Preserve parser/declaration errors as whole-file errors, including malformed HCL, unsupported `globals`, invalid target block shape, unknown kinds, duplicate names, numeric names, and duplicate locals rejected during declaration collection.
- [ ] Return declarations despite semantically unresolved target-body locals, unknown target-body functions, unknown body attributes, missing scripts, and invalid dependency expressions when the HCL remains parseable.
- [ ] Make declaration ordering, kind-local indexing, ID construction, source ranges, and selector alias derivation explicit and deterministic.
- [ ] Extract or formalize a locals phase separate from target discovery while preserving file-local `local` references and function-provider behavior.
- [ ] Keep rich target decoding available through `Targets` and `ConfigFor` after locals are evaluated.
- [ ] Make graph assembly consume the declaration index and phase outputs, resolving `kind.name` traversals within the dependent file and preserving explicit cross-file entity-ID dependencies.
- [ ] Update the detector-level target listing to use `DeclaredTargets` and include scriptless declarations.
- [ ] Enforce missing-script errors only for script-backed target kinds; preserve custom target-kind execution contracts.
- [ ] Preserve target IDs, selector aliases, target registry extension, path handling, provider scoping, and context cancellation.
- [ ] Add tests for declaration ordering and metadata; scriptless declarations; invalid locals; unknown body functions and attributes; invalid dependency expressions; provider non-invocation; globals rejection; malformed HCL; duplicate locals; unknown kinds; invalid labels; numeric names; duplicate names; and selector compatibility.
- [ ] Add tests for rich `Targets`/`ConfigFor` evaluation, file-local provider calls, same-file symbolic target resolution, missing symbolic targets, explicit cross-file entity-ID dependencies, and custom target kinds without scripts.
- [ ] Remove stale inherited-globals documentation and fixtures, and document declaration-only target discovery in `detector/attehcl/README.md`.
- [ ] Run the repository verification commands after implementation.
