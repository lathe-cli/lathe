# Workflow conditional execution design

Status: stage 1 approved for implementation. Stage 2 is on hold, blocked on the
shared operation-invocation contract.

This document proposes conditional step execution for workflow commands. It
supersedes the `if` / `else` line in the Non-Goals section of `docs/workflow.md`
and replaces it with a narrower design.

## 1. Decision

Add step-level conditions through a `when` field. Do not add nested
`if` / `elif` / `else` blocks, and do not add a `switch` AST.

**Stage 1** adds `when`, restricted to workflow inputs and prior step outputs
that are already reachable today. It does not change failure semantics, and it
ships `CatalogSchemaVersion` 12.

**Stage 2** would add `allow_status` and a `${result.*}` namespace so conditions
can branch on API responses. It is **on hold**: a successful step's status code
is not exposed by the shared invocation contract today (section 6.1). Stage 2 is
reviewed
separately and reserves nothing in advance: no schema number is set aside, and
the field sketched in section 6.3 is a direction, not a commitment.

## 2. Motivation

Workflow commands are a linear sequence today (`pkg/runtime/workflow.go:111`).
Every declared step always runs. Two classes of real requests do not fit.

**Class A — the condition depends only on command arguments.**

- An optional flag drives an optional call: only call `Apps_SetLabel` when
  `--label` was provided.
- One command covers several argument shapes: `--type gpu` calls one operation,
  `--type cpu` calls another. This is the actual "if/else" request.
- A destructive step is gated on `--force`.

**Class B — the condition depends on a previous step's response.**

- Only call `Apps_Update` when the current status is `active` or `pending`.
- Create the resource only when it does not already exist.

Stage 1 covers guarded steps that are **independent or terminal** — a step that
nothing downstream references. It does **not** cover branch convergence, where a
later step needs a value from whichever branch ran; see section 4.5.

Within class B, stage 1 reaches only conditions whose deciding value appears in
a **successful** response body. Cases that need a status code, or that need a
non-2xx response to not abort the run, are stage 2.

## 3. Boundary

Conditions stay a builder-facing codegen input. End users see a normal Cobra
command and must not need to know a condition language exists.

This proposal does not change the rule from `docs/workflow.md`: a workflow
command is a linear composition of generated API operations. Anything needing
local environment checks, interactive prompts, or non-API IO stays handwritten
Go.

## 4. Stage 1: `when`

### 4.1 DSL

```yaml
workflow:
  version: 1
  commands:
    - use: deploy
      inputs:
        - name: kind
          flag: kind
          type: string
        - name: label
          flag: label
          type: string
      steps:
        - id: deploy_gpu
          uses: console.Apps_DeployGPU
          when:
            - value: ${input.kind}
              operator: in
              values: [gpu]
        - id: deploy_cpu
          uses: console.Apps_DeployCPU
          when:
            - value: ${input.kind}
              operator: in
              values: [cpu]
        - id: label
          uses: console.Apps_SetLabel
          when:
            - value: ${input.label}
              operator: notin
              values: [""]
```

`when` is a list of conditions. Conditions are joined with **AND**. Values
inside one condition's `values` are joined with **OR**.

The `input` / `operator` / `values` triple in Tekton's `WhenExpression` is the
closest prior art and the structure is borrowed from it. The key names are not:
`input:` collides with the workflow's own `inputs:`, and `expr:` would imply a
general expression language this design deliberately does not have. The left
operand is `value:`.

There is no `else`. Two complementary `when` blocks express a two-way branch, as
shown above. Lathe does not verify that conditions are mutually exclusive or
exhaustive; that is the builder's responsibility, and the docs must say so.

The two branches above are terminal — no later step reads their output. That
restriction is not incidental; see section 4.5.

### 4.2 Operators

Stage 1 ships exactly two operators: `in` and `notin`.

Comparison is string comparison. Both sides are normalized with the existing
`workflowString` helper (`pkg/runtime/workflow.go:296`), so a JSON number `404`,
the YAML scalar `404`, and the string `"404"` all compare equal. This must be
documented explicitly, because YAML parses `values: [404]` as an integer and
users will expect that to matter.

A missing or unset reference evaluates to the empty string rather than raising
an error. That gives existence checks without a dedicated operator:

```yaml
when:
  - value: ${input.label}
    operator: notin
    values: [""]        # "was --label provided and non-empty?"
```

This is a deliberate trade: an absent field and a present-but-empty field are
indistinguishable. Adding `exists` / `undefined` later is backward compatible.

Because the DSL must accept `values: [404]` as well as `values: ["404"]`, the
manifest type cannot be `[]string` — YAML would reject the unquoted form before
normalization ever runs. `values` parses as `[]any` (or through a custom
unmarshaller) and is stringified during normalization, so the generated spec
still carries plain strings.

### 4.3 Lenient evaluation

Condition evaluation **must not** reuse `evalWorkflowValue`
(`pkg/runtime/workflow.go:262`). That path raises errors for unknown inputs and
missing JSON paths (`workflowRefValue`, `pkg/runtime/workflow.go:270`), which is
correct for request construction and wrong for conditions — referencing a field
that may be absent is the normal case in a condition.

Stage 1 introduces a sibling evaluator that resolves the same reference syntax
but returns the empty string instead of an error when a reference does not
resolve. Request construction keeps the strict evaluator unchanged.

**Leniency covers missing data, not skipped steps.** A reference to a step in
the skipped set returns the sentinel from section 4.4 in *both* evaluators; only
the lenient one turns an unresolvable field within an existing step output into
an empty string. Without this distinction a step whose condition reads a skipped
step would silently compare against an empty value and reach a confident wrong
answer, instead of being skipped itself.

### 4.4 Skip propagation via a runtime skipped set

The runtime keeps a `skipped` set alongside `workflowState`
(`pkg/runtime/workflow.go:165`). A step is added to it when its `when` evaluates
false.

Propagation falls out of the strict evaluator. When `workflowRefValue` resolves
`steps.<id>` and `<id>` is in the skipped set, it returns a dedicated sentinel
error rather than `unknown step`. `workflowOperationInput` propagates it, and
the step that referenced a skipped step is itself marked skipped. Transitivity
is automatic, because that step is now in the set too.

**No dependency graph is generated, serialized, or validated at codegen time.**
An earlier draft of this document proposed precomputing skip edges and embedding
them in the generated spec. That is unnecessary: the sentinel gives the same
propagation with no graph structure, no new generated fields, and no second
source of truth to keep in sync. It also behaves identically if stage 2 later
makes conditions depend on responses, so the mechanism does not need revisiting.

Alternatives rejected:

- **Fail on reference to a skipped step.** Simple, but a user passing a valid
  flag combination gets an error naming an internal step ID.
- **Evaluate to null.** Nothing crashes, but nulls flow silently into request
  bodies.

### 4.5 What conditions cannot express: branch convergence

Complementary conditions produce two branches, but every `${steps.<id>}`
reference is statically bound to one named step. There is no way to write
"whichever of these ran".

```yaml
- id: deploy_gpu
  uses: console.Apps_DeployGPU
  when:
    - value: ${input.kind}
      operator: in
      values: [gpu]
- id: deploy_cpu
  uses: console.Apps_DeployCPU
  when:
    - value: ${input.kind}
      operator: in
      values: [cpu]
- id: notify
  uses: console.Apps_Notify
  params:
    deploymentId: ${steps.deploy_gpu.data.id}   # bound to one branch only
```

With `--kind cpu`, `deploy_gpu` is skipped, the sentinel propagates through
`notify`, and `notify` is skipped as well — even though a deployment did happen.

This is a consequence of section 4.4, not a defect in it. The alternative,
resolving a skipped reference to null, would send a null deployment ID to a
real endpoint. Skipping is the safer failure mode, but it means conditions are
appropriate only for:

- guarded steps that nothing downstream references
- branches that never rejoin

Branch convergence, and unified output assembled from whichever branch ran, are
**out of scope**. Supporting them does not require a branch AST — naming a value
across branches (an explicit alias or output binding) would be enough — but that
is a separate, smaller proposal and is not made here. The `deploy_gpu` /
`deploy_cpu` example in section 4.1 is well-formed precisely because those steps
are terminal.

### 4.6 Evaluation order inside the step loop

`when` must be evaluated **before** any host or auth work for that step.

Today the loop body loads host options and may refresh credentials
(`pkg/runtime/workflow.go:116-130`) *before* it builds the step input
(`:131`). Evaluating conditions in place would mean a step that is about to be
skipped still triggers credential loading and a possible token refresh — a
side effect for a request that is never sent.

The loop body is reordered to:

1. evaluate `when` with the lenient evaluator; if false, mark `skipped` and
   continue
2. build the step input with the strict evaluator; on the skipped sentinel,
   mark `skipped` and continue
3. load host options and auth
4. `InvokeOperation`

Step 2 moves ahead of auth for the same reason: a step skipped through
propagation must not touch credentials either.

### 4.7 Output

If `output.from` references a skipped step, the command emits the workflow step
summary instead, exactly as when `output.from` is omitted. It is not an error.
`executeWorkflow` evaluates `output.from` through the strict evaluator
(`pkg/runtime/workflow.go:154`), so this requires handling the sentinel there
explicitly rather than letting it surface as a command failure.

If every step is skipped, the command succeeds and emits a summary in which all
steps are `skipped`. This is a legitimate outcome, not an error.

## 5. Step status model

`WorkflowStepResult.Status` (`pkg/runtime/workflow.go:18`) gains one value in
stage 1:

| Status | Meaning | Introduced |
| --- | --- | --- |
| `ok` | step ran, response was 2xx | existing |
| `failed` | step ran and failed; workflow aborted here | existing |
| `skipped` | `when` was false, or a referenced step was skipped | stage 1 |

Stage 2 would add a fourth value for a tolerated failure. It is not reserved
here.

## 6. Stage 2 (on hold): `allow_status`

### 6.1 Why this is blocked

The canonical class B workflow — probe, then create when absent — cannot be
written with `when` alone. `DoRawFull` turns any non-2xx response into an
`*HTTPError` (`pkg/runtime/client.go:188`), `InvokeOperation` returns it, and
`executeWorkflow` aborts immediately (`pkg/runtime/workflow.go:142`). A 404
terminates the workflow before the next step's condition is ever evaluated.

**The blocker is the successful status code, and only that.** `OperationResult`
carries only `Data` and `DryRun` (`pkg/runtime/operation.go:31`). `DoRawFull`
does return a `RawResult` with `StatusCode` (`pkg/runtime/client.go:75`), but
`InvokeOperation` discards it and returns bytes. A successful step's status code
is therefore unreachable without changing `OperationResult`, a downstream-facing
type shared with every generated API command.

**A tolerated response is not lost.** `InvokeOperation` returns
`OperationResult{}, err` on any error (`pkg/runtime/operation.go:87`), but the
data survives: `HTTPError` carries both `Status` and `Body`
(`pkg/runtime/client.go:211`), and `DoRaw` already returns the body alongside
the same error (`pkg/runtime/client.go:204`). `executeWorkflow` can recover both
with `errors.As` without touching `OperationResult`. Whether an allowed non-2xx
response *should* write its body into `${steps.<id>}` is a semantic decision for
stage 2, not a plumbing obstacle.

This narrows the problem considerably. For a tolerated step the status code is
available today; only successful steps lack one. Shipping
`${result.<id>.status}` as defined-on-failure and undefined-on-success would be
an asymmetric contract not worth publishing, so stage 2 must either extend
`OperationResult` or restrict the namespace to tolerated steps and say so
explicitly. That choice touches a `pkg/**` type shared with generated CLIs,
which is a wider blast radius than stage 1 and belongs in its own review.

### 6.2 Sketch: status allowlist, not a boolean

Recorded so the direction is not relitigated later. This is not an approved
design.

```yaml
- id: probe
  uses: console.Apps_Get
  params:
    appId: ${input.app_id}
  allow_status: [404]
```

A boolean `continue_on_error: true` is rejected. `ClassifyError`
(`pkg/runtime/errors.go:52`) sorts errors into four classes and a boolean
swallows all of them, including `CodeGeneral` — which covers reference and
body-construction failures, i.e. builder bugs in the DSL that must surface
immediately.

Note that a server 401 is **not** `CodeNotAuthenticated`. That code comes from
the `ErrNotAuthenticated` sentinel, which means no host is configured locally
(`pkg/runtime/ctx.go:17`). Any HTTP status, 401 included, classifies as
`CodeAPIError`. The argument against a boolean rests on `CodeGeneral`, not on
auth classification.

An allowlist tolerates a failure only when the error is an `*HTTPError` **and**
its status is listed. Validation would require each entry to be in `400..599` —
an unbounded `>= 400` would accept 999.

`401` and `403` are still excluded: continuing past an auth failure makes every
later step fail for a reason the user can no longer see.

### 6.3 Sketch: the `${result.*}` namespace

The status code cannot live inside `${steps.<id>}` — that reference **is** the
response body (`state.steps[step.ID] = workflowStepValue(opResult.Data)`,
`pkg/runtime/workflow.go:148`), so any response with its own `status` field
would collide. A second namespace is needed.

Stage 2 would publish exactly one field:

| Reference | Type | Meaning |
| --- | --- | --- |
| `${result.<id>.status}` | number | HTTP status code |

`ok` is derivable from `status` and adds no expressive power. A `state` field
mirroring the step status adds almost none for conditions. Neither is worth
locking into public catalog JSON in a first release.

## 7. Codegen-time validation (stage 1)

Workflow specs are built and validated in `internal/lathecmd/workflow.go:52`,
alongside `validateWorkflowStepParams` and `validateWorkflowStepRefs`. Condition
validation belongs there, not in `internal/codegen/app/app.go`, which only
collects the finished specs.

- `when` conditions parse, and `operator` is `in` or `notin`
- `values` is non-empty
- every `${...}` reference in `when` resolves to a declared input or to a step
  declared earlier, reusing the existing reference checks

## 8. Catalog and verify contract

`CatalogWorkflowStep` (`pkg/runtime/catalog.go:83`) gains the step's conditions.
Agents inspecting the catalog must be able to tell that a step is conditional
without running it.

```json
{
  "id": "deploy_gpu",
  "operation_id": "Apps_DeployGPU",
  "http": {"method": "POST", "path_template": "/apps/deploy/gpu"},
  "when": [
    {"value": "${input.kind}", "operator": "in", "values": ["gpu"]}
  ]
}
```

`CatalogSchemaVersion` (`pkg/runtime/catalog.go:13`) goes `11` → `12`.

The DSL identifier stays `lathe.workflow.v1`. Stage 1 is a backward-compatible
addition: an existing workflow with no `when` generates byte-identical behavior.
`verify.go:192` compares this string exactly, so changing it would be a
gratuitous break.

`verifyWorkflowContract` (`pkg/lathe/verify.go:182`) gains: every condition in
catalog JSON has a non-empty `operator` and `values`. The verify report schema
does not change.

## 9. Open questions

For stage 1: none. `value:` is settled (section 4.1), schema 12 is settled, and
skip propagation is settled (section 4.4).

For stage 2, before it can be re-reviewed:

1. Does an allowed non-2xx response write its body into `${steps.<id>}`? The
   data is reachable either way (section 6.1), so this is a contract choice, not
   a plumbing one.
2. Is `${result.<id>.status}` defined for successful steps? If yes,
   `OperationResult` has to expose a status code without disrupting the
   generated API commands that share it. If no, the namespace is restricted to
   tolerated steps and the asymmetry must be documented.

## 10. Deliberate exclusions

Unchanged from `docs/workflow.md`, and not reopened:

- rollback and compensation
- loops, parallel steps, retry policy, backoff
- shell commands and external actions
- workflow-level dry-run
- arithmetic, regex, or general expression evaluation in conditions
- `else` blocks

Long-running operation polling is also out of scope. `OperationOptions.Wait`
exists (`pkg/runtime/operation.go:28`), but workflow steps never set it
(`pkg/runtime/workflow.go:138`) and `--wait` is only bound for generated
mutating commands (`pkg/runtime/build.go:190`). If workflows gain wait support,
its interaction with conditions is a new contract decided at that time.

## 11. Implementation order (stage 1)

1. `pkg/config/manifest.go` — `WorkflowStep.When`, parsing, normalization
2. `pkg/runtime/spec.go` — `WorkflowStepSpec.When`
3. `internal/lathecmd/workflow.go` — condition parsing into specs and reference
   validation, beside the existing `validateWorkflowStepRefs`
4. `internal/codegen/render/render.go` — serialize conditions in
   `workflowStepSpecsLiteral` (`render.go:684`)
5. `pkg/runtime/workflow.go` — lenient evaluator, skipped set and sentinel,
   reordered step loop, `skipped` status
6. `pkg/runtime/catalog.go` — catalog field, schema `12`
7. `pkg/lathe/verify.go` — contract check
8. `docs/workflow.md` — rewrite the Non-Goals entry, document conditions

## 12. Verification

- `pkg/runtime/workflow_test.go` — condition true and false; propagation through
  a skipped reference in `params`; **propagation through a skipped reference
  inside `when` itself**; transitive propagation; `output.from` pointing at a
  skipped step; all-steps-skipped; **a skipped step performs no host or auth
  loading**; **branch convergence skips the converging step** (section 4.5), so
  the documented limit is pinned by a test rather than by prose alone
- `pkg/config/manifest_test.go` — DSL parsing and rejection cases
- `internal/lathecmd/workflow_test.go` (new file) — reference validation for
  conditions
- `internal/codegen/render/render_test.go` — generated literal shape
- `pkg/lathe/verify_test.go` — new `workflow_contract` assertion
- `go test ./pkg/config ./internal/lathecmd ./pkg/runtime ./pkg/lathe`
- `make check` before PR

## 13. Rollback

Stage 1 can be withdrawn freely before release. Once schema 12 ships, the
conditional field is public catalog contract and cannot be retracted as though
it never existed — a later removal would need its own schema bump.
