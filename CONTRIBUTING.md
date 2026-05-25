# Contributing to lathe

Thanks for your interest in lathe.

## Local setup

```sh
git clone https://github.com/lathe-cli/lathe.git
cd lathe
make check        # fmt-check, vet, lint, test — the full quality gate
```

Requires Go (version in `go.mod`) and [`golangci-lint`](https://golangci-lint.run/). No other tooling needed.

To follow the end-to-end generated CLI workflow, see:

- [CLI Usage](docs/cli-usage.md) for the exact `go mod init` -> `lathe bootstrap` -> `go build` path.
- [Showcase](SHOWCASE.md) for the petstore and richapi generation paths.

## Workflow

1. Fork the repo, create a feature branch off `main`.
2. Keep the change small and focused. Split unrelated work into separate PRs.
3. Run `make check` before opening the PR. New features should land with tests.
4. Sign off every commit with `-s`: `git commit -s -m "..."`. This attests to the Developer Certificate of Origin ([developercertificate.org](https://developercertificate.org/)).
5. Open a PR describing the problem, the fix, and how you verified it. Link any related issue.

## Scope

- **In scope**: codegen accuracy, runtime correctness, spec backend improvements (Swagger 2.0, OpenAPI 3, proto), test coverage, docs, `Authenticator` / `Formatter` extensions, overlay ergonomics, `cli.yaml` schema.
- **Out of scope**: new spec formats beyond Swagger / OpenAPI / proto, plugin loaders, GUI/TUI. These can ship as sibling projects on top of lathe.

## Project conventions

- Commit messages follow Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`). Scope is optional.
- Error wrapping uses `fmt.Errorf("...%w", err)`; never drop context.
- Don't commit generated code (`internal/generated/`) or upstream clones (`.cache/`).
- For anything data-driven (auth endpoints, CLI identity, spec sources), prefer extending `cli.yaml` / `specs/sources.yaml` over hard-coding.

For architecture details (package layout, IR pipeline, overlay matrix, extension points), see [docs/architecture.md](docs/architecture.md).

## Reporting bugs

Open an issue with a minimal reproduction: the spec (or its shape), the command you ran, and what you expected vs. what happened. Logs from `--debug` and `-o raw` are usually more useful than `-o table`.

## Security

See [SECURITY.md](SECURITY.md) for vulnerability reporting.
