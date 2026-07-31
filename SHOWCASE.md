# Showcase

This page collects Lathe generation paths and public adoption notes.

## CLI Generation Path

Use the `lathe` CLI to generate a downstream Cobra CLI from declared API specs:

```sh
go mod init example.com/acme   # skip when go.mod already exists
lathe bootstrap
go mod tidy
go build -o bin/<cli-name> ./cmd/<cli-name>
```

Then verify the generated agent-facing surface:

1. `<cli-name> search "<intent>" --json`
2. `<cli-name> commands show <path...> --json`
3. `<cli-name> commands schema --json`

This is the core Lathe promise: agents discover commands, inspect exact contracts, check schema compatibility, and only then execute.

## Examples

- `examples/petstore`: minimal OpenAPI 3 path from pinned spec to generated CLI.
- `examples/richapi`: broader generated CLI path covering pagination, enums, headers, request bodies, public endpoints, streaming hints, and long-running operation hints.
- `examples/graphql`: policy-curated GraphQL path covering request envelopes, complex variables, body-cursor pagination, and generated output hints.

See [CLI Usage](docs/cli-usage.md) for the exact files, commands, and generated CLI verification loop.

## Add a Showcase

Open a pull request adding a short entry when you use Lathe in a real project. Good entries include:

- What API spec type you use.
- What generated CLI workflow Lathe replaced.
- Which catalog or agent workflow made the project safer or faster.
- Public links when available.
