[English](README.md) | **中文**

<p align="center">
  <img src="docs/images/lathe-logo.png" alt="Lathe logo" width="180">
</p>

# lathe

> 从 OpenAPI、Swagger、protobuf 和 GraphQL API 规格生成 Agent 友好的 Cobra CLI。

[![CI](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml/badge.svg)](https://github.com/lathe-cli/lathe/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Lathe 把声明式 API 规格转换成一套同时供人和 AI Agent 使用的可检查 CLI。
生成的二进制既提供普通 Cobra 命令，也提供机器可读契约；Agent 可以先发现命令，
再确认精确的 flags、认证、请求体、HTTP 路径和输出结构，最后执行，不需要猜。

![Lathe architecture](docs/images/architecture.png)

## 为什么需要 Lathe

手写 API CLI 会复制一份已有契约，并持续与 API 漂移。Lathe 以 API 规格和仓库内
配置为事实来源，同时生成命令树、运行时元数据和 Agent Skill。

Lathe 适合这些场景：

- 从 Swagger 2.0、OpenAPI 3、带 `google.api.http` 注解的 protobuf API，或显式
  筛选的 GraphQL schema 生成 CLI。
- 让面向人的 CLI 和面向 Agent 的 command catalog 共享同一份契约。
- 锁定 Git 输入实现可复现生成，或显式跟随声明的本地 working tree。
- 通过 overlay 做有限的 CLI 润色，而不手改生成代码。

## 生成内容

| 表面 | 用途 |
|---|---|
| Cobra 命令树 | 提供带认证、请求体、分页、流式响应、轮询和结构化输出的类型化 API 命令。 |
| Runtime catalog | `search`、`commands --json`、`commands show` 和 `commands schema` 暴露精确的 operation 与 workflow 契约。 |
| Agent Skill | 生成 `skills/<cli-name>/`，并引导 Agent 回到 runtime catalog。 |
| 可选内置能力 | 在 `cli.yaml` 启用后，生成 workflow 和内嵌的 `<cli> skill install`。 |

runtime catalog 才是执行权威。搜索结果和生成的 Skill 只负责发现，不能替代
`commands show`。

## 从这里开始

从 [latest release](https://github.com/lathe-cli/lathe/releases/latest) 下载
`lathe`，或在当前 checkout 运行 `make build`。

- 从 API 规格生成 CLI：[CLI 使用说明](docs/cli-usage.md)
- 创建 CLI-first 应用：[`lathe init`](docs/lathe-init-design.md)
- 安全检查生成的 CLI：先运行 `<cli> __lathe verify --json`，再按
  [CLI 使用说明](docs/cli-usage.md#agent-operation-loop)中的 catalog 流程执行

## 文档

- [架构](docs/architecture.md) — 生成期/运行期边界、包职责和不变量。
- [CLI 使用说明](docs/cli-usage.md) — 安装、配置、生成、构建和执行。
- [机器契约](docs/contracts.md) — 版本化的生成代码、catalog、verify 和错误契约。
- [Workflow 命令](docs/workflow.md) — 声明式多操作命令及其边界。
- [应用初始化器](docs/lathe-init-design.md) — starter 契约、初始化流程和跨仓库 gate。
- [Lathe Registry](https://lathe-cli.github.io/lathe-registry/) — 在独立仓库维护的可复现社区 recipe。

## 项目

- [Adopters](ADOPTERS.md)
- [Contributing](CONTRIBUTING.md)
- [Governance](GOVERNANCE.md)
- [Maintainers](MAINTAINERS.md)
- [Security](SECURITY.md)

Lathe 是 spec-to-agent-toolchain generator，不是通用脚手架、运行时插件系统、
GUI/TUI、API gateway 或手写 SDK 的替代品。

## 许可证

[Apache License 2.0](LICENSE) © lathe-cli

写入下游项目的生成物适用 [LICENSE](LICENSE) 中的 generated-output exception，
不适用本仓库许可证。
