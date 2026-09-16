package render

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lathe-cli/lathe/pkg/config"
)

func renderSkillMD(manifest *config.Manifest, refs []moduleRef) string {
	cli := manifest.CLI.Name
	skill := SkillDirName(cli)
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: >\n  Use when operating the %s generated CLI. Discover commands, inspect parameters,\n  check auth state, and execute API operations safely.\n---\n\n", skill, cli)
	fmt.Fprintf(&b, "# %s CLI\n\n", cli)
	fmt.Fprintf(&b, "Use this skill when a user asks you to operate `%s`, inspect its API commands, or find the right generated command for an API task.\n\n", cli)
	b.WriteString("## Workflow\n\n")
	fmt.Fprintf(&b, "1. Search for candidates with `%s search \"<intent>\" --json`; use `--limit` when needed. Search is only candidate discovery.\n", cli)
	fmt.Fprintf(&b, "2. Inspect the exact command with `%s commands show <path...> --json` before executing an unfamiliar command.\n", cli)
	fmt.Fprintf(&b, "3. If the command detail has `auth.required=true`, run `%s auth status -o json` before execution and read `hostname` and `source`. Host resolution order: `--hostname` > `$%s` > the selected host (`%s auth use <host>`) > `http.default_hostname` > the single host in `hosts.yml`. If none applies, stop and ask the user to authenticate or select a host.\n", cli, manifest.CLI.HostEnv, cli)
	if len(manifest.Contexts) > 0 {
		fmt.Fprintf(&b, "4. If a flag has `context`, inspect `%s auth context status -o json`. Explicit flags override the declared environment variable, which overrides the value stored for the selected host.\n", cli)
		b.WriteString("5. Execute only after flags, body, auth, HTTP path, `mutation`, `dry_run`, and output hints are clear from `commands show`. When `mutation` is not `read`, preview with `--<dry_run.flag>` if `dry_run.mode` is `http_preview`; if preview is unavailable, obtain explicit user confirmation before execution.\n\n")
	} else {
		b.WriteString("4. Execute only after flags, body, auth, HTTP path, `mutation`, `dry_run`, and output hints are clear from `commands show`. When `mutation` is not `read`, preview with `--<dry_run.flag>` if `dry_run.mode` is `http_preview`; if preview is unavailable, obtain explicit user confirmation before execution.\n\n")
	}
	if manifest.Auth.Login != nil && manifest.Auth.Login.Type == config.AuthLoginOAuthDevice {
		b.WriteString("## Auth Login\n\n")
		fmt.Fprintf(&b, "- Use `%s auth login --device-auth --hostname <host> --provider <provider>` when the user needs browser-based OAuth login. The browser opens by default in an interactive terminal; use `--no-browser` for manual login.\n", cli)
		b.WriteString("- The saved host will use `auth_type: bearer`; OAuth is the login method, and the resulting API credential is a bearer token.\n\n")
	}
	b.WriteString("## General Commands\n\n")
	fmt.Fprintf(&b, "- `%s commands --json`: full generated command catalog.\n", cli)
	fmt.Fprintf(&b, "- `%s commands --include-hidden --json`: include hidden generated commands.\n", cli)
	fmt.Fprintf(&b, "- `%s commands show <path...> --json`: source of truth for one command.\n", cli)
	fmt.Fprintf(&b, "- `%s commands schema --json`: catalog schema version, surfaces, and dry-run result shape.\n", cli)
	fmt.Fprintf(&b, "- `%s search \"<intent>\" --json`: ranked candidate commands.\n", cli)
	fmt.Fprintf(&b, "- `%s auth status -o json`: resolved `hostname`, its `source`, the `selected` host, and every logged-in host.\n\n", cli)
	if len(manifest.Contexts) > 0 {
		fmt.Fprintf(&b, "- `%s auth context status -o json`: effective account-scoped context values for the selected host.\n\n", cli)
	}
	b.WriteString("## Maintenance Commands\n\n")
	fmt.Fprintf(&b, "- `%s --version` or `%s -v`: print CLI build version.\n", cli, cli)
	if manifest.Update.GitHub != nil {
		fmt.Fprintf(&b, "- `%s update`: update this CLI from configured GitHub Releases. Run only when the user explicitly asks to update `%s`; it may replace the current executable. Use `--yes` only when explicitly authorized.\n", cli, cli)
	}
	b.WriteString("\n")
	b.WriteString("## References\n\n")
	b.WriteString("- Read `references/catalog.md` for the command discovery protocol and catalog field meanings.\n")
	for _, ref := range refs {
		fmt.Fprintf(&b, "- Read `references/modules/%s` for the `%s` module command index.\n", ref.File, moduleName(ref.Module))
	}
	b.WriteString("\n## Rules\n\n")
	b.WriteString("- Do not guess flags or request body shape from command names.\n")
	b.WriteString("- Do not execute directly from search results; confirm with `commands show` first.\n")
	b.WriteString("- When `mutation` is not `read`, preview before execution if `dry_run.mode` is `http_preview`. If preview is unavailable, obtain explicit user confirmation before execution. `unknown` is not safe to treat as read.\n")
	b.WriteString("- Prefer `-o json` for machine-readable command output unless the user asks for human-readable output.\n")
	b.WriteString("- With `-o json` or `-o yaml`, branch on `error.code` and process exit status; `error.message` and `error.hint` are safe human guidance, and `error.http.status` is the only optional HTTP context.\n")
	b.WriteString("- A configured stream pause is successful (`exit 0`); inspect the collected output field mapped from the pause event instead of treating it as an error.\n")
	b.WriteString("- For collected streams, choose one mode: `-o json` for one stable document, `--stream` in the default output mode when catalog `output.streaming.policy.live` is present, or `-o raw` for wire events.\n")
	b.WriteString("- Use `--file`, `--set`, or `--set-str` for JSON request bodies according to `commands show` body requirements.\n")
	b.WriteString("- When `body.runtime_schema` is present, normal execution fetches and validates against that schema before the target request; an `http_preview` dry-run stays network-free and skips this preflight.\n")
	b.WriteString("- For sensitive flags, prefer safe modes from `flags[].input_modes`: `--<flag>-env`, `--<flag>-file`, or `--<flag>-stdin`.\n")
	return b.String()
}

func renderOpenAIYAML(manifest *config.Manifest) string {
	cli := manifest.CLI.Name
	skill := SkillDirName(cli)
	return fmt.Sprintf("interface:\n  display_name: %s\n  short_description: %s\n  default_prompt: %s\n",
		yamlString(cli+" CLI"),
		yamlString("Use the "+cli+" generated CLI"),
		yamlString("Use $"+skill+" to find and run the right "+cli+" command."),
	)
}

func renderCatalogReference(manifest *config.Manifest) string {
	cli := manifest.CLI.Name
	var b strings.Builder
	b.WriteString("# Catalog Protocol\n\n")
	b.WriteString("Use the runtime catalog as the source of truth. Generated references are a fast index; command execution details come from the CLI itself.\n\n")
	b.WriteString("## Search\n\n")
	fmt.Fprintf(&b, "Run `%s search \"<intent>\" --json` to find candidate commands. Use `--limit` to control result count. Treat search output as candidates only.\n\n", cli)
	b.WriteString("## Full Catalog\n\n")
	fmt.Fprintf(&b, "Run `%s commands --json` to inspect the generated command catalog. Use `--include-hidden` only when hidden commands are relevant.\n\n", cli)
	b.WriteString("Key fields:\n\n")
	b.WriteString("- `path`: command path to pass to `commands show` or execute after the CLI name.\n")
	b.WriteString("- `shortcuts`: root-level commands that execute the same operation with preset flag values.\n")
	b.WriteString("- `http`: HTTP method and path template.\n")
	fmt.Fprintf(&b, "- `http.default_hostname`: optional source-level host used after `--hostname`, `$%s`, and the host selected with `auth use`, and before the single-host fallback from `hosts.yml`.\n", manifest.CLI.HostEnv)
	b.WriteString("- `flags`: CLI flags, parameter location, type, required state, defaults, enum values, format, input modes, and help.\n")
	b.WriteString("- `body`: request body requirement, media type, and optional `runtime_schema` preflight source, including its active-context prerequisites.\n")
	b.WriteString("- `auth`: whether auth is required and which scopes are declared.\n")
	b.WriteString("- `mutation`: `read`, `write`, or `unknown`. Do not infer write vs read from the HTTP method alone. Treat any value other than `read` as requiring preview when `dry_run.mode` is `http_preview`, or explicit user confirmation when preview is unavailable.\n")
	b.WriteString("- `dry_run`: preview contract. `http_preview` is declared only when the generated runner can preview; use `--<flag>` and print the resolved HTTP request JSON. `unsupported` means the command has no preview, including all workflow commands. A `--dry-run` flag on a custom command is not a preview contract.\n")
	b.WriteString("- `examples`: runnable examples with optional body shape, output hints, and follow-up commands.\n")
	b.WriteString("- `output`: list path, default columns, response media type, pagination, and streaming hints; a streaming policy describes collection, terminal outcomes, and optional live projection.\n")
	b.WriteString("- `flags[].context`: an optional account-scoped default with explicit flag, declared environment, then stored-value precedence.\n")
	b.WriteString("- `sets_context`: a successful operation persists its declared parameter as the selected host's active context.\n")
	b.WriteString("- `notes`, `prerequisites`, and `known_errors`: overlay-provided operation context that is not inferred from the API spec.\n\n")
	b.WriteString("## Command Detail\n\n")
	fmt.Fprintf(&b, "Run `%s commands show <path...> --json` before executing an unfamiliar command. This is the source of truth for flags, body, auth, HTTP path, `mutation`, `dry_run`, and output hints.\n\n", cli)
	b.WriteString("## Schema\n\n")
	fmt.Fprintf(&b, "Run `%s commands schema --json` to read `catalog_schema_version`, `surfaces`, and `dry_run.result` before parsing catalog JSON with durable tooling. `dry_run.result=http_preview` means a preview prints the resolved HTTP request JSON (`method`, `url`, `hostname`, `host_source`, `headers`, `body`, `auth`, `output`).\n\n", cli)
	b.WriteString("## Sensitive Flags\n\n")
	b.WriteString("When a flag entry has `input_modes`, prefer safe modes over putting secrets directly in shell arguments.\n\n")
	b.WriteString("- `flag`: pass the direct `--<flag>` value; keep this for compatibility or non-secret values.\n")
	b.WriteString("- `env`: pass `--<flag>-env NAME` to read the value from an environment variable.\n")
	b.WriteString("- `file`: pass `--<flag>-file path` to read the value from a file.\n")
	b.WriteString("- `stdin`: pass `--<flag>-stdin` to read the value from stdin.\n")
	b.WriteString("- Use only one input mode for the same flag.\n\n")
	b.WriteString("## Request Bodies\n\n")
	b.WriteString("- `--file path`: read a JSON body from a file.\n")
	b.WriteString("- `--file -`: read a JSON body from stdin.\n")
	b.WriteString("- `--set key.path=value`: build JSON with type inference for booleans, null, integers, and floats.\n")
	b.WriteString("- `--set-str key.path=value`: build JSON while forcing the value to remain a string.\n\n")
	b.WriteString("If `body.runtime_schema` is present, normal execution fetches that JSON Schema and validates the body before the target request. An `http_preview` dry-run does not fetch it.\n\n")
	b.WriteString("## Output\n\n")
	b.WriteString("Use `-o json` for machine-readable command output. Other supported formats are `table`, `yaml`, and `raw`. For collected streams, choose one mode: JSON or YAML for one document, `--stream` in the default output mode when `output.streaming.policy.live` is present, or raw for wire events.\n\n")
	b.WriteString("On a non-zero exit with JSON or YAML output, read `error.code`, `error.message`, and `error.hint`; optional `error.http` contains only `status`. A configured pause exits zero and is represented by the field mapping in its stream collection policy.\n\n")
	b.WriteString("## Auth\n\n")
	fmt.Fprintf(&b, "If command detail returns `auth.required=true`, run `%s auth status -o json` before execution and read `hostname` and `source`. Host resolution order: `--hostname` > `$%s` > the selected host (`%s auth use <host>`) > `http.default_hostname` > the single host in `hosts.yml`. When more than one host is logged in and the host was chosen implicitly, the CLI also prints a `current host: <name>` line on stderr; read provenance from `auth status`, not from that line. If no matching host is logged in, stop and ask the user to authenticate.\n", cli, manifest.CLI.HostEnv, cli)
	if manifest.Auth.Login != nil && manifest.Auth.Login.Type == config.AuthLoginOAuthDevice {
		fmt.Fprintf(&b, "For browser-based OAuth login, run `%s auth login --device-auth --hostname <host> --provider <provider>`. The browser opens by default in an interactive terminal; use `--no-browser` for manual login. `auth_type: bearer` in `hosts.yml` is expected after login because API requests use the issued bearer token.\n", cli)
	}
	if len(manifest.Contexts) > 0 {
		fmt.Fprintf(&b, "Inspect effective account-scoped defaults with `%s auth context status -o json`; `local_set` reports whether `auth context set` is allowed. A command's explicit bound flag wins over its declared environment variable and stored host value.\n", cli)
	}
	return b.String()
}

func yamlString(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(raw)
}
