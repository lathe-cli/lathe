# Petstore CLI Path

Demonstrates the minimal Lathe workflow: OpenAPI 3 spec -> codegen -> working CLI binary.

## Path

1. Inspect `cli.yaml`, `specs/sources.yaml`, and the checked-in fixture cache.
2. Run `lathe codegen -cache fixtures -overlay overlays`.
3. Use `cmd/petstore/main.go` to mount `internal/generated`.
4. Run `go mod tidy` and `go build -o bin/petstore ./cmd/petstore`.
5. Verify the generated agent loop with `search`, `commands show`, and `commands schema`.
6. Try the generated-command shortcut: `petstore pet-123` executes `petstore pets get --id 123`.

## Expected output

```
Petstore CLI demo

Usage:
  petstore [command]

Authentication:
  auth        Authenticate petstore with a host

Modules:
  pet-123     Get a pet by ID
  pets        Pets operations

Additional Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  version     Print version information
```

## Adapting for your project

See [CLI Usage](../../docs/cli-usage.md) for the full command sequence. The key files are:

- **`cli.yaml`** — CLI name, description, auth endpoint
- **`specs/sources.yaml`** — upstream specs pinned at immutable tags
- **`overlays/pets.yaml`** — generated-command shortcuts and polish
- **`cmd/<name>/main.go`** — embed `cli.yaml`, call `lathe.NewApp`, then handle `generated.MountModules` errors
