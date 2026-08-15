# Workflow Commands

Workflow commands are generated root commands that run an ordered sequence of
generated API operations. Use them for API-only composition; use handwritten Go
when the command needs local IO, shell execution, interactive prompts, or other
imperative behavior.

## Configuration

Declare workflows in `cli.yaml`:

```yaml
workflow:
  version: 1
  commands:
    - use: deploy
      short: Deploy an application
      inputs:
        - name: app_id
          flag: app-id
          type: string
          required: true
      steps:
        - id: app
          uses: console.Apps_Get
          params:
            appId: ${input.app_id}
        - id: deploy
          uses: console.Deployments_Create
          params:
            appId: ${input.app_id}
          set:
            input.region: us-east-1
      output:
        from: ${steps.deploy}
```

`use` must be one root command name. Codegen rejects conflicts with generated
modules, shortcuts, other workflows, and the reserved roots `__lathe`,
`auth`, `commands`, `help`, `login`, `search`, `skill`, and `update`.

Workflow inputs become Cobra flags. Supported types are `string`, `int64`,
`float64`, `bool`, and their slice forms. Inputs also support the normal
`required`, `default`, `enum`, `format`, and `deprecated` metadata.

## Operation references

Each step names one generated operation. Prefer the stable source and
`operationId` form:

```yaml
uses: console.Apps_Get
```

Generated command paths are also accepted:

```yaml
uses: console apps get
uses: console.apps.get
```

Unknown or ambiguous operations fail during codegen.

## Values and request bodies

References use `${...}`:

- `${input.app_id}` reads a workflow input.
- `${steps.app}` reads a prior step's complete output.
- `${steps.app.data.id}` reads a dotted JSON path.

Step IDs cannot contain dots. References to unknown inputs, unknown paths, or
later steps fail validation or execution.

`params` uses the operation parameter name or generated flag name. Context-bound
parameters still resolve through the active CLI context before the operation is
invoked.

```yaml
params:
  appId: ${input.app_id}
```

Build JSON bodies with the same assignment model as generated operation
commands:

```yaml
set:
  input.name: ${input.name}
  input.replicas: "3"
set_str:
  input.label: ${input.label}
```

`set` applies the same scalar type inference as the generated `--set` flag;
`set_str` always writes a string.

## Conditional steps

`when` guards a step. Conditions on one step are ANDed; values within one
condition are ORed.

```yaml
when:
  - value: ${input.region}
    operator: in
    values: [us-east-1, eu-west-1]
  - value: ${input.label}
    operator: notin
    values: [""]
```

Only `in` and `notin` are supported. Comparisons use normalized string
values, so unquoted scalar values are valid. A missing reference inside a
condition contributes an empty string; a reference to a skipped step propagates
the skip.

A false condition sends no request and performs no host or credential loading.
Any later step that reads the skipped step is also skipped. If `output.from`
points to a skipped step, Lathe returns the step summary.

There is no `else` or branch merge. Complementary conditions can form two
branches, but Lathe does not prove they are exclusive or exhaustive.

## Execution and output

Steps run in declaration order through `runtime.InvokeOperation`, the same
request path used by generated API commands. Parameter validation, request
construction, contexts, auth, runtime-schema validation, HTTP execution, and
stream collection therefore keep the operation contract.

Execution is fail-fast:

- A failed step returns `WorkflowError` with the step ID and completed summary.
- Successful earlier remote changes are not rolled back.
- A skipped step has status `skipped`.
- A configured stream pause stops the workflow successfully, returns that
  step's collected data, and exits `0`; later steps do not run.
- Any other successful step has status `ok`.

Without `output.from`, Lathe returns a step summary:

```json
{"status":"ok","steps":[{"id":"app","status":"ok"},{"id":"deploy","status":"ok"}]}
```

With `output.from`, it formats the referenced value with the workflow's normal
`-o` output handling.

## Generated and agent-facing contracts

Codegen writes `internal/generated/workflows/workflows_gen.go` and mounts it
through `generated.Mount`. Workflow commands appear in the runtime catalog as
`kind=workflow` with `lathe.workflow.v1` metadata. Generated binaries attach
the `workflow.dsl` capability, and `__lathe verify --json` adds the
`workflow_contract` check.

Use the running binary's `commands schema --json` result as the current catalog
version; do not hard-code an old version in integrations.

## Deliberate limits

Workflows do not provide rollback, loops, parallelism, workflow-specific retry
or accepted-status policy, shell commands, local IO, plugins, or
workflow-level dry-run. Workflow steps do not expose operation-command controls
such as `--all`, `--max-pages`, or `--wait`. A non-success HTTP response stops
the workflow.
