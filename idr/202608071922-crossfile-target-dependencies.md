# 2026-08-07: crossfile target dependencies

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

We want to be able to add a dependency to a target that is delcared in a
different `atte.hcl`, example.

```hcl
# src/py/atte.hcl
test {
}

# src/go/atte.hcl
test {
  depends_on = [
    target("//src/py", "test.0"),
  ]
}
```

We will use the `target` function to specify a dependency on another target
declared in a different `atte.hcl` file.

This will enable relative path references and will support paths with
complicated representations.

```hcl
# src/path with.space/atte.hcl
test {
}

# atte.hcl
test {
  depends_on = [
    target("//src/path with.space", "test.0"),
  ]
}

# src/go/atte.hcl
test {
  depends_on = [
    target("../path with.space", "test.0"),
  ]
}
```

`locals` usage should also be valid

```hcl
# src/py/atte.hcl
test {
}

# src/go/atte.hcl
locals {
  py_test = target("//src/py", "test.0")
}

test {
  depends_on = [
    local.py_test,
  ]
}
```

### Context (as needed)

The target-centric HCL evaluator currently evaluates each atte.hcl file in isolation. It preserves literal target traversals for a later graph phase, but those traversals are not resolved into graph entities, so a dependency on a target in another file cannot currently be represented.

This IDR is narrower than repository-wide HCL evaluation. The target function must not evaluate the referenced file or inspect its declarations: doing so would create evaluation cycles between files. Instead, the function performs only enough validation to establish that the referenced repository directory contains an atte.hcl file, then constructs a graph.EntityID for the target address supplied by the caller. All target existence and identity validation is deferred until every atte.hcl file has been evaluated and the complete target set is available.

Ordinary declarations remain file-local: a target cannot read another file's locals or use cross-file HCL values. The deferred validation phase resolves the prospective entity IDs against the complete set of evaluated targets and turns invalid references into diagnostics before the graph is finalized.

The existing target identity contains the declaring atte.hcl path, target kind, and a display identifier. Anonymous targets use their kind-local index; named targets use their label. The new reference syntax needs to construct that identity from a path plus a target identifier without changing CLI selector behavior.

### Goals

- Add a target(path, identifier) HCL function for target dependencies.
- Resolve repository-rooted paths beginning with // and paths relative to the declaring atte.hcl file's directory.
- Verify during the function call that the referenced directory contains an atte.hcl file, without evaluating that file or checking its target set.
- Return a graph.EntityID identifying the requested target address so the dependency can be carried through HCL evaluation without recursion.
- Validate all returned target references after every repository atte.hcl file has been evaluated.
- Produce source-aware errors for invalid paths, missing files, malformed identifiers, missing targets, and references to unknown target kinds.
- Preserve deterministic target ordering and existing local-file evaluation semantics.

### Non-Goals

- General cross-file HCL expression evaluation, cross-file locals, or a global namespace.
- References to arbitrary HCL declarations that are not registered targets.
- Changing CLI selectors, target aliases, or atte run resolution syntax.
- Inferring dependencies from scripts, imports, or target execution behavior.
- Automatically adding transitive dependencies; each declared reference is one explicit graph edge.
- Defining a new target kind or changing target naming and anonymous-index rules.

### Proposed Solution

Add a target(path, identifier) HCL function to the evaluation context for each source file. The function resolves its first argument relative to the source file (or from the repository root for // paths), appends atte.hcl, and checks that the resulting repository object exists and is an HCL file. It does not evaluate the referenced file, enumerate its targets, or otherwise verify the second argument.

After resolving the path and syntactic target address, the function constructs the target's canonical graph.EntityID from the HCL namespace, the referenced atte.hcl path, and the requested target identifier. The dependency decoder stores that ID as a typed entity dependency. Once all repository atte.hcl files have been evaluated, a validation phase builds an index of the actual target IDs and verifies every deferred reference. Graph construction only proceeds when each referenced ID corresponds to an evaluated target.

This separates file evaluation from cross-file target validation: evaluating a file never evaluates another file, and validation has a complete target set against which to check references. Literal traversals and ordinary string/path dependencies retain their current behavior until their separate resolution contracts are defined.

## Detailed Design (as needed)

### Reference syntax and path resolution

The first argument to target identifies the directory containing the target's atte.hcl; the file name is implicit. A path beginning with // is resolved from the repository root. Any other path is resolved from the directory containing the source atte.hcl. Both forms must remain inside the repository and should use the existing reference helpers rather than a second path implementation.

The function resolves the directory to <directory>/atte.hcl and performs a basic existence/type check before returning. A missing file, a repository path that escapes the root, or a path resolving to a non-blob repository object is a function-evaluation error. The function must not parse or evaluate the referenced file as part of this check.

The second argument is a two-component target identifier of the form <kind>.<name-or-index>. Both named and anonymous targets use this form:

- test.0 addresses the anonymous test target with kind-local index 0.
- test.build addresses the named test target with label build.

The function validates the two-component syntax needed to construct a graph.EntityID, but does not determine whether the target exists. Numeric names are interpreted as anonymous target indices; nonnumeric names are interpreted as target labels. The deferred validation phase performs the existence, kind, and ambiguity checks against the evaluated target set.

### Evaluator lifecycle and deferred validation

Every top-level evaluator operation evaluates repository atte.hcl files independently, as it does today. The evaluation context for a source file gets a target function bound to that source file and the repository, but invoking the function does not request evaluation of another file. It only resolves the referenced path, checks for the referenced atte.hcl, parses the target address syntax, and returns its prospective entity ID.

The evaluator accumulates the returned entity IDs along with their source locations and owning target. After all files have been evaluated, it builds an index from the actual evaluated targets. It then validates every accumulated reference against that index. Targets and Graph must validate against the complete repository target set before returning results or adding relationships. ConfigFor remains file-local: it returns prospective graph.EntityID references without evaluating other files and without validating whether the referenced targets exist.

A reference from a/atte.hcl to b/atte.hcl therefore does not cause b to be evaluated while a is being decoded. A pair of references in opposite directions is not an HCL evaluation cycle. If the resulting dependency graph is cyclic, that is a graph-level dependency cycle to be handled by graph validation or execution ordering, not by the target function.

### Target reference representation

A successful target call returns an opaque target-reference value containing the prospective graph.EntityID, or an equivalent internal value that the dependency decoder immediately converts into the existing dependency.entity field. The ID is constructed from the canonical HCL target namespace, the referenced atte.hcl path, and the syntactically supplied target identifier. It is a promise of identity, not proof that the target exists.

The value must remain distinct from cty.String and from attegit.RepositoryPathType, so it cannot be confused with a file dependency. The function must not capture a pointer to mutable target-decoder state or require a target lookup. The deferred validation phase is the only place that can confirm that the ID names an evaluated target. The value may be stored in file-local locals and passed through expressions. Both depends_on and triggered_by accept it; triggered_by is an alias for depends_on. Script and ordinary string/path fields must reject it.

### Graph projection

Graph construction must first materialize the complete set of evaluated target entities and build an index keyed by their IDs. It then validates every target reference produced by target(). For each valid reference, it emits a DependsOnRelation from the containing target to the referenced target. References collected from depends_on and triggered_by are merged into one target-dependency collection during evaluation. The graph layer handles duplicate relationships.

A reference to a target that was never declared, has an unknown kind, or has an ID that does not match the target identity rules is an error; graph construction must not leave a dangling target entity as a way to hide that error. A target with no script is rejected while decoding the target block; target-reference validation does not need an additional script check.

## Cross cutting concerns (as needed)

### Determinism

Canonical paths, target indices, deferred validation results, error chains, entity IDs, and relation ordering must be deterministic. Validation must produce the same result regardless of the order in which files were evaluated. The validation index must be built from the complete evaluated target set, not from references encountered so far.

### Cycles and failure handling

Because target() never evaluates its referenced file, target-reference resolution cannot create recursive HCL evaluation. A dependency cycle between fully evaluated targets is ordinary graph data and is allowed.

The deferred validation phase must not publish a graph containing partially validated references. If any target file fails to parse or evaluate, or any reference fails validation, the operation returns an error and no graph is produced. Context cancellation must be returned rather than converted into a target-not-found error.

### Error quality

Errors should identify the source atte.hcl, the target being decoded, the target() call, and the referenced path or identifier where applicable. The implementation should distinguish invalid repository paths, missing atte.hcl, invalid target identifiers, target-not-found, ambiguous-target.

### Compatibility and security

Existing literal traversal dependencies, detector references, repository-path dependencies, graph IDs, selectors, and file-local locals must retain their current behavior. A target reference must never bypass repository path validation or permit a path outside the repository. It must not execute target scripts while checking a reference; the function only checks for the referenced atte.hcl and constructs a prospective ID.

The post-evaluation validation phase must compare canonical IDs rather than trust arbitrary strings supplied by HCL. It must also ensure that a target reference cannot manufacture an ID for a different namespace or repository path.

## Alternatives considered

### Resolve all symbolic references in a later graph phase

This is the preferred lifecycle for explicit target() references. The function performs only path/file checks and returns a prospective entity ID; the complete target set is available when references are validated. This avoids recursive HCL evaluation and cleanly separates file-local decoding from repository-wide graph assembly.

The remaining question is whether the validation/index phase belongs inside the HCL evaluator or in a separate graph assembler. It must happen after all relevant files have been evaluated, and all consumers must use the same canonical target identity rules.

### Pre-evaluate every atte.hcl before evaluating any target

Not needed for this design. The function does not inspect the referenced target set, so evaluation can remain file-local and deterministic. A later complete set/index phase supplies the information required for validation without forcing unrelated files to participate in an individual file's HCL context.

### Return a string entity ID from target()

Rejected as the primary representation. Strings can be confused with ordinary file dependencies and make it possible for user expressions to manufacture internal IDs. A typed opaque value keeps target references distinct until the dependency decoder handles them.

### Evaluate all files as one HCL namespace

Rejected. It would reintroduce the cross-file declaration coupling explicitly excluded by the target-centric HCL redesign and would expose unrelated locals or declarations to target evaluation.

## Future plans

- Resolve and validate preserved literal target traversals using a separately defined HCL address contract.
- Support references to non-target HCL declarations if a concrete use case requires them.
- Define which expressions may carry target references beyond file-local locals; depends_on and triggered_by are both accepted and have identical semantics.
- Consider a repository-wide immutable target index if deferred validation becomes a measurable performance concern.

## Other reading

- detector/attehcl/attehcl.go — evaluator lifecycle, target decoding, target identity, and graph projection.
- detector/attehcl/target_registry.go — dependency decoding and the current representation of symbolic and entity dependencies.
- detector/attehcl/README.md — current file-local evaluation contract.
- detector/attegit/hcl_plugin.go — repository-relative path HCL function and capsule value type.
- reference/reference.go — repository path normalization and boundary validation.
- detector/graph/graph.go — graph entity and relationship validation.
- idr/202608061806-attehcl-spec-redesign.md — target-centric redesign that deliberately deferred cross-file graph assembly.
- idr/202608052235-formalize-the-selector-spec.md — distinction between CLI selectors and HCL declaration addresses.

## Implementation (ephemeral)

Status: design development; no production implementation changes have been made for this IDR.

Initial implementation checklist:

- [x] Use `<kind>.<name-or-index>` for the second argument: `test.0` for anonymous index 0 and `test.build` for named target `build`.
- [ ] Merge target references from depends_on and triggered_by into one list of graph.EntityID values during decoding.
- [ ] Add a source-file-bound target HCL function that checks only the referenced atte.hcl file and constructs a prospective graph.EntityID.
- [ ] Preserve prospective entity IDs through dependency decoding without evaluating referenced files.
- [ ] Add a post-evaluation target index and validate all prospective IDs against the complete evaluated target set.
- [ ] Ensure Targets and Graph validate against the complete repository target set; keep ConfigFor file-local and return prospective references without validation.
- [ ] Add tests for rooted paths, relative paths, missing files, invalid paths, address grammar, missing targets, unknown kinds, and cross-file references in both directions.
- [ ] Add graph-level tests for valid cross-file edges and dependency cycles.
- [ ] Update the HCL README and any user-facing syntax documentation.
- [ ] Run the repository verification commands before marking the IDR approved.
