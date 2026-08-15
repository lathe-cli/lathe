# Machine Contracts

Lathe's durable value is a small set of versioned, machine-readable contracts.
Generated CLIs, agents, and sibling tooling (installers, verifiers, registries)
integrate through these contracts, not through Lathe internals. Any new tool or
capability should answer one question first: which contract does it consume or
produce, and how is that contract versioned?

Code is the source of truth. Each section below points at the defining source
file; when this page and the code disagree, trust the code and fix this page.

## Generated code schema

- Version constant: `runtime.SchemaVersion` in `pkg/runtime/spec.go`.
- Couples generated `[]runtime.CommandSpec` literals
  (`internal/generated/<module>/<module>_gen.go` in downstream repos) to the
  runtime that executes them.
- Enforced by `runtime.AssertSchema` at mount time: a mismatch fails fast at
  startup with an instruction to re-run codegen.
- Bump the constant whenever `CommandSpec` semantics or the generated mount
  contract change in a way that requires regeneration.

## Command catalog

- Version field: `catalog_schema_version`, constant
  `runtime.CatalogSchemaVersion` in `pkg/runtime/catalog.go`.
- Served by `<cli> commands --json`, `commands show <path...> --json`,
  `search "<intent>" --json`, and `commands schema --json`.
- This is the agent-facing discovery contract and the source of truth for
  generated operation details: HTTP method and path template, auth
  requirements, source parameter names, CLI flags and positional alternatives,
  body schema, output, pagination, and stream collection hints.
- `catalog.cli.capabilities` lists compiled first-party capabilities such as
  `skill.bundle`. Capability commands are not catalog operations.
- Only generated API operation commands carry catalog entries. Framework
  commands (`auth`, `commands`, `search`, `skill`, `update`, `__lathe`) are
  discovered through `--help`, not the catalog.
- Consumers: agents following the documented loop (search, then
  `commands show`, then `auth status`, then execute), generated Skill files
  (guidance and indexes only, never execution authority), and external
  conformance tooling.

## Verify report

- Emitted by `<cli> __lathe verify --json`; implemented in
  `pkg/lathe/verify.go`.
- Version field: `version`, currently `1`.
- Shape: `{"version": number, "ok": bool, "checks": [{"name": string, "ok":
  bool, "error": string}]}`. The process exits non-zero when any check fails.
- This is the generated CLI's self-evidence: root help contract, catalog
  schema and JSON round-trip, per-command flag consistency, and an isolated
  unauthenticated `auth status` probe. When `skill.bundle` is compiled in, the
  `skill_install` check runs a temp-`HOME` install with an explicit Codex target.

## Structured errors and exit codes

- Defined in `pkg/runtime/errors.go`.
- JSON and YAML failures are written to stderr as
  `{"error":{"code":...,"message":...}}`. Optional fields are `hint`,
  `http_status`, `method`, `url`, and `server_body`. The server body is emitted
  only when it is valid JSON, after sensitive fields are redacted and the
  result is bounded.
- Error codes: `general`, `usage`, `api_error`, `not_authenticated`,
  `canceled`.
- Exit codes: `0` OK, `1` general, `2` usage, `3` API error,
  `4` not authenticated, `130` canceled. A collected stream pause remains a
  successful exit `0`; its pending data is the overlay-defined collected
  payload, not an error envelope.
- Consumers: agents and scripts that branch on failure classes instead of
  parsing prose.

## Durable inputs

- `cli.yaml`, parsed by `pkg/config.Load`, plus codegen-only keys
  (`skill.root`, `skill.include`) parsed in `internal/lathecmd`.
  `skill.bundle` is a first-party capability switch in the same domain block.
- `specs/sources.yaml`, parsed by `internal/sourceconfig`, pinning upstream
  spec refs per module.
- Optional overlay files, parsed by `internal/overlay`, merged at codegen time
  only.
- Together these are the reproducibility contract: generated output must be
  reproducible from these pinned inputs. Overlay concepts never reach the
  runtime.

## Rules

- The runtime catalog is authoritative for operation details; generated Skill
  files are guidance.
- Search results are discovery only; agents must inspect a command via
  `commands show` before executing it.
- A change to any surface above is product behavior: prove it with focused
  tests and treat the version fields as the compatibility gate.
