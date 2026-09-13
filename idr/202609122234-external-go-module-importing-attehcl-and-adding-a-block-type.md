# 2026-09-12: External Go module importing attehcl and adding a block type

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

Create a go module that will require ../go.mod and will extend attehcl with a
new block type.

### Context (as needed)

The HCL detector currently has built-in `test`, `codegen`, and `lint` target kinds. `attehcl.Register` already permits a package to register a kind with an HCL schema and decoder, and the decoded value is preserved in `attehcl.Target.Decoded`. The existing registration path is sufficient for configuration inspection, but graph construction and `atte run` still special-case the built-in script-backed target representation.

This change should make registration a complete target-kind extension point. A package imported by the host process should be able to register a kind whose targets are discovered, represented in the graph, and optionally runnable without changes to the core detector or command code for that particular kind. Registration remains explicit: an external module is active only when the executable imports the package, typically causing its registration to run during package initialization.

The example is a separate Go module at `examples/attehcl-custom-block/`. Its current `go.mod` uses Go 1.26.5, replaces `github.com/ghthor/atte` with `../..`, and requires the local root module at version `v0.0.0` plus `github.com/hashicorp/hcl/v2`. Its current `main.go` imports both `cmd` and `detector/attehcl`. It is becoming the host executable: it registers the `deploy` kind and delegates to the normal command system, so the executable can be exercised manually with `go run . run deploy` from the example directory. Automated acceptance coverage will live in `main_test.go` and invoke the command entry point directly. The example will provide a mock repository to the command system rather than relying on the checkout's real `atte.hcl`. The intended block schema is `deploy "<name>" { env = "dev" }`. The decoder will preserve the name and environment, and the runnable behavior will construct `echo "deploy <name> to <env>"`. The current example is an incomplete scaffold: its decoder has the wrong function signature and does not yet define this schema, mock repository, or behavior.

### Goals

1. ensure @attego can parse cross module references through replace statements
1. ensure we can add custom block types to attehcl as a package user
1. create an example of extending attehcl

### Non-Goals

* Dynamically discovering or loading arbitrary Go modules from an `atte` repository.
* Including the example module in the published `atte` executable by default.
* Making every custom target runnable; a registered kind may be configuration- or graph-only.
* Requiring custom kinds to use shell scripts. A runnable kind may construct whatever command representation the execution boundary supports.
* Treating the local `replace` directive as a runtime dependency loader; it is a module-development and test arrangement.

### Proposed Solution

Extend `attehcl` target-kind registration from schema-plus-decoder to a complete kind specification. The specification will retain the decoder and add optional behavior for converting a decoded value into target execution data and graph relationships. Built-in kinds will use the same registration path as external kinds instead of being recognized through private type assertions in graph and command code.

A registered kind will always be treated as an `attehcl` target and will retain its decoded value. If its specification supplies runnable behavior, the target will be included in `atte run` discovery and executed through the same selector, dry-run, and command path as built-in targets. If it supplies no runnable behavior, it will remain visible to configuration and graph consumers but will be omitted from the runnable-target list. Graph participation, including source-file and dependency relationships, will likewise be opt-in through the kind specification.

Complete `examples/attehcl-custom-block/` as a consumer and host of the public API. It will register a custom `deploy` block with the schema:

```hcl
deploy "<name>" {
  env = "dev"
}
```

The decoded target will retain the block name and `env` value. Its command projection will produce a shell command equivalent to `echo "deploy <name> to <env>"`. The example executable will create or otherwise provide a mock `attegit.Repo` containing the described `atte.hcl`, register the custom kind in its process, and invoke the normal command system. The command system will accept an injected repository override, so `go run . run deploy` from `examples/attehcl-custom-block/` discovers the mocked target and runs it through the same path as a built-in target. The module replacement will also provide the fixture needed to verify that `attego` resolves imports from a nested module through `replace`.

The same repository override will be used by `config show`. Acceptance tests in `examples/attehcl-custom-block/main_test.go` will invoke the command entry point with `config show` and assert that it renders only the mocked `deploy "release" { env = "dev" }` target in the command's normal configuration output, including the custom `env` value, and does not display targets from the checkout's real repository. The executable remains manually runnable with `go run . config show`.

## Detailed Design (as needed)

The public registration contract must describe four independent capabilities:

1. **Decoding:** validate the block body against the registered `hcl.BodySchema` and return a kind-owned value.
2. **Graph projection:** optionally turn the decoded value into repository entities and relationships. Common target relationships such as the declaring HCL file, source script, and dependencies should be available without requiring custom kinds to use private `attehcl` types.
3. **Execution:** optionally turn the decoded value and target location into an executable command. The execution result must carry the working directory and command arguments needed by the existing run path.
4. **Configuration projection:** optionally expose the decoded value in the standard `config show` output. The custom `deploy` projection must preserve the target kind, name, and `env = "dev"` value in the command's configured output format.

Evaluation takes a registry snapshot, so registrations completed before evaluation are visible consistently throughout a graph or target evaluation. Duplicate kind names and invalid HCL identifiers remain registration errors. Registration is process-local; importing an external package into the host executable is the activation mechanism.

The evaluator, `Targets`, `ConfigFor`, graph construction, and `cmd/run.go` should operate on registered capabilities rather than asserting that decoded values are the built-in private script-target type. `attehcl.Target` remains the common public identity and decoded-value container. Command discovery should ask the evaluated target whether it is runnable instead of checking only `Target.Script`.

The command system must expose an execution/repository override for hosts such as the example. The override supplies the `*attegit.Repo` consumed by the Go and HCL detectors and the repository-relative working directory used for selector resolution. `run` must use the injected repository instead of reopening the Git repository discovered from the process working directory, while retaining normal selector matching, runnable-target filtering, command construction, and process execution. The default `atte` executable path remains unchanged and continues to discover its repository from the current directory. The same override must be honored by `config show`, so both acceptance paths evaluate the mock repository rather than the checkout. The command execution API must allow tests to provide command arguments and output writers without depending on the process's `os.Args`, stdout, or stderr. Cobra should provide the command output destinations through its configured writers (`SetOut` and `SetErr` or the equivalent command API), so acceptance tests can use `bytes.Buffer` values directly. The tests must not invoke `exec.Command` or capture the process's real stdout/stderr. Tests must also be isolated so global Cobra command state, flags, and registered target kinds do not leak between the `run deploy` and `config show` cases.

The example should contain a custom `deploy` block with a decoded value and runnable behavior:

```hcl
deploy "release" {
  env = "dev"
}
```

The target's command should be equivalent to `echo "deploy release to dev"`, and the acceptance test should invoke the command entry point with `run --dry-run deploy` using the normal HCL selector rules. The dry-run flag should assert the resolved selector and command without launching a subprocess. A second acceptance test should invoke it with `config show` and assert the standard configuration output for only `deploy "release" { env = "dev" }`. Tests in `main_test.go` will set the command arguments, inject the mock repository, configure Cobra's output writers with `bytes.Buffer` values, execute `run --dry-run deploy` and `config show`, and assert both success and exact output without spawning a subprocess. They should verify the custom kind, selector identity, decoded name and environment, graph representation, generated command, dry-run behavior, and configuration rendering against the injected mock repository. A non-runnable registration should also be possible without causing graph evaluation to fail.

## Cross cutting concerns (as needed)

**Process boundaries.** Go initialization only registers a kind in a process that imports the external package. Core `atte` must not scan, compile, or dynamically load arbitrary repository modules. A host that wants the custom kind must explicitly link the package. The example is itself such a host: it imports `cmd`, registers `deploy`, injects its mock repository, and invokes the normal command path.

**Failure handling.** Invalid schemas, duplicate registrations, decoder failures, graph projection failures, configuration projection failures, and command-construction failures should identify the custom kind and target location. A missing optional execution capability should make the target non-runnable, not make configuration or graph discovery fail.

**Security.** Custom code executes with the same authority as the host executable because it is compiled into that executable. The extension mechanism is not a sandbox. Existing command execution and repository path validation still apply to any repository-backed paths exposed by common target helpers.

**Determinism.** Registration and evaluator snapshots must produce stable target identities and graph output. The example's local `replace` is only for local development and testing.

## Alternatives considered (as needed)

**Keep custom kinds configuration-only.** This is the capability currently demonstrated by `Register` and `Target.Decoded`, but it leaves graph and execution code coupled to built-in kinds and does not meet the goal that an imported external kind behave like a built-in.

**Add `deploy` directly to `atte`.** This would make execution straightforward but defeats the purpose of validating the package-user extension boundary and would require publishing every kind with the core executable.

**Dynamically load the external module.** Go does not provide a general, portable mechanism for safely loading arbitrary repository modules as `atte` plugins. Explicitly importing the package into the host is simpler, deterministic, and consistent with normal Go linking.

**Use a separate detector namespace for every custom block.** A custom block is intended to be an `attehcl` target kind, so it should share HCL target identity, selectors, graph behavior, and execution plumbing rather than requiring a separate detector implementation for each kind.

## Future plans (as needed)

* Define reusable helpers for common script-backed and Go-command-backed custom kinds.
* Add richer target metadata or output serialization for custom decoded values where consumers need it.
* Document a host-application pattern for building an `atte` binary with organization-specific target packages linked in.

## Other reading (as needed)

* `detector/attehcl/target_registry.go` — current target-kind registration and decoder contract.
* `detector/attehcl/attehcl.go` — target evaluation, graph construction, and public `Target` representation.
* `detector/attehcl/attehcl_test.go` — existing custom-kind registration tests.
* `cmd/run.go`, `cmd/config.go`, and `cmd/root.go` — current command execution, configuration rendering, and repository discovery boundaries that the example must override without changing default behavior.
* `detector/attego/module.go` and `detector/attego/golist_test.go` — Go module scanning, local replacements, and graph expectations.
* `examples/attehcl-custom-block/go.mod`, `main.go`, and `main_test.go` — the external-module host, mock-repository acceptance tests, and intended `deploy` target.
* `detector/attehcl/README.md` — documented HCL target behavior and file-local evaluation rules.

## Implementation (ephemeral)

### Acceptance-first checkpoint

1. Add `examples/attehcl-custom-block/main_test.go` before implementing the extension behavior.
2. Build a mock repository containing only the intended `atte.hcl` deployment target:

   ```hcl
   deploy "release" {
     env = "dev"
   }
   ```

3. Add acceptance cases that invoke the command entry point with `run --dry-run deploy` and `config show`, inject the mock repository, configure Cobra's output writers with `bytes.Buffer` values, and assert the expected resolved command and configuration output. The dry-run case must verify that the deploy command is resolved but not executed. Do not invoke `exec.Command` or capture the process's real stdout/stderr.
4. Run `go test ./...` from `examples/attehcl-custom-block/` and leave the acceptance suite failing because the custom registration, repository override, execution projection, and configuration projection are not implemented yet. The known incomplete decoder scaffold may need to be accounted for so the failure remains a meaningful test/build failure rather than silently testing the real checkout.
5. **Stop at this checkpoint. Do not implement production behavior or fix the acceptance failures in the same pass. Wait for review and further direction from the operator before proceeding.**

### Checkpoint result

* Added `examples/attehcl-custom-block/main_test.go` with a Git-backed mock repository fixture and acceptance cases for `run --dry-run deploy` and `config show`.
* The tests configure Cobra output through buffers and do not spawn a command subprocess.
* Added the example module's test dependency on `github.com/shoenig/test` via `go get`.
* Implemented the planned `cmd.ExecuteWithOptions` and `cmd.ExecuteOptions` APIs with Cobra stream injection, argument injection, and repository/working-directory overrides.
* Updated `run` and `config show` to honor the injected repository while preserving default repository discovery.
* `go test ./...` from `examples/attehcl-custom-block/` now builds and runs both acceptance tests, but they fail because `deploy` is still an unknown HCL target kind and the custom target capabilities are not implemented.
* `go test ./cmd/...` passes.
* Stop here for operator review; do not implement the custom target features in this pass.

### Planned follow-on checkpoints

Each checkpoint below should be implemented and verified independently, then paused for operator review before starting the next one. The acceptance suite should remain red until the checkpoint that completes the corresponding capability.

1. **Public target-kind capabilities and built-in parity.** Define the public registration contract for decoding, graph projection, execution projection, and configuration projection. Refactor built-in target handling to consume the same registered capabilities instead of private built-in type assertions. Add focused `attehcl` unit tests for capability registration, registry snapshots, duplicate registrations, and non-runnable kinds. Run the relevant detector tests and leave the example acceptance tests failing on unsupported custom behavior.
2. **Custom decoding, target identity, and graph projection.** Implement the `deploy` schema and decoded value containing the block name and `env`. Register the example kind through the new API and project its target identity and graph relationships. Add tests for `deploy "release"`, selector matching, decoded values, and graph output. The acceptance tests should progress past “unknown target kind” but may still fail because command and configuration projections are incomplete.
3. **Runnable command projection.** Implement the `deploy` execution capability so it resolves to the equivalent of `echo "deploy release to dev"`, and make `run --dry-run deploy` use the common runnable-target path. Verify the dry-run acceptance test passes without launching a subprocess while `config show` remains red if configuration projection is not complete.
4. **Configuration projection.** Implement the custom configuration projection, including the target identity and `env = "dev"` in the normal `config show` output. Verify the configuration acceptance test passes and that only the injected example target is rendered.
5. **Nested-module replacement coverage and full verification.** Add the approved `attego` tests for nested modules and local `replace` directives, then run the repository verification targets from `AGENTS.md`. Update this section with factual results and any remaining design or implementation discrepancies.

### Public target-kind capabilities checkpoint result

* Added the public `TargetKindSpec` registration contract with independent decoder, graph, execution, and configuration projections.
* Refactored built-in HCL target handling, graph construction, `run`, and `config show` to consume registered capabilities. Built-in script targets retain their existing graph, command, and configuration behavior.
* Added common target graph/configuration/command helpers and registry-snapshot and non-runnable capability tests.
* Updated the custom-block example scaffold to compile against the new registration contract; its acceptance tests remain red because `deploy` has not yet been registered with decoding and projections.
* Verified `go test ./detector/attehcl ./cmd` and root `go test ./...`; `codegen.go`, `codegen.fmt`, `codegen.rendered`, and `lint.go` pass. The aggregate `test.go` target fails only because the intentionally red example acceptance suite is included and still reports the unsupported `deploy` kind.
* Added `reference/target.Computed` for the common configuration fields. `attehcl.Target.Configuration` now returns it directly, with kind-specific values in `Meta`; `config show` uses the shared type for JSON and HCL conversion.

### Custom decoding, target identity, and graph projection checkpoint result

* Implemented the example `deploy` body schema requiring a string `env` attribute and added a decoder returning the deploy-specific decoded value.
* Registered `deploy` during package initialization with decoding and graph capabilities; the target retains its HCL name, stable selector, and decoded environment.
* Added a graph projection using the common target graph base, including the deploy entity, declaring file, and tree containment relationships.
* Added acceptance coverage for decoded identity and graph output.
* The example acceptance suite now progresses past unknown target kind. The graph/identity test passes; `run --dry-run deploy` remains red because execution is not implemented, and `config show` remains red because configuration projection is not implemented.

### Runnable command projection checkpoint result

* Added the `deploy` execution projection, which constructs `echo "deploy release to dev"` with the injected repository root as its working directory.
* The existing common runnable-target path now discovers and dry-runs the custom target without launching a subprocess.
* Updated dry-run command formatting to quote arguments containing whitespace while preserving the direct argument representation used for execution.
* The example `run --dry-run deploy` acceptance case passes; `config show` remains red because configuration projection is not implemented.

### Configuration projection checkpoint result

* Added the `deploy` configuration projection, preserving `env` as kind-specific metadata in the shared `target.Computed` output.
* The custom target now renders its common identity and `meta.env` through the normal JSON and HCL configuration paths.
* The complete example acceptance suite passes, including `run --dry-run deploy`, target identity and graph coverage, and `config show`.
* Updated the example executable to create the same temporary Git-backed mock repository used by the acceptance scenario and inject it through `cmd.ExecuteWithOptions`, so `go run . config show` and `go run . run --dry-run deploy` exercise the custom target instead of the checkout repository.
* Extracted temporary Git repository creation into `detector/attegitmock`; `detector/attegittest` now wraps that implementation for testing, and the example host uses it directly.
* Restored the shared `RunGitScript` multiline-script operation and used it for `attegitmock.New` repository initialization. The repository uses a nested temporary directory so sibling Git fixtures such as alternates remain isolated.
* Added context propagation to `attegitmock.New` and `RunGitScript`; the test wrapper and example host pass their active contexts through to Git setup commands.
