# Detector Glossary

> This glossary defines the vocabulary for detector composition in Atte.
>
> A Sensor observes repository state and contributes detection capabilities.
> A Builder collects Sensors and configuration during setup.
> A Scanner is the immutable runtime aggregate produced by compiling a Builder.
>
> The vocabulary described here is the target vocabulary for the detector
> package. Older plugin and registry terms are collected in the Previous
> Vocabulary section for historical reference.

## Sensor

A Sensor is one namespace-scoped detector implementation.

A Sensor observes repository state and contributes one or more capabilities to a Scanner. Sensors are the units that understand a particular representation of the Universe.

Examples include:

* Git repository Sensor
* Go dependency Sensor
* HCL target Sensor
* Rust package Sensor
* Docker image Sensor
* Kubernetes relationship Sensor

In the detector package, the base Sensor contract is:

```go
type Sensor interface {
    Namespace() string
}
```

The built-in attegit, attego, and attehcl implementations are Sensors.

A Sensor is not the compiled aggregate. It is one participant attached to a Builder.

## Sensor Capability

A Sensor Capability is an optional operation supplied by one Sensor.

Sensor capability interfaces use the Sensor suffix so their capability is explicit:

* GraphSensor provides graph construction.
* TargetSensor provides target discovery, selector presentation, and execution as one capability.
* EntityDecodingSensor provides entity-ID decoding.

The capability interfaces are:

```go
type GraphSensor interface {
    Sensor
    Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
}

type TargetSensor interface {
    Sensor
    Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    TargetSelector(graphtarget.ID) selector.Target
    ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
}

type EntityDecodingSensor interface {
    Sensor
    DecodeID(graph.EntityID) (graph.Entity, error)
}
```

Capabilities are optional. A Sensor may provide graph construction, target discovery, entity decoding, or any combination of them. Target discovery, selector presentation, and executable command construction are coupled: method-based Sensors are adapted as target-capable only when they implement the complete TargetSensor interface, and SensorSpec must provide all three functions or none.

Sensors may also expose HCL capabilities during attachment:

* SensorProvidingHCLFunctions supplies HCL function factories.
* SensorProvidingHCLTargetBlocks supplies HCL target-kind specifications.

AttachSensor discovers these interfaces and attaches the supplied HCL
capabilities to the Builder together with the Sensor.

A Sensor that needs runtime capabilities from other attached Sensors may
implement `SensorWithScanner`. During compilation, the Builder injects the
compiled Scanner through `AttachScanner(any)`. The Sensor validates the subset
it needs without importing the detector package; compilation returns an error
if injection fails.

## SensorSpec

A SensorSpec is an attachment-time description of one Sensor's optional capabilities.

SensorSpec is useful when attaching function-based capabilities directly rather than adapting a method-based Sensor:

```go
type SensorSpec struct {
    Namespace      string
    Graph          func(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
    Targets        func(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    TargetSelector func(graphtarget.ID) selector.Target
    ExecuteTarget  func(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
    DecodeID       func(graph.EntityID) (graph.Entity, error)
}
```

A SensorSpec is setup data. It is not a runtime Scanner.

## Builder

A Builder is the mutable setup-time object used to collect Sensors, target kinds, and HCL function factories.

A Builder:

* accepts Sensor and capability attachments
* validates duplicate namespaces, target kinds, and function names
* may be extended with application-specific Sensors and HCL capabilities
* is not safe for concurrent mutation
* must be compiled before it is used by runtime consumers

The intended setup flow is:

```go
builder, err := detector.NewDefaultBuilder()
if err != nil {
    return err
}

if err := detector.AttachHCLTargetBlock(builder, "deploy", spec); err != nil {
    return err
}

scanner, err := builder.Compile()
if err != nil {
    return err
}
```

Builder state is setup state. Applications should not pass a Builder to command execution, HCL evaluation, graph construction, or target discovery.

## Attach

Attach is the setup-time verb for adding capabilities to a Builder:

* Attach adds a SensorSpec.
* AttachSensor adapts and attaches a method-based Sensor.
* AttachHCLTargetBlock attaches a target-kind specification.
* AttachHCLFunction attaches an HCL function factory.

AttachSensor also discovers SensorProvidingHCLFunctions and
SensorProvidingHCLTargetBlocks implementations and attaches their HCL capabilities.

## Default Builder

NewDefaultBuilder creates a Builder containing Atte's built-in Sensors, target kinds, and HCL functions.

The default Builder includes the built-in attegit, attego, and attehcl Sensors. The attego and HCL Sensors are wired during compilation to receive the immutable Scanner rather than retaining the mutable Builder.

## Scanner

A Scanner is the immutable runtime aggregate produced by Builder.Compile.

A Scanner coordinates compiled Sensors and exposes the operations used by commands and direct APIs:

```go
type Scanner interface {
    Targets(context.Context, *attegit.Repo) ([]graphtarget.ID, error)
    TargetSelector(graphtarget.ID) (selector.Target, bool)
    TargetString(graphtarget.ID) (string, bool)
    TargetMatches(graphtarget.ID, string, string) bool
    ResolveTarget(context.Context, *attegit.Repo, string, string) (graphtarget.ID, error)
    ExecuteTarget(context.Context, *attegit.Repo, string, graphtarget.ID) (graphtarget.Execution, error)
    Graph(context.Context, *attegit.Repo, ...graphset.Option) (*graph.Graph, error)
    TargetKinds() map[attehcl.Kind]attehcl.TargetKindSpec
    DecodeID(graph.EntityID) (graph.Entity, error)
    HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}
```

A Scanner:

* contains an immutable snapshot of sensors made before compilation
* is safe for concurrent runtime use
* is isolated from later Builder mutations
* dispatches graph and target operations across Sensors
* renders, matches, and resolves targets using only the target Sensors in its compiled snapshot
* constructs execution commands through the Sensor that discovered each target
* dispatches entity-ID decoding by namespace
* supplies the capabilities required by HCL evaluation

Scanner-aware target operations belong to detector. The selector package retains only shared selector values and pure syntax/path helpers; it does not discover repository targets or maintain process-global Sensor mappings.

Scanner is the compiled runtime object because it actively scans repositories and builds detection results. It is not merely a map of sensors.

## Compile

Compile transforms mutable Builder state into an immutable Scanner.

Compilation:

* copies regular Sensor attachments, target-kind specifications, schemas, and HCL function factories
* injects the newly compiled Scanner into Sensors implementing SensorWithScanner
* adapts those Sensors into the compiled runtime snapshot
* returns an error if a Sensor rejects the Scanner
* produces a runtime value that does not retain the mutable Builder

A Builder may be compiled more than once. Each compilation produces an independent Scanner snapshot, so later Builder attachments do not affect previously compiled Scanners.

## Capabilities

Capabilities is the HCL package's aggregate interface for the capabilities required during HCL evaluation.

Capabilities describes what evaluation can use, not one attached Sensor:

```go
type Capabilities interface {
    TargetCapabilities
    FunctionCapabilities
    EntityCapabilities
}
```

The HCL Sensor accepts Capabilities rather than depending on the root detector package. This structural boundary prevents an import cycle when detector constructs the built-in HCL Sensor.

## TargetCapabilities

TargetCapabilities provides attached HCL target-kind specifications to the HCL evaluator.

```go
type TargetCapabilities interface {
    TargetKinds() map[Kind]TargetKindSpec
}
```

The returned target-kind map and schemas are defensive copies. Callers may inspect or modify the returned values without mutating the Scanner.

## FunctionCapabilities

FunctionCapabilities provides repository- and file-aware HCL functions to the HCL evaluator.

```go
type FunctionCapabilities interface {
    HCLFunctions(context.Context, *attegit.Repo, reference.Blob) (map[string]function.Function, error)
}
```

Factories receive the evaluation context, repository, and source file. They construct fresh function values for each evaluation scope.

## EntityCapabilities

EntityCapabilities provides entity-ID decoding to the HCL evaluator and graph projections.

```go
type EntityCapabilities interface {
    DecodeID(graph.EntityID) (graph.Entity, error)
}
```

Entity decoding is dispatched by the namespace encoded in the entity ID. This lets custom Sensors contribute entity kinds without a process-wide decoder registry.

## TargetKindSpec

TargetKindSpec describes the capabilities of one attached HCL target kind.

It contains the required decoder and optional graph, execution, configuration, and script projections.

TargetKindSpec belongs to attehcl because it describes HCL target semantics. Sensor attachment belongs to detector because it composes the complete runtime system.

## Namespace

A Namespace identifies a Sensor and prefixes the entity IDs that the Sensor owns.

Examples include:

* attegit for Git entities
* attego for Go entities
* attehcl for HCL target entities

Scanner uses the namespace in an entity ID to select the corresponding EntityDecodingSensor capability.

## Previous Vocabulary

The following generic terms were used by earlier versions of the detector design. They remain useful when reading historical records and migration notes, but new APIs should use the Sensor and Scanner vocabulary.

### Registry

Registry was the previous name for the compiled runtime capability interface in detector/plugin.

Registry is a reasonable setup-oriented term for a collection of attachments, but it is misleading for the immutable runtime object because that object actively executes detection operations.

Use Builder for setup state and Scanner for runtime state.

### Plugin

Plugin was the previous vocabulary for individual detector implementations and the detector/plugin package.

Use Sensor for a namespace-scoped implementation and Scanner for the compiled aggregate:

```text
Sensor       one namespace-scoped implementation
Scanner      the compiled aggregate of Sensors
```

These previous terms may appear in historical records or migration notes. They should not be introduced into new public APIs.

## scanner Local Variable

Scanner-aware Sensor code should use scanner as the local name for the
capability value supplied to `AttachScanner`:

```go
func (Detector) AttachScanner(value any) (any, error) {
    scanner, ok := value.(Capabilities)
    // ...
    return &Detector{scanner: scanner}, nil
}
```

The name communicates that the capabilities are supplied by the runtime
Scanner. It also avoids masking Go's predeclared `cap` function.

## Lifecycle

The intended lifecycle is:

```text
Create a Builder
    |
    v
Attach built-in and application-specific Sensors
    |
    v
Compile the Builder into a Scanner
    |
    v
Pass the Scanner to commands, HCL evaluation, graph construction, and target discovery
```

In short:

* Sensors provide capabilities.
* Builders collect attachments.
* Compile creates an immutable Scanner.
* Capabilities provide the HCL evaluation boundary.
