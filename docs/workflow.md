# Workflow Commands

Status: implemented v0.

## Purpose

Workflow commands let a CLI builder publish a normal command whose
implementation is a deterministic sequence of generated API operations.

The user-facing command should look like any other Cobra command:

```sh
mycli doctor -o json
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
- `if` / `else`, loops, parallel steps, or workflow-specific retry policy.
- Shell commands or external actions.
- Dynamic plugins or remote extension code.
- A public `GeneratedApp` or workflow engine ABI.
- Local diagnostic primitives.
- Workflow-level dry-run.

## Failure Semantics

Workflow commands run steps in order and stop at the first failure.

If step N fails, steps 1 through N-1 may already have changed remote state.
Lathe does not roll them back. API specifications do not provide a universal
undo contract, so automatic rollback would be a false transaction.

The current runtime returns a `WorkflowError` with the failed step ID and the
completed step summary. If `output.from` is omitted, successful workflows output
a small JSON step summary:

```json
{"status":"ok","steps":[{"id":"health","status":"ok"}]}
```

## Configuration

Workflow configuration lives in `cli.yaml` under `workflow`:

```yaml
workflow:
  version: 1
  commands:
    - use: doctor
      short: Check CLI API readiness
      inputs:
        - name: app_id
          flag: app-id
          type: string
          required: true
          help: App ID to inspect
      steps:
        - id: app
          uses: console.Apps_Get
          params:
            appId: ${input.app_id}
        - id: deployment
          uses: console.Apps_DeploymentStatus
          params:
            appId: ${input.app_id}
      output:
        from: ${steps.deployment}
```

`commands[].use` is mounted as a root command. The example above produces
`mycli doctor`.

## Operation References

Each step uses one generated API operation. The recommended reference form is:

```yaml
uses: <source>.<operationId>
```

For example, a source named `console` with `operationId: Apps_Get` is referenced
as `console.Apps_Get`.

Lathe also accepts generated command-path references for specs with awkward
operation IDs:

```yaml
uses: console apps get
uses: console.apps.get
```

Ambiguous references fail at codegen time.

## Inputs And References

Workflow inputs become normal command flags.

```yaml
inputs:
  - name: tenant_id
    flag: tenant
    type: string
    required: true
```

Supported input types are `string`, `int64`, `float64`, `bool`, and the matching
slice forms `[]string`, `[]int64`, `[]float64`, `[]bool`.

References use `${...}`:

- `${input.tenant_id}` reads a workflow input.
- `${steps.health}` reads the full JSON output of a prior step.
- `${steps.health.data.id}` reads a dotted path from a prior JSON output.

Step IDs must not contain dots. References to unknown inputs or later steps fail
at codegen time.

## Step Parameters And Bodies

`params` maps workflow values into the target operation's parameters by
operation parameter name or flag name. Codegen validates every key.

```yaml
steps:
  - id: app
    uses: console.Apps_Get
    params:
      appId: ${input.app_id}
```

JSON request bodies can be built with `set` and `set_str`, matching generated
API command body flags:

```yaml
steps:
  - id: create
    uses: console.Apps_Create
    set:
      input.name: ${input.name}
      input.replicas: "3"
    set_str:
      input.label: ${input.label}
```

## Output

If `output.from` is set, the command outputs that referenced value using the
normal `-o` formatter.

If `output.from` is omitted, the command outputs the workflow step summary.

## Runtime Model

Workflow commands call the same operation path as generated Cobra API commands.
They do not shell out to the CLI.

Generated API commands are thin Cobra adapters around:

```go
InvokeOperation(ctx, spec, input, opts) (result, error)
```

The invoker owns:

- Parameter validation and enum checks.
- Request path, query, header, form, and body construction.
- Auth and host behavior equivalent to generated API commands.
- Dry-run request resolution for generated API commands.
- Pagination and wait behavior.
- Output bytes and HTTP errors.

The generated workflow command owns:

- Step ordering.
- Input interpolation.
- Reading JSON fields from prior step outputs.
- Fail-fast behavior.

## Generated Output

Codegen emits workflow commands as static generated Go:

```text
internal/generated/workflows/workflows_gen.go
```

The generated package mounts workflow commands through `generated.Mount` after
generated API modules and before bundled Skill commands.

Workflow command names are reserved root command names. Codegen rejects
conflicts with:

- Lathe framework commands: `auth`, `commands`, `search`, `update`, `skill`,
  and `__lathe`.
- Generated module names.
- Generated shortcuts.
- Other workflow commands.

## Catalog Contract

Workflow commands are discoverable through the runtime catalog, but they do not
pretend to be single API operations.

Operation commands use:

```json
{"kind":"operation"}
```

Workflow commands use:

```json
{
  "kind": "workflow",
  "path": ["doctor"],
  "workflow": {
    "dsl": "lathe.workflow.v1",
    "output_from": "${steps.deployment}",
    "steps": [
      {
        "id": "app",
        "operation_id": "Apps_Get",
        "http": {"method": "GET", "path_template": "/apps/{appId}"}
      }
    ]
  }
}
```

The catalog schema version is `11` for this contract. Generated binaries with
workflow commands also attach capability:

```text
workflow.dsl
```

## Verification

`__lathe verify --json` adds a `workflow_contract` check when workflows are
compiled in:

- At least one workflow command is present when `workflow.dsl` is attached.
- Every workflow command appears in catalog JSON as `kind=workflow`.
- Every workflow command has workflow metadata.
- Every workflow step has an ID and operation HTTP metadata.

The verify report schema does not change.
