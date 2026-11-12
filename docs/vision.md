# ProtoBot: Vision

> ProtoBot V2 · November 2026

**Contents:**

- [Purpose](#purpose)
- [Intended users](#intended-users)
- [Desired outcomes](#desired-outcomes)
- [Role in the Hermes pipeline](#role-in-the-hermes-pipeline)
- [A Git-native specification factory](#a-git-native-specification-factory)
- [What V2 keeps](#what-v2-keeps)
- [Authority](#authority)

---

## Purpose

ProtoBot is a Git-native specification factory. It turns prose into
precise EARS requirements, and those requirements are the authoritative
artifact from which code is derived.

The durable asset is the specification stored in Git: the Vision,
Architecture, requirements, and interfaces. Code and ordinary tests are
derived artifacts that converge on the approved requirement text. If
behavior needs to change, the change belongs in the specification first;
the implementation follows.

ProtoBot keeps this idea small enough for one person to understand and
operate. Git provides history, branches, review, and merge approval.
GitHub provides issue coordination and automation. ProtoBot does not
rebuild those primitives in a separate platform.

## Intended users

ProtoBot serves Red Hat teams that need to turn an idea into a precise,
validated specification and then into working, demonstrable software.

The primary users are:

- **Stakeholders and architects** who have researched an idea with
  IdeaBot and need a clear, reviewable statement of the intended
  behavior.
- **Engineers and specification authors** who refine source intent into
  complete EARS requirements and interface definitions rather than
  treating generated code as the source of truth.
- **Implementation and review agents** that use the approved
  specification as their shared input for coding, testing, and
  independent review.

The workflow supports both a single developer working locally and a team
collaborating through Git branches, pull requests, and reviews.

## Desired outcomes

A successful ProtoBot deployment achieves the following:

1. **Prose becomes precise requirements.** A stakeholder goal, source
   issue, or body of research is elicited into unambiguous EARS
   statements reviewed by humans.
2. **The specification remains authoritative.** Requirement text, not
   generated code, defines the intended behavior. Code and tests are
   derived evidence that the system is converging on that specification.
3. **Traceability stays visible.** Each active requirement can be traced
   to its Git history and to ordinary test evidence or an explicit,
   visible gap.
4. **Validation is deterministic.** Specification structure, EARS form,
   identifiers, references, and replacement rules are checked
   mechanically before implementation proceeds.
5. **Behavior is demonstrable.** A recorded revision has a reproducible
   acceptance procedure, expected results, and visible known limitations
   that a person can inspect.
6. **Agent handoff is well-defined.** The Drafting Table elicits and
   organizes the specification; the Fullsend coder and reviewer receive
   the approved requirements as their authoritative input.
7. **The system stays comprehensible.** The complete workflow fits
   within ordinary Git and GitHub concepts rather than a custom control
   plane.

## Role in the Hermes pipeline

ProtoBot is the second tool in the Hermes pipeline:

```text
IdeaBot --> ProtoBot --> TransferBot
(idea)     (specification) (product transfer)
```

- **IdeaBot** captures and researches ideas. Its artifacts provide the
  initial evidence, constraints, and assumptions for a new project and
  seed the first Drafting Table session.
- **ProtoBot** elicits precise EARS requirements, validates and reviews
  them, and hands the approved specification to implementation and
  review agents.
- **TransferBot** receives the specification and its test expectations,
  not the prototype's source code, so a product team can continue from
  what was specified and demonstrated.

The handoff preserves intent. It carries the specification revision,
interface definitions, test evidence, review conclusions, and known
gaps so the next stage does not have to reconstruct why the system
behaves as it does.

## A Git-native specification factory

The specification lives with the code as plain files in the repository.
A specification change is proposed as a pull request; human review and
merge approve it. The complete specification on the default branch is
the current target. Git history and merged specification pull requests
provide provenance without creating a separate delivery record.

GitHub Issues carry coordination and ownership, never behavioral
authority. Fullsend coders can work in parallel on disjoint requirement
scopes, while the Fullsend reviewer assesses their work independently
against the requirement text. Each reported result remains tied to an
exact repository revision.

This model makes the specification the common boundary between people
and agents. People decide what the system should do. Agents help
determine how to satisfy that description and produce the evidence that
they have done so.

## What V2 keeps

ProtoBot V2 keeps the capabilities that make requirement-driven
development trustworthy:

- **Specification validation** checks record structure, EARS form,
  identifier integrity, references, and replacement rules.
- **Traceability** connects active requirements to current test
  evidence, Git history, and any explicit gaps.
- **Agent handoff** gives drafting, implementation, and review agents a
  shared, human-approved description of the intended behavior.

These capabilities are the product. The surrounding platform is not.

## Authority

This document explains and organizes the intent behind ProtoBot V2. It
does not impose an obligation or add a condition.

EARS requirement text is the complete normative statement of behavior
or an explicit implementation constraint. The Vision, Architecture,
interfaces, metadata, and tests organize, explain, or provide evidence
for those requirements; none of them silently adds a condition or
outcome. An obligation exists only when the requirement text states it.
