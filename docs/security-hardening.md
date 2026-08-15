# Repository Security Controls

This file records security controls that are not visible from source alone.
`SECURITY.md` owns vulnerability-reporting instructions.

## Versioned controls

- CI: `.github/workflows/ci.yml`
- Release: `.github/workflows/release.yml`
- CodeQL: `.github/workflows/codeql.yml`
- OpenSSF Scorecard: `.github/workflows/scorecard.yml`
- Dependency updates: `.github/dependabot.yml`
- Reporting policy: `SECURITY.md`

## GitHub settings snapshot

Verified through the GitHub API on 2026-08-15:

- Dependabot vulnerability alerts: enabled.
- Dependabot automated security fixes: enabled.
- A default-branch ruleset blocks branch deletion and non-fast-forward pushes.
- CI, CodeQL, and Scorecard have successful runs on the current `main` HEAD.
- Pull requests, reviews, and status checks are not required by the ruleset.
- Private vulnerability reporting is disabled.
- Secret scanning and push protection are disabled.

Open repository controls:

- Require pull requests before merging to `main`.
- Require at least one approval and dismiss stale approvals.
- Require the stable checks `build`, `Analyze Go (go)`, and
  `OpenSSF Scorecard`.
- Enable private vulnerability reporting.
- Enable secret scanning and push protection when the repository plan permits.

## Release artifacts

GoReleaser publishes checksums with release artifacts. Artifact signing or
provenance is not currently published. Release notes should link checksum
verification until a stronger provenance contract exists.
