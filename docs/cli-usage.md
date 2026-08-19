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
bin/acmectl __lathe verify --json
```

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

When bundling is enabled, codegen pins:

```sh
go get github.com/lathe-cli/kitup/go@v0.1.3 github.com/lathe-cli/kitup/go-cobra@v0.1.3
```

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
alternatives, active contexts, runtime schema preflights, and stream collection.
Unknown groups and conflicting root names fail codegen. Command overrides apply
only to an exact command/match; unmatched command entries and most unmatched
parameter entries are ignored. An `argument` entry for an unknown parameter
fails validation.

`ignore: true` removes a command. `hidden: true` keeps it out of normal help,
search, and catalog output; `--include-hidden` can still inspect it.

### Multipart Input

OpenAPI multipart object fields and Swagger `formData` parameters become
normal command flags. A field with `format: binary` accepts a local file path;
the runtime opens it and builds the multipart request. These commands do not use
the JSON body builder's `--file`, `--set`, or `--set-str` flags.

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
`--dry-run` remains network-free and skips this preflight. Static request-body
schemas from the API spec remain discovery metadata; they are not runtime
validators.

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

## Agent Operation Loop

```sh
bin/acmectl __lathe verify --json
bin/acmectl search "create user" --json
bin/acmectl commands show users create --json
bin/acmectl auth status --hostname api.acme.com
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

Framework commands such as `auth`, `host`, `commands`, `completion`, `search`,
`skill`, `update`, and `__lathe` are documented by `--help`; generated API
operations and workflows are documented by the runtime catalog.

A persisted default host is optional. Resolve with `--hostname`, then the
configured host environment variable, then `host default`, then codegen
`default_hostname`, then the unique host in `hosts.yml`. `auth status --json`
and `--dry-run` expose the resolved host and its source.

## Examples

- [Petstore](../examples/petstore/README.md): minimal OpenAPI path and shortcut.
- [Rich API](../examples/richapi/README.md): broader runtime metadata.
- [GraphQL](../examples/graphql/README.md): curated GraphQL policy and request envelope.
