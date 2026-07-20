# Lathe Extension Architecture

Status: proposed.

## Purpose

Lathe should stay small at its core while still letting generated CLIs grow
optional capabilities such as bundled Skill installation, local workflow
commands, daemon control, or protocol adapters.

The core distinction is:

```text
Lathe core compiles a generated CLI application.
Extension packages provide optional capabilities.
The generated CLI is the final product that links them together.
```

This is not a runtime plugin system. Extensions are selected at generation time,
compiled into the downstream binary through normal Go builds, and must preserve
Lathe's reproducibility and inspectability guarantees.

## Current Boundary

Lathe already has a strong two-phase boundary:

- Codegen-time reads `specs/sources.yaml`, `cli.yaml`, optional overlays, and
  synced API specs.
- Runtime executes compiled `runtime.CommandSpec` literals through the generated
  Cobra command tree.
- `pkg/runtime` must not import `internal/codegen/**`.
- Generated output is static Go code and Skill files, not a live dependency on
  specs or codegen state.

Extensions must fit this boundary. They must not make runtime depend on raw
specs, overlays, sync cache state, or generator internals.

## Design Rule

Lathe should expose an extension composition model, not a public plugin ABI.

The safe first public surface is a domain capability switch, not an extension
mechanism:

```yaml
skill:
  bundle: true
```

Do not support remote extensions, dynamic plugins, `use:`, or third-party
codegen hooks until there is a clear security and compatibility protocol.

External capability code can still be linked into the generated CLI as ordinary
Go dependencies. Lathe codegen should generate import and mount glue, not execute
third-party extension code during generation.

## GeneratedApp

`GeneratedApp` is the internal model for the CLI application Lathe is about to
emit. It is not a public API and should not be treated as stable extension ABI.

It should collect every contribution before any files are written:

```go
type GeneratedApp struct {
	Manifest         *config.Manifest
	Modules          []GeneratedModule
	Files            []GeneratedFile
	Mounts           []GeneratedMount
	GoDeps           []GoDependency
	ReservedCommands []string
	Checks           []GeneratedCheck
}
```

The important behavior is the pipeline, not the exact struct shape:

```text
sources.yaml + cli.yaml
  -> parse and normalize API operations
  -> build GeneratedApp
  -> apply built-in extension contributions
  -> validate GeneratedApp
  -> render generated files
  -> write owned outputs
```

This avoids turning `runCodegen` into a growing list of feature-specific `if`
blocks. Modules, Skills, bundled installers, workflow commands, and future
adapters all become contributions to one generated app model.

## ExtensionContribution

An extension contribution should be boring and declarative:

```go
type ExtensionContribution struct {
	Files            []GeneratedFile
	Mounts           []GeneratedMount
	GoDeps           []GoDependency
	ReservedCommands []string
	Checks           []GeneratedCheck
}
```

Each contribution must answer:

- Which files will be generated?
- Which commands will be mounted?
- Which Go modules are required?
- Which root command names are reserved?
- Which verification checks prove the generated capability works?

It must not directly mutate files, edit `go.mod`, execute external generators, or
patch the Cobra root command outside the generated mount path.

## Generated Mount

The generated package should expose one stable entrypoint:

```go
os.Exit(lathe.Run(lathe.RunOptions{
	Manifest: manifestBytes,
	Mount:    generated.Mount,
}))
```

`generated.Mount(root)` should mount API modules first, then extension commands.
`generated.MountModules(root)` can remain for compatibility, but new docs should
prefer `generated.Mount(root)`.

This keeps downstream `cmd/<cli>/main.go` stable when new generated capabilities
are enabled. A downstream app should not need to learn whether a command came
from an API module, a bundled Skill installer, a workflow extension, or a daemon
adapter.

## First Built-In Extension

The first extension should be bundled Skill installation:

```yaml
skill:
  bundle: true
```

The public config should stay in the Skill domain. Do not use `kitup: true`;
`kitup` is the implementation detail.

The built-in extension should contribute:

- `skills/<cli-name>/` as the generated Skill source.
- A tiny embed bridge for that Skill tree.
- A generated mount that adds `<cli> skill install`.
- Go dependencies for the kitup packages used by the generated CLI.
- `skill` as a reserved root command.
- A temp-`HOME` install smoke check.

Kitup should own host detection, install planning, confirmation, conflict
handling, force behavior, dry-run output, and ownership metadata. Lathe should
only provide the generated Skill bytes and command mounting.

## Dependencies

The first implementation should rely on normal Go behavior:

```sh
go get github.com/lathe-cli/kitup/go@v0.1.3 github.com/lathe-cli/kitup/go-cobra@v0.1.3
```

Generated code imports the required packages, and Lathe pins the two kitup Go
modules explicitly when `skill.bundle` is enabled. This is simple and
reproducible.

Do not add a separate dependency command until a second dependency source exists.

## OperationInvoker

Workflow, daemon, websocket, and branching automation extensions should not shell
out to the generated CLI. Shelling out would make flags, stdout parsing, auth
state, dry-run behavior, error structure, redaction, and streaming semantics
second-hand.

Before workflow-like extensions are added, Lathe needs a programmatic operation
invocation layer:

```go
Invoke(ctx, OperationRef, Input) (Result, error)
```

Cobra commands should use this layer. Workflow and daemon extensions can then
reuse the same API execution path without duplicating request construction or
auth behavior.

Until this exists, do not add a workflow extension.

## Validation

Generated app validation should fail before writing outputs when any of these
are true:

- Two generated files target the same path without an explicit ownership rule.
- A module, shortcut, or extension reserves the same root command name.
- An extension requires Skill generation while `skill.root` is disabled.
- An extension declares a Go dependency that cannot be represented
  deterministically.
- A generated mount has no corresponding verification check for user-visible
  behavior.

Every extension must produce evidence. For bundled Skill installation, the
minimum evidence is a generated CLI build plus a temp-`HOME` run of:

```sh
<cli> skill install --scope user --agent codex --yes
```

The check should verify that `~/.agents/skills/<cli-name>/SKILL.md` and kitup
ownership metadata exist.

## Non-Goals

Do not add these in the first extension architecture pass:

- Runtime plugin loading.
- Remote extension fetching.
- Third-party codegen execution.
- A public `GeneratedApp` ABI.
- A workflow DSL.
- A daemon framework.
- MCP server generation.
- A Skill marketplace or registry.

Those may become downstream extensions or separate projects later, but they
should not enter the core until the generated app model and invocation contract
are stable.

## Implementation Order

1. Introduce `GeneratedApp` internally and migrate current module and Skill
   outputs into it without changing generated behavior.
2. Generate `generated.Mount(root)` and keep `generated.MountModules(root)` for
   compatibility.
3. Add `skill.bundle: true`, the Skill embed bridge, kitup dependency pinning,
   and generated mount wiring.
4. Add `__lathe verify` report versioning and the `skill_install` check.
5. Add `catalog.cli.capabilities` and bump the catalog schema.
6. Add app-level validation for file paths, root command names, dependencies,
   and checks.
7. Design `OperationInvoker` before any workflow, daemon, websocket, or
   branching API orchestration extension.

## Principle

Do not trade Lathe's deterministic core for early ecosystem breadth.

The valuable core is:

```text
API spec -> normalized operation IR -> command catalog -> inspectable CLI runtime
```

Extensions are allowed only when they cannot break that contract.
