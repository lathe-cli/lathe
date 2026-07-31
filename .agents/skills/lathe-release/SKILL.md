---
name: lathe-release
description: Run Lathe release validation and publishing workflows. Use when the user asks to prepare, validate, cut, tag, publish, or ship a Lathe versioned release; runs the captured pre-release regression gate before tagging.
---

# Lathe Release

## Contract

Start read-only. Read repo-local instructions first: `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, release docs, Makefile, CI, and release config. Trust live code/config over old docs.

Default to validation only. Do not create tags, commits, pushes, PRs, or GitHub releases unless the user explicitly asks to cut/publish/release the target version.

Require a concrete target version before any publish step. If the version is missing or conflicts with existing tags/releases, ask one question.

Use ignored scratch paths for generated validation artifacts. Prefer `.local/regression-$VERSION/` and ignored release output such as `dist/`.

Use `rtk` for commands when available. If `rtk` blocks a command, run the command raw and report the fallback.

## Release Flow

1. Confirm target version and current baseline.
2. Check worktree cleanliness and current branch.
3. Fetch tags and verify the target tag does not exist locally or remotely.
4. Read release workflow config and CI status for `HEAD`.
5. Run the repo's declared local gate plus release-specific artifact dry-run.
6. Run product-specific end-to-end smoke tests.
7. Report `ready`, `blocked`, or `ready with skipped checks`.
8. Only if explicitly asked to publish: create the tag, push it, then monitor the release workflow.

Never claim a release is ready without fresh command output from this run.

## Lathe Pre-Release Gate

Use this section in a Lathe checkout. Derive the baseline from the latest release and validate the current tracked product surfaces; do not copy assumptions from a version-named scratch directory.

### Live State

```bash
git status --short --branch
git fetch origin --tags --prune
gh release list --limit 10
git tag --list 'v*' --sort=-v:refname | head -20
git ls-remote --tags origin "refs/tags/$VERSION"
gh run list --branch main --limit 6 --json databaseId,headSha,event,status,conclusion,workflowName,createdAt,updatedAt
```

Set the baseline to the latest GitHub release tag unless the user gives another baseline.

```bash
BASELINE=$(gh release list --limit 1 --json tagName -q '.[0].tagName')
git log --oneline --decorate --no-merges "$BASELINE"..HEAD
git diff --stat "$BASELINE"..HEAD
git diff --name-only "$BASELINE"..HEAD
```

Treat non-green `ci`, `codeql`, or `scorecard` on `HEAD` as a release blocker unless the user explicitly accepts the risk.

### Local Gates

```bash
make check
go test -race ./...
go build ./...
git diff --check
```

Run uncached targeted tests for Lathe's release-sensitive surfaces.

```bash
go test ./internal/sourceconfig ./internal/specsync -count=1
go test ./internal/codegen/render ./internal/overlay -count=1
go test ./internal/codegen/backends/... -count=1
go test ./internal/lathecmd ./internal/projectinit ./internal/latheskill -count=1
go test ./pkg/runtime ./pkg/lathe -count=1
```

### Candidate Binary

```bash
ROOT=".local/regression-$VERSION"
rm -rf "$ROOT"
mkdir -p "$ROOT/bin"
COMMIT=$(git rev-parse --short HEAD)
DATE_UTC=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go build -trimpath -ldflags "-X github.com/lathe-cli/lathe/pkg/lathe.Version=$VERSION -X github.com/lathe-cli/lathe/pkg/lathe.Commit=$COMMIT -X github.com/lathe-cli/lathe/pkg/lathe.Date=$DATE_UTC" -o "$ROOT/bin/lathe" ./cmd/lathe
go build -trimpath -ldflags "-X github.com/lathe-cli/lathe/pkg/lathe.Commit=$COMMIT -X github.com/lathe-cli/lathe/pkg/lathe.Date=$DATE_UTC" -o "$ROOT/bin/lathe-init" ./cmd/lathe
"$ROOT/bin/lathe" version
"$ROOT/bin/lathe" --help | grep -F -- "lathe init" >/dev/null
"$ROOT/bin/lathe" --help | grep -F -- "lathe skill" >/dev/null
"$ROOT/bin/lathe" skill install --help 2>&1 | grep -F -- "-scope string" >/dev/null
CATALOG_SCHEMA=$(awk '/const CatalogSchemaVersion =/{print $4}' pkg/runtime/catalog.go)
REPO=$(pwd)
```

### Generated CLI E2E

Copy examples into scratch and rewrite their module replacement to the live checkout.

```bash
for EX in petstore richapi graphql; do
  cp -R "examples/$EX" "$ROOT/$EX"
  rm -rf "$ROOT/$EX/internal/generated" "$ROOT/$EX/skills" "$ROOT/$EX/bin" "$ROOT/$EX/go.sum"
  (cd "$ROOT/$EX" && go mod edit -replace github.com/lathe-cli/lathe="$REPO")
done
```

Petstore proves OpenAPI codegen, overlay shortcut, catalog, search, show, and schema.

```bash
cd "$ROOT/petstore"
../bin/lathe codegen -cache fixtures -overlay overlays
go mod tidy
go build -trimpath -o bin/petstore ./cmd/petstore
./bin/petstore --help | grep -F -- "pet-123" >/dev/null
./bin/petstore commands --json | jq -e --argjson schema "$CATALOG_SCHEMA" '.catalog_schema_version == $schema and (.commands[] | select(.path == ["pets","get"] and .http.method == "GET" and .http.path_template == "/pets/{id}" and .shortcuts[0].use == "pet-123"))' >/dev/null
./bin/petstore commands show pets get --json | jq -e '.path == ["pets","get"] and (.flags[] | select(.name == "id" and .required == true))' >/dev/null
./bin/petstore commands schema --json | jq -e --argjson schema "$CATALOG_SCHEMA" '.catalog_schema_version == $schema' >/dev/null
./bin/petstore search pet --json | jq -e 'length > 0' >/dev/null
cd "$REPO"
```

Richapi proves pagination, non-JSON body, body file help, long-running wait help, public auth, streaming hints, and search.

```bash
cd "$ROOT/richapi"
../bin/lathe codegen -cache fixtures
go mod tidy
go build -trimpath -o bin/richapi ./cmd/richapi
./bin/richapi commands --json | jq -e --argjson schema "$CATALOG_SCHEMA" '.catalog_schema_version == $schema and (.commands[] | select(.path == ["users","list"] and .output.pagination.strategy == "cursor" and .output.pagination.token_param == "page_token"))' >/dev/null
./bin/richapi commands show users upload-avatar --json | jq -e '.body.media_type == "application/octet-stream" and .body.required == true' >/dev/null
./bin/richapi users upload-avatar --help | grep -F -- "--file string" >/dev/null
./bin/richapi jobs create --help | grep -F -- "--wait" >/dev/null
./bin/richapi commands show system healthz --json | jq -e '.auth.required == false' >/dev/null
./bin/richapi commands show events stream --json | jq -e '.output.streaming.strategy == "sse"' >/dev/null
./bin/richapi search users --json | jq -e 'length > 0' >/dev/null
cd "$REPO"
```

GraphQL proves `/graphql`, request envelope merging, body-cursor pagination, required variables, and search.

```bash
cd "$ROOT/graphql"
../bin/lathe codegen -cache fixtures
go mod tidy
go build -trimpath -o bin/graphqlctl ./cmd/graphqlctl
./bin/graphqlctl commands --json | jq -e --argjson schema "$CATALOG_SCHEMA" '.catalog_schema_version == $schema and (.commands[] | select(.path == ["apps","list-apps"] and .http.method == "POST" and .http.path_template == "/graphql" and .body.merge_path == "variables" and .output.pagination.strategy == "body-cursor"))' >/dev/null
./bin/graphqlctl commands show apps list-apps --json | jq -e '.body.media_type == "application/json" and .body.merge_path == "variables" and .output.list_path == "data.listApps.nodes" and .output.pagination.token_param == "variables.after"' >/dev/null
./bin/graphqlctl commands show apps create-app --json | jq -e '(.flags[] | select(.name == "name" and .required == true))' >/dev/null
./bin/graphqlctl search apps --json | jq -e 'length > 0' >/dev/null
cd "$REPO"
```

If a JSON assertion fails, inspect the actual `commands show --json` output before calling it a product regression. Catalog shape can legitimately evolve with a schema bump.

Lathe does not currently have a tracked proto example app. Keep `go test ./internal/codegen/backends/proto -count=1` green and report product-level proto E2E as skipped, not passed.

### Application Initialization E2E

Initialize every supported starter from the candidate commit. Use the latest released baseline for init's internal `go mod tidy`, because the target version is not resolvable until it is published; then replace Lathe with the live checkout before running each starter's gate.

```bash
for LANGUAGE in node go python rust; do
  APP="$ROOT/init-$LANGUAGE"
  LATHE_INIT_VERSION="$BASELINE" "$ROOT/bin/lathe-init" init "$APP" \
    --language "$LANGUAGE" \
    --app-name "Lathe $LANGUAGE Smoke" \
    --cli-name "${LANGUAGE}ctl" \
    --go-module "example.com/lathe-$LANGUAGE-smoke" \
    --license none \
    --json > "$ROOT/init-$LANGUAGE.json"
  jq -e --arg language "$LANGUAGE" \
    '.schema_version == 1 and .language == $language and .git.has_commits == false and .git.has_remote == false and .git.staged_files == 0 and (.next_command | length > 0)' \
    "$ROOT/init-$LANGUAGE.json" >/dev/null
  (cd "$APP" && go mod edit -replace github.com/lathe-cli/lathe="$REPO")
done

(cd "$ROOT/init-node" && pnpm check)
(cd "$ROOT/init-go" && make check)
(cd "$ROOT/init-python" && make check)
(cd "$ROOT/init-rust" && make check)
```

### Bundled Skill Install Smoke

Install into an isolated home so validation never touches real user configuration.

```bash
SKILL_HOME="$ROOT/skill-home"
mkdir -p "$SKILL_HOME"
HOME="$SKILL_HOME" "$ROOT/bin/lathe" skill install --scope user --agent codex --yes
test -f "$SKILL_HOME/.agents/skills/lathe/SKILL.md"
```

### Skill Include Smoke

If `.local/skill-include-smoke` exists, copy it into the scratch root and run it against the candidate Lathe. Do not run its old `run-smoke.sh` blindly if it pins an older Lathe version.

```bash
rm -rf "$ROOT/skill-include-smoke"
cp -R .local/skill-include-smoke "$ROOT/skill-include-smoke"
cd "$ROOT/skill-include-smoke"
rm -rf internal/generated skills bin go.sum
go mod edit -replace github.com/lathe-cli/lathe="$REPO"
../bin/lathe codegen -sources specs/sources.yaml -cache .cache
grep -F 'This paragraph came from `skill.include`.' skills/smokectl/SKILL.md >/dev/null
grep -F 'This reference file was copied from `skill.include`.' skills/smokectl/references/local-runbook.md >/dev/null
go mod tidy
go build -trimpath -o bin/smokectl ./cmd/smokectl
./bin/smokectl commands --json | jq -e --argjson schema "$CATALOG_SCHEMA" '.catalog_schema_version == $schema and (.commands[] | select(.path == ["users","list"]))' >/dev/null
./bin/smokectl search users --json | jq -e 'length > 0' >/dev/null
cd "$REPO"
```

If the smoke fixture is missing, report it as skipped.

### GoReleaser Dry Run

```bash
goreleaser check
goreleaser release --snapshot --clean
(cd dist && shasum -a 256 -c *_checksums.txt)
SNAPSHOT_BIN=$(find dist -path "*/lathe" -type f | grep "$(go env GOOS)_$(go env GOARCH)" | head -1)
"$SNAPSHOT_BIN" version
```

Snapshot mode uses the previous tag in the version string. That is normal; the real version comes from the pushed release tag.

If GoReleaser is missing, report that artifact validation was not run.

## Publish Step

Only run this when the user explicitly asks to publish the target release and all required checks are green.

```bash
git status --short --branch
git tag "$VERSION"
git push origin "$VERSION"
gh run list --limit 5
```

Then monitor the release workflow and report the GitHub release URL or the failing job.

## Final Report

Report:

- release verdict: `ready`, `blocked`, or `ready with skipped checks`
- target version and baseline tag
- GitHub workflows checked for `HEAD`
- exact local commands that passed
- E2E surfaces that passed
- starter languages and bundled Skill install that passed
- whether proto product-level E2E was skipped
- skipped checks and why
- worktree cleanliness
- whether tag, push, or release actions were performed
