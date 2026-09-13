# 2026-09-13: Isolate Cobra command execution state

Owner: Will Owens <ghthor@gmail.com>

## Overview

### Problem Statement

The embeddable command entry point currently executes every invocation against a shared Cobra command tree. `ExecuteWithOptions` mutates the shared root command, its arguments and output writers, and package-level flag variables. Repeated or concurrent invocations can therefore observe state left by another invocation, making the API unsuitable for hosts and tests that execute more than one command in a process.

This change will make each command execution independent while preserving the existing CLI behavior and default repository discovery path.

### Context (as needed)

The `cmd` package currently constructs `rootCmd`, `runCmd`, `configCmd`, `configShowCmd`, and `graphCmd` as package-level Cobra values during initialization. Flags are bound to package-level variables such as `runDryRun`, `runList`, `configRef`, `configWorkingTree`, `configFormat`, and the graph options. `ExecuteWithOptions` then calls `SetArgs`, `SetIn`, `SetOut`, and `SetErr` on the shared root command before executing it.

The package is now used both as a process entry point and as an embeddable command system. The latter requires repeated calls with different arguments, repositories, working directories, and output buffers. The current example acceptance tests exercise different subcommands in a favorable order and do not detect flag leakage.

### Goals

* Ensure every command invocation has isolated Cobra command objects, flag values, arguments, and I/O streams.
* Make sequential and concurrent `ExecuteWithOptions` calls independent of one another.
* Preserve the current CLI command tree, flag names, flag defaults, output, repository discovery, and injected-repository behavior.
* Keep explicit process-wide target-kind registration separate from per-invocation Cobra state.
* Add regression tests that expose argument, flag, output, repository, and concurrent-execution leakage.

### Non-Goals

* Changing the command names, flag names, flag defaults, selector semantics, or output formats.
* Removing explicit process-wide registration of HCL target kinds.
* Making arbitrary shared state elsewhere in the repository concurrent-safe.
* Redesigning Cobra or exposing the internal command tree as a public API.
* Changing command business logic beyond the refactoring needed to read invocation-local options.

### Proposed Solution

Replace the package-level Cobra command tree with command factories. A fresh root command and fresh subcommands will be constructed for each call to `ExecuteWithOptions` and `ExecuteContext`. Each factory will bind flags to an invocation-local options value and close over that value, rather than binding flags to package-level variables.

`ExecuteWithOptions` will configure the newly constructed root with the supplied arguments, repository execution context, working directory, and Cobra input/output writers, then execute it. Because no invocation mutates a shared command tree or flag storage, separate calls can run sequentially or concurrently. The existing process-wide target registry remains unchanged and continues to be the explicit activation mechanism for target kinds.

## Detailed Design (as needed)

#### Command construction

* Add a `newRootCommand` factory that creates the root command and attaches fresh `run`, `config`, and `graph` command instances.
* Move flag declarations into the relevant command factory. Each command receives a local options struct containing its flag values.
* Pass local options into command handlers through closures or handler parameters. Handlers must not read mutable package-level flag variables.
* Keep reusable, non-Cobra helpers such as target loading, graph printing, configuration rendering, selector matching, and command execution independent of command-instance state.
* Remove package-level Cobra command instances and flag variables once all call sites and tests use the factories.

#### Execution entry points

* `ExecuteWithOptions` creates a fresh root for every call, sets its args and I/O streams, attaches the existing repository/working-directory execution context, and calls `ExecuteContext` on that root.
* `ExecuteContext` uses the same fresh-root path so the normal executable also avoids shared Cobra state.
* Nil stream options continue to resolve to the corresponding process streams.
* The injected repository override and working-directory semantics remain unchanged. Default invocations continue to discover the repository from the process working directory.
* The command factory must not modify `os.Args`, process streams, or another invocation's command state.

#### Tests

Add command-package regression tests that execute multiple commands in one process and assert independent buffers and results. At minimum, cover:

* an invocation with `run --dry-run` followed by one with `run --list`, proving `--dry-run` does not persist;
* an invocation with `run --list` followed by one with `run --dry-run`, proving `--list` does not persist;
* `config show --format hcl` followed by default `config show`, proving the format flag resets to JSON;
* separate injected repositories and working directories, proving repository context does not leak;
* concurrent invocations with different args, repositories, and output buffers, proving results remain isolated;
* the default `ExecuteContext` path, proving the normal executable behavior remains intact.

The example acceptance tests should continue to use `bytes.Buffer` through Cobra's configured writers and should not need subprocesses to test command isolation.

## Cross cutting concerns (as needed)

**Concurrency.** Fresh command trees remove Cobra's mutable per-invocation state from the shared process. Process-wide target registration remains shared, but registry snapshots and registration synchronization continue to govern its access. Any remaining shared state discovered in command handlers must be moved into the invocation context or made immutable.

**Compatibility.** The public `ExecuteContext` and `ExecuteWithOptions` entry points retain their signatures and behavior. The default command-line executable remains unchanged from a user's perspective.

**Failure handling.** Errors remain returned from the invocation that caused them and must be written only to that invocation's configured Cobra error writer. A failed invocation must not alter later invocations' arguments, flags, streams, or repository context.

**Testing.** Tests must avoid relying on execution order, process-global output, or a subprocess to establish isolation. Parallel tests must use independent repositories and buffers.

## Alternatives considered (as needed)

**Reset the shared command tree after each execution.** Rejected because cleanup is fragile: every flag, argument, output writer, and command-local mutable value must be reset on every success and error path. It also cannot make concurrent executions safe.

**Protect the shared command tree with a mutex.** Rejected because it serializes otherwise independent executions, retains hidden global state, and does not provide true isolation for callers that retain command references.

**Expose the root Cobra command and let callers configure it.** Rejected because it makes Cobra internals part of the embedding contract and leaves state ownership ambiguous. The command package should own construction and isolation.

**Only use Cobra `SetArgs` per call.** Rejected because `SetArgs` does not reset bound flag values, output writers, or state captured by command handlers.

## Future plans (as needed)

* Add a documented command-construction hook if hosts eventually need to add subcommands without forking the command package.
* Audit other package-level mutable state in command helpers as new embeddable use cases are added.
* Consider a typed execution request if repository-root and working-directory overrides need to be distinguished more explicitly.

## Other reading (as needed)

* `cmd/root.go` — current shared root command and execution entry points.
* `cmd/run.go` — runnable command flags and target execution path.
* `cmd/config.go` — configuration command flags and output path.
* `cmd/graph.go` — graph command flags and repository discovery.
* `examples/attehcl-custom-block/main_test.go` — current in-process command acceptance tests.
* Cobra command execution and `SetArgs`/`SetOut`/`SetErr` APIs.

## Implementation (ephemeral)

### Implementation notes

Added a standalone Cobra tree example under idr/202609130136-isolate-cobra-command-execution-state/. The example includes:

* a separate main.go entry point and cmd package;
* NewRootCommand, which constructs a fresh root and binds persistent --project and --verbose flags to invocation-local options;
* a direct hello subcommand;
* a config subcommand with nested show and check subcommands; and
* leaf handlers that read the persistent values through the local options passed by the command factories.

The example's Execute function constructs a new command tree for every call and configures its arguments and streams, demonstrating the intended ownership boundary without changing the production command package. It has its own go.mod and uses Cobra v1.10.2.

Verified from the example module:

* go test ./...
* go run . --project atte --verbose config show
* go run . --project atte hello

The production cmd package now follows the same factory structure. newRootCommand constructs fresh run, config, and graph command trees; each command binds flags to a local options value captured by its handlers; and ExecuteContext/ExecuteWithOptions execute a newly constructed root. Regression coverage exercises sequential flag and format isolation, repository and working-directory isolation, and concurrent executions.
