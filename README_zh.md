[English](README.md) | **中文**

# lathe

> 从 OpenAPI、Swagger 和 protobuf API 规格生成 Agent 友好的 Cobra CLI。

[![CI](https://github.com/samzong/lathe/actions/workflows/ci.yml/badge.svg)](https://github.com/samzong/lathe/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Lathe 是一个 API-to-CLI 生成器，适合需要同时服务人类用户和 AI Agent 的团队。
它可以把 Swagger 2.0、OpenAPI 3，以及带 `google.api.http` 注解的 protobuf
API 生成生产级 Cobra CLI，并内置结构化命令发现、认证元数据、请求体构造器和
机器可读输出。

生成的 CLI 自带 command catalog JSON、意图搜索、单命令详情 JSON、认证元数据、
请求体构造器、结构化输出，以及仓库内的 Skill 目录 `skills/<cli-name>/`。

先看 CLI 使用说明：

- [CLI Usage](docs/cli-usage.md)：使用 `lathe bootstrap` 生成下游 CLI，接入 `cmd/<name>/main.go`，构建并验证 agent loop。

![lathe 架构图](docs/images/architecture.png)

---

## Lathe 是什么？

Lathe 可以从已有 API 规格生成单文件命令行工具。你不需要手写一套容易和 API
漂移的 CLI，只需要锁定上游规格、配置 CLI 身份、可选地用 overlay 补强帮助文案，
然后在 API 变化时重新生成。

最终得到的不只是一个面向人的 API 包装器。Lathe 会生成 Agent 友好的 CLI 表面，
让命令可以通过机器可读契约被搜索、检查、验证和执行。

## 使用场景

当你需要做这些事时，可以使用 Lathe：

- 从 OpenAPI 3、Swagger 2.0 或 protobuf service 生成 Cobra CLI。
- 让内部或面向客户的 CLI 和上游 API 规格保持同步。
- 把 API 操作暴露给 AI Agent，同时避免它们猜 flag、认证、body 结构或输出格式。
- 交付一个内置命令发现、认证预检、结构化输出和 Agent Skill 文档的单文件二进制。
- 通过 overlay 改善生成的帮助文案和示例，而不编辑生成的 Go 代码。

## 为什么需要 Lathe

只要一个 API 被团队认真使用，在 LLM 时代就会想要 CLI。常见做法是照着
Swagger、OpenAPI 或 protobuf 手写一棵命令树，然后长期维护一份和 API 规格
高度重复、随时可能漂移的代码。

Lathe 的判断很简单：API 规格才是事实来源，CLI 应该从规格生成，而不是靠人工
翻写。

你只需要锁定上游规格、声明 CLI 名称和认证行为，再用 overlay 补强少量不够
清楚的帮助文案。API 变更时，升级锁定的 tag，重新生成即可。

最终得到的不是一个薄包装，而是一套 Agent 友好的 CLI 表面。runtime catalog
会告诉 Agent 有哪些命令、哪些 flag 必填、是否需要认证、会发起什么 HTTP
请求、请求体如何构造，以及应该优先使用哪种输出格式。

## 你会得到什么

| 能力 | 说明 |
|---|---|
| 多后端生成 | Swagger 2.0、OpenAPI 3，以及带 `google.api.http` 注解的 protobuf service 都可以生成 Cobra 命令树。 |
| 统一运行时 | 生成模块共享同一套认证、请求构造、输出格式化、分页、流式响应和错误处理逻辑。 |
| Agent 友好的发现能力 | `search`、`commands --json`、`commands show` 和 `commands schema` 会把 CLI 能力暴露成结构化数据。 |
| 生成 Skill | Codegen 会写入 `skills/<cli-name>/`，让 Agent 能快速读取 CLI 的使用指南和模块 reference。 |
| 可复现输入 | API 规格按 tag 锁定，解析到 commit SHA，并从仓库内配置重新生成。 |
| 真实 CLI 体验 | 按 hostname 管理认证，支持 `--file`、`--set`、`--set-str`、`-o table|json|yaml|raw`、枚举校验、分页、流式响应和 `--debug`。 |
| Overlay 润色 | 不改生成代码，也能补充摘要、别名、参数帮助、分组和示例。 |

## 项目资源

- [Governance](GOVERNANCE.md)：决策流程和兼容性预期。
- [Maintainers](MAINTAINERS.md)：维护者职责和 review 预期。
- [Showcase](SHOWCASE.md)：CLI 生成路径和真实使用记录。
- [CLI Usage](docs/cli-usage.md)：生成并验证下游 CLI 的命令序列。
- [Adopters](ADOPTERS.md)：公开或匿名用户条目。
- [Contributing](CONTRIBUTING.md)：本地 setup、PR 流程和项目范围。
- [Security](SECURITY.md)：私密漏洞报告和支持版本。

## 快速开始

基于 [github.com/samzong/lathe](https://github.com/samzong/lathe) 创建仓库，
然后配置两个文件。

### 安装工具

Lathe release archive 包含一个命令行工具 `lathe`，生成流程通过子命令进入：

- `lathe specsync`：把锁定版本的上游 API 规格同步到本地 cache。
- `lathe codegen`：生成 runtime command specs 和可选 Skill 文件。
- `lathe bootstrap`：一次性执行 `specsync` 和 `codegen`。

从 [latest release](https://github.com/samzong/lathe/releases/latest) 下载对应平台的 archive，解压后把 `lathe` 放进 `PATH`。

如果你是在 source checkout 里工作，也可以继续使用下面的 Make targets，不需要单独安装 release 工具。

### 1. 定义 CLI

`cli.yaml`:

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

### 2. 锁定 API 来源

`specs/sources.yaml`:

```yaml
sources:
  iam:
    repo_url: https://github.com/acme/iam.git
    pinned_tag: v1.4.0
    backend: swagger
    swagger:
      files:
        - api/openapi/user.swagger.json

  billing:
    repo_url: https://github.com/acme/billing.git
    pinned_tag: v0.9.2
    backend: proto
    proto:
      staging:
        - from: api/proto
          to: "."
      entries:
        - v1/accounts.proto

  payments:
    repo_url: https://github.com/acme/payments.git
    pinned_tag: v2.1.0
    backend: openapi3
    openapi3:
      files:
        - api/openapi.yaml
```

### 3. 生成并构建

```sh
lathe bootstrap
go build -o bin/acmectl ./cmd/acmectl
```

`lathe bootstrap` 会同步锁定的规格并运行 codegen。Codegen 会生成 Go 模块，
并默认生成 Skill 目录 `skills/acmectl/`。

### 4. 使用 CLI

先登录，再发现生成命令、检查命令的精确形态，最后执行：

```sh
./bin/acmectl auth login --hostname api.acme.com
./bin/acmectl search "create user" --json
./bin/acmectl commands show iam users create-user --json
./bin/acmectl auth status --hostname api.acme.com
./bin/acmectl iam users create-user \
  --set email=alice@example.com \
  --set role=viewer \
  -o json
```

## Agent 友好的 CLI 能力

生成的 CLI 不要求 Agent 猜命令、猜参数、猜认证状态。

| 命令 | 用途 |
|---|---|
| `<cli> search "<intent>" --json` | 根据意图查找候选命令。支持 `--limit`。Search 只用于发现候选项。 |
| `<cli> commands --json` | 读取完整的 generated command catalog。需要隐藏命令时使用 `--include-hidden`。 |
| `<cli> commands show <path...> --json` | 执行前检查单个命令的事实来源，包括 flags、body、auth、HTTP method/path 和 output hints。 |
| `<cli> commands schema --json` | 在做长期机器解析前确认 catalog schema version。 |
| `<cli> auth status --hostname <host>` | 当命令详情显示 `auth.required=true` 时，先确认该 host 的登录状态。 |

推荐的 Agent 执行流程：

1. 用 `search "<intent>" --json` 找候选命令。
2. 用 `commands show <path...> --json` 检查目标命令。
3. 如果 `auth.required=true`，先运行 `auth status --hostname <host>`；未登录就停止并让用户认证。
4. 确认 flags、body requirements、auth、HTTP path 和 output hints 后再执行。
5. 除非用户明确要表格输出，否则优先使用 `-o json`。

## 生成的 Skill 目录

Codegen 默认写入标准 Skill 目录：

```text
skills/<cli-name>/
|-- SKILL.md
|-- agents/openai.yaml
`-- references/
    |-- catalog.md
    `-- modules/<source-name>.md
```

Skill 是给 Agent 的精简操作指南，说明如何发现命令、读取 catalog、做 auth
preflight、构造 body、选择输出格式，以及如何查看按 source 拆分的模块
reference。

runtime catalog 仍然是事实来源。Agent 应该先通过 Skill 理解 CLI 的使用方式，
再用 `commands show <path...> --json` 获取精确执行细节。

如果不需要生成 Skill，可以关闭：

```sh
go run ./cmd/lathe codegen -skill-root ""
```

## 配置

### `cli.yaml`

定义 CLI 身份和可选的认证校验行为。

| 字段 | 说明 |
|---|---|
| `cli.name` | 二进制和命令名称，例如 `acmectl`。 |
| `cli.short` | 根命令摘要。 |
| `auth.validate` | 可选端点，供 `auth status` 展示已登录用户。 |

### `specs/sources.yaml`

声明哪些上游规格会变成 CLI 模块。

| 字段 | 必填 | 说明 |
|---|---|---|
| `repo_url` | 是 | `git clone` 能接受的任意 URL。 |
| `pinned_tag` | 是 | 不接受浮动分支；可复现性是硬要求。 |
| `backend` | 是 | `swagger`、`openapi3` 或 `proto` 之一。 |
| `swagger.files` | 仅 Swagger | 一个或多个 Swagger 2.0 JSON 规格。 |
| `openapi3.files` | 仅 OpenAPI 3 | JSON 或 YAML OpenAPI 规格。 |
| `proto.staging` | 仅 Proto | 解析前暂存到 `protoc` include root 的文件。 |
| `proto.entries` | 仅 Proto | 入口 proto 文件；只有带 `google.api.http` 的 RPC 会变成命令。 |

分组规则：

- Swagger 和 OpenAPI 3 使用 operation 的第一个 tag。
- Proto 使用 service 名称。

### Overlays

Overlay 用来润色生成命令，不需要修改上游规格，也不需要编辑生成的 Go 代码。

`internal/overlay/iam.yaml`:

```yaml
commands:
  create-user:
    short: "Create a user in the IAM service"
    aliases: [adduser]
    example: |
      acmectl iam create-user \
        --set email=alice@example.com \
        --set role=viewer
    params:
      role:
        help: "User role (viewer, editor, admin)"
        default: viewer
```

使用 overlay 目录运行 codegen：

```sh
go run ./cmd/lathe codegen -overlay internal/overlay
```

## 运行时能力

### 全局 Flags

| Flag | 效果 |
|---|---|
| `--hostname` | 为本次调用选择 host。 |
| `-o, --output` | 输出格式：`table`、`json`、`yaml` 或 `raw`。 |
| `--insecure` | 跳过 TLS 证书验证。 |
| `--debug` | 将 HTTP 请求/响应详情打印到 stderr。 |

### 环境变量

| 环境变量 | 效果 |
|---|---|
| `$<NAME>_HOST` | 不修改 host 配置也能选择 host。 |
| `$<NAME>_CONFIG_DIR` | 覆盖配置目录，默认是 `~/.config/<name>`。 |
| `LATHE_SPECS_CACHE` | spec sync 暂存上游规格的位置，默认是 `.cache`。 |

`<NAME>` 是 `cli.name` 的大写形式。

### 请求体

当 API operation 接受 body 时，生成命令会暴露请求体辅助 flag：

| Flag | 用途 |
|---|---|
| `--file path.json` | 从 JSON 文件加载请求体。 |
| `--set key.path=value` | 通过重复的 key/value 赋值构造 JSON。 |
| `--set-str key.path=value` | 构造 JSON，并强制该值保持字符串类型。 |

## 架构

Lathe 分两步工作：

1. `lathe specsync` clone 锁定的上游规格，校验解析到的 commit SHA，并写入本地
   spec state。
2. `lathe codegen` 将规格标准化为统一的中间表示，应用 overlays，渲染 Go 命令模块，
   并渲染 Skill 目录。

生成的 CLI 使用 `pkg/lathe` 和 `pkg/runtime` 提供共享 command catalog、认证、
请求构造、输出格式化、分页、流式响应和稳定错误处理。

## 设计原则

1. **Spec is truth.** 生成命令的行为应该来自 API 规格。
2. **Catalog is contract.** 人可以读 help text，Agent 需要结构化命令事实。
3. **Search is not execution.** Search 只找候选项，`commands show` 才确认精确命令形态。
4. **Auth is explicit.** 凭据按 hostname 记录，Agent 执行受保护调用前应先检查 auth。
5. **Overlay after generation.** 补强薄弱的规格文案，但不 fork 生成代码。
6. **One binary at runtime.** 生成的 CLI 应该易于安装、检查和自动化。

## 参与贡献

参见 [CONTRIBUTING.md](CONTRIBUTING.md)。所有 commit 必须使用 `git commit -s`
签署，遵循 [Developer Certificate of Origin](https://developercertificate.org/)。

## 安全

参见 [SECURITY.md](SECURITY.md) 了解私密漏洞披露流程。

## 许可证

[MIT](LICENSE) (c) samzong
