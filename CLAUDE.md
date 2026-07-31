# CLAUDE.md

## Project intent

Lathe generates agent-friendly Cobra CLIs from Swagger 2.0, OpenAPI 3, `google.api.http` protobuf APIs, and policy-curated GraphQL schemas. Declared API specs and project configuration are the source of truth. Pinned Git sources are reproducible; `local_path` sources intentionally follow the referenced working tree.

## Product positioning

Lathe is a spec-to-agent-toolchain generator. It turns declared API specs, from pinned Git sources or explicit local working trees, plus repo-local configuration into one inspectable Cobra CLI that both humans and AI agents can run safely.

The core product promise is: agents should not guess command names, flags, auth state, request body shape, HTTP path, or output format. Generated CLIs must expose machine-readable contracts (`search`, `commands --json`, `commands show`, `commands schema`) and generated Skill guidance so agents can discover, inspect, verify auth, and then execute.

Lathe also owns `lathe init` for CLI-first application repositories and `lathe skill install` for its bundled Agent Skill. These distribution surfaces must serve the spec-to-agent workflow; Lathe is not a generic scaffolder, CLI framework, plugin loader, GUI/TUI, API gateway, or hand-written SDK replacement. Product work should strengthen spec fidelity, reproducibility, generated command correctness, runtime catalog inspectability, auth/body/output behavior, application initialization, and generated Skill quality. Challenge features that move product weight into manually authored commands, runtime plugins, or unrelated scaffolding.

## Source of truth

- Follow this file for repository structure, commands, style, tests, commits, PR rules, and release workflow entry points.
- Follow `CONTRIBUTING.md` for contributor workflow and public contribution scope.
- Trust code and current Makefile targets over stale documentation when they disagree.
- In this repository, do not commit generated output under `internal/generated/`, upstream clones under `.cache/`, example build artifacts, or ad-hoc generated `skills/<cli-name>/` directories. Downstream repositories created by `lathe init` have their own generated-output policy.

## Project structure

- `cmd/lathe` contains the executable entry point for init, Skill installation, spec syncing, code generation, bootstrap, and version reporting.
- `internal/lathecmd` orchestrates CLI commands; `internal/projectinit` and `internal/latheskill` own application initialization and the bundled Lathe Skill.
- `internal/sourceconfig`, `internal/specsync`, `internal/codegen/{app,backends,normalize,rawir,render}`, and `internal/overlay` own the generation pipeline.
- `internal/auth` holds implementation-only runtime authentication support.
- `pkg/runtime`, `pkg/config`, and `pkg/lathe` are downstream-facing runtime/library surfaces for generated CLIs.
- Tests live beside implementation as `*_test.go`; golden fixtures live under package-local `testdata/`.
- `examples/` contains example generation paths, and `docs/` contains architecture material, usage guides, and images.

## Architecture invariants

- The generation pipeline has two phases: codegen-time (`cmd/lathe`, `internal/lathecmd`, `internal/sourceconfig`, `internal/specsync`, `internal/codegen/**`, `internal/overlay`) and runtime (`pkg/config`, `pkg/runtime`, `pkg/lathe`, `internal/auth`, plus generated modules).
- The seam is `internal/generated/<module>/<module>_gen.go`: generated `[]runtime.CommandSpec` literals compiled into the downstream CLI.
- `pkg/runtime` must remain independent of `internal/codegen/**`; runtime behavior cannot depend on raw specs, overlays, or sync cache state.
- Overlays are codegen-time polish only. They are merged into `CommandSpec`; the runtime must not learn overlay concepts.
- `specs/sources.yaml`, `cli.yaml`, pinned upstream refs or explicit `local_path` sources, and optional overlays are the durable inputs. A local source is not pinned; sync state must record and validate its resolved path. Generated files are outputs, not hand-edited source.

## Development workflow

- Keep changes small and focused; avoid speculative abstractions.
- Prefer configuration or overlays (`cli.yaml`, `specs/sources.yaml`, overlay config) over hard-coded generated behavior.
- Preserve package boundaries: `internal/**` is implementation-only, `pkg/**` is downstream-facing API.
- Use standard Go formatting through `gofmt` / `go fmt`.
- Wrap errors with context using `fmt.Errorf("...: %w", err)`.
- For codegen, normalization, or runtime behavior changes, use focused package-local tests. Use golden fixtures only when serialized generated output or IR is the contract.
- For CLI-visible behavior changes, update docs or examples only when the user-facing output or workflow actually changes.
- If a change alters generated command shape, catalog JSON, auth flow, body building, output formatting, retry/debug behavior, or Skill rendering, treat it as product behavior and prove it with focused tests or an example run.

## Commands

- `make help` is the source of truth for available Make targets.
- `make build` builds `./bin/lathe`; `make install` copies it into `BINDIR`.
- `make check` is the full local quality gate: format check, `go vet`, `golangci-lint`, and tests.
- `make test` runs `go test ./...`.
- `make fmt`, `make fmt-check`, `make vet`, `make lint`, and `make tidy` run focused maintenance tasks.
- Generation workflows use an installed `lathe` or `go run ./cmd/lathe`: `specsync`, `codegen`, and `bootstrap`. There are no Make wrappers for these commands.
- `docs/cli-usage.md` documents the end-to-end generated CLI workflow.
- Prefer the narrowest Make target that proves the changed surface.
- Use `make check` before commit or PR unless the change is documentation-only and the user agrees to skip it.

## Verification rules

- Before claiming completion, run the narrowest command that proves the changed surface.
- For runtime-sensitive changes, also run the relevant example script or `go test -race ./...`.
- For codegen changes, verify regenerated output behavior, but do not commit generated output under `internal/generated/`.
- For `lathe init` or bundled Skill changes, run the focused package tests and a scratch init or isolated-home install smoke. Never verify Skill installation against the real user Skill directory.
- CI runs `go build ./...`, `go vet ./...`, `golangci-lint`, and `go test -race ./...`; local proof should explain any narrower substitute.
- If a command cannot be run because a dependency is missing, report that directly instead of claiming success.

## Agent-facing contract

- The runtime catalog is the source of truth for generated CLI operation details. Generated Skill files are guidance and indexes, not execution authority.
- Use `<cli> __lathe verify --json` to validate the generated CLI contract before broader acceptance checks.
- Preserve the agent loop: `search "<intent>" --json` for candidates, `commands show <path...> --json` for exact command detail, `auth status --hostname <host>` when `auth.required=true`, then execute with known flags/body/output.
- Search results are discovery only. Agents must not execute directly from search output.
- Prefer `-o json` for machine-readable command output unless the user asked for table, yaml, or raw output.
- Changes touching `pkg/runtime/catalog.go`, `pkg/lathe/catalog.go`, `pkg/runtime/build.go`, `internal/codegen/app/**`, or `internal/codegen/render/skill.go` must consider catalog schema compatibility, generated Skill instructions, and agent inspectability.

## Security-sensitive surfaces

- HTTP runtime changes must account for SSRF, unsafe TLS, retry/debug logging, header handling, token leakage, and response error leakage.
- Host config and auth changes must protect persisted credentials and avoid printing secrets in normal, debug, or error output.
- Spec sync and codegen path handling must reject traversal, unsafe output roots, and accidental deletion of non-owned directories.
- Code templates must not emit shell-sensitive or injection-prone behavior from untrusted spec text.
- Treat upstream specs as trusted product inputs but still escape or validate anything that becomes Go code, CLI flags, help text, file paths, or generated Skill markdown.

## Git and release hygiene

- Commit messages use Conventional Commits.
- Use `git commit -s` for every commit.
- Keep PRs focused and include the exact verification commands run.
- Use `.agents/skills/lathe-release/SKILL.md` for versioned release validation and publishing. It starts read-only; tagging and publishing still require explicit authorization.

## Local instructions

- If `CLAUDE.local.md` exists, read it before starting non-trivial work; some non-Claude-Code agents will not load it automatically, but its instructions are still required.
