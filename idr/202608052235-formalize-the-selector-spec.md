# 2026-08-05: formalize the selector spec

Owner: Will Owens <ghthor@gmail.com>

> **Current implementation note (2026-09):** The process-global `reference/selector.Mapping`
> described in this historical record has been removed. Selector presentation,
> matching, resolution, and executable command construction are capabilities of
> the compiled `detector.Scanner` and its attached `TargetSensor`s. The shared
> selector package now owns only selector values and pure syntax/path helpers;
> see `reference/selector/refactor.md` for the implementation contract. The
> earlier mapping-specific design below records the original decision, not the
> current API.

## Overview

### Problem Statement

Atte currently supports several selector spellings, but their syntax and
resolution behavior are distributed across `reference/selector`, detector
mappings, `cmd/run.go`, and `cmd/runcomp`. The implementation has useful tests,
but there is no single normative description of which forms are accepted, how
paths are interpreted, how detector-specific identifiers are matched, or how
completion relates to resolution.

The cross-file HCL work in
`idr/202608052031-enable-cross-file-hcl-evaluation.md` adds another traversal
form for addressing HCL declarations. We need to document that form alongside,
but distinctly from, the CLI run-selector syntax so future selector additions
do not create accidental or undocumented behavior.

### Context

The selector implementation has several layers and consumers:

- `reference/selector` parses and renders the CLI `#` forms, normalizes and
  resolves repository paths, and provides detector-neutral matching helpers.
- Detector packages register namespace-specific render and match functions in
  `reference/selector.Mapping`. These mappings define target identifiers and
  aliases, but aliases are CLI conveniences rather than alternate graph IDs.
- `cmd/run.go` resolves canonical selectors, path-qualified selectors, pathless
  aliases, and labels. Pathless aliases are scoped to the current working
  directory and must resolve uniquely there; a path-qualified alias must resolve
  uniquely within its specified path.
- `cmd/runcomp` implements CLI completion separately from target resolution. It
  must support canonical, path-qualified, pathless, and relative partial forms.
- HCL expressions use a separate path-plus-traversal address such as
  `"//job/example".docker_bake.image.name`. The `//<repository-path>` prefix
  identifies the HCL file location and the dot traversal identifies the HCL
  declaration. This address is repository-wide and resolves to a
  detector-owned declaration; it is not a short CLI selector and is not parsed
  by the CLI `#` grammar.

The formal specification must distinguish these forms by consumer and
resolution scope. The HCL address is unambiguous because its repository path is
explicitly rooted with `//`; relative HCL addresses are intentionally not part
of the initial design.

### Goals

- Establish one normative specification for all currently supported CLI
  selector spellings and their canonical representation.
- Specify path normalization, repository-root qualification, relative
  resolution, HCL file-versus-containing-directory behavior, and repository
  escape errors.
- Specify the identifier contract independently from detector-specific aliases,
  including matching precedence and ambiguity handling.
- Specify the detector mapping contract used by HCL and Go targets.
- Specify shell completion inputs, output forms, ordering, and its relationship
  to selector resolution.
- Define how the HCL declaration-address form from the cross-file HCL IDR is
  related to CLI selectors without conflating the two syntaxes or semantics.
- Turn the specification into focused conformance tests so future selector
  forms must be added intentionally.
- Keep existing canonical selectors and target IDs stable.

### Non-Goals

- Implementing cross-file HCL evaluation or Docker Bake declarations; that work
  belongs to `idr/202608052031-enable-cross-file-hcl-evaluation.md`.
- Designing arbitrary imports, aliases, escaping, or bracket syntax for HCL
  declaration addresses. The related HCL IDR explicitly leaves those for later.
- Replacing detector-owned target discovery or graph identity with user-facing
  selector strings.
- Allowing HCL expressions to use the CLI-only `<path>#<identifier>` forms for
  cross-file references.
- Adding a CLI form for non-runnable HCL declarations.

## Proposed Solution

Write a normative selector specification at `reference/selector/README.md`,
with package-level documentation and conformance tests kept next to the
implementation. The specification will define separate grammars and
resolution contracts for:

1. **HCL declaration addresses** — a rooted repository path followed by a
   complete HCL traversal, for example
   `"//job/example".docker_bake.image.name`. The `//<repository-path>` prefix
   identifies the path portion and the dot-separated suffix is the HCL
   traversal. This is the only form used by HCL expressions for cross-file
   declaration references. It has no short or relative form initially.
2. **CLI hash selectors** — `<relative-path>#<HCL identifier>`,
   `//<repository-relative-path>#<HCL identifier>`, and
   `#<HCL identifier>`. These are CLI-only forms. The first two identify a
   specific `atte.hcl` location; the last scopes the identifier to the CWD.
3. **CLI aliases and short forms** — identifier-only inputs such as `test.go`
   or `go_test`, which are convenience inputs and must resolve uniquely in the
   CWD. They are not accepted as HCL declaration references.
4. **Completion** — completion for every CLI form, including relative paths,
   while retaining the distinction between display forms and canonical
   repository-root-qualified output.

The generic selector package will validate the identifier portion using the
same identifier character rules accepted for an HCL block type or kind. A CLI
selector contains at most one `#`; an additional `#` is malformed. A leading
`#` means an identifier scoped to the local CWD, rather than an identifier with
an empty repository path in a general-purpose grammar.

The document will include grammar tables, accepted and rejected examples,
resolution precedence, a compatibility table for HCL and Go targets, and a
clear distinction between CLI selectors and HCL declaration addresses. The
HCL address rules, discovery, collisions, and typed entity references remain
owned by `idr/202608052031-enable-cross-file-hcl-evaluation.md` and will be
cross-referenced rather than duplicated.

The design decisions are now settled: the CLI hash separator occurs exactly
once; `#<identifier>` is a CLI-only CWD form; aliases are CLI-only and must be
unique within the explicit path or CWD; relative completion is supported; and
identifier components use HCL block identifier syntax. The normative artifact
is `reference/selector/README.md`.

## Detailed Design

### 1. Selector forms and grammar

The specification should use these terms consistently:

- An **HCL declaration address** is a complete repository-wide HCL
  declaration address. It has a quoted, rooted repository path followed by a
  dot-separated HCL traversal.
- A **CLI hash selector** is a CLI-only selector containing exactly one `#`.
  Its suffix is an HCL identifier or detector-specific identifier/traversal
  composed of valid HCL identifier components. The exact traversal shape may
  vary by target kind, but every component is dot-separated.
- A **CLI alias** is an identifier-only convenience input. It is resolved
  against targets in the CWD and is never a cross-file HCL reference.
- A **canonical selector** is a repository-root-qualified hash selector using
  `//<repository-relative-path>#<identifier>`.

The normative CLI grammar is:

```text
cli-selector       = rooted-selector | relative-selector | cwd-selector | alias
rooted-selector    = "//" repository-path "#" identifier
relative-selector  = relative-path "#" identifier
cwd-selector       = "#" identifier
alias              = identifier

identifier        = hcl-identifier *("." hcl-identifier)
hcl-identifier    = identifier accepted for an HCL block type or kind
```

`relative-selector` includes path-qualified forms such as
`pkg/atte.hcl#test.go`, `./atte.hcl#test.go`, and
`../pkg/atte.hcl#test.go`. It resolves the path from the CWD and must stay
inside the repository. `rooted-selector` always resolves from the repository
root and identifies the same target regardless of CWD. `cwd-selector` and
`alias` are CWD-scoped convenience forms.

There is exactly one `#` in a valid CLI hash selector. `#identifier` is not a
path with an empty component: it explicitly means identifier in the CWD. A
second `#`, an empty identifier, or an identifier component that is not a valid
HCL identifier is malformed. The implementation must reject these cases
rather than splitting on the first `#` and treating the remainder as opaque.

For HCL runnable targets, a hash selector's path points directly to one
`atte.hcl` location. The containing directory may remain an accepted shorthand
for CLI matching, but it must resolve to that one file and never broaden the
identifier search to other HCL files. The canonical renderer continues to use
the `atte.hcl` file path.

The HCL declaration address grammar is intentionally separate:

```text
hcl-address   = hcl-root hcl-traversal
hcl-root      = '"' '//' repository-path '"'
hcl-traversal = '.' hcl-identifier ('.' hcl-identifier)*
```

The quoted `//<repository-path>` root is the path portion. In HCL source it is
one quoted string traversal step, for example `"//pkg".test`. It identifies
the `atte.hcl` location by repository-relative directory and is followed by
the complete dot-separated HCL traversal. It has no `#`, no short form, and no
relative form initially. HCL expression evaluation—not the CLI selector parser—
resolves it. The path/traversal boundary is explicit rather than inferred from
ambiguous dot-separated components.

### 2. Identifier and alias behavior

The generic package validates identifier components using the HCL block
identifier rules. Detector mappings still own the meaning of a complete
identifier and may define which dotted forms represent a target kind, label,
index, or traversal. The current compatibility matrix to capture in tests is:

| Target family | Canonical identifier | CLI convenience aliases |
| --- | --- | --- |
| HCL labeled block | `kind.label` | `label`, `kind`, `kind.index`, custom aliases |
| HCL unlabeled block | current kind/index form | current kind and index aliases |
| Go package test | `go_test` | `go_test.0`, path-local package spelling |

Aliases and short forms are CLI conveniences only. They do not create new HCL
addresses, new graph entities, or new canonical selector spellings. They must
resolve uniquely within the specified path. If no path is specified, they must
resolve uniquely among targets rooted at the CWD. A pathless alias must not
silently select a target elsewhere in the repository merely because it has the
same alias.

The normative CLI resolution order is:

1. exact canonical selector string;
2. a hash selector's path resolution, followed by complete identifier matching
   within that one path;
3. a pathless alias or `#identifier` scoped to targets in the CWD;
4. standalone HCL label fallback, if retained as a compatibility alias and if
   it resolves uniquely in the CWD;
5. not-found or ambiguity error.

A path-qualified input is never disambiguated by preferring the CWD: its path
already establishes the scope. A pathless input that matches multiple targets
is an error after restricting candidates to the CWD. Matching compares whole
identifier components and never uses raw string prefixes.

### 3. Detector mapping contract

`reference/selector.Register` should be documented as process-wide registration
of one matcher and one renderer per detector namespace. Registration rejects an
empty namespace, nil functions, and duplicate namespaces. The renderer must
return a parseable canonical selector. The matcher receives a detector-neutral
`graphtarget.ID` and an identifier already separated from the path.

The spec should call out these invariants:

- `graphtarget.ID.ID`, namespace, kind, path, name, index, and aliases serve
  graph or matching purposes; only the rendered selector is user-facing.
- A selector renderer must be deterministic and must not depend on map
  iteration order.
- A matcher must honor the target's aliases and the generic path semantics.
- Unknown namespaces do not match and cannot be rendered.

If the mapping API is later changed, the specification should be updated first
or the compatibility impact recorded in this IDR.

### 4. Completion contract

Completion is CLI-only and is a projection of discovered canonical selectors,
aliases, and repository paths. It must support partial canonical, path-qualified,
pathless, and relative forms. Relative completion resolves the path prefix from
the CWD in the same way as a complete relative selector, while preserving the
user's requested relative spelling in the returned candidates.

For a partial input, completion may return:

- canonical candidates retaining the `//` prefix;
- repository-relative candidates retaining the requested relative path prefix;
- short identifiers when the current working directory contains the target; or
- the next HCL identifier component after a `#` prefix, where the consumer is
  explicitly the CLI. HCL declaration addresses are not CLI completion forms.

Completion must never suggest a second `#` or an identifier component that is
not valid under the HCL identifier rules. Candidates must be deterministic,
retain the requested prefix, and be valid input to the documented CLI resolver.
Short aliases may be display forms, but resolving the completed value must use
CWD/path scope and the same uniqueness rules as direct CLI input.

The conformance tests should cover completion at the repository root, inside an
HCL directory, inside a Go package directory, after a path separator, after
`#`, and for `./` and `../` relative paths. HCL declaration addresses used in
HCL expressions are not CLI completion candidates unless a future command
explicitly opts into that grammar.

### 5. HCL declaration addresses

The related HCL IDR's `"//job/example".docker_bake.image.name` form is an
**HCL declaration address**, not a CLI selector. It is the only cross-file HCL
reference form in this design. Its initial shape is:

```text
"//<repository-path>".<block>.<label>.<block>.<label>
```

The rooted, quoted `//<repository-path>` prefix identifies the repository
location. Every suffix component is an HCL identifier and every descendant
contributes one dot-separated component. The traversal shape may vary by block
type, but it always uses complete descendant components; there is no abbreviated
or pathless HCL form. `atte.hcl` is omitted from the path. Relative HCL paths
are reserved for a future design.

Because the path starts with `//` and is delimited before the dot traversal,
the path/traversal boundary is explicit. The detailed declaration discovery,
address validation, and collision rules remain owned by the related HCL IDR.

The selector specification should include this distinction table:

| Form | Consumer | Scope | Meaning | Result |
| --- | --- | --- | --- | --- |
| `"//job/example".docker_bake.image.name` | HCL evaluator | whole repository | rooted path + complete HCL traversal | typed HCL entity reference |
| `pkg/atte.hcl#test.go` | CLI | specified relative path | HCL target selector | one target in that file |
| `//pkg/atte.hcl#test.go` | CLI | whole repository | canonical HCL target selector | one target in that file |
| `#test.go` | CLI | CWD | local HCL target selector | one target in the CWD file |
| `test.go` | CLI | CWD | local convenience alias | unique target or error |

CLI hash selectors are not accepted as HCL declaration references. HCL
expressions must use the full HCL declaration address even when the target happens to be
in the current file's directory.

## Cross cutting concerns

### Compatibility

The first implementation phase is documentation and tests, followed by the
small parser and completion changes required to enforce this contract. Existing
canonical selector outputs and target IDs must remain unchanged. Existing
aliases remain accepted where they satisfy the new uniqueness and CWD-scope
rules; aliases are not promoted to canonical forms.

The following changes are intentional and must be covered by tests:

- identifiers containing a second `#` are rejected;
- identifier components are validated as HCL identifiers;
- relative completion is supported;
- pathless aliases are restricted to the CWD and must be unique; and
- rooted HCL declaration addresses are reserved for HCL evaluation and are not
  added to `atte run` as short forms; relative HCL addresses are deferred.

The spec should mark behavior as normative, compatibility legacy, or
intentionally unsupported. Unresolved behavior must not be presented as a
stable contract.

### Determinism

Canonical rendering, alias lists, target ordering, ambiguity diagnostics, and
completion candidates must be deterministic. Tests should not rely on Go map
iteration order or repository object enumeration order.

### Error quality

The spec should define error categories rather than exact prose where prose is
likely to evolve: malformed selector, invalid path, repository escape, target
not found, and ambiguous selector. Existing tests should preserve useful
context such as the original selector and possible canonical alternatives.

### Security and boundaries

All path-bearing CLI selectors resolve within the repository. Backslashes are
normalized consistently with the existing path helpers. A CLI selector has
exactly one separator and validates every identifier component before matching.
HCL declaration addresses are identifier traversals and must never be
interpreted as filesystem paths or allowed to escape repository scope.

### Documentation drift

The normative document should be referenced from package comments and command
help where practical. Conformance tests should be named after specification
sections so a future syntax addition has an obvious place to update both the
rules and tests.

## Alternatives considered

### Document only the generic parser

Rejected. Detector aliases, `cmd/run.go` precedence, HCL directory shorthand,
and completion are part of the behavior users experience and would remain
undocumented.

### Make the CLI selector parser understand HCL declaration traversals

Rejected for now. HCL declaration addresses are evaluated in an HCL context and
resolve to typed declarations, while CLI selectors resolve runnable targets.
Combining them would imply semantics that the related HCL IDR does not define.

### Put the entire specification in command help

Rejected. Help is useful for examples but is not a maintainable normative
reference for grammar, detector contracts, completion, and compatibility tests.

### Infer the specification from tests alone

Rejected. Tests are necessary conformance checks, but they do not explain the
model, distinguish stable behavior from compatibility behavior, or document
why the different selector layers exist.

### Treat aliases and HCL addresses as the same selector language

Rejected. CLI aliases are convenience spellings whose scope is the CWD or an
explicit CLI path. HCL declaration addresses are complete repository-wide HCL
traversals and are required for HCL-to-HCL references. Giving either language
the other's short forms would make scope and ambiguity implicit.

### Redesign all selector behavior before documenting it

Rejected. This IDR makes only the decisions needed to formalize the supported
forms: one `#`, HCL identifier validation, CWD-scoped unique aliases, and
relative CLI completion. Larger changes to target identity or declaration
addressing remain separate decisions.

## Future plans

- Consider a structured selector type that separates canonical rendering from
  display aliases without changing the public syntax.
- Add a future CLI command or selector form for non-runnable HCL declarations
  only if a concrete use case requires it.
- Add escaping or bracket syntax for HCL declaration components that are not
  valid HCL identifiers, as contemplated by the related HCL IDR.

## Other reading

- `reference/selector/selector.go` — parser, path resolution, target aliases,
  rendering, and matching helpers.
- `reference/selector/mapping.go` — detector namespace registration and mapping
  behavior.
- `reference/selector/selector_test.go` — current parser, path, alias, and
  target matching examples.
- `reference/selector/mapping_test.go` — mapping registration and matching
  examples.
- `cmd/run.go` — target discovery, resolution precedence, ambiguity handling,
  and label fallback.
- `cmd/runcomp/shcomp.go` — shell completion behavior.
- `cmd/runcomp/shcomp_test.go` — completion corpus and ordering expectations.
- `cmd/run_matching_test.go` — command-level resolution and ambiguity cases.
- `detector/attehcl/detector.go` — HCL selector mapping registration.
- `detector/attehcl/attehcl.go` — HCL target identity and selector aliases.
- `idr/202608052031-enable-cross-file-hcl-evaluation.md` — HCL declaration
  address design and cross-file evaluation lifecycle.
- `idr/202608052235-formalize-the-selector-spec/hcl-id-test/main.go` —
  executable HCL parser experiment showing that `"//pkg".test` parses as a
  string path expression plus a relative traversal, and that application-level
  namespace lookup is required before evaluating the traversal.

## Implementation (ephemeral)

Status: planning and repository research; an executable HCL parser experiment
has been added, but no production selector implementation changes have been
made beyond moving this IDR into `idr/`.

### Experimental implementation

`idr/202608052235-formalize-the-selector-spec/hcl-id-test/main.go` parses:

```hcl
target = "//pkg".test
```

It verifies that ordinary HCL evaluation rejects applying `.test` directly to
`"//pkg"` because the path is a string. It then supplies a path-keyed cty
namespace, looks up the evaluated `//pkg` path, and applies the parsed
relative traversal to resolve the declaration. This confirms the HCL
syntax/evaluation boundary relevant to the selector specification: the rooted
HCL declaration-address form is not a CLI `#` selector, and its path lookup is
application-defined.

Verified with:

```text
go run ./idr/202608052235-formalize-the-selector-spec/hcl-id-test
```

### Proposed work sequence

1. Inventory current accepted and rejected forms from `reference/selector`,
   `cmd/run.go`, `cmd/runcomp`, and their tests.
2. Write `reference/selector/README.md` as the normative specification with
   separate CLI hash, CLI alias, and HCL declaration-address grammars.
3. Add or reorganize table-driven conformance tests in
   `reference/selector`, `cmd`, and `cmd/runcomp` using the repository's Go test
   conventions.
4. Update the parser and command resolution so one `#`, HCL identifier
   validation, CWD-scoped unique aliases, and path-qualified uniqueness are
   enforced.
5. Implement relative completion with the same path resolution semantics as
   direct CLI input.
6. Update package comments and command help only where they currently
   contradict or omit the normative rules.
7. Compare the resulting spec and tests with the cross-file HCL IDR before
   implementing that IDR, especially to ensure HCL declaration addresses are
   preferred at the traversal boundary and are not accidentally treated as CLI
   selectors.

### Initial inventory

- Generic CLI separator: `#`; valid CLI hash selectors contain exactly one
  separator and a non-empty identifier. A second separator is malformed.
- `#<identifier>` is specifically the CLI form for an identifier in the CWD;
  it is not a general empty-path representation.
- Hash-form HCL identifiers are scoped to the one `atte.hcl` location named by
  the selector path; cross-file HCL references use the full rooted HCL declaration-address form.
- Root-qualified rendering: `//<path>#<identifier>`; root-level paths render
  with an empty path component.
- `#<identifier>` is a CLI-only CWD-scoped form; bare aliases are also CWD
  scoped and must resolve uniquely.
- Path-qualified HCL selectors identify one direct `atte.hcl` location, with
  the containing directory retained as a CLI matching shorthand.
- Relative path resolution accepts repository-relative paths from the CWD and
  rejects paths escaping the repository.
- HCL aliases are generated from kind, name, short name, index, and custom
  aliases; aliases are CLI conveniences and are not HCL declaration addresses.
- Go package targets have path-local and path-independent aliases, subject to
  the same CLI uniqueness rules.
- Completion must support canonical, path-qualified, pathless, and relative
  CLI forms, with deterministic output.
- The cross-file HCL IDR adds rooted HCL declaration addresses such as
  `"//job/example".docker_bake.image.name`; these are HCL expression values,
  have no short or relative form initially, and are not accepted by `atte run`.

All five design questions raised in the initial plan are resolved by this IDR:
one `#` only; aliases are CLI-only and uniquely CWD/path scoped; relative
completion is required; identifier components use HCL block identifier rules;
and the normative document belongs at `reference/selector/README.md`.
