# Governance

Lathe uses founder-led governance while the project is early. The goal is to keep decisions fast, technical, and aligned with the product boundary.

## Decision Principles

Maintainers prioritize:

- Spec fidelity over manually authored command behavior.
- Reproducible inputs over generated output drift.
- Runtime safety over convenient but surprising API execution.
- Agent inspectability over hidden behavior.
- Small, reviewable changes over broad rewrites.

## Maintainer Authority

Maintainers can:

- Accept or reject issues and pull requests.
- Request design changes before implementation.
- Close proposals that are outside Lathe's scope.
- Cut releases and define release notes.
- Enforce the Code of Conduct.

## Decision Process

Most changes are decided in issues and pull requests. A proposal should explain:

- The user problem.
- The affected surface.
- Why existing configuration, overlays, or generated behavior are insufficient.
- Compatibility impact for generated command shape, runtime catalog schema, auth, body handling, output, or generated Skills.
- Verification evidence.

For large changes, maintainers may ask for a short design issue before accepting an implementation PR.

## Compatibility

Lathe is pre-1.0. Breaking changes are allowed when they make the generated CLI safer, more correct, or easier to inspect, but they must be documented in release notes.

Catalog schema changes should bump `runtime.CatalogSchemaVersion` and explain migration impact.

## Releases

Maintainers cut releases from release tags. Release notes should explain user-visible changes, compatibility impact, and verification. Source archives, checksums, and installable binaries should be published for stable releases.

## Becoming a Maintainer

Maintainer access is earned through sustained, high-quality contributions that show judgment in Lathe's core areas: spec parsing, normalization, runtime behavior, catalog inspectability, docs, testing, and release hygiene.
