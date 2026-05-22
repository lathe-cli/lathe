# Maintainers

## Current Maintainers

- samzong - project owner and release maintainer
  - GitHub: https://github.com/samzong
- scydas - project collaborator and code reviewer
  - GitHub: https://github.com/scydas
- Frapschen - project collaborator and code reviewer
  - GitHub: https://github.com/Frapschen

## Responsibilities

Maintainers are responsible for:

- Triaging issues and pull requests.
- Keeping project direction aligned with Lathe's product boundary.
- Reviewing changes for correctness, safety, compatibility, and maintainability.
- Cutting releases and publishing release notes.
- Enforcing the Code of Conduct.
- Responding to security reports according to `SECURITY.md`.

## Review Expectations

Pull requests should be reviewed for:

- Whether the change fixes the stated problem.
- Generated command correctness.
- Runtime behavior for HTTP, auth, body, output, pagination, polling, and errors.
- Catalog and generated Skill inspectability.
- Focused tests or example runs that prove the changed surface.
- Compatibility notes when generated behavior changes.

## Security Reports

Do not open public issues for vulnerabilities. Use GitHub Security Advisories as described in `SECURITY.md`.
