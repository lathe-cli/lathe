# Lathe agent guide

`AGENTS.md` links to this file. Read `CLAUDE.local.md` if present.

## Product and sources

Lathe generates inspectable Cobra CLIs and Agent Skills from Swagger 2.0, OpenAPI 3, `google.api.http` protobuf APIs, and policy-curated GraphQL. `lathe init` and `lathe skill install` support this workflow. Keep generic scaffolding, plugin loaders, GUI/TUI, API gateways, and hand-written SDK replacement out of scope.

- Durable inputs are `specs/sources.yaml`, `cli.yaml`, declared specs, and optional overlays and Skill includes. Pinned Git sources are reproducible; `local_path` follows the referenced working tree, and sync state must record and validate its resolved path.
- Change inputs or the generator, never hand-edit generated files. Do not commit `internal/generated/`, `.cache/`, example build artifacts, or ad-hoc generated `skills/<cli-name>/` in this repository. Starter repositories have their own generated-output policy.
- Follow [CONTRIBUTING.md](CONTRIBUTING.md) for contribution scope, Go conventions, Conventional Commits, and DCO sign-off. Use live code and Makefile targets to check documentation claims.

## Architecture boundaries

See [docs/architecture.md](docs/architecture.md) for package ownership and request flow.

- `cmd/lathe` and `internal/lathecmd` orchestrate generation; `internal/sourceconfig`, `internal/specsync`, `internal/codegen/**`, and `internal/overlay` own its pipeline. `internal/projectinit` owns initialization; `internal/latheskill` owns the bundled Lathe Skill.
- Generated `runtime.CommandSpec` declarations cross into the runtime through `generated.Mount`, alongside compiled workflows and bundled capabilities. `pkg/runtime` must not depend on `internal/codegen/**` or read raw specs, overlays, or sync caches.
- Overlays are codegen-time inputs that can alter command semantics; compile them into runtime declarations without introducing overlay concepts at runtime.
- `pkg/config`, `pkg/runtime`, and `pkg/lathe` are downstream-facing, compatibility-sensitive packages. `internal/**` is implementation-only.

## Agent contract

See [docs/contracts.md](docs/contracts.md) for machine contracts and [docs/cli-usage.md](docs/cli-usage.md) for generation and execution.

- The running CLI catalog is execution authority; generated Skills provide guidance. Discover with `search "<intent>" --json`, then inspect `commands show <path...> --json` before execution. Never execute directly from search results.
- When `auth.required=true`, use `auth status -o json` and read `hostname` and `source`; pass the same explicit `--hostname` for status and execution when overriding the host. Read `mutation` and `dry_run` from command detail: for anything other than `read`, preview when supported; otherwise obtain explicit user confirmation. Prefer `-o json` for machine output unless another format was requested.
- Changes to command shape, catalog, auth, body, output, retry/debug, or Skill rendering must preserve agent inspectability. Check generated-code compatibility (`runtime.SchemaVersion`) separately from catalog compatibility (`runtime.CatalogSchemaVersion`); keep generated Skill guidance aligned.

## Commands and verification

Use `make help` for targets and `go.mod` for the Go version.

- `make build` produces `bin/lathe`; `make install` copies it to `BINDIR`.
- Run generation with `go run ./cmd/lathe specsync`, `codegen`, or `bootstrap`; use `--help` for flags.
- Prove the changed behavior with focused package tests or an example run. Add tests when they protect a behavior boundary; use golden fixtures only when serialized output is the contract.
- Run `make check` before commit or PR: format check, vet, lint, and tests. Documentation-only changes may skip it only with user agreement. CI also builds and runs `go test -race ./...`; for runtime-sensitive changes, run the relevant example script or race tests.
- For codegen changes, regenerate and exercise the generated CLI. Run `<cli> __lathe verify --json` before broader acceptance checks. Do not commit scratch outputs.
- For init or bundled Skill changes, run focused package tests and a scratch init. Skill installation smoke must combine a user-scope dry-run with a project-scope install in scratch; never override `HOME` or write to the real user Skill directory.
- Report missing dependencies and unrun checks explicitly.

## Sensitive paths and releases

- HTTP and auth changes must protect credentials and account for SSRF, TLS, redirects, retries, headers, and debug/error leakage. Spec sync and generation must reject traversal, unsafe output roots, and deletion of non-owned directories; escape spec text emitted as code, flags, paths, or Skill Markdown.
- Use [.agents/skills/lathe-release/SKILL.md](.agents/skills/lathe-release/SKILL.md) for versioned release validation. Tagging and publishing require explicit authorization.
