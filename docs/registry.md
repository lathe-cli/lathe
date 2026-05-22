# Lathe Registry Design

Status: proposed.

## Purpose

Lathe Registry is a community layer for reusable, inspectable Lathe CLI
recipes. It lets users discover an API, inspect how its CLI is generated, and
reproduce the same CLI and Skill from pinned inputs.

The registry should aggregate generated Lathe applications without turning
Lathe into an arbitrary binary marketplace.

The core rule is:

> Contributors submit reproducible recipes, not generated code as the source of
> truth.

## Product Boundary

Lathe core owns the generator, runtime, catalog contract, Skill renderer, and
recipe validation rules.

The registry owns community recipes, generated indexes, build proof, generated
catalog snapshots, generated Skill bundles, and optional release artifacts.

This split keeps the core repository focused while letting the registry accept
frequent community updates.

```text
lathe
  owns: generator, runtime, recipe schema, validators, docs, examples

lathe-registry
  owns: community recipes, generated index, build proof, Skills, artifacts
```

## Why Not Keep Recipes In Lathe Core?

Community recipes will churn more often than Lathe itself. A registry entry may
update a pinned API version, fix an overlay, adjust auth notes, repair a broken
upstream spec URL, or refresh generated evidence. That should not block Lathe
core releases or mix third-party API maintenance with generator changes.

The durable standard belongs in Lathe. The shared content belongs in a separate
registry repository.

## Recipe Shape

Each registry entry is a small, reviewable directory:

```text
recipes/<name>/
  lathe.yaml
  cli.yaml
  specs/sources.yaml
  overlays/
  README.md
```

`lathe.yaml` is registry metadata:

```yaml
name: museum-api
display_name: Redocly Museum API
category: examples
homepage: https://github.com/Redocly/museum-openapi-example
description: Example museum service CLI generated from a public OpenAPI spec.
maintainers:
  - github: example-user
auth:
  type: none
  notes: Uses the public mock server for generated CLI smoke checks.
smoke:
  intent: list museum events
```

`cli.yaml` and `specs/sources.yaml` keep the existing Lathe input model. They
remain the authoritative generation inputs.

`overlays/` is optional polish for command names, summaries, examples, aliases,
grouping, visibility, and parameter help. Overlays must not become a hidden
runtime system.

`README.md` explains what the generated CLI targets, how auth works, and any
upstream limitations.

## Generated Outputs

Registry CI may publish these generated outputs:

- `catalog.json`: generated command catalog snapshot.
- `skills/<cli-name>/`: generated Skill directory.
- `build.json`: build metadata, Lathe version, source refs, and timestamps.
- `checksums.txt`: checksums for generated archives or binaries, when present.

Generated outputs are evidence and distribution material. They are not the
source of truth. A user should be able to regenerate them from the recipe.

## CI Contract

Every recipe pull request should pass a deterministic generation workflow:

1. Validate `lathe.yaml`.
2. Validate `cli.yaml` and `specs/sources.yaml`.
3. Sync pinned specs.
4. Run Lathe codegen.
5. Build the generated CLI.
6. Run catalog smoke checks.
7. Generate the Skill directory.
8. Update the registry index.

Minimum smoke checks:

```sh
<cli> commands schema --json
<cli> commands --json
<cli> search "<basic intent>" --json
```

Recipe-specific checks can add auth-free read-only commands when the API has a
public or mockable surface.

## Registry Index

The registry should publish a machine-readable index:

```json
{
  "schema_version": 1,
  "recipes": [
    {
      "name": "museum-api",
      "display_name": "Redocly Museum API",
      "category": "examples",
      "description": "Example museum service CLI generated from a public OpenAPI spec.",
      "recipe_path": "recipes/museum-api",
      "skill_path": "skills/museum-api",
      "catalog_path": "catalogs/museum-api.json",
      "source_refs": [
        {
          "name": "museum",
          "backend": "openapi3",
          "pinned_tag": "vX.Y.Z",
          "resolved_sha": "..."
        }
      ]
    }
  ]
}
```

The index should be easy for humans, Lathe, and agents to consume. It should
favor transparency over ranking or marketplace behavior.

## Proposed User Experience

These commands are product direction, not an implemented contract yet:

```sh
lathe registry search museum
lathe registry show museum-api
lathe registry init museum-api
lathe bootstrap
```

The first implementation can be simpler: clone `lathe-registry`, copy a recipe
directory, then run the existing `lathe bootstrap` flow.

Later, Lathe can add installation commands:

```sh
lathe install museum-api
lathe skill install museum-api
lathe skill install museum-api --codex
```

Binary installation should wait until the project has checksum, provenance,
platform, and retention rules.

## Skill Distribution

Generated Skills are a first-class registry output.

The Skill should point agents to Lathe's runtime catalog workflow:

1. Search with `<cli> search "<intent>" --json`.
2. Inspect the exact command with `<cli> commands show <path...> --json`.
3. Check auth with `<cli> auth status --hostname <host>` when required.
4. Execute only after flags, body, auth, HTTP path, and output hints are known.

This keeps the Skill small and durable because command details come from the
generated catalog, not from hand-written documentation.

## Contribution Policy

Good registry entries should be:

- Reproducible from pinned upstream inputs.
- Clear about auth requirements.
- Clear about upstream API ownership.
- Covered by catalog smoke checks.
- Small enough to review.
- Maintained by at least one reachable contributor.

Recipes should not vendor large upstream specs unless the upstream source cannot
be pinned or fetched reliably.

## Non-Goals

The first version should not:

- Accept arbitrary hand-written CLIs as registry entries.
- Treat generated code as the source of truth.
- Become a general package marketplace.
- Publish unsigned binaries as the primary distribution path.
- Mix third-party recipe churn into Lathe core releases.
- Guarantee business correctness for every upstream API operation.

## Rollout Plan

- Phase 1: document the recipe format and create one minimal example.
- Phase 2: add recipe validation and CI for a separate `lathe-registry` repository.
- Phase 3: publish a generated registry index with catalog and Skill snapshots.
- Phase 4: add Lathe CLI helpers for search, show, and init.
- Phase 5: evaluate binary distribution after provenance and signing rules are in place.
