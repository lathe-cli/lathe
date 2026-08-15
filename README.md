[English](README.md) | [中文](README_zh.md)

# lathe

> Generate agent-friendly Cobra CLIs from OpenAPI, Swagger, protobuf, and GraphQL API specs.

[![CI](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml/badge.svg)](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Lathe is an API-to-CLI generator for teams that want one binary humans can use
and AI agents can inspect safely. It turns Swagger 2.0, OpenAPI 3, and
`google.api.http` protobuf APIs, plus curated GraphQL schemas, into
production-grade Cobra CLIs with structured command discovery, auth metadata,
request body builders, and machine-readable output.

Generated CLIs ship with command catalog JSON, intent search, per-command detail
JSON, auth metadata, body builders, structured output formats, and a repo-local
Skill directory under `skills/<cli-name>/`.

Start with the CLI usage guide:

- [CLI Usage](docs/cli-usage.md): generate a downstream CLI with `lathe bootstrap`, wire `cmd/<name>/main.go`, build it, and verify the agent loop.

![lathe architecture](docs/images/architecture.png)

---

## What is Lathe?

Lathe generates single-binary command-line tools from existing API specifications.
Instead of hand-writing a CLI that can drift away from the API, you pin upstream
specs, configure the CLI identity, optionally add overlays for better help text,
and regenerate when the API changes.

The result is more than a human-facing API wrapper. Lathe emits an
agent-friendly CLI surface where commands can be searched, inspected, validated,
and executed through machine-readable contracts.

## Use Cases

Use Lathe when you need to:

- Generate a Cobra CLI from OpenAPI 3, Swagger 2.0, protobuf services, or GraphQL control-plane APIs.
- Keep an internal or customer-facing CLI synchronized with upstream API specs.
- Expose API operations to AI agents without making them guess flags, auth, body
  shape, or output format.
- Ship one binary with command discovery, auth preflight, structured output, and
  generated agent Skill documentation.
- Improve generated help text and examples through overlays without editing
  generated Go code.

## Why Lathe

Every serious API eventually needs a CLI. Most teams still hand-write command
trees that mirror an existing API spec, then spend the rest of the project's
life keeping those commands from drifting.

Lathe makes the API spec the source of truth.

You declare pinned upstream specs or local working-tree specs, declare the CLI
identity, add optional help-text overlays, and generate the binary. When the API
changes, bump the pinned tag or rerun sync for the local source, then regenerate.

The result is not just a wrapper. It is an agentic-friendly CLI surface with a
runtime catalog that tells agents what commands exist, which flags are required,
whether auth is needed, what HTTP request will be made, how request bodies are
built, and which output format to prefer.

## What You Get

| Capability | What it means |
|---|---|
| Multi-backend generation | Swagger 2.0, OpenAPI 3, protobuf services with `google.api.http` annotations, and curated GraphQL schemas become Cobra command trees. |
| Single runtime shape | Generated modules share one runtime for auth, request building, output formatting, pagination, streaming, and error handling. |
| Agentic-friendly discovery | `search`, `commands --json`, `commands show`, and `commands schema` expose the CLI as structured data. |
| Generated Skills | Codegen writes `skills/<cli-name>/` and can bundle it into the CLI with `skill.bundle`, exposing `<cli> skill install`. |
| Reproducible inputs | Git specs are pinned by tag and resolved to commit SHA; local sources are staged from checked-in configuration. |
| Real CLI UX | Hostname-keyed auth, --file, --set, --set-str, -o table\|json\|yaml\|raw, enum validation, pagination, and streaming. |
| Overlay polish | Improve summaries, aliases, parameter help, grouping, and examples without editing generated code. |

## Project Resources

- [Governance](GOVERNANCE.md): decision process and compatibility expectations.
- [Maintainers](MAINTAINERS.md): maintainer responsibilities and review expectations.
- [CLI Usage](docs/cli-usage.md): command sequence for generating and validating a downstream CLI.
- [Adopters](ADOPTERS.md): public and anonymized user entries.
- [Contributing](CONTRIBUTING.md): local setup, PR workflow, and project scope.
- [Security](SECURITY.md): private vulnerability reporting and supported versions.

## Quick Start

Start a CLI-first application repository from the latest starter `main`:

```sh
lathe init ./my-app \
  --language node \
  --app-name "My App" \
  --cli-name my-appctl \
  --go-module example.com/my-app
```

Use `--language node|go|python|rust`. Override the starter with
`--template '<git-url>#<ref>'`; the optional ref may be a branch or tag. The
result is a new local Git repository with generated CLI files, no commit, no
staged files, and no remote.

Install Lathe's Agent Skill with:

```sh
lathe skill install --scope user --agent codex --yes
```

Create a repository from [github.com/lathe-cli/lathe](https://github.com/lathe-cli/lathe),
then configure two files.

### Install the Tools

Lathe release archives include one command-line tool, `lathe`, with generation
subcommands:

- `lathe specsync`: sync declared API specs into the local cache.
- `lathe codegen`: generate runtime command specs and optional Skill files.
- `lathe bootstrap`: run `specsync` and `codegen` in one pass.
- `lathe init`: create a CLI-first application repository from a starter.
- `lathe skill install`: install Lathe's bundled Agent Skill.

Download the archive for your platform from the [latest release](https://github.com/lathe-cli/lathe/releases/latest), unpack it, and put `lathe` on your `PATH`.

When working from a source checkout, run `make build` and use `./bin/lathe` instead of installing a release archive.

### 1. Define the CLI

`cli.yaml`:

```yaml
cli:
  name: acmectl
  short: "Command-line tool for Acme services"

auth:
  login:
    type: oauth_device
    start_path: /auth/device/start
    token_path: /auth/device/token
    refresh_path: /auth/device/refresh
  validate:
    method: GET
    path: /api/v1/whoami
    display:
      username_field: data.username
      fallback_field: data.email
```

`cli.command_path` defaults to `auto`: one source module uses
`<cli> <group> <operation>` when safe, while multiple modules keep
`<cli> <module> <group> <operation>`. Use `namespaced` to always keep the
module segment.

### 2. Declare API Sources

`specs/sources.yaml`:

```yaml
sources:
  iam:
    repo_url: https://github.com/acme/iam.git
    pinned_tag: v1.4.0
    backend: swagger
    swagger:
      files:
        - api/openapi/user.swagger.json

  billing:
    repo_url: https://github.com/acme/billing.git
    pinned_tag: v0.9.2
    backend: proto
    proto:
      staging:
        - from: api/proto
          to: "."
      entries:
        - v1/accounts.proto

  payments:
    repo_url: https://github.com/acme/payments.git
    pinned_tag: v2.1.0
    backend: openapi3
    openapi3:
      files:
        - api/openapi.yaml

  console:
    repo_url: https://github.com/acme/graphql-console.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema/console.graphql
      expose:
        queries: ["listApps", "getApp"]
        mutations: ["createApp"]
      groups:
        - match: ["*App*"]
          group: Apps
      output:
        - match: ["listApps"]
          list_path: data.listApps.nodes
          default_columns: ["id", "name", "status"]
      selection:
        max_depth: 2
        prune: ["App.secretToken"]

  awire:
    local_path: ../awire
    backend: openapi3
    openapi3:
      files:
        - openapi/awire.yaml
```

### 3. Generate and Build

```sh
lathe bootstrap
go build -o bin/acmectl ./cmd/acmectl
```

`lathe bootstrap` syncs declared specs and runs codegen. Codegen emits generated Go
modules and, by default, a Skill directory at `skills/acmectl/`.

### 4. Use the CLI

Log in, discover the generated command, inspect its exact shape, then run it:

```sh
./bin/acmectl auth login --hostname api.acme.com
./bin/acmectl search "create user" --json
./bin/acmectl commands show iam users create-user --json
./bin/acmectl auth status --hostname api.acme.com
./bin/acmectl iam users create-user \
  --set email=alice@example.com \
  --set role=viewer \
  -o json
```

## Agentic-Friendly CLI Surface

Generated CLIs are designed so an agent does not have to guess.

| Command | Purpose |
|---|---|
| `<cli> search "<intent>" --json` | Find candidate commands from natural-language intent. Supports `--limit`. Search output is for discovery only. |
| `<cli> commands --json` | Read the complete generated command catalog. Use `--include-hidden` when hidden commands matter. |
| `<cli> commands show <path...> --json` | Inspect the source of truth for one command before execution: flags, body, auth, HTTP method/path, and output hints. |
| `<cli> commands schema --json` | Check the catalog schema version before durable machine parsing. |
| `<cli> auth status --hostname <host>` | Confirm credentials before running a command whose detail says `auth.required=true`. |
| `<cli> <command> --dry-run` | Print the resolved request JSON, including URL, redacted headers, redacted body, auth, and output hints, without sending the request. |

Recommended agent loop:

1. Use `search "<intent>" --json` to find candidates.
2. Use `commands show <path...> --json` for the selected command.
3. If `auth.required=true`, run `auth status --hostname <host>` and stop if the user is not logged in.
4. Run the command with `--dry-run` when the resolved URL, headers, or body need confirmation.
5. Execute only after flags, body requirements, auth, HTTP path, and output hints are clear.
6. Prefer `-o json` for machine-readable command output unless the user asked for a human-readable table.

## Generated Skill Directory

Codegen writes a standard Skill directory by default:

```text
skills/<cli-name>/
|-- SKILL.md
|-- agents/openai.yaml
`-- references/
    |-- catalog.md
    `-- modules/<source-name>.md
```

The Skill is a compact operating guide for agents. It explains command
discovery, catalog inspection, auth preflight, body input, output formats, and
per-source module references.

The runtime catalog remains the source of truth. Agents should use the Skill to
learn how to operate the CLI, then use `commands show <path...> --json` for exact
execution details.

Disable Skill output when needed:

```sh
go run ./cmd/lathe codegen -skill-root ""
```

## Configuration

### `cli.yaml`

Defines CLI identity and auth behavior.

| Field | Notes |
|---|---|
| `cli.name` | Binary and command name, for example `acmectl`. |
| `cli.short` | Root command summary. |
| `auth.default_type` | Default `auth login` type: `bearer`, `apikey`, `basic`, or `oauth`. |
| `auth.api_key_header` | API key header used by `apikey` login; defaults to `X-API-Key`. |
| `auth.login` | Optional OAuth device login endpoints used by `auth login --device-auth`. |
| `auth.validate` | Optional endpoint used to validate credentials during login. Supports `assert.field` and `assert.non_empty`. |

### `specs/sources.yaml`

Declares which upstream specs become modules.

| Field | Required | Notes |
|---|---|---|
| `repo_url` | Git sources | Any URL `git clone` accepts. |
| `pinned_tag` | Git sources | Floating branches are rejected; reproducibility is mandatory. |
| `local_path` | Local sources | Local filesystem path or `file://` URL, relative paths resolve from `specs/sources.yaml`. Mutually exclusive with `repo_url` and `pinned_tag`. |
| `backend` | Yes | One of `swagger`, `openapi3`, `proto`, or `graphql`. |
| `swagger.files` | Swagger only | One or more Swagger 2.0 JSON specs. |
| `openapi3.files` | OpenAPI 3 only | JSON or YAML OpenAPI specs. |
| `proto.staging` | Proto only | Files staged into the `protoc` include root before parsing. |
| `proto.entries` | Proto only | Entry proto files. Only RPCs declared in these files, and annotated with `google.api.http`, become commands; imported files, including dependencies, never contribute commands. |
| `proto.import_roots` | Proto only | Additional staged include roots passed to `protoc`. |
| `proto.dependencies` | Proto only | Pinned `buf`, `go_module`, or `git` sources staged before `protoc`; each dependency requires explicit staging. |
| `graphql.schema` | GraphQL only | Pinned SDL file staged from the upstream repository. |
| `graphql.expose` | GraphQL only | Explicit `queries` and/or `mutations` allow policy; missing policy fails closed. |
| `graphql.groups` | GraphQL only | Operation-name globs mapped to CLI groups. Ambiguous matches fail closed. |
| `graphql.output` | GraphQL only | Configured list path and default columns for generated output hints. |
| `graphql.selection` | GraphQL only | Selection-set max depth and per-type field pruning. Explicit `max_depth` must be greater than zero. |

Buf dependencies require `module`, an immutable `commit`, and the `b5` digest
the BSR reports for that commit. Go module dependencies require
`module`, `version`, and `sum`. Git dependencies require `repo_url` and
immutable `pinned_tag`. Dependency staging uses the same repository-relative
`from` and include-tree `to` fields as `proto.staging`. Syncing these sources
requires `buf`, `go`, or `git` respectively on `PATH`. Staging never overwrites:
two staged trees that write different content to the same include path fail the
sync instead of silently picking one.

Grouping rules:

- Swagger and OpenAPI 3 use the operation's first tag.
- Proto uses the service name.
- GraphQL uses `graphql.groups` policy and falls back to the source module name when no group policy matches.

GraphQL commands execute `POST /graphql` with a baked `{query, variables}`
request envelope. Scalar and enum arguments become typed flags that merge under
`variables`. Input-object arguments expand scalar and enum leaf fields into
dotted variable flags such as `--input-name`; complex fields remain in the
request body schema and are supplied with `--set` or `--file`.
Relay-style outputs with `list_path: data.<operation>.nodes` or `.edges`,
`pageInfo.endCursor`, `pageInfo.hasNextPage`, and an `after` argument get
`--all`; subsequent pages write the cursor back under `variables.after`.

### Overlays

Overlays polish generated commands and bind bounded runtime behavior that the
upstream spec cannot express, without editing generated Go code.

`internal/overlay/iam.yaml`:

```yaml
defaults:
  pagination:
    match_commands: ["list-*", "query-*"]
    params:
      page: "1"
      pageSize: "20"
commands:
  create-user:
    short: "Create a user in the IAM service"
    aliases: [adduser]
    shortcuts:
      - use: quick-user
        params:
          type: human
          role: viewer
    example: |
      acmectl iam create-user \
        --set email=alice@example.com \
        --set role=viewer
    params:
      type:
        help: "Required by the backend even when the upstream spec omits it"
        required: true
      role:
        help: "User role (viewer, editor, admin)"
        default: viewer
```

Command `ignore: true` removes a generated command. Command `hidden: true`
keeps it generated but hidden from normal help and catalog output unless
`--include-hidden` is used. Parameter `hidden: true` is a legacy alias for
`deprecated: true`; prefer `deprecated: true` for parameter overlays.
Use `match.method` and `match.path` to scope an override when duplicate upstream
operation IDs generate the same command name.
Bulk pagination defaults fill matching command params that have no spec default.
Explicit `commands.<use>.params.<name>.default` still wins when a command needs a
different value. Parameter `required: true` marks an existing generated flag as
required when an upstream spec is incomplete; it does not create new flags.
Command `shortcuts` add root-level commands that execute the same generated
operation with preset values for existing parameters. Shortcut params may use
the parameter name or flag name; invocation flags can still override the preset.

When a read-only operation returns the JSON Schema for a later request body, an
overlay can bind that preflight without hard-coding provider behavior:

```yaml
commands:
  run-app:
    body:
      runtime_schema:
        operation_id: describeApp
        response_path: input_schema
        params:
          app_id: ${params.app_id}
          fields: input_schema
```

The schema operation must be a visible, non-streaming `GET` or `HEAD` in the
same module, use the same hostname, return JSON, and require no more auth than
the target. Parameter mappings use source parameter names or flags; values are
literals or exact `${params.<target-name-or-flag>}` references. Before the
target request, the runtime executes the schema operation and validates the
JSON body against `type`, `properties`, `required`, `additionalProperties`,
`items`, `enum`, `minLength`, and `maxLength`. Unsupported assertion keywords
fail the preflight instead of being ignored. `--dry-run` remains network-free,
so it shows the static request without dynamic validation.

For an SSE or NDJSON operation whose event semantics are not described by the
API spec, an overlay can collect JSON frames into one stable result:

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

Reducers are fixed to `first`, `last`, `concat`, and `append`. Choose one output
mode: `-o json` or `-o yaml` collects one document, `-o raw` preserves wire
events, and `--stream` in the default table mode prints the configured live
field. A pause event returns exit code 0 with the collected result; workflows
stop before their next step. Stream
policies cannot call operations or branch and therefore do not replace the
workflow DSL.

Run codegen with an overlay directory:

```sh
go run ./cmd/lathe codegen -overlay internal/overlay
```

## Runtime Features

### Global Flags

| Flag | Effect |
|---|---|
| `--hostname` | Select host for this invocation. |
| `-o, --output` | Output format: `table`, `json`, `yaml`, or `raw`. |

### Lathe Control Commands

Lathe runtime commands that should not occupy generated API command names live under the hidden `__lathe` command:

| Command | Effect |
|---|---|
| `<cli> __lathe verify --json` | Verify the generated CLI contract without making network calls. |
| `<cli> __lathe version` | Print version information. |
| `<cli> __lathe completion <shell>` | Generate shell completion scripts. |

### Environment

| Env var | Effect |
|---|---|
| `$<NAME>_HOST` | Select host without editing the host config. |
| `$<NAME>_CONFIG_DIR` | Override the config directory, defaulting to `~/.config/<name>`. |
| `LATHE_SPECS_CACHE` | Where spec sync stages upstream specs, defaulting to `.cache`. |

`<NAME>` is the uppercased `cli.name`.

### Request Bodies

Generated commands expose request body helpers when the API operation accepts a
body:

| Flag | Use |
|---|---|
| `--file path.json` | Load the request body from a JSON file. |
| `--set key.path=value` | Build JSON from repeated key/value assignments. |
| `--set-str key.path=value` | Build JSON while forcing the value to remain a string. |

### Streaming Outputs

OpenAPI and Swagger streaming media types produce raw incremental output with
`-o raw`. When a stream collection overlay is present, structured formats
return the collected document; an optional `--stream` flag in the default table
mode prints only the configured live field as frames arrive. The complete policy
is inspectable at `output.streaming.policy` in `commands show ... --json`.

## Architecture

Lathe has two phases:

1. `lathe specsync` checks out pinned Git sources or stages `local_path`
   sources, then writes local spec state.
2. `lathe codegen` normalizes specs into one intermediate representation, applies
   overlays, renders Go command modules, and renders the Skill directory.

The generated CLI uses `pkg/lathe` and `pkg/runtime` for the shared command
catalog, auth, request construction, output formatting, pagination, streaming,
and stable error handling.

## Design Principles

1. **Spec is truth.** Generated command behavior should come from the API spec.
2. **Catalog is contract.** Humans can read help text; agents need structured command facts.
3. **Search is not execution.** Search finds candidates; `commands show` confirms exact command shape.
4. **Auth is explicit.** Credentials are keyed by hostname, and agents should preflight auth before protected calls.
5. **Overlay after generation.** Polish weak spec text without forking generated code.
6. **One binary at runtime.** The generated CLI should be easy to install, inspect, and automate.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). All commits must be signed off with
`git commit -s` per the [Developer Certificate of Origin](https://developercertificate.org/).

## Security

See [SECURITY.md](SECURITY.md) for private vulnerability disclosure.

## License

[Apache License 2.0](LICENSE) © lathe-cli

Generated output written into a downstream project is not covered by this
license. See the generated-output exception in [LICENSE](LICENSE).
