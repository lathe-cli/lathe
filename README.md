[English](README.md) | [中文](README_zh.md)

# lathe

> Generate agent-friendly Cobra CLIs from OpenAPI, Swagger, protobuf, and GraphQL API specs.

[![CI](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml/badge.svg)](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Lathe turns declared API specifications into one inspectable CLI for humans and
AI agents. The generated binary exposes the API as normal Cobra commands and as
machine-readable contracts, so agents can discover a command, inspect its exact
flags, auth, request body, HTTP path, and output shape, then execute it without
guessing.

![Lathe architecture](docs/images/architecture.png)

## Why Lathe

Hand-written API CLIs duplicate an existing contract and drift from it. Lathe
keeps the specification and repo-local configuration as source of truth, then
generates the command tree, runtime metadata, and Agent Skill together.

Use Lathe when you need to:

- Generate a CLI from Swagger 2.0, OpenAPI 3, `google.api.http` protobuf APIs,
  or an explicitly curated GraphQL schema.
- Keep a human-facing CLI and an agent-facing command catalog on the same
  contract.
- Pin Git inputs for reproducible generation or intentionally follow a declared
  local working tree.
- Add bounded CLI polish through overlays without hand-editing generated code.

## What It Produces

| Surface | Purpose |
|---|---|
| Cobra command tree | Typed API commands with auth, body, pagination, streaming, polling, and structured output support. |
| Runtime catalog | `search`, `commands --json`, `commands show`, and `commands schema` expose exact operation and workflow contracts. |
| Agent Skill | A generated `skills/<cli-name>/` guide that points agents back to the runtime catalog. |
| Optional bundled capabilities | Generated workflows and an embedded `<cli> skill install` command when enabled in `cli.yaml`. |

The runtime catalog is execution authority. Search results and generated Skill
text are discovery aids, not substitutes for `commands show`.

## Start Here

Download `lathe` from the [latest release](https://github.com/lathe-cli/lathe/releases/latest),
or build the current checkout with `make build`.

- Building a CLI from API specs: [CLI usage](docs/cli-usage.md)
- Starting a CLI-first application: [`lathe init`](docs/lathe-init-design.md)
- Inspecting a generated CLI safely: run `<cli> __lathe verify --json`, then
  follow the catalog loop documented in [CLI usage](docs/cli-usage.md#agent-operation-loop)

## Documentation

- [Architecture](docs/architecture.md) — generation/runtime boundaries, package ownership, and invariants.
- [CLI usage](docs/cli-usage.md) — installation, configuration, generation, build, and operation.
- [Machine contracts](docs/contracts.md) — versioned generated-code, catalog, verify, and error contracts.
- [Workflow commands](docs/workflow.md) — declarative multi-operation commands and their limits.
- [Application initializer](docs/lathe-init-design.md) — starter contract, pipeline, and cross-repository gate.
- [Lathe Registry](https://lathe-cli.github.io/lathe-registry/) — reproducible community recipes maintained in a separate repository.

## Project

- [Adopters](ADOPTERS.md)
- [Contributing](CONTRIBUTING.md)
- [Governance](GOVERNANCE.md)
- [Maintainers](MAINTAINERS.md)
- [Security](SECURITY.md)

Lathe is a spec-to-agent-toolchain generator. It is not a generic scaffolder,
runtime plugin loader, GUI/TUI, API gateway, or hand-written SDK replacement.

## License

[Apache License 2.0](LICENSE) © lathe-cli

Output generated into a downstream project is covered by the generated-output
exception in [LICENSE](LICENSE), not by this repository's license.
