# GraphQL CLI Path

Demonstrates the Lathe workflow for a GraphQL control-plane API backed by a
pinned schema and explicit `graphql:` policy.

## Path

1. Inspect `cli.yaml`, `specs/sources.yaml`, and the checked-in fixture cache.
2. Run `lathe codegen -cache fixtures`.
3. Use `cmd/graphqlctl/main.go` to mount `internal/generated`.
4. Run `go mod tidy` and `go build -o bin/graphqlctl ./cmd/graphqlctl`.
5. Inspect generated GraphQL contracts with `commands show`.

## Surfaces To Verify

The GraphQL path is useful when validating:

- `backend: graphql` schema staging.
- Explicit `expose.queries` and `expose.mutations` policy.
- Policy-driven grouping, output hints, and selection pruning.
- `POST /graphql` commands with baked `{query, variables}` request envelopes.
- Variable flags merged under `body.merge_path=variables`.
- Generated Skill module references for GraphQL sources.

## Smoke

```sh
lathe codegen -cache fixtures
go mod tidy
go build -o bin/graphqlctl ./cmd/graphqlctl
bin/graphqlctl commands show apps list-apps --json
```

The command detail should expose `POST /graphql`, a templated body containing a
GraphQL query document, `body.merge_path` set to `variables`, and output hints
for `data.listApps.nodes`.

See [CLI Usage](../../docs/cli-usage.md) for the full command sequence and
agent loop.
