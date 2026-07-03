# Workflow Commands

Status: proposed.

## Purpose

Workflow commands let a CLI builder publish a normal command whose implementation
is a deterministic sequence of generated API operations.

The user-facing command should look like any other Cobra command:

```sh
mycli doctor --json
mycli deploy --app-id app_123
```

The workflow DSL is a builder-facing codegen input. It compiles into named
commands, and end users should not need to know a workflow DSL exists.

## Boundary

Workflow commands sit between generated API operations and handwritten custom Go:

- Use a workflow command when behavior is a linear composition of generated API
  operations.
- Keep handwritten Go when behavior needs local environment checks, custom
  target resolution, non-API IO, build metadata, interactive prompts, or other
  imperative logic.
- Keep overlays and source config for shaping individual generated API commands.

The first version is API-only. A Mosoo-style `doctor` command that checks target
resolution, local credential stores, build metadata, and raw HTTP reachability is
not fully covered by v0 unless those checks are exposed as generated API
operations. It can remain custom Go until diagnostic primitives are designed.

## Non-Goals

The first version does not support:

- Rollback or compensation.
- `if` / `else`, loops, parallel steps, or retry policy.
- Shell commands or external actions.
- Dynamic plugins or remote extension code.
- A public `GeneratedApp` or workflow engine ABI.
- End-user commands not explicitly named by the CLI builder.

## Failure Semantics

Workflow commands run steps in order and stop at the first failure.

If step N fails, steps 1 through N-1 may already have changed remote state. Lathe
does not roll them back. API specifications do not provide a universal undo
contract, so automatic rollback would be a false transaction.

Failures must be structured. JSON output should include:

- The failed step ID.
- The underlying error class.
- Completed step IDs.
- The successful step outputs needed for manual recovery.

Human output should explain that partial completion may have occurred.

## Configuration

Workflow configuration belongs in `cli.yaml` as its own domain block:

```yaml
workflow:
  root: workflows
  commands:
    - doctor.yaml
```

Paths are relative to the directory containing `cli.yaml`. The root defaults to
`workflows` when omitted.

Do not add an `extensions:` block for first-party workflow commands.

## DSL Shape

Each workflow file defines one user-facing command:

```yaml
version: 1
command:
  use: doctor
  short: Check CLI API readiness
  long: Check the configured API by running read-only generated operations.
  aliases: [check]

inputs:
  app_id:
    flag: app-id
    type: string
    required: true
    help: App ID to inspect

steps:
  - id: app
    operation: "console apps app-detail"
    params:
      appId: "${input.app_id}"
    capture:
      status: "app.status"
      deployment: "app.deployment.status"

  - id: deployment
    operation: "console apps app-deployment-status"
    params:
      appId: "${input.app_id}"
    capture:
      state: "deployment.status"

output:
  app_status: "${steps.app.status}"
  deployment_status: "${steps.deployment.state}"
```

`operation` is the generated command path shown by `commands show`. Codegen must
validate that the path exists and maps to exactly one generated operation.

`params` keys match generated parameter names or flags. Codegen must validate
that every key maps to a known `runtime.ParamSpec`.

`capture` and `output` use dot paths over JSON responses. Missing paths are
errors unless the field is marked optional in a later schema version.

## Runtime Model

Workflow commands must call the same operation path as generated Cobra API
commands. They must not shell out to the CLI.

First extract a runtime operation invoker:

```go
InvokeOperation(ctx, spec, input, opts) (result, error)
```

Generated API commands become thin Cobra adapters around that function. Workflow
commands call it directly with compiled `runtime.CommandSpec` values.

The invoker owns:

- Parameter validation and enum checks.
- Request path, query, header, form, and body construction.
- Auth and host behavior equivalent to generated API commands.
- Dry-run request resolution.
- Pagination and wait behavior.
- Output bytes and HTTP errors.

The generated workflow command owns:

- Step ordering.
- Input interpolation.
- Capturing JSON fields from step outputs.
- Structured partial-failure reporting.

## Generated Output

Codegen should emit workflow commands as static generated Go:

```text
internal/generated/workflows/workflows_gen.go
```

The generated package should mount workflow commands through `generated.Mount`
after generated API modules and before returning.

Workflow command names are reserved root command names. Codegen must reject
conflicts with:

- Lathe framework commands: `auth`, `commands`, `search`, `update`, `skill`,
  and `__lathe`.
- Generated module names.
- Generated shortcuts.
- Other workflow commands.

## Catalog Contract

Workflow commands should be discoverable through the runtime catalog, but they
must not pretend to be single API operations.

Add a command kind:

```json
{
  "kind": "workflow",
  "path": ["doctor"],
  "workflow": {
    "version": 1,
    "steps": [
      {"id": "app", "operation": ["console", "apps", "app-detail"]},
      {"id": "deployment", "operation": ["console", "apps", "app-deployment-status"]}
    ],
    "rollback": false,
    "branching": false
  }
}
```

This requires a catalog schema bump. Operation commands can use
`"kind": "operation"` or omit the field only if the schema defines that as the
default.

The binary should also attach a capability:

```text
workflow.dsl
```

## Dry Run

Workflow commands should expose `--dry-run`.

Dry-run must not send HTTP requests. It should resolve and print the ordered
plan, including each step's operation path and resolved request shape when all
inputs are available.

For JSON output, dry-run should use a stable shape so CI and agents can validate
workflow wiring without touching a real API.

## Verification

`__lathe verify --json` should add a workflow contract check when workflows are
compiled in:

- Every workflow command is mounted.
- Every workflow command appears in catalog JSON as `kind=workflow`.
- Every referenced operation resolves.
- Every workflow command dry-run completes without network access.
- Root command conflicts were rejected at codegen time.

The verify report schema does not need to change unless the check result shape
changes.
