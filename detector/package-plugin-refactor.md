# Detector Package Refactor

## Status

Implementation complete. This document records the package merge plan, naming decisions, and implementation outcome.

## Tasklist

- [x] Choose Sensor for an individual detector implementation.
- [x] Choose Scanner for the immutable compiled runtime aggregate.
- [x] Define Sensor capability names: SensorGraph, SensorTarget, and SensorEntityDecoder.
- [x] Define SensorSpec for registration-time capability descriptions.
- [x] Define the HCL capability names: Capabilities, TargetCapabilities, FunctionCapabilities, and EntityCapabilities.
- [x] Move the builder, scanner, registration, and built-in construction APIs into the root detector package.
- [x] Rename root detector capability interfaces to Sensor names.
- [x] Rename plugin registration and runtime types to SensorSpec and Scanner.
- [x] Update attehcl's capability boundary and implementation names.
- [x] Update commands, examples, tests, and documentation to use detector directly.
- [x] Remove the detector/plugin package and verify there are no active references.
- [x] Run the complete repository validation suite.
- [x] Mark the plan complete and record any deviations from the proposed design.

## Implementation outcome

The package merge is complete. The root detector package now owns the Sensor
contracts, Builder, immutable Scanner, SensorSpec, HCL function factories, and
built-in construction. The detector/plugin package has been removed.

The active API uses:

    detector.Sensor
    detector.SensorGraph
    detector.SensorTarget
    detector.SensorEntityDecoder
    detector.SensorSpec
    detector.Builder
    detector.Scanner
    detector.NewBuilder
    detector.NewDefaultBuilder

The HCL package keeps its capability boundary local to avoid an import cycle and
now uses Capabilities, TargetCapabilities, FunctionCapabilities, and
EntityCapabilities. Historical plugin and registry terms remain only in the
research history and the glossary's Previous Vocabulary section.

Validation completed:

* go test ./...
* go test -race ./detector/... ./cmd/...
* go test ./... in examples/attehcl-custom-block
* go test -race ./... in examples/attehcl-custom-block
* all required atte build, test, code-generation, rendered-output, and lint targets
* git diff --check

## Plan

The repository currently separates detector capability contracts in detector/ from the mutable setup builder and immutable runtime registry in detector/plugin/. The proposal is to merge those packages into detector/.

The motivation is that detector/plugin/ is not an independent domain. It is the setup and runtime composition layer for detectors. Removing the extra package should make the API easier to discover and eliminate the need for callers to import both detector and detector/plugin.

## Original design and rationale

The root detector package currently defines the contracts implemented by individual detector packages:

    type Sensor interface {
        Namespace() string
    }

    type SensorGraph interface {
        Sensor
        Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
    }

    type SensorTarget interface {
        Sensor
        Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    }

    type SensorEntityDecoder interface {
        Sensor
        DecodeID(graph.EntityID) (graph.Entity, error)
    }

The detector/plugin package currently defines Builder, Registry, a registration record containing optional Graph, Targets, and DecodeID functions, HCLFunctionFactory, the default constructors, and the capability aggregation logic.

The two packages therefore contain related but differently named parts of the same abstraction. The current plugin.Detector name is also incompatible with using detector.Scanner for the compiled runtime aggregate unless the registration record is renamed.

## Recommended package shape

Move registry.go, builtin.go, and the registry tests from detector/plugin/ into detector/ and remove the detector/plugin package. The detector implementation subpackages remain separate:

    detector/
        builtin.go
        interfaces.go
        registry.go
        registry_test.go
        attegit/
        attego/
        attehcl/
        graph/
        graphset/
        graphtarget/

The root detector package would contain both the contracts implemented by individual plugins and the builder/runtime that aggregates them.

## Recommended naming

### Individual plugin contracts

Rename the current root detector.Detector interface to Sensor and use Sensor as the prefix for its optional capabilities:

    // Sensor identifies one registered detector implementation.
    type Sensor interface {
        Namespace() string
    }

    type SensorGraph interface {
        Sensor
        Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
    }

    type SensorTarget interface {
        Sensor
        Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    }

    type SensorEntityDecoder interface {
        Sensor
        DecodeID(graph.EntityID) (graph.Entity, error)
    }

The prefix makes it clear that these are optional capabilities of one registered sensor, rather than general graph, target, or entity-decoding services. Sensor is also a good domain term for attegit, attego, and attehcl: each observes repository state and contributes a particular kind of detection capability. A Scanner then compiles and coordinates a set of sensors.

### Runtime aggregate

Use Scanner for the immutable compiled runtime interface:

    // Scanner executes the compiled detector sensors and scans repositories
    // for targets and relationships.
    type Scanner interface {
        Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
        Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
        TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec
        DecodeID(graph.EntityID) (graph.Entity, error)
        HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
    }

Scanner replaces the current plugin.Registry interface. It is an active runtime object that scans a repository, builds graphs, resolves entities, and supplies HCL capabilities rather than merely indexing registrations.

### Registration record

Rename the current plugin.Detector struct to SensorSpec:

    // SensorSpec describes the capabilities supplied by one detector sensor.
    type SensorSpec struct {
        Namespace string
        Graph     func(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
        Targets   func(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
        DecodeID  func(graph.EntityID) (graph.Entity, error)
    }

SensorSpec follows the existing TargetKindSpec naming pattern and avoids a collision with the runtime Scanner interface.

### Builder functions and methods

Keep the lifecycle names explicit:

    func NewBuilder() *Builder
    func NewDefaultBuilder() (*Builder, error)
    func (b *Builder) Compile() Scanner

Rename the current plugin.New function to NewBuilder. A bare detector.New would be too generic in the root package and would not communicate that it returns mutable setup state.

The registration API should become:

    func RegisterHCLBlock[K ~string](
        b *Builder,
        kind K,
        spec attehcl.TargetKindSpec,
    ) error

    func (b *Builder) Register(spec SensorSpec) error
    func (b *Builder) RegisterSensor(value Sensor) error
    func (b *Builder) RegisterHCLFunction(name string, factory HCLFunctionFactory) error

RegisterSensor is more accurate than RegisterDetector because the argument is one namespace sensor, while Scanner denotes the compiled aggregate.

## Example usage after the merge

Custom target registration would become:

    import (
        "github.com/ghthor/atte/detector"
        "github.com/ghthor/atte/detector/attehcl"
    )

    builder, err := detector.NewDefaultBuilder()
    if err != nil {
        return err
    }

    if err := detector.RegisterHCLBlock(builder, "deploy", spec); err != nil {
        return err
    }

    scanner := builder.Compile()

Command injection would use the runtime Scanner type:

    type ExecuteOptions struct {
        Detector detector.Scanner
    }

    err := cmd.ExecuteWithOptions(ctx, args, cmd.ExecuteOptions{
        Detector: scanner,
    })

Method-based built-in plugins would be registered as:

    builder.RegisterSensor(attegit.Detector{})
    builder.RegisterSensor(attego.Detector{})

The concrete implementations may continue to be named attegit.Detector and attego.Detector because their package-qualified names remain distinct from detector.Sensor and detector.Scanner.

## Alternative runtime names

### Engine

Engine is the strongest lower-churn alternative:

    type Engine interface { ... }
    func (b *Builder) Compile() Engine

It avoids renaming the existing root Detector interface and clearly communicates that the compiled value performs operations. detector.Engine is understandable and would require less API churn.

The tradeoff is that Engine is less specific than Scanner and leaves the root package with two unrelated runtime concepts: detector.Sensor for one sensor and detector.Engine for the aggregate.

### DetectionEngine

DetectionEngine is explicit but verbose. It may be useful if Scanner is considered too narrow because the runtime also builds graphs, decodes IDs, and provides HCL functions.

### Dish

Dish is memorable and could fit an intentional project metaphor, but it is not self-describing to a Go user. A value typed as detector.Dish does not indicate whether it is a builder, registry, runtime scanner, or detection result. It would require project-specific terminology to be learned before normal API usage became obvious.

Unless Dish is already established domain language elsewhere in the project, Scanner or Engine is preferable.

## Import dependency analysis

The current relevant dependency direction is approximately:

    detector/plugin
        |-- detector
        |-- detector/attegit
        |-- detector/attego
        `-- detector/attehcl

After merging, it becomes:

    detector
        |-- detector/attegit
        |-- detector/attego
        `-- detector/attehcl

The merge removes one package and removes the plugin package's import of the root detector package. It should not introduce a cycle with the current implementation.

The important constraint is that the built-in implementation packages must not import the root detector package. The root package will import attegit, attego, and attehcl to construct the default builder. If one of those packages imports detector, the dependency would become cyclic.

The current implementations already satisfy the root interfaces structurally and do not import the root detector package, so the existing code meets this constraint.

### attehcl capability boundary

attehcl currently defines a local Plugin interface and related capability interfaces for the capabilities required by HCL evaluation:

    type Plugin interface {
        TargetRegistry
        FunctionProvider
        EntityDecoder
    }

    type TargetRegistry interface {
        TargetKinds() map[Kind]TargetKindSpec
    }

    type FunctionProvider interface {
        HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
    }

    type EntityDecoder interface {
        DecodeID(graph.EntityID) (graph.Entity, error)
    }

Rename these to capability-oriented names:

    type Capabilities interface {
        TargetCapabilities
        FunctionCapabilities
        EntityCapabilities
    }

    type TargetCapabilities interface {
        TargetKinds() map[Kind]TargetKindSpec
    }

    type FunctionCapabilities interface {
        HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
    }

    type EntityCapabilities interface {
        DecodeID(graph.EntityID) (graph.Entity, error)
    }

NewDetector would then accept Capabilities, with scanner as the local variable name:

    type Detector struct {
        scanner Capabilities
    }

    func NewDetector(scanner Capabilities) Detector {
        return Detector{scanner: scanner}
    }

    func (d Detector) Graph(ctx context.Context, repo *attegit.Repo, options ...graphset.Option) (*graph.Graph, error) {
        return Graph(ctx, repo, d.scanner, options...)
    }

The name Capabilities describes what HCL evaluation consumes: a set of target-kind, function, and entity-decoding capabilities. It avoids confusing this aggregate with detector.Sensor, which identifies one registered namespace implementation.

These interfaces should remain local to attehcl rather than importing detector. The root detector package will import attehcl to construct the built-in HCL scanner, so changing attehcl to import detector would create:

    detector -> attehcl -> detector

The local structural interface is therefore still the correct cycle-avoidance boundary. detector.Scanner can satisfy attehcl.Capabilities without attehcl importing the root package.

The local variable name scanner communicates that the capabilities are supplied by the compiled Scanner. It also avoids masking Go's predeclared cap function.

## Benefits

- One public package for detector contracts, setup, and runtime execution.
- No detector/plugin import for callers that already use detector contracts.
- Clear distinction between an individual Sensor and the compiled Scanner aggregate.
- More discoverable default construction through detector.NewDefaultBuilder.
- More explicit low-level registration through SensorSpec.
- Fewer package-level names that duplicate the detector domain.
- The existing immutable Builder-to-runtime lifecycle remains intact.

## Costs and constraints

- The root detector package becomes a composition root and imports the built-in detector implementations.
- Importing detector for only the basic Sensor interface also brings the default-builder dependency graph into that package's build surface.
- Future built-in detector implementations must not import the root detector package.
- The root Scanner interface becomes broader than the current namespace-only interface because it includes HCL and graph capabilities.
- The rename changes public package and type names. The repository is the only consumer, so compatibility aliases for hypothetical external users are not necessary.
- Existing local variables named detector may be clearer as scanner or runtime after the type becomes detector.Scanner.

## Suggested migration sequence

1. Move registry.go, builtin.go, and the registry tests into the root detector package.
2. Rename the current root Detector interface to Sensor.
3. Rename GraphDetector and TargetDetector to SensorGraph and SensorTarget.
4. Rename EntityDecoder to SensorEntityDecoder.
5. Rename the current plugin detector record to SensorSpec.
6. Rename the compiled plugin.Registry interface to detector.Scanner.
7. Rename Builder.RegisterDetector to Builder.RegisterSensor.
8. Rename plugin.New to detector.NewBuilder.
9. Keep NewDefaultBuilder, Compile, RegisterHCLBlock, and RegisterHCLFunction with their current lifecycle meanings.
10. Update cmd, examples, tests, and documentation to import detector instead of detector/plugin.
11. Keep the attehcl capability-interface rename to Capabilities, TargetCapabilities, FunctionCapabilities, and EntityCapabilities.
12. Update attehcl.NewDetector and evaluator code to use the capabilities aggregate, using scanner for the local value supplied by the compiled Scanner.
13. Remove detector/plugin/ and run the full repository validation suite.
14. Update package and API documentation to explain that detector.Sensor is an individual implementation and detector.Scanner is the compiled runtime aggregate.

## Recommendation

Proceed with the package merge and use this vocabulary:

    detector.Sensor               one registered namespace implementation
    detector.SensorGraph          graph capability of one sensor
    detector.SensorTarget         target capability of one sensor
    detector.SensorEntityDecoder entity-ID capability of one sensor
    detector.SensorSpec           registration-time capability description
    detector.Builder              mutable setup state
    detector.Scanner              immutable compiled runtime aggregate

For HCL evaluation, use the capability vocabulary:

    attehcl.Capabilities
    attehcl.TargetCapabilities
    attehcl.FunctionCapabilities
    attehcl.EntityCapabilities

This provides a coherent hierarchy. Scanner is a better runtime name than Registry because the value actively scans repositories and builds results. Engine is a valid lower-churn alternative, but Dish should only be used if it is an intentional and documented project metaphor.
