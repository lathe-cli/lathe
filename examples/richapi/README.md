# Rich API CLI Path

Demonstrates the broader Lathe workflow for APIs that exercise more runtime
metadata than the Petstore path.

## Path

1. Inspect `cli.yaml`, `specs/sources.yaml`, and the checked-in fixture cache.
2. Run `lathe codegen -cache fixtures`.
3. Use `cmd/richapi/main.go` to mount `internal/generated`.
4. Run `go mod tidy` and `go build -o bin/richapi ./cmd/richapi`.
5. Inspect generated command contracts with `commands --json` and `commands show`.

## Surfaces To Verify

The rich API path is useful when validating:

- Pagination hints.
- Enum flags.
- Query and cookie parameter serialization (`roles`, `tenant`).
- Header parameters.
- Required and optional JSON request bodies.
- Public endpoints with no auth requirement.
- Streaming response hints.
- Long-running operation hints.

See [CLI Usage](../../docs/cli-usage.md) for the full command sequence and agent loop.
