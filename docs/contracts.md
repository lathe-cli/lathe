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
`runtime.CatalogSchemaVersion` in `pkg/runtime/catalog_schema.go`. Agents must read
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

`mutation` is `read`, `write`, or `unknown`. An explicit overlay
`mutation: read|write` override wins over every inference. Otherwise GraphQL
operations are classified from the request template (`query` / `mutation`),
not from HTTP POST. Otherwise RFC 9110 safe methods (GET, HEAD, OPTIONS,
TRACE) are `read` and every other declared method is `write`; `unknown` is
reserved for operations whose method cannot be determined. Consumers that
previously treated `unknown` as dangerous now see `write` for the same
operations: handle it the same way, the classification is just more precise.

Framework commands such as `auth`, `commands`, `search`, `skill`, `update`, and
`__lathe` are discovered through `--help`; they are not operation entries.
`catalog.cli.capabilities` reports compiled first-party capabilities such as
`skill.bundle` and `workflow.dsl`.

Operation entries may carry `search_terms`: overlay-curated synonyms that
flow from the overlay through the generated `CommandSpec` into the catalog.
Search indexes them as identifying synonyms weighted like the summary text,
so a single curated term surfaces the command without outranking exact
command-name or operation-id matches. Generated Skill module references list
them per operation. Downstream CLIs must be regenerated to pick up
`search_terms` (SchemaVersion 15, CatalogSchemaVersion 22).

When JSON body flags are enabled, `body.set_only_fields` lists body fields
that received no typed flag (nested object properties); they remain settable
through `--set`, `--set-str`, or `--file` (SchemaVersion 16,
CatalogSchemaVersion 23).

Multipart commands expose `flags[].content_type`, the effective part media
type. A single concrete type is sent as declared. A comma-separated list or
wildcard is resolved per part: the first element equal to the sniffed media
type, otherwise the first matching `type/*` or `*/*` (sending the sniffed
type), otherwise the first concrete element, otherwise
`application/octet-stream`. An empty file-part content type, which is how
Swagger 2 `type: file` is represented, uses the file extension and then
`application/octet-stream`. OpenAPI 3 binary properties set an explicit
default, so a declared encoding or `application/octet-stream` wins over the
extension. Text parts omit the part header when the selected type is exactly
`text/plain`. `body.unsupported_fields` lists optional multipart properties
that were not given a flag. It does not describe a top-level `oneOf` or
`anyOf`. Codegen fails when one of those properties is required, when a
required object body has no supported part, or when the top-level multipart
schema is not an object even if the body is optional. An overlay
`ignore: true` on that command is the remedy (SchemaVersion 22,
CatalogSchemaVersion 29).

Table output may declare per-column `column_alignments` (`left` or `right`).
Currency `column_formats` default to right alignment unless overridden
(SchemaVersion 17, CatalogSchemaVersion 24).

Operation entries may carry `group_description` from document-level tags or
an overlay group summary (CatalogSchemaVersion 25). Generated declarations
reuse `CommandSpec.GroupShort`; SchemaVersion remains 17. Regenerate downstream
CLIs to pick up source descriptions.

Operation `auth.requirements` preserves OpenAPI security alternatives and
combinations. Any one requirement entry satisfies the command, and every
scheme inside an entry is required. An empty entry `{}` allows anonymous
access and sets `auth.required` to false. Scheme objects carry `name` and,
when declared, `type`, `scheme`, `in`, `param`, and `scopes`; they never
carry credential values. `auth.scopes` stays the sorted, deduplicated union
of those scopes. Workflow entries still aggregate `required` and `scopes`
and omit `requirements`. Protobuf and GraphQL operations declare none.
Satisfaction is checked against the resolved request: the stored credential
plus header and query parameters passed to the command. A bearer
`Authorization` satisfies `http` bearer, `oauth2`, and `openIdConnect`; basic
satisfies `http` basic; an `Authorization` value set by an API key credential
or a parameter satisfies any `http`, `oauth2`, or `openIdConnect` scheme; an
`apiKey` scheme is satisfied when its `param` header, query parameter, or
cookie is non-empty (a stored API key is sent in its configured header,
default `X-API-Key`). Undefined scheme names, mutual TLS, unknown scheme
types, custom authenticators, and a nil authenticator are left unchecked. A
request that satisfies no entry fails with `not_authenticated` before it is
sent, and `error.detail` lists the accepted alternatives.
(SchemaVersion 19, CatalogSchemaVersion 26). Regenerate downstream CLIs.

Search is discovery only. Inspect the selected command with `commands show`
before execution. Read `mutation` and `dry_run` from that JSON; do not infer
write vs read from the HTTP method, and do not assume a `--dry-run` flag is
a preview contract. When `mutation` is not `read`, preview before execution
if `dry_run.mode` is `http_preview`; if preview is unavailable, obtain
explicit user confirmation before execution. Generated Skill files explain
this loop but never override the catalog.

Search supports keyword containment for uncased-script text such as Chinese,
Japanese, and Thai, including combining marks. These matches score below
exact tokens, prefixes, and English stems. Latin infix matches remain excluded;
no language configuration is required.

## Verify report

`<cli> __lathe verify --json`, implemented in `pkg/lathe/verify.go`, emits a
versioned report and exits non-zero if any check fails. Report version is 2.
It validates the root help contract, catalog serialization and flags, and an
isolated auth-status probe. Capability-specific checks are added only when
compiled in:

- `skill_install` for `skill.bundle`
- `workflow_contract` for `workflow.dsl`

`provenance.schema_version` is the generator/runtime contract
(`runtime.SchemaVersion`). `provenance.catalog_schema_version` is
`runtime.CatalogSchemaVersion`. `provenance.sources` is compiled from
`specs/sources.yaml` and sync-state at codegen. `repo_url` is sanitized:
passwords, HTTP user info, query, and fragment are stripped; SSH user names
are kept; filesystem URLs are omitted.
Local sources have `kind` `local` and `reproducible` false, and never include
a path or SHA. Empty `sources` means the binary was generated before
provenance existed; it is not a verify failure. A git source is reproducible
only when both a public `repo_url` and a resolved SHA are recorded and it has no
`git` proto dependency, whose tag is not resolved to a recorded SHA; sync-state
records the sanitized `repo_url`, and codegen rejects a state whose recorded
`repo_url` differs from the configured one. Generated code from earlier releases still mounts
without regeneration.

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

For `api_error`, `message` stays `API request failed` and `detail` is filled
from the first available layer:

1. The response Content-Type is JSON and a string is extractable from a
   declared field (`message`; `error` as a string or `error.message`;
   `detail`; depth <= 2): detail is that message, control characters
   stripped, bounded to 240 runes. Bodies over 32KB are never parsed.
2. Otherwise, if the command catalog declares a `known_errors` entry matching
   the HTTP status, detail is its `cause`. Layers never stack.
3. Otherwise detail is absent and only the numeric status plus hint remain.

The raw response body is never emitted in any layer.

| Code | Exit |
| --- | ---: |
| `general` | 1 |
| `usage` | 2 |
| `api_error` | 3 |
| `not_authenticated` | 4 |
| `canceled` | 130 |

`not_authenticated` is also returned before any request when the stored
credential and request parameters satisfy no declared security requirement. `error.detail` then
lists the accepted alternatives from spec metadata only.

A configured stream pause is a successful terminal outcome and exits `0`.

## Static request-body validation

A compiled JSON `body.schema` enables local structural validation for commands,
workflows, public operation invocation, and HTTP dry-run previews. Invalid JSON,
supported type mismatches, and missing required fields fail before transport
with `usage` / exit `2`; error details contain body paths rather than body values.
The supported subset and template-payload boundary are documented in
[CLI usage](cli-usage.md#static-body-schema). SchemaVersion 18 added this
validation, including GraphQL coercion and ProtoJSON input metadata.
Regenerate modules before linking a newer runtime. Old modules fail mounting
with a regeneration instruction.

Binary downloads add `output.binary.flag`. The flag is required unless the
invocation is a dry-run: `--<output.binary.flag> <new-file>` writes a new file,
and `-` writes the raw bytes to stdout. The command refuses a path that already
exists, including a directory or symlink, and does not send the request. A
missing or unwritable directory is also usage and sends no request; the detail
includes the path-free system error. The final file appears only after a
complete 2xx response and is mode `0600`. When the declared type is specific,
an unexpected JSON (`application/json`, `*+json`) or `text/html` Content-Type
fails before any response byte is written. Declared `application/octet-stream`,
`*/*`, or a wildcard accepts the object's real type. Ctrl-C (SIGINT)
and non-2xx responses leave no output file. A process killed by another signal
may leave a hidden `.<name>.*.part` file next to the target. A failure while
publishing the file, after the response completed, is `general` / exit 1: the
request was sent and the output file is not written. Generated `SchemaVersion`
is 20 and `CatalogSchemaVersion` is 27: regenerate modules before linking the
new runtime. Old modules fail mounting with a regeneration instruction.
Workflow steps cannot consume binary responses. `runtime.InvokeOperation`
still buffers the response body; streaming applies to the generated command
path. An OpenAPI operation with a 2xx response that declares its own JSON or
event/ndjson media type is not binary. Swagger `produces` does not apply that
block.

## Parameter serialization

Generated requests follow the serialization declared for a supported parameter shape. An absent `style` means the location default: path `simple`, query `form` exploded, header `simple`, cookie `form`. When `style` is present, an absent `explode` means false, except `form`, where it means true. `allowReserved` is honored only for query parameters; elsewhere it is ignored. Codegen stores a style only when the pair differs from that location default (SchemaVersion 21, CatalogSchemaVersion 28).

| in | style | explode | type | wire |
| --- | --- | --- | --- | --- |
| path | simple (default) | any | scalar | `url.PathEscape(v)` |
| path | simple | any | array | `url.PathEscape(a),url.PathEscape(b)` |
| path | label | false / true | scalar | `.url.PathEscape(v)` |
| path | label | false / true | array | `.a,b` / `.a.b` |
| path | matrix | false / true | scalar | `;name=url.PathEscape(v)` |
| path | matrix | false / true | array | `;name=a,b` / `;name=a;name=b` |
| query | form (default) | true (default) | scalar or array | `k=v`, `k=a&k=b` |
| query | form | false | array | `k=a,b` (a comma inside an item is `%2C`) |
| query | spaceDelimited / pipeDelimited | false | array | `k=a%20b` / `k=a%7Cb` |
| query | spaceDelimited / pipeDelimited | true | array | `k=a&k=b` |
| query | any | — | — | `allowReserved: true` leaves `:/?[]@!$'()*,;` literal; `# & = + %` and space stay encoded |
| header | simple | any | scalar or pre-joined array | verbatim |
| cookie | form | any | scalar | `name=` plus `url.QueryEscape` with `+` replaced by `%20`, joined into one `Cookie` header with `; ` |

Query pairs are sorted stably by the unescaped key. Values use `url.QueryEscape`, so spaces are `+`, unless `allowReserved` restores the characters above. Path array flags are `[]string`. Header arrays stay strings and are entered already joined. Cookie parameters are appended after authentication cookies and never replace them. Path array parameters changed from `string` to `[]string`; an overlay shortcut preset or context binding on one now fails codegen with `repeated params are not supported` or `context parameter "<name>" must be a string`, and the remedy is to drop that preset or binding or point it at a scalar parameter.

`--dry-run` reveals a cookie pair only when it exactly equals the encoded `name=value` produced by a non-sensitive cookie parameter on that request. A cookie whose name contains `session`, `sid`, or `csrf` is sensitive, and so is any name already treated as a sensitive header or query name. An authentication cookie with the same name as a public cookie parameter is redacted unless its value is identical to the value passed on the command line.

Unsupported shapes fail codegen after overlays are applied. The error names the source, `METHOD /path (operationId)`, parameter name, and location, and tells the author to fix the specification or exclude the operation. An overlay `ignore: true`, or an OpenAPI 3 `expose` list that drops the operation, skips that failure. The operation still normalizes; it is rejected only when the command survives. Unsupported shapes are object parameters, a path style other than `simple`, `label`, or `matrix`, a query style other than `form`, `spaceDelimited`, or `pipeDelimited`, a header style other than `simple`, a cookie style other than `form`, `spaceDelimited` or `pipeDelimited` on a non-array, array cookies, and Swagger `collectionFormat: tsv` (`tabDelimited`).

Swagger 2 array query parameters without `collectionFormat` use the specification default `csv`: one comma-separated value (`k=a,b`) rather than repeated keys. `multi` stays repeated keys. `ssv` and `pipes` on a query map to `spaceDelimited` and `pipeDelimited`. Header arrays are not style-mapped and pass through as strings. Path arrays with `ssv` or `pipes` fail codegen, and the error names the original `collectionFormat`; path `multi` maps to `form` and fails the location check. `tsv` is `tabDelimited` and fails.

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
