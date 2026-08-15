# Security Policy

## Supported versions

Lathe is pre-release (v0.x). Security fixes land on `main` and are published in
the next release. Older v0.x release lines are not backported.

## Reporting a vulnerability

Please **do not** open a public GitHub issue for a vulnerability. Email the
project owner at [samzong.lu@gmail.com](mailto:samzong.lu@gmail.com).

Include enough detail to reproduce the issue: the spec, the command, and the
impact you observed. Expect a first response within 7 days.

## Scope

In scope:

- Command-injection / argument-parsing flaws in the generated CLI surface.
- SSRF / path-traversal / unsafe TLS defaults in the HTTP client (`pkg/runtime/client.go`).
- Leakage of tokens or host secrets via logs, error messages, or persisted files.
- Codegen emitting unsafe patterns that propagate to downstream binaries.

Out of scope:

- Vulnerabilities in the upstream spec content itself (lathe trusts `specs/sources.yaml` inputs).
- Issues in third-party dependencies — report those upstream and let us know if a version bump is needed.
- Social-engineering, physical, or supply-chain attacks unrelated to lathe's code.

## Credit

We'll credit reporters in the release notes unless they prefer to remain anonymous.
