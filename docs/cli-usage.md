# Lathe CLI Usage

Lathe ships one generator binary: `lathe`.

Use it in a target CLI repository that owns:

- `go.mod`: the downstream module path for generated internal package imports.
- `cli.yaml`: the generated CLI identity and optional auth validation config.
- `specs/sources.yaml`: pinned upstream API specs.
- `cmd/<cli-name>/main.go`: the thin runtime entrypoint for the generated CLI.

The normal path is:

```sh
go mod init example.com/acme   # skip when go.mod already exists
lathe bootstrap
go mod tidy
go build -o bin/<cli-name> ./cmd/<cli-name>
```

`lathe bootstrap` is equivalent to:

```sh
lathe specsync
lathe codegen
```

## Install

Download the archive for your platform from the latest GitHub release, unpack
it, and place `lathe` on your `PATH`.

From a source checkout of this repository, build a local snapshot with Go:

```sh
go build -o bin/lathe ./cmd/lathe
./bin/lathe version
```

Use the built binary as `lathe`, or pass its path explicitly in examples.
Release-shaped snapshots are built by the release workflow with Goreleaser.

## Initialize the Go Module

Lathe codegen reads the target repository's module path when it renders
`internal/generated/modules_gen.go`. If the target repository does not already
have `go.mod`, initialize it before running `lathe bootstrap`:

```sh
go mod init example.com/acme
```

## Configure the Generated CLI

Create `cli.yaml` in the target repository:

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

The `cli.name` value becomes the generated binary name and root command name.

## Pin API Specs

Create `specs/sources.yaml`:

```yaml
sources:
  users:
    repo_url: https://github.com/acme/users-api.git
    pinned_tag: v1.4.0
    backend: openapi3
    openapi3:
      files:
        - openapi.yaml

  billing:
    repo_url: https://github.com/acme/billing-api.git
    pinned_tag: v0.9.2
    backend: swagger
    swagger:
      files:
        - swagger.json

  accounts:
    repo_url: https://github.com/acme/accounts-api.git
    pinned_tag: v2.1.0
    backend: proto
    proto:
      staging:
        - from: api/proto
          to: "."
      entries:
        - v1/accounts.proto
```

Use immutable tags for reproducibility. `lathe specsync` resolves each tag to a
commit SHA and writes sync state under `.cache/specs-sync/`.

## Generate Code

After `go.mod`, `cli.yaml`, and `specs/sources.yaml` exist, run both phases:

```sh
lathe bootstrap
```

Or run them separately:

```sh
lathe specsync
lathe codegen
```

Useful flags:

```sh
lathe specsync -source users
lathe specsync -cache .cache
lathe codegen -overlay internal/overlay
lathe codegen -skill-root ""
```

Generated outputs:

```text
internal/generated/
skills/<cli-name>/
```

`internal/generated/` contains generated Go command specs. `skills/<cli-name>/`
contains the generated agent Skill guide and module references. These outputs
are reproducible from `cli.yaml`, `specs/sources.yaml`, pinned specs, and
overlays.

## Wire the Generated CLI

Create `cmd/<cli-name>/main.go`:

```go
package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/samzong/lathe/pkg/config"
	"github.com/samzong/lathe/pkg/lathe"
	"github.com/samzong/lathe/pkg/runtime"

	"example.com/acme/internal/generated"
)

//go:embed cli.yaml
var manifestBytes []byte

func main() {
	m, err := config.Load(manifestBytes)
	if err != nil {
		panic(err)
	}
	config.Bind(m)

	root := lathe.NewApp(m)
	if err := generated.MountModules(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(runtime.Execute(root))
}
```

Copy `cli.yaml` next to `main.go` so `go:embed` can include it:

```sh
mkdir -p cmd/acmectl
cp cli.yaml cmd/acmectl/cli.yaml
```

Build the generated CLI:

```sh
go mod tidy
go build -o bin/acmectl ./cmd/acmectl
```

## Agent Operation Loop

Generated CLIs expose machine-readable contracts. Agents should use this loop:

```sh
bin/acmectl search "create user" --json
bin/acmectl commands show users users create --json
bin/acmectl commands schema --json
bin/acmectl auth status --hostname api.acme.com
bin/acmectl users users create --set email=alice@example.com -o json
```

Rules:

- Treat `search` output as candidates only.
- Inspect exact command details with `commands show` before execution.
- Run `auth status --hostname <host>` before authenticated commands.
- Prefer `-o json` for agent-readable output.
- Use `--file`, `--set`, or `--set-str` according to the command detail body
  contract.

## Example Paths

### Petstore

`examples/petstore` is the minimal OpenAPI 3 path:

```text
cd examples/petstore
cli.yaml
specs/sources.yaml
cmd/petstore/main.go
lathe codegen -cache fixtures
go mod tidy
go build -o bin/petstore ./cmd/petstore
bin/petstore search "list pets" --json
bin/petstore commands show pets pets list --json
bin/petstore commands schema --json
```

### Rich API

`examples/richapi` is the broader generated CLI path for APIs with pagination,
enums, headers, request bodies, public endpoints, streaming hints, and
long-running operation hints:

```text
cd examples/richapi
cli.yaml
specs/sources.yaml
cmd/richapi/main.go
lathe codegen -cache fixtures
go mod tidy
go build -o bin/richapi ./cmd/richapi
bin/richapi commands --json
bin/richapi commands show acme users list --json
```
