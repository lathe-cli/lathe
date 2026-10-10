# Lathe CLI Usage

This is the canonical operator guide for the `lathe` generator and generated
CLIs. Architecture belongs in [Architecture](architecture.md); serialized
compatibility belongs in [Machine contracts](contracts.md).

## Generator Commands

| Command | Purpose |
|---|---|
| `lathe init` | Create a CLI-first application repository from a supported starter. |
| `lathe skill install` | Install Lathe's bundled Agent Skill. |
| `lathe specsync` | Stage declared API specs and record sync state. |
| `lathe codegen` | Generate Go command packages and optional Skill output. |
| `lathe bootstrap` | Run `specsync` and `codegen` in sequence. |
| `lathe version` | Print generator version metadata. |

Use `lathe <command> --help` for the current flags. Application initialization
has a separate [starter contract and maintenance guide](lathe-init-design.md).

## Install

Download the archive for your platform from the
[latest release](https://github.com/lathe-cli/lathe/releases/latest), unpack it,
and put `lathe` on `PATH`.

From this repository:

```sh
make build
./bin/lathe version
```

## Target Repository

A generated CLI repository owns:

- `go.mod`: downstream module path used by generated imports.
- `cli.yaml`: CLI identity and runtime capability configuration.
- `specs/sources.yaml`: declared Git or local API sources.
- Optional overlay files.
- `cmd/<cli-name>/main.go`: thin runtime entrypoint.

The normal build path is:

```sh
go mod init example.com/acme   # skip when go.mod exists
lathe bootstrap
go mod tidy
go build -o bin/acmectl ./cmd/acmectl
bin/acmectl __lathe verify
bin/acmectl __lathe verify --json
```

`__lathe verify` prints a human summary of CLI version, schema versions, and
compiled source provenance. `--json` emits the versioned report; read
`provenance` for schema versions and per-source revision.

## `cli.yaml`

### CLI and Auth

```yaml
cli:
  name: acmectl
  short: Command-line tool for Acme services
  command_path: auto

auth:
  default_type: apikey
  api_key_header: X-Auth-Token
  login:
    type: oauth_device
    start_path: /auth/device/start
    token_path: /auth/device/token
    refresh_path: /auth/device/refresh
    start_request:
      client_id: acmectl
      device_label: ${device_label}
    poll_request:
      client_id: acmectl
      device_code: ${device_code}
    poll_response:
      access_token: data.token
      contexts:
        organization: data.organization_id
  validate:
    method: GET
    path: /api/v1/whoami
    assert:
      field: data.id
      non_empty: true
    display:
      username_field: data.username
      fallback_field: data.email
```

`cli.command_path` supports:

- `auto` (default): flatten one safe module; namespace multiple modules.
- `namespaced`: always retain the module segment.
- `flat`: require one module and fail on root-command conflicts.

`auth.default_type` is `bearer`, `apikey`, `basic`, or `oauth`.
`oauth` requires `auth.login.type: oauth_device`. Interactive device login
opens the verification URL by default; `--no-browser` keeps the flow manual.
Expired OAuth bearer credentials refresh before execution when
`refresh_path` and a refresh token are available. `start_request` and
`poll_request` replace the default JSON bodies; values may be literals or the
supported `${hostname}`, `${provider}`, `${device_label}`, and poll-only
`${device_code}` placeholders. `poll_response` maps dot-separated response
paths for status, errors, tokens, expiry, user identity, and declared contexts.
Omitted mappings use the standard OAuth field names.

`auth.validate` proves that saved credentials work. `assert.field` resolves a
dot-separated JSON path; it and display paths require JSON. `non_empty: true`
rejects an empty field, or checks the raw response body when no field is set.
With neither an assertion nor display paths, any successful response is
accepted, including non-JSON.

### Host Selection

Host resolution order is `--hostname`, `$ACMECTL_HOST`, the selected host,
`http.default_hostname`, then the single logged-in host. With several hosts and
none of the above, the command fails and lists them.

```sh
bin/acmectl auth use api.acme.com
```

`auth use` marks the host `selected: true` in `hosts.yml`, as does the first
`auth login`. When the host is chosen implicitly and more than one is logged
in, the CLI prints `current host: <name>` on stderr; explicit selection is
silent.

### Active Contexts

Account-scoped defaults are opt-in:

```yaml
contexts:
  organization:
    env: ACMECTL_ORG_ID
    local_set: true
```

An overlay binds an operation parameter to the context:

```yaml
commands:
  list-projects:
    params:
      organization_id:
        context: organization
  switch-organization:
    context:
      set_on_success:
        name: organization
        from_param: organization_id
```

Resolution order is explicit operation flag, declared environment variable,
then the selected host's stored value. `auth context status|unset` is generated
when contexts exist; `set` is added only for entries with `local_set: true`.
A selector operation persists its declared context only after successful
completion.

Nested `contexts` lists describe possible active-context fallback. Workflow
step mappings suppress context metadata for the supplied parameter. Runtime
schema mappings suppress it only for literals and references guaranteed by a
required parameter flag or a nonempty default. Optional references and required
body fields supplied through JSON retain fallback metadata.
Omitted parameter flags leave schema-source context fallback available; explicit
values, including empty strings, zero, and false, override it. The catalog schema
is unchanged.

### Generated Skill

```yaml
skill:
  root: skills
  include: internal/skill-include
  bundle: true
```

- `root: ""` disables Skill generation.
- `include` merges repo-local resources into the generated Skill.
- `bundle: true` embeds the Skill and mounts `<cli> skill install`.

Object-form `include` supports per-file `append`, `create`, `replace`, and
`omit` policies. Includes may target `SKILL.md`, `agents/`, `references/`,
`scripts/`, and `assets/`. Dotfiles, symlinks, traversal, and paths inside
the generated Skill root are rejected.

When bundling is enabled, codegen runs `go mod tidy`. The bundle's Kitup
dependencies resolve to the versions required by the Lathe runtime in
`go.mod`. Keep that runtime at the generator's version.
Inside a Go workspace (`go env GOWORK` is set), codegen skips `go mod tidy`;
run it yourself before building with `GOWORK=off`.

### Version and Update

Generated CLIs expose `--version` and `-v`. Supply `Version`, `Commit`, and
`Date` through `lathe.RunOptions` or Go `-ldflags`; they are build metadata,
not manifest data.

Optional GitHub Release self-update configuration:

```yaml
update:
  github:
    owner: acme
    repo: acmectl
    asset: "acmectl_{{ .Version }}_{{ .OS }}_{{ .Arch }}.tar.gz"
```

The release asset must expose a `sha256:` digest. Archive assets must contain a
binary named after `cli.name`. Update asks before replacing the executable
unless `--yes` is passed.

### Workflows

`workflow.commands` compiles API-only multi-step commands into the generated
binary. Its DSL, failure model, conditional execution, and limits are owned by
[Workflow commands](workflow.md).

## `specs/sources.yaml`

```yaml
sources:
  users:
    repo_url: https://github.com/acme/users-api.git
    pinned_tag: v1.4.0
    backend: openapi3
    openapi3:
      files: [openapi.yaml]
      expose:
        operation_ids: [Users_List, Users_Get, Users_Create]

  accounts:
    repo_url: https://github.com/acme/accounts-api.git
    pinned_tag: v2.1.0
    backend: proto
    proto:
      staging:
        - from: api/proto
          to: "."
      entries: [v1/accounts.proto]

  console:
    repo_url: https://github.com/acme/graphql-console.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema/console.graphql
      expose:
        queries: [listApps, getApp]
        mutations: [createApp]

  local:
    local_path: ../service
    backend: openapi3
    openapi3:
      files: [openapi/service.yaml]
```

| Field | Contract |
|---|---|
| `repo_url` + `pinned_tag` | Immutable Git input. Floating branches are rejected. |
| `local_path` | Explicit working-tree input. Relative paths resolve from `sources.yaml`; it cannot be mixed with Git fields. |
| `backend` | `swagger`, `openapi3`, `proto`, or `graphql`. |
| `swagger.files` | Swagger 2.0 JSON files. |
| `openapi3.files` | OpenAPI 3.x JSON or YAML files. |
| `openapi3.expose.operation_ids` | Optional exact allowlist; every configured ID must match exactly once. |
| `proto.entries` | Entry files whose annotated RPCs may become commands. Imported dependencies never add commands. |
| `proto.dependencies` | Explicitly staged immutable Buf, Go module, or Git dependencies. |
| `graphql.expose` | Required query/mutation allow policy; GraphQL has no implicit expose-all mode. |

Buf dependencies require a module, commit, and digest; Go module dependencies
require a version and checksum; Git dependencies require `repo_url` and
`pinned_tag`. Staging never overwrites different content at the same include
path.

GraphQL policy can also define operation grouping, output hints, and selection
depth/pruning. Generated GraphQL commands execute `POST /graphql` with a baked
`{query, variables}` envelope. Scalar and enum arguments become typed flags;
input-object leaves become dotted variable flags. Relay-shaped results can use
body-cursor pagination.

## Overlays

Overlays own bounded behavior that upstream specs cannot express cleanly. They
are merged during codegen and never read at runtime.

### Command Shape

OpenAPI 3 and Swagger document-level `tags[].description` supplies group help
and generated Skill group introductions. An overlay `groups.<name>.short`
overrides it. Undocumented groups retain `<group> operations` help. When an
overlay moves a command, it uses the destination group's description; the
source group's description does not follow the command. Conflicting non-empty
tag descriptions across spec files warn and keep the first non-empty description.
Group summaries use the first non-empty line; group help and the catalog retain
the full description.

```yaml
groups:
  users:
    short: Manage users and access

commands:
  get-user:
    short: Get one user
    aliases: [show-user]
    params:
      user_id:
        argument: id
        help: User ID
  delete-user:
    hidden: true
```

Supported uses include command/group summaries, aliases, examples, shortcuts,
visibility, parameter help/defaults/required tightening/deprecation, positional
alternatives, active contexts, runtime schema preflights, JSON body flags, and
stream collection. Unknown groups and conflicting root names fail codegen.
Command overrides apply only to an exact command/match; unmatched command
entries and most unmatched parameter entries are ignored. An `argument` entry
for an unknown parameter fails validation.

`ignore: true` removes a command. `hidden: true` keeps it out of normal help,
search, and catalog output; `--include-hidden` can still inspect it. Root
shortcuts remain visible and executable even when their canonical command is
hidden. Those shortcuts have their own catalog entries and appear in generated
Skill guidance. Fully hidden groups and modules stay executable but are omitted
from help; a group with any visible command remains visible.

### Multipart Input

OpenAPI `multipart/form-data` object properties and Swagger `formData`
parameters become command flags. These commands do not accept the JSON body
builder's `--file`, `--set`, or `--set-str` flags.

| Schema | Flag | Wire |
| --- | --- | --- |
| `string` with `format: binary`, type absent with `format: binary`, or empty `{}` | one local file path | one file part; `filename` is the base name; default `application/octet-stream` |
| `string`, `number` | string | one text part; default `text/plain` |
| `integer` | int64 | one text part; default `text/plain` |
| `boolean` | bool | one text part; default `text/plain` |
| object, or type absent with `properties` / `additionalProperties` | JSON text, sent as written | one text part; default `application/json` |
| array of binary or `{}` | repeatable file paths, comma-separated or repeated | one file part per path, same name; default `application/octet-stream` |
| array of string or number | repeatable strings, comma-separated or repeated | repeated text parts; default `text/plain` |
| array of integer or boolean | repeatable int64 or bool values | repeated text parts; default `text/plain` |

`encoding.<property>.contentType` overrides the default for every part of that
property. It is a comma-separated list of media types or wildcards such as
`image/*` and `*/*`. Invalid elements are dropped. When several types remain,
the runtime picks the first element equal to the sniffed type, otherwise the
first matching wildcard (and emits the sniffed type), otherwise the first
concrete type, otherwise `application/octet-stream`. File parts always send
`Content-Type`. Text parts send it only when the selected type is not exactly
`text/plain`. OpenAPI binary parts with no `encoding.contentType` send
`application/octet-stream`. Swagger `type: file` cannot declare a part type.
When that part's content type is empty, the runtime uses the file extension
and otherwise sends `application/octet-stream`.

A property written as one `allOf` entry around a `$ref` uses the referenced
schema and keeps the wrapper description. A body may omit `type: object` when
it has `properties` or `additionalProperties`. An `allOf` of object schemas,
including a `$ref` to that `allOf`, is merged into one set of part flags.

Optional properties whose shape cannot be represented — an array of objects,
an array of arrays, a property that is `oneOf` or `anyOf` only, or an
unresolved reference — are omitted and listed in the catalog as
`body.unsupported_fields`. That list is per property, not a dump of a
top-level composition. A required property of that shape fails codegen. A
multipart body whose top-level schema is still not an object after that merge
— `oneOf`, `anyOf`, a non-object type, or an unresolved schema — fails
codegen whether or not the body is required. A required object body with no
supported part fails the same way. Ignore that command in an overlay
(`ignore: true`) or change the spec. `readOnly` properties are not sent and
are not listed. File paths that contain commas need CSV quoting on `[]string`
flags. Object parts are not checked as JSON before they are sent.

### Parameter Serialization

Path array flags are comma-separated. Each item is escaped on its own, then joined in the path. Header array values are entered already joined, and the runtime copies that string into the header. Cookie flags are sent together in one `Cookie` header, appended after authentication cookies.

Query arrays follow the declared style: repeated keys when the style is exploded, or one delimited value when it is not. `allowReserved` applies only to query parameters.

These shapes fail codegen: object parameters, a style the location does not support, `spaceDelimited` or `pipeDelimited` on a non-array, array cookies, and Swagger `collectionFormat: tsv`. The check runs after overlays. `ignore: true` on that command, or an OpenAPI `expose` list that leaves the operation out, removes the failure. Swagger array query parameters with no `collectionFormat` send one comma-separated value, which is the `csv` default.

Regenerate modules for SchemaVersion 21 and CatalogSchemaVersion 28 before upgrading the runtime. Catalog flags include `style`, `explode`, and `allow_reserved` only when they differ from the location default.

### JSON Body Flags

Opt in per command to turn a flat JSON object body into typed flags. Default
codegen is unchanged.

```yaml
commands:
  update-limits:
    body:
      flags: true
```

Supported property types are string, number, integer, boolean, nullable scalars,
and scalar arrays. Nested object properties (including arrays of objects) are
skipped instead of failing codegen: top-level scalar and scalar-array
properties still become typed flags, while the skipped fields stay reachable
through `--set`, `--set-str`, or `--file`. The `--set`/`--set-str` help and the
catalog `body.set_only_fields` list name those fields. Flattening nested
fields into typed flags (e.g. `--limits-max-budget-usd`) is a possible future
extension, not current behavior. A body whose properties are all nested
objects, plus maps, leftover `oneOf`/`anyOf`/`allOf` after nullable
flattening, multipart bodies, and GraphQL templates still fail codegen.
Property descriptions become flag help, and typed scalar or array-item enum
flags are validated locally. Required properties may be supplied by a typed flag,
`--file`, `--set`, or `--set-str`; omitting an optional body remains valid.
`--file` cannot be combined with `--set`, `--set-str`, or body flags on these
commands. The same field cannot be set by a typed flag and `--set`. Explicit
`null` still uses `--set field=null`. Upgrading to this capability requires
regenerating modules against the matching runtime and updating catalog consumers
for the new body-location and array-item enum contract.

### Static Body Schema

JSON request bodies with a compiled `body.schema` are validated locally before
HTTP execution, including `--file`, `--set`, `--set-str`, typed body flags,
workflow steps, and `--dry-run`. Validation covers declared object, array, and
scalar types, nullable values, nested properties/items, required fields, and
`allOf` constraints from resolved references. Template bodies validate the
payload at `body.merge_path`, such as GraphQL `variables`. GraphQL schemas
preserve nullability; list singleton coercion and ID string/integer inputs use
union schemas, and custom scalars remain untyped. These shapes do not acquire
stricter static type validation. Protobuf schemas preserve field nullability,
numeric and enum string/number alternatives, and well-known ProtoJSON shapes
such as timestamp strings and arbitrary `Value` JSON. Regenerate modules
before upgrading the runtime; older generated modules fail mounting with a
regeneration instruction.

Failures exit with usage code `2` and identify the body path without including
body values. Optional omitted bodies, commands without a compiled schema, and
non-JSON bodies retain their existing behavior.

This is structural validation of the compiled subset, not full JSON Schema or
OpenAPI validation. Enum, format, `anyOf`, `oneOf`, `additionalProperties`, and
unresolved references (including recursive reference boundaries) are not
validated by this preflight. Unknown type names and absent schema nodes are
unconstrained; duplicate required names are deduplicated. Read-only properties
do not become required request fields. Swagger `x-nullable` is preserved.
No external references are fetched.

### Runtime Body Schema

Bind a visible, bodyless, non-streaming `GET` operation in the same module as a
request-body schema source:

```yaml
commands:
  run-app:
    body:
      runtime_schema:
        operation_id: describeApp
        response_path: input_schema
        params:
          app_id: ${params.app_id}
```

The schema operation uses the same hostname and cannot require stronger auth
than the target. Normal execution fetches the schema and validates the JSON
body before the target request. External `$ref` loading is disabled.
`--dry-run` remains network-free and skips the fetched-schema preflight.
Static body validation still runs during dry-run.

### Table Columns

Override the generated table columns for one command when schema-derived
columns do not surface the operator-facing identity or status fields:

```yaml
commands:
  list-resources:
    output:
      default_columns: [resourceId, displayName, status, spendMicro, cpuMillis]
      column_labels:
        resourceId: Resource ID
        displayName: Name
      column_formats:
        spendMicro:
          kind: currency
          currency: USD
          source_scale: 6
          grouping: true
          min_fraction_digits: 2
          max_fraction_digits: 6
      column_alignments:
        cpuMillis: right
```

The paths are ordered, dot-separated JSON fields. The override is compiled
into the generated command and affects table output only; JSON, YAML, and raw
responses remain unchanged. `column_labels` changes only the displayed header;
it does not transform values. Unlabeled columns retain the default uppercase
header. `column_formats` transforms table values only. Currency formats treat
the source as a fixed-point integer, move the decimal point left by
`source_scale`, and retain every non-zero fractional digit through
`max_fraction_digits`. The maximum must be at least the source scale, so
configured output never rounds away source precision. USD uses `$`; other
three-letter currency codes remain explicit. `column_alignments` sets
per-column table alignment to `left` or `right`. Omitted columns stay left.
Currency-formatted columns default to right alignment; an explicit
`column_alignments` entry overrides that default.

### Pagination

Generated list commands with pagination metadata expose `--all` to collect
pages and `--max-pages` to cap the number of requests. With offset pagination,
`--all` starts at the offset sent in the first request, including an explicit
flag or a declared default. Each subsequent offset advances by the number of
items returned, and an empty page ends collection.

### Stream Collection

```yaml
commands:
  run-job:
    output:
      streaming:
        data: json
        event_name_path: event
        collect:
          require_stop: true
          stop_events: [done]
          pause_events: [input_required]
          error_events: [error]
          fields:
            - events: [chunk]
              from: text
              to: output
              reduce: concat
        live:
          events: [chunk]
          from: text
```

Reducers are `first`, `last`, `concat`, and `append`. JSON/YAML returns one
collected document, raw output preserves wire events, and `--stream` prints the
configured live field in the default output mode. A pause is a successful
terminal outcome; a workflow stops before its next step.

SSE collection ignores one leading UTF-8 BOM and dispatches events only after
a blank line. An incomplete final event is discarded at EOF; CRLF, CR, and LF
line endings are supported.

## Generate and Build

```sh
lathe bootstrap
```

Equivalent explicit phases:

```sh
lathe specsync
lathe codegen
```

Useful overrides:

```sh
lathe specsync -source users
lathe specsync -cache .cache
lathe codegen -overlay internal/overlay
lathe codegen -skill-root ""
lathe codegen -skill-include internal/skill-include
```

Prefer `cli.yaml` over one-off Skill flags when generation must be reproducible.

Wire the generated package:

```go
package main

import (
	_ "embed"
	"os"

	"github.com/lathe-cli/lathe/pkg/lathe"
	"example.com/acme/internal/generated"
)

//go:embed cli.yaml
var manifestBytes []byte

func main() {
	os.Exit(lathe.Run(lathe.RunOptions{
		Manifest: manifestBytes,
		Mount:    generated.Mount,
	}))
}
```

Copy `cli.yaml` beside `main.go`, then:

```sh
go mod tidy
go build -o bin/acmectl ./cmd/acmectl
bin/acmectl __lathe verify --json
```

## Binary Responses

A generated operation is binary when its success response is not JSON and not a
configured stream, and either every success schema is Swagger `type: file` or
OpenAPI `type: string, format: binary`, or the media type is
`application/octet-stream`, `application/pdf`, `application/zip`,
`application/gzip`, or a top-level `image`, `audio`, `video`, or `font` type
whose subtype does not end in `+xml`. Any 2xx response that declares its own
JSON or stream media type (`text/event-stream` or ndjson), including a `202`
next to a `200` file response, keeps the operation from being binary. Swagger
`produces` is not applied to each status for that check. Protobuf and
GraphQL operations are not binary. A response that prefers JSON is not binary.
`text/csv` without `format: binary` stays text. There is no overlay switch for
this classification.

Binary commands require `--output-file`, or the renamed flag in
`output.binary.flag` when a parameter already uses that name. Pass a path that
does not exist, or `-` to write the raw bytes to stdout. An existing file,
directory, symlink, or dangling symlink is a usage error and no request is
sent. A missing or unwritable destination directory is the same kind of usage
error; its detail includes the system error without the path (`no such file
or directory`, `permission denied`). The command writes a temporary file in
the destination directory and publishes it only after a complete 2xx response.
When the declared type is specific, such as `application/pdf`, `image/png`, or
`application/zip`, a JSON (`application/json` or `*+json`) or `text/html`
Content-Type is rejected before any byte is written. Declared
`application/octet-stream`, `*/*`, or any media type containing a wildcard
keeps the stored object's type, including JSON and HTML. The final file mode
is `0600`. Ctrl-C (SIGINT), a non-2xx response, that unexpected media type, or
a read or write failure removes the temporary file and leaves no output file.
A process killed by another signal
may leave a hidden `.<name>.*.part` file next to the target. Publishing the
file happens after the request completed, so a failure there, including the
path appearing in the meantime, is a general error: the output file is not
written. `-o` changes only error rendering. Binary commands do not register
`--wait`. Workflow steps cannot call a binary operation.
`runtime.InvokeOperation` still buffers the body.

```sh
bin/acmectl reports download --report-id r1 --output-file report.pdf
```

Regenerate modules for `SchemaVersion` 20 and refresh catalog consumers for
`CatalogSchemaVersion` 27.

## Agent Operation Loop

```sh
bin/acmectl __lathe verify --json
bin/acmectl search "create user" --json
bin/acmectl commands show users create --json
bin/acmectl auth status -o json
bin/acmectl users create --set email=alice@example.com --dry-run
bin/acmectl users create --set email=alice@example.com -o json
```

Rules:

1. Treat search results as candidates only.
2. Use `commands show <path...> --json` as the exact operation/workflow
   contract.
3. Check auth when `auth.required=true`.
4. Read `mutation` and `dry_run` from `commands show`. When `mutation` is not
   `read`, preview with `--<dry_run.flag>` if `dry_run.mode` is `http_preview`;
   if preview is unavailable, obtain explicit user confirmation before
   execution. `unknown` is not read.
5. Prefer `-o json` for machine-readable output.
6. Use `--file`, `--set`, and `--set-str` according to the body contract.
7. For a flag with `input_modes`, prefer `--<flag>-env`, `--<flag>-file`, or
   `--<flag>-stdin` for sensitive values.
8. Branch on structured `error.code` and process exit status, not error prose.
9. When `output.binary` is present, pass `--<output.binary.flag>` with a new
   path, or `-` when piping to another program.

Framework commands such as `auth`, `commands`, `completion`, `search`,
`skill`, `update`, and `__lathe` are documented by `--help`; generated API
operations and workflows are documented by the runtime catalog.

## Examples

- [Petstore](../examples/petstore/README.md): minimal OpenAPI path and shortcut.
- [Rich API](../examples/richapi/README.md): broader runtime metadata.
- [GraphQL](../examples/graphql/README.md): curated GraphQL policy and request envelope.
