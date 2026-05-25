[English](README.md) | [中文](README_zh.md)

# lathe

> Generate agent-friendly Cobra CLIs from OpenAPI, Swagger, and protobuf API specs.

[![CI](https://github.com/samzong/lathe/actions/workflows/ci.yml/badge.svg)](https://github.com/samzong/lathe/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Lathe is an API-to-CLI generator for teams that want one binary humans can use
and AI agents can inspect safely. It turns Swagger 2.0, OpenAPI 3, and
`google.api.http` protobuf APIs into production-grade Cobra CLIs with structured
command discovery, auth metadata, request body builders, and machine-readable
output.

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

- Generate a Cobra CLI from OpenAPI 3, Swagger 2.0, or protobuf services.
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

You pin upstream specs, declare the CLI identity, add optional help-text
overlays, and generate the binary. When the API changes, bump the pinned tag and
regenerate.

The result is not just a wrapper. It is an agentic-friendly CLI surface with a
runtime catalog that tells agents what commands exist, which flags are required,
whether auth is needed, what HTTP request will be made, how request bodies are
built, and which output format to prefer.

## What You Get

| Capability | What it means |
|---|---|
| Multi-backend generation | Swagger 2.0, OpenAPI 3, and protobuf services with `google.api.http` annotations become Cobra command trees. |
| Single runtime shape | Generated modules share one runtime for auth, request building, output formatting, pagination, streaming, and error handling. |
| Agentic-friendly discovery | `search`, `commands --json`, `commands show`, and `commands schema` expose the CLI as structured data. |
| Generated Skills | Codegen writes `skills/<cli-name>/` so agents can load the CLI's operating guide and module references. |
| Reproducible inputs | Specs are pinned by tag, resolved to commit SHA, and regenerated from checked-in configuration. |
| Real CLI UX | Hostname-keyed auth, --file, --set, --set-str, -o table\|json\|yaml\|raw, enum validation, pagination, streaming, and --debug. |
| Overlay polish | Improve summaries, aliases, parameter help, grouping, and examples without editing generated code. |

## Project Resources

- [Governance](GOVERNANCE.md): decision process and compatibility expectations.
- [Maintainers](MAINTAINERS.md): maintainer responsibilities and review expectations.
- [Showcase](SHOWCASE.md): CLI generation paths and real-world usage notes.
- [CLI Usage](docs/cli-usage.md): command sequence for generating and validating a downstream CLI.
- [Adopters](ADOPTERS.md): public and anonymized user entries.
- [Contributing](CONTRIBUTING.md): local setup, PR workflow, and project scope.
- [Security](SECURITY.md): private vulnerability reporting and supported versions.

## Quick Start

Create a repository from [github.com/samzong/lathe](https://github.com/samzong/lathe),
then configure two files.

### Install the Tools

Lathe release archives include one command-line tool, `lathe`, with generation
subcommands:

- `lathe specsync`: sync pinned upstream specs into the local cache.
- `lathe codegen`: generate runtime command specs and optional Skill files.
- `lathe bootstrap`: run `specsync` and `codegen` in one pass.

Download the archive for your platform from the [latest release](https://github.com/samzong/lathe/releases/latest), unpack it, and put `lathe` on your `PATH`.

When working from a source checkout, you can use the Make targets shown below instead of installing the release tools.

### 1. Define the CLI

`cli.yaml`:

```yaml
cli:
  name: acmectl
  short: "Command-line tool for Acme services"

auth:
  validate:
    method: GET
    path: /api/v1/whoami
    display:
      username_field: data.username
      fallback_field: data.email
```

### 2. Pin API Sources

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
```

### 3. Generate and Build

```sh
lathe bootstrap
go build -o bin/acmectl ./cmd/acmectl
```

`lathe bootstrap` syncs pinned specs and runs codegen. Codegen emits generated Go
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

Recommended agent loop:

1. Use `search "<intent>" --json` to find candidates.
2. Use `commands show <path...> --json` for the selected command.
3. If `auth.required=true`, run `auth status --hostname <host>` and stop if the user is not logged in.
4. Execute only after flags, body requirements, auth, HTTP path, and output hints are clear.
5. Prefer `-o json` for machine-readable command output unless the user asked for a human-readable table.

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

Defines CLI identity and optional auth validation behavior.

| Field | Notes |
|---|---|
| `cli.name` | Binary and command name, for example `acmectl`. |
| `cli.short` | Root command summary. |
| `auth.validate` | Optional endpoint used by `auth status` to display the logged-in user. |

### `specs/sources.yaml`

Declares which upstream specs become modules.

| Field | Required | Notes |
|---|---|---|
| `repo_url` | Yes | Any URL `git clone` accepts. |
| `pinned_tag` | Yes | Floating branches are rejected; reproducibility is mandatory. |
| `backend` | Yes | One of `swagger`, `openapi3`, or `proto`. |
| `swagger.files` | Swagger only | One or more Swagger 2.0 JSON specs. |
| `openapi3.files` | OpenAPI 3 only | JSON or YAML OpenAPI specs. |
| `proto.staging` | Proto only | Files staged into the `protoc` include root before parsing. |
| `proto.entries` | Proto only | Entry proto files; only RPCs with `google.api.http` become commands. |

Grouping rules:

- Swagger and OpenAPI 3 use the operation's first tag.
- Proto uses the service name.

### Overlays

Overlays polish generated commands without changing the upstream spec or editing
generated Go code.

`internal/overlay/iam.yaml`:

```yaml
commands:
  create-user:
    short: "Create a user in the IAM service"
    aliases: [adduser]
    example: |
      acmectl iam create-user \
        --set email=alice@example.com \
        --set role=viewer
    params:
      role:
        help: "User role (viewer, editor, admin)"
        default: viewer
```

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
| `--insecure` | Skip TLS certificate verification. |
| `--debug` | Print HTTP request/response details to stderr. |

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

## Architecture

Lathe has two phases:

1. `lathe specsync` clones pinned upstream specs, verifies the resolved commit
   SHA, and writes local spec state.
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

[MIT](LICENSE) (c) samzong
