# Lathe CLI Usage

Lathe ships one generator binary: `lathe`.

Use it in a target CLI repository that owns:

- `go.mod`: the downstream module path for generated internal package imports.
- `cli.yaml`: the generated CLI identity and optional auth validation config.
- `specs/sources.yaml`: declared upstream or local API specs.
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
  login:
    type: oauth_device
    start_path: /auth/device/start
    token_path: /auth/device/token
    refresh_path: /auth/device/refresh
  validate:
    method: GET
    path: /api/v1/whoami
    display:
      username_field: data.username
      fallback_field: data.email
```

The `cli.name` value becomes the generated binary name and root command name.
`cli.command_path` defaults to `auto`: a single source module uses
`<cli> <group> <operation>` when its groups do not conflict with root commands,
and multiple modules use `<cli> <module> <group> <operation>`. Set it to
`namespaced` to always keep the module segment, or `flat` to require the single
module flat path and fail codegen on root command conflicts.

`auth.login.type: oauth_device` enables `auth login --device-auth` and
`auth login --auth-type oauth`. The service must provide `start_path` and
`token_path` endpoints using OAuth 2.0 device-flow fields (`device_code`,
`verification_uri`, optional `user_code`, `interval`, `expires_in`, and later
`access_token`). `refresh_path` is optional; when present, generated commands
refresh expired bearer tokens before execution.

To customize generated Skill output, add an optional top-level `skill` block:

```yaml
skill:
  root: skills
  include: internal/skill-include
  bundle: true
```

The optional `skill.root` value controls where generated Skill files are written;
set it to `""` to disable Skill generation. The optional `skill.include` value
points at repo-local Skill resources merged into generated Skill files. The
include path must stay outside `skill.root`. Set `skill.bundle: true` to compile
the generated Skill into the CLI and expose `<cli> skill install`; this requires
Skill generation to remain enabled. For per-file policy, use object form:

```yaml
skill:
  root: skills
  include:
    path: internal/skill-include
    files:
      agents/openai.yaml: replace
      references/modules/acme.md: omit
```

Generated CLIs expose `--version` and `-v`. The release version is
build metadata, not `cli.yaml` data: pass `Version`, `Commit`, and `Date` through
`lathe.RunOptions`, or inject `github.com/lathe-cli/lathe/pkg/lathe.Version`,
`Commit`, and `Date` with Go `-ldflags` in your release build.

To let the generated CLI update itself from GitHub Releases, add:

```yaml
update:
  github:
    owner: acme
    repo: acmectl
    asset: "acmectl_{{ .Version }}_{{ .OS }}_{{ .Arch }}.tar.gz"
```

`asset` is a Go template with `Name`, `Version`, `Tag`, `OS`, and `Arch`.
`update` fetches the latest GitHub release, reports when the current binary is
already current, and asks for confirmation before replacing the executable. Pass
`--yes` to skip the prompt. The release asset must expose a `sha256:` digest; zip,
tar.gz, and tgz assets must contain a binary named after `cli.name`.

## Declare API Specs

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

  console:
    repo_url: https://github.com/acme/graphql-console.git
    pinned_tag: v3.0.0
    backend: graphql
    graphql:
      schema: schema/console.graphql
      expose:
        queries: ["listApps", "getApp"]
        mutations: ["createApp"]
      groups:
        - match: ["*App*"]
          group: Apps
      output:
        - match: ["listApps"]
          list_path: data.listApps.nodes
          default_columns: ["id", "name", "status"]
      selection:
        max_depth: 2
        prune: ["App.secretToken"]

  awire:
    local_path: ../awire
    backend: openapi3
    openapi3:
      files:
        - openapi/awire.yaml
```

Use immutable tags for reproducibility. `lathe specsync` resolves each tag to a
commit SHA and writes sync state under `.cache/specs-sync/`.

Use `local_path` instead of `repo_url`/`pinned_tag` for a working-tree source.
Relative paths resolve from `specs/sources.yaml`; sync state records the
resolved local path for cache checks.

For `backend: graphql`, the pinned `graphql.schema` SDL file supplies API facts:
root operations, arguments, return types, and selectable fields. The required
`graphql.expose` policy decides which root fields become commands; there is no
implicit expose-all mode. Mutations are listed separately so a query wildcard
cannot expose destructive operations.

Optional GraphQL policy blocks tune the generated CLI contract:

- `graphql.groups`: operation-name globs mapped to CLI groups. If one operation
  matches more than one group rule, codegen fails closed.
- `graphql.output`: operation-name globs with configured `list_path` and/or
  `default_columns`. Dotted paths such as `data.listApps.nodes` are supported.
- `graphql.selection`: `max_depth` for generated selection sets and `prune`
  patterns in `Type.field` form. Explicit `max_depth` must be greater than zero.

GraphQL commands execute `POST /graphql` with a baked JSON body template:
`{"query": "...", "variables": {}}`. Scalar and enum arguments become typed CLI
flags and merge under `variables`. Input-object arguments expand scalar and enum
leaf fields into dotted variable flags such as `--input-name`; complex fields
remain query-declared in the body schema and can be supplied with `--set` or
`--file`.
Relay-style outputs with `list_path: data.<operation>.nodes` or `.edges`,
`pageInfo.endCursor`, `pageInfo.hasNextPage`, and an `after` argument get
`--all`; subsequent pages write the cursor back under `variables.after`.

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
lathe codegen -skill-include internal/skill-include
```

`-skill-root` and `-skill-include` override the `skill.root` and `skill.include`
path from `cli.yaml` for one run. Prefer `cli.yaml` for reproducible generated
Skill output. When an include path is set, the directory must already exist and
must be outside `skill.root`. Files under the include directory are mapped by
relative path onto `<skill.root>/<cli-name>/<rel>`. Include files may target
`SKILL.md`, `references/`, `scripts/`, `assets/`, and `agents/`.
By default, `SKILL.md` and existing `references/**/*.md` targets are appended
after a blank line; new files under the allowed Skill resource directories are
created. Object-form `skill.include.files` can set a path to `append`, `create`,
`replace`, or `omit`. `replace` requires a same-path include file and an existing
generated target. `omit` removes eligible generated files such as
`agents/openai.yaml` or `references/modules/*.md`; omitted module references are
also removed from generated `SKILL.md`. `SKILL.md` may be appended or replaced,
but not omitted. `.lathe-skill`, dotfiles, path traversal, and symlinks are
rejected.

Generated outputs:

```text
internal/generated/
<skill.root>/<cli-name>/
```

`internal/generated/` contains generated Go command specs. `<skill.root>/<cli-name>/`
contains the generated agent Skill guide and module references when Skill
generation is enabled. These outputs are reproducible from `cli.yaml`,
`specs/sources.yaml`, synced specs, and overlays. Skill output also includes any
resources declared by `skill.include`. When `skill.bundle` is true, codegen also
mirrors the Skill tree under `internal/generated/skillbundle/` and pins:

```sh
go get github.com/lathe-cli/kitup/go@v0.1.3 github.com/lathe-cli/kitup/go-cobra@v0.1.3
```

## Wire the Generated CLI

Create `cmd/<cli-name>/main.go`:

```go
package main

import (
	_ "embed"
	"os"

	"github.com/lathe-cli/lathe/pkg/lathe"

	"example.com/acme/internal/generated"
)

//go:embed cli.yaml
var manifestBytes []byte

func main() {
	os.Exit(lathe.Run(lathe.RunOptions{
		Manifest: manifestBytes,
		Mount:    generated.Mount,
	}))
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
bin/acmectl __lathe verify --json
bin/acmectl search "create user" --json
bin/acmectl commands show users users create --json
bin/acmectl commands schema --json
bin/acmectl auth status --hostname api.acme.com
bin/acmectl users users create --set email=alice@example.com --dry-run
bin/acmectl users users create --set email=alice@example.com -o json
```

Rules:

- Treat `search` output as candidates only.
- Run `__lathe verify --json` after building a generated CLI to check the local
  command contract before live calls.
- Inspect exact command details with `commands show` before execution.
- Use `examples` from command detail when overlays provide runnable command metadata.
- Run `auth status --hostname <host>` before authenticated commands.
- Use `--dry-run` to inspect the resolved request JSON before sending it.
- Prefer `-o json` for agent-readable output.
- Use `--file`, `--set`, or `--set-str` according to the command detail body
  contract.
- If a command detail flag exposes `input_modes`, prefer `--<flag>-env NAME`,
  `--<flag>-file path`, or `--<flag>-stdin` over passing secrets directly in
  shell arguments.

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
bin/petstore commands show pets list --json
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
bin/richapi commands show users list --json
```

### GraphQL

`examples/graphql` is the minimal GraphQL path for a staged SDL file plus
explicit `graphql:` policy:

```text
cd examples/graphql
cli.yaml
specs/sources.yaml
cmd/graphqlctl/main.go
lathe codegen -cache fixtures
go mod tidy
go build -o bin/graphqlctl ./cmd/graphqlctl
bin/graphqlctl commands show apps list-apps --json
bin/graphqlctl commands show apps create-app --json
```

The command detail exposes `POST /graphql`, the templated `{query, variables}`
body, `body.merge_path=variables`, typed variable flags, and configured output
hints.
