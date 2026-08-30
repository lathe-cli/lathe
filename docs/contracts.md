# Machine Contracts

Lathe exposes a small set of versioned contracts. Agents and downstream tools
must consume these contracts instead of generated Go details or codegen
internals. The defining constants and structs in code remain authoritative.

## Generated code schema

`runtime.SchemaVersion` in `pkg/runtime/spec.go` couples generated
`runtime.CommandSpec` literals to the runtime that executes them.
`runtime.AssertSchema` checks the version when generated modules mount and
fails with a regeneration instruction on mismatch.

This schema is a compiler coupling. It is not the agent-facing contract.
Bump it when a generated command or mount contract changes in a way that
requires regeneration.

## Capability contract

The runtime catalog is the agent-facing capability contract. Its version is
`runtime.CatalogSchemaVersion` in `pkg/runtime/catalog.go`. Agents must read
the contract from the running CLI:

```sh
<cli> commands --json
<cli> commands show <path...> --json
<cli> commands schema --json
<cli> search "<intent>" --json
```

`commands schema --json` reports `catalog_schema_version`, the committed
`surfaces`, and `dry_run.result`. `dry_run.result=http_preview` means a
preview prints the resolved HTTP request JSON (`method`, `url`, `hostname`,
`host_source`, `headers`, `body`, `auth`, `output`).

Catalog entries have two kinds:

- `operation`: one generated API operation, including HTTP, auth, `mutation`,
  `dry_run`, parameter, body, output, pagination, stream, and runtime-schema
  metadata. Operation `dry_run.mode` is `http_preview` only when the
  generated runner wired a real preview; otherwise it is `unsupported`.
- `workflow`: one generated workflow command, including its DSL version,
  steps, conditions, and referenced operation metadata. Workflow `mutation`
  is the heaviest step classification. Workflow `dry_run.mode` is
  `unsupported`.

`mutation` is `read`, `write`, or `unknown`. GET and HEAD are `read`. GraphQL
operations are classified from the request template (`query` / `mutation`),
not from HTTP POST. Other methods stay `unknown` unless the template proves
otherwise.

Framework commands such as `auth`, `commands`, `search`, `skill`, `update`, and
`__lathe` are discovered through `--help`; they are not operation entries.
`catalog.cli.capabilities` reports compiled first-party capabilities such as
`skill.bundle` and `workflow.dsl`.

Search is discovery only. Inspect the selected command with `commands show`
before execution. Read `mutation` and `dry_run` from that JSON; do not infer
write vs read from the HTTP method, and do not assume a `--dry-run` flag is
a preview contract. When `mutation` is not `read`, preview before execution
if `dry_run.mode` is `http_preview`; if preview is unavailable, obtain
explicit user confirmation before execution. Generated Skill files explain
this loop but never override the catalog.

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

`error.detail` is optional, locally constructed from spec metadata only
(flag names, declared value sets, required field names); it never echoes
user-provided values. It is a single bounded line. Human-readable output
prints it as a `Detail:` line between `Error:` and `Hint:`. Cobra unknown
command and unknown flag errors never carry a detail.

| Code | Exit |
| --- | ---: |
| `general` | 1 |
| `usage` | 2 |
| `api_error` | 3 |
| `not_authenticated` | 4 |
| `canceled` | 130 |

A configured stream pause is a successful terminal outcome and exits `0`.

## Host provenance

`auth status -o json` reports `hostname`, `source`, `selected`, and `hosts`;
dry-run output carries `hostname` and `host_source`. `source` is `flag`, `env`,
`selected`, `codegen-default`, or `unique`. The stderr `current host` notice is
for operators, not a machine contract.

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
