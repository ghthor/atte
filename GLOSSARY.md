# Atte Glossary

> **Atte** is a change attenuation engine that computes the minimum work required after a perturbation of a software Universe.
>
> Rather than rebuilding everything that *might* have changed, Atte first computes a conservative approximation of the affected Universe, then progressively attenuates that approximation into the smallest provably correct Work Set.

---

## Universe

The complete set of entities under consideration.

The Universe represents every object that participates in change propagation.

Examples include:

* Repositories
* Source files
* Libraries
* Packages
* APIs
* Generated artifacts
* Tests
* Deployment manifests
* Documentation

There is exactly one Universe.

Atte models software as a single, unified Universe regardless of how it is physically organized across repositories, languages, or build systems. Repository boundaries are implementation details, not computational boundaries.

Every analysis, Perturbation, Impulse, Propagation, and Attenuation occurs within the Universe.

---

## Unirepo

A unified software Universe composed of all entities under consideration.

Unlike a **monorepo**, which describes how source code is physically organized, a **Unirepo** describes the logical scope of analysis. A Unirepo may consist of one repository or thousands; what matters is that Atte models them as a single dependency graph.

Every Atte deployment operates on exactly one Universe, which defines its Unirepo.

---

## Entity

A single object within the Universe.

Everything known to Atte is modeled as an Entity.

Examples:

* Rust crate
* Java package
* Docker image
* Helm chart
* GraphQL schema
* Protocol Buffer
* Binary
* Test suite

---

## Relationship

A directional dependency between two Entities.

Relationships define how Perturbations propagate through the Universe.

Common relationship types include:

* imports
* depends on
* links against
* generates
* references
* consumes
* exposes
* implements

---

## Detector

A component that observes system-specific artifacts and records the
Entities and Relationships they reveal.

Detectors translate source code, manifests, build metadata, schemas, and
other representations into the language-agnostic structure of the
Universe. Their output provides the graph on which Propagation operates.

Examples include:

* Git repository detector
* Go dependency detector
* Rust package detector
* Java import detector
* Docker image detector
* Kubernetes relationship detector
* Protocol Buffer dependency detector
* CI workflow detector

A Detector identifies possible structure. It does not determine whether an
Entity experienced an observable change; that responsibility belongs to a
Refiner.

The detector package's composition vocabulary is defined in
[detector/GLOSSARY.md](detector/GLOSSARY.md). It defines Sensors, Scanners,
Builders, Sensor capabilities, and the HCL evaluation Capabilities boundary.

---

## Projection

A consumer-specific representation of an Entity or declaration derived from
its underlying identity and decoded data.

Projections adapt the same underlying object for a particular boundary without
changing the object itself. In Atte, a target may have independent Projections
for:

* graph Entities and Relationships
* executable command data
* configuration output

A Projection describes how an object is represented for a consumer; it does not
create a second identity or imply that every consumer can use the object.

---

## Perturbation

A modification to one or more Entities within the Universe.

A Perturbation is the originating event that introduces uncertainty into the system.

Examples include:

* Editing source code
* Updating a dependency
* Changing a compiler flag
* Modifying a schema
* Updating generated code

A Perturbation does not directly produce work. Instead, it generates one or more Impulses.

---

## Impulse

The propagation event produced by a Perturbation.

An Impulse represents a unit of change moving through the Universe. It is the beginning of Propagation.

Examples include:

* A source file was modified
* A package version changed
* A compiler configuration changed
* A generated artifact was regenerated

A single Perturbation may generate multiple independent Impulses.

---

## Propagation

The process by which an Impulse traverses Relationships within the Universe.

Propagation is intentionally conservative.

Its purpose is to answer:

> **"Where could this Impulse have propagated?"**

Propagation computes possibility, not certainty.

---

## Cone

The conservative region of the Universe that could be affected by an Impulse.

The Cone is the primary output of the Broad Phase.

Every Entity within the Cone is considered a Candidate for refinement.

The Cone intentionally over-approximates the true impact of a Perturbation.

Conceptually, it is analogous to a future light cone in physics: every Entity that could have been influenced by an Impulse belongs to the Cone.

---

## Broad Phase

The first stage of analysis.

The Broad Phase rapidly computes the Cone by propagating Impulses through the dependency graph.

Design goals:

* Extremely low latency
* High recall
* Language agnostic
* Scalable across very large Universes

False positives are acceptable.

False negatives are not.

---

## Candidate

An Entity contained within the Cone.

Candidates are potentially affected but have not yet been verified.

Every Candidate is processed independently during the Narrow Phase.

---

## Narrow Phase

The refinement stage.

Each Candidate is evaluated independently to determine whether its observable state actually changed.

The Narrow Phase is embarrassingly parallel.

---

## Refiner

A Narrow Phase analyzer responsible for evaluating a specific kind of Entity.

Examples include:

* AST comparison
* ABI comparison
* Public API comparison
* Symbol analysis
* Semantic hashing
* Build-and-compare
* Test impact analysis

Refiners determine whether a Candidate becomes a confirmed Impact.

---

## Attenuation

The defining operation of Atte.

Attenuation progressively removes false positives from the Cone until only confirmed Impacts remain.

Example:

```text
100 Entities in the Cone

↓

Attenuation

↓

4 confirmed Impacts
```

Unlike Propagation, Attenuation increases precision.

---

## Impact

A confirmed observable effect of an Impulse.

An Impact exists only when refinement determines that an Entity's externally observable state has changed.

Examples:

* Public API changed
* ABI changed
* Generated output changed
* Binary changed
* Test behavior changed

Not every Candidate in the Cone experiences Impact.

---

## Influence

The theoretical set of Entities that could be affected by an Impulse.

The Broad Phase computes Influence.

The Cone is the concrete representation of that Influence for a particular Perturbation.

---

## Frontier

The active boundary of Propagation.

As Impulses propagate, the Frontier expands through the dependency graph.

During Attenuation, the effective Frontier contracts as Candidates are eliminated.

---

## Observer

A component capable of inspecting an Entity without modifying it.

Observers provide information used during refinement.

Examples:

* AST parser
* ABI inspector
* Binary analyzer
* API extractor
* Hash calculator

---

## Signature

A representation of an Entity's observable state.

Signatures are compared during refinement.

Examples:

* Content hash
* ABI hash
* Exported symbol graph
* Public API fingerprint
* Semantic hash

---

## Semantic Signature

A Signature that captures externally observable behavior while ignoring irrelevant implementation details.

Two Entities with identical Semantic Signatures are considered equivalent, even if their implementations differ.

---

## Snapshot

A recorded state of the Universe at a specific point in time.

Snapshots enable Atte to compare the observable state of Entities across Perturbations.

---

## Work Set

The minimal set of confirmed Impacts requiring execution.

The Work Set is the primary output of an Atte analysis.

Consumers of the Work Set may include:

* Build systems
* Test runners
* Package managers
* Deployment systems
* CI pipelines

---

## Executor

A system that performs work on the Work Set.

Examples include:

* Building
* Testing
* Packaging
* Deploying
* Linting
* Code generation

Executors consume Atte's output but are not part of the Attenuation algorithm itself.

---

## Echo

A Candidate that was included in the Cone but eliminated during Attenuation.

An Echo represents a conservative false positive that produced no observable Impact.

Example:

```text
Shared library changes

↓

20 downstream Entities in the Cone

↓

19 Echoes

↓

1 confirmed Impact
```

Echoes demonstrate that the Broad Phase remained conservative while the Narrow Phase recovered precision.

---

## Resonance

The degree to which an Impulse propagates through the Universe.

High-resonance Perturbations produce large Cones.

Low-resonance Perturbations attenuate quickly.

Examples:

| ChangeDoes implus             | Resonance |
| ----------------------------- | --------- |
| Formatting change             | Very low  |
| Private implementation change | Low       |
| Public API change             | High      |
| Shared compiler configuration | Very high |

---

## Equilibrium

The state of the Universe after all Impulses have been fully attenuated and no additional work remains.

At Equilibrium:

* No unresolved Candidates remain.
* The Cone is empty.
* The Work Set is empty.
* The Universe is synchronized with all known Perturbations.

