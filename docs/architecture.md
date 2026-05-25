# Architecture

How lathe turns an API spec into a CLI, the packages involved, and the contracts between them. For user-facing usage, see [../README.md](../README.md).

## Prime idea

> The spec is input. The CLI is output. Humans curate the edges; code fills the middle.

## Two-phase model

lathe has two disjoint phases. They share types (`runtime.CommandSpec`) but run at different times and in different binaries.

```mermaid
flowchart LR
    subgraph codegen["Codegen-time (in the template repo)"]
        direction TB
        S1[specs/sources.yaml] --> S2[lathe specsync]
        S2 --> S3[lathe codegen]
        S3 --> S4[internal/generated/*]
    end

    subgraph runtime["Runtime (in the end-user's shell)"]
        direction TB
        R1[go build] --> R2[single binary]
        R2 --> R3[cobra command tree]
        R3 --> R4[HTTP request]
        R4 --> R5[formatted output]
    end

    S4 -.compiled into.-> R1

    style codegen fill:#eef2ff,stroke:#4338ca
    style runtime fill:#ecfdf5,stroke:#047857
```

The seam is `internal/generated/<module>/<module>_gen.go` — a `[]runtime.CommandSpec` literal per module. Everything above the seam is a build concern; everything below is a user concern.

## Codegen pipeline

```mermaid
flowchart TD
    A[specs/sources.yaml] -->|sourceconfig.Load| B[Config]
    B --> C{backend?}

    C -->|swagger| D1[specsync.syncSwagger]
    C -->|openapi3| D3[specsync.syncOpenAPI3]
    C -->|proto| D2[specsync.syncProto]

    D1 --> E1[.cache/specs-sync/mod/*.swagger.json]
    D3 --> E3[.cache/specs-sync/mod/*.yaml / *.json]
    D2 --> E2[.cache/specs-sync/mod/*.proto + deps]

    E1 -->|swagger.Parse| F[rawir.RawModule]
    E3 -->|openapi3.Parse| F
    E2 -->|proto.Parse| F

    F -->|normalize.Normalize| G["[]runtime.CommandSpec"]
    H[internal/overlay/&lt;mod&gt;.yaml] -->|overlay.LoadDir| I[Overrides]
    I -->|merge| G

    G -->|render.RenderModule| J[internal/generated/mod/mod_gen.go]

    style F fill:#fef3c7,stroke:#b45309
    style G fill:#fef3c7,stroke:#b45309
    style J fill:#dcfce7,stroke:#16a34a
```

Three backends fan in to a single raw IR (`rawir.RawModule`). `normalize` projects it onto `CommandSpec`. `render` is a pure template emit.

### Raw IR vs runtime spec

Two IRs exist on purpose. `rawir` preserves backend-adjacent detail (schemas, refs, per-response shape) needed for normalization decisions (list-path detection, column picking). `runtime.CommandSpec` is the minimal declarative form the runner needs. The boundary is enforced by the package graph: nothing under `pkg/runtime` imports `internal/codegen/**`.

### Why three backends, one IR

| Concern | Swagger backend | OpenAPI 3 backend | Proto backend |
|---|---|---|---|
| Grouping | operation's first `tag` | operation's first `tag` | `service` name |
| Operation ID | `operationId` | `operationId` | `rpc` name |
| Path / method | operation object | operation object | `google.api.http` annotation |
| Body schema | `requestBody` | `requestBody` (with `$ref` rewrite) | input message |
| Response schema | first 2xx response | first 2xx response | output message |

All normalized into the same `RawOperation` fields. By the time a spec reaches `normalize.Normalize`, the origin is irrelevant.

## Package layout

```mermaid
graph TD
    subgraph cmd["cmd/ — binaries"]
        C1[cmd/lathe]
    end

    subgraph internal["internal/ — codegen-only"]
        I0[lathecmd]
        I1[specsync]
        I2[sourceconfig]
        I3[codegen/backends/swagger]
        I3b[codegen/backends/openapi3]
        I4[codegen/backends/proto]
        I5[codegen/normalize]
        I6[codegen/rawir]
        I7[codegen/render]
        I8[overlay]
    end

    subgraph pkg["pkg/ — library (compiled into downstream CLI)"]
        P1[pkg/config]
        P2[pkg/runtime]
        P3[pkg/lathe]
        P4[internal/auth]
    end

    C1 --> I0
    I0 --> I1
    I0 --> I2
    I0 --> I3
    I0 --> I3b
    I0 --> I4
    I0 --> I5
    I0 --> I7
    I0 --> I8

    I3 --> I6
    I3b --> I6
    I4 --> I6
    I5 --> I6
    I5 --> P2
    I7 --> P2
    I7 --> I8

    P3 --> P1
    P3 --> P2
    P3 --> P4
    P4 --> P1

    style pkg fill:#ecfdf5,stroke:#047857
    style internal fill:#eef2ff,stroke:#4338ca
    style cmd fill:#fef3c7,stroke:#b45309
```

### Responsibilities

| Package | Phase | Responsibility |
|---|---|---|
| `cmd/lathe` | codegen | Single binary entrypoint for `specsync`, `codegen`, and `bootstrap`. |
| `internal/lathecmd` | codegen | Resolves CLI flags and orchestrates: sync specs, load sources, verify sync state, parse, normalize, render. |
| `internal/sourceconfig` | codegen | Parse `specs/sources.yaml`. Requires `pinned_tag`; treats the value as an immutable ref. |
| `internal/specsync` | codegen | `git clone --filter=blob:none`, checkout pinned ref, stage relevant files into `.cache/specs-sync/<module>/`. Writes `sync-state.yaml` (including `resolved_sha`). |
| `internal/codegen/backends/swagger` | codegen | Parse `*.swagger.json` → `RawModule`. Merges multiple files; first-seen wins on duplicates. |
| `internal/codegen/backends/openapi3` | codegen | Parse OpenAPI 3.x YAML/JSON → `RawModule`. Rewrites `$ref`; inherits path-level parameters. |
| `internal/codegen/backends/proto` | codegen | Parse staged `.proto` tree → `RawModule`. Only RPCs with `google.api.http` become operations. |
| `internal/codegen/rawir` | codegen | Backend-agnostic raw types (`RawModule`, `RawOperation`, `RawSchema`). Includes `$ref` resolution. |
| `internal/codegen/normalize` | codegen | Semantic projection: groups, `Short`, list path, default columns, method-ordering for determinism. |
| `internal/codegen/render` | codegen | `text/template` → gofmt'd Go. Emits per-module `_gen.go` and top-level `modules_gen.go`. |
| `internal/overlay` | codegen | Load `internal/overlay/<module>.yaml`. Baked into `CommandSpec` at codegen time. Runtime never sees overlays. |
| `internal/auth` | runtime | `auth login/logout/status`. Calls the configured validate endpoint. |
| `pkg/config` | runtime | `Manifest` (CLI identity) and `Hosts` (per-hostname credentials). `Bind(m)` seeds package-level helpers. |
| `pkg/runtime` | runtime | `CommandSpec` IR, `Build`, body builder, HTTP client with retry, `Authenticator` interface, `Formatter` registry, `LatheError`, schema version contract. |
| `pkg/lathe` | runtime | `NewApp(m)` — root cobra command with auth subtree and module groups. |

## Spec lifecycle

```mermaid
stateDiagram-v2
    [*] --> Declared: edit specs/sources.yaml
    Declared --> Cloned: lathe specsync\n(git clone + checkout pinned_tag)
    Cloned --> Staged: backend-specific staging\n(.cache/specs-sync/&lt;mod&gt;/)
    Staged --> Parsed: backend.Parse\n→ RawModule
    Parsed --> Normalized: normalize.Normalize\n→ []CommandSpec
    Normalized --> Polished: overlay merge\n(optional)
    Polished --> Emitted: render.RenderModule\n→ internal/generated/&lt;mod&gt;/&lt;mod&gt;_gen.go
    Emitted --> Compiled: go build
    Compiled --> Runnable: ./bin/&lt;name&gt; &lt;mod&gt; &lt;cmd&gt;
    Runnable --> [*]

    Declared --> Declared: bump pinned_tag\n→ restart cycle
```

Each transition is idempotent and cache-checked. `specsync.VerifyState` rejects a stale cache where `sync-state.yaml` does not match `pinned_tag`.

## Overlay merge matrix

Overlays apply at codegen-time. The runtime has no overlay types. This matrix shows what each field's value source is and whether overlay can modify it.

### CommandSpec level

| IR field | Spec source | Overlay | Priority |
|---|---|---|---|
| `Use` | `operationId`-derived | rename | overlay > spec |
| `Group` | `tags[0]` / service name | override | overlay > spec |
| `Short` | `summary` / first comment | override | overlay > spec |
| `Long` | `description` / comment block | override | overlay > spec |
| `Aliases` | — | append | overlay-only |
| `Example` | — | set | overlay-only |
| `Method`, `PathTpl`, `HasBody` | spec | locked | spec-only |
| `OperationID` | `operationId` | — | spec-only |
| `Hidden` | `x-cli-hidden` | bool | overlay > spec |
| `Deprecated` | `deprecated` / proto option | bool + message | overlay > spec |
| `Security` | `security` / proto option | override (post-v0.1) | overlay > spec |
| `RequestBody.MediaType` | `consumes[0]` | override | overlay > spec |
| `Ignore` (command filter) | — | bool | overlay-only |

### ParamSpec level

| IR field | Spec source | Overlay | Priority |
|---|---|---|---|
| `Name` | spec | locked | spec-only |
| `Flag` | kebab-derived | rename | overlay > spec |
| `In` | spec | locked | spec-only |
| `GoType` | `type` / `format` | narrowing only (post-v0.1) | overlay > spec |
| `Help` | description / comment | override | overlay > spec |
| `Required` | `required` / path rule | relaxation only (post-v0.1) | overlay > spec |
| `Default` | spec or overlay | value | overlay > spec |
| `Enum`, `Format` | spec | override (post-v0.1) | overlay > spec |
| `Deprecated` | `param.deprecated` | bool | overlay > spec |

Two restricted-override rules: **`Required`** may only relax (required → optional), never tighten. **`GoType`** may only narrow (e.g. `string` → typed enum), never widen.

## Runtime request lifecycle

```mermaid
sequenceDiagram
    autonumber
    participant Shell
    participant Root as cobra root
    participant Runner as runtime.Build
    participant Host as hosts.yml / $NAME_HOST
    participant Body as body builder
    participant HTTP
    participant Out as output formatter

    Shell->>Root: acmectl iam create-user --email alice@example.com
    Root->>Runner: dispatched to generated module cmd
    Runner->>Host: ResolveHost(--hostname, $NAME_HOST, hosts.yml)
    alt exactly one host && no flag
        Host-->>Runner: auto-selected host
    else multiple hosts, no flag, no env
        Host-->>Runner: ErrAmbiguousHost
    else --hostname or env
        Host-->>Runner: selected host + credentials
    end

    Runner->>Body: assemble body (--file, --set dotted.paths, --set-str dotted.paths)
    Body-->>Runner: JSON payload

    Runner->>HTTP: method + PathTpl(params) + body + auth header
    HTTP-->>Runner: response

    Runner->>Out: format(-o table|json|yaml|raw, OutputHints)
    Out-->>Shell: stdout
```

Three pieces of state cross the boundary:

1. **Manifest** (immutable, from `cli.yaml` embedded at build time) — CLI identity and auth shape.
2. **Hosts** (mutable, `~/.config/<name>/hosts.yml`) — per-hostname credentials. No "current host" stored.
3. **Flags** (transient) — `--hostname`, `--output`, `--insecure`, plus operation-specific flags.

## Extension points

| Extension | Built-in | Planned |
|---|---|---|
| Transport | Retry with exponential backoff, `Retry-After`, User-Agent. `WithTransport` injection. | Tracing, rate-limit middleware. |
| Authenticator | `Bearer` / `NoAuth`. Selectable per host via `ClientOptions.Auth`. | API key, mTLS, SigV4, OAuth-refresh. |
| Formatter | `table`, `json`, `yaml`, `raw`. `RegisterFormatter(name, f)`. | JMESPath, CSV, user templates. |
| Post-processor | Cursor-based pagination (`--all`, `--max-pages`), LRO polling (202 + Location). | Link header, offset-based pagination. |

Each extension is an injection point. The core works with a zero-config `CommandSpec`.

## Design invariants

These are structural, not stylistic. Violating any means the architecture breaks.

1. **`pkg/runtime` does not import `internal/codegen/**`.** The runtime cannot know how a `CommandSpec` was produced. This is what makes "three backends, one IR" real rather than aspirational.
2. **`pinned_tag` is required and validated.** `sourceconfig.Load` rejects empty values and floating refs (`HEAD`, `main`, `refs/heads/*`). Only immutable tags and 40-char SHAs are accepted. `specsync` records the resolved SHA and codegen verifies it.
3. **Codegen is never invoked at `go build` time.** Downstream consumers need no Go toolchain tags, build flags, or network access to install.
4. **Overlays bake at codegen-time.** The runtime has no overlay concept. This keeps `pkg/runtime` small and overlay bugs from being runtime bugs.
5. **No ambient "current host".** The host is a per-invocation input. This mirrors `gh` and avoids the "oops, wrong cluster" class of bug.
6. **`sync-state.yaml` guards the cache.** `lathe codegen` refuses a cache that doesn't match `pinned_tag`. Stale generation fails loud, not silent.
7. **Static codegen.** Downstream binaries carry no spec parser. The generated file is a pure data literal.
8. **Single Go binary.** No `protoc`, `buf`, or other toolchain at install time. `go install` is the install path.

## Where to look next

- **Using the CLI** — [../README.md](../README.md)
- **Contributing** — [../CONTRIBUTING.md](../CONTRIBUTING.md)
- **Runtime IR** — [../pkg/runtime/spec.go](../pkg/runtime/spec.go)
- **Raw IR** — [../internal/codegen/rawir/types.go](../internal/codegen/rawir/types.go)
- **Example overlay** — [../examples/overlay/example.yaml](../examples/overlay/example.yaml)
