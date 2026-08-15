# Petstore CLI Path

Demonstrates the minimal Lathe workflow: OpenAPI 3 spec -> codegen -> working CLI binary.

## Path

1. Inspect `cli.yaml`, `specs/sources.yaml`, and the checked-in fixture cache.
2. Run `lathe codegen -cache fixtures -overlay overlays`.
3. Use `cmd/petstore/main.go` to mount `internal/generated`.
4. Run `go mod tidy` and `go build -o bin/petstore ./cmd/petstore`.
5. Verify the generated agent loop with `search`, `commands show`, and `commands schema`.
6. Try the generated-command shortcut: `petstore pet-123` executes `petstore pets get --id 123`.

## Expected surface

The generated root exposes the `pets` module, the `pet-123` shortcut, and the
agent discovery commands `search` and `commands`. Inspect live help instead of
copying a full Cobra help snapshot:

```sh
./bin/petstore --help
./bin/petstore search pet --json
./bin/petstore commands show pets get --json
./bin/petstore commands schema --json
```

## Adapting for your project

See [CLI Usage](../../docs/cli-usage.md) for the full command sequence. The key files are:

- **`cli.yaml`** — CLI name and description
- **`specs/sources.yaml`** — upstream specs pinned at immutable tags
- **`overlays/pets.yaml`** — generated-command shortcuts and polish
- **`cmd/<name>/main.go`** — embed `cli.yaml` and call `lathe.Run` with `generated.MountModules`
