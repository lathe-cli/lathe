# Machine Contracts

Lathe exposes a small set of versioned contracts. Agents and downstream tools
must consume these contracts instead of generated Go details or codegen
internals. The defining constants and structs in code remain authoritative.

## Generated code schema

`runtime.SchemaVersion` in `pkg/runtime/spec.go` couples generated
`runtime.CommandSpec` literals to the runtime that executes them.
`runtime.AssertSchema` checks the version when generated modules mount and
fails with a regeneration instruction on mismatch.

Bump this version when a generated command or mount contract changes in a way
that requires regeneration.

## Runtime catalog

The catalog is the agent-facing discovery contract. Its version is
`runtime.CatalogSchemaVersion` in `pkg/runtime/catalog.go` and is available
through:

```sh
<cli> commands --json
<cli> commands show <path...> --json
<cli> commands schema --json
<cli> search "<intent>" --json
```

Catalog entries have two kinds:

- `operation`: one generated API operation, including HTTP, auth, parameter,
  body, output, pagination, stream, and runtime-schema metadata.
- `workflow`: one generated workflow command, including its DSL version,
  steps, conditions, and referenced operation metadata.

Framework commands such as `auth`, `commands`, `search`, `skill`, `update`, and
`__lathe` are discovered through `--help`; they are not operation entries.
`catalog.cli.capabilities` reports compiled first-party capabilities such as
`skill.bundle` and `workflow.dsl`.

Search is discovery only. Inspect the selected command with `commands show`
before execution. Generated Skill files explain this loop but never override
the catalog.

Request body schemas publish reachable recursive targets under
`body.schema.definitions`, so `#/definitions/...` references remain resolvable
from the catalog contract.

## Verify report

`<cli> __lathe verify --json`, implemented in `pkg/lathe/verify.go`, emits a
versioned report and exits non-zero if any check fails. It validates the root
help contract, catalog serialization and flags, and an isolated auth-status
probe. Capability-specific checks are added only when compiled in:

- `skill_install` for `skill.bundle`
- `workflow_contract` for `workflow.dsl`

## Structured errors

`pkg/runtime/errors.go` defines machine-readable errors and exit codes. JSON
and YAML errors contain `code`, `message`, and `hint`; `error.http` may contain
only a numeric status. URLs, headers, bodies, credentials, and raw transport
errors are excluded.

| Code | Exit |
| --- | ---: |
| `general` | 1 |
| `usage` | 2 |
| `api_error` | 3 |
| `not_authenticated` | 4 |
| `canceled` | 130 |

A configured stream pause is a successful terminal outcome and exits `0`.

## Durable inputs

- `cli.yaml` defines generated CLI behavior and first-party capabilities.
- `specs/sources.yaml` declares API sources. Git sources are reproducible only
  when pinned to immutable refs; `local_path` deliberately follows the current
  local working tree.
- Overlay files provide codegen-time command and execution-policy changes,
  including contexts, runtime-schema preflights, and stream collection.
  Overlay concepts do not enter the runtime.

Generated Go, catalogs, and Skill files are outputs. Regenerate them from the
inputs; do not treat them as independent sources of truth.
