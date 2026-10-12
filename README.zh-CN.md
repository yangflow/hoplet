<p align="center">
  <img src="web/public/icon.svg" width="96" height="96" alt="Hoplet">
</p>

<h1 align="center">Hoplet</h1>

<p align="center">
  自托管的编程 agent 会话远程控制与跨机器交接工具，首先支持 Claude Code。
</p>

<p align="center">
  <a href="https://github.com/yangflow/hoplet/actions/workflows/ci.yml"><img src="https://github.com/yangflow/hoplet/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/yangflow/hoplet" alt="License: Apache-2.0"></a>
  <a href="https://github.com/yangflow/hoplet/releases"><img src="https://img.shields.io/github/v/tag/yangflow/hoplet?label=version" alt="Version"></a>
</p>

<p align="center">
  <a href="README.md">English</a> · <b>简体中文</b>
</p>

## 为什么做 Hoplet

Claude Code 的会话只存在于启动它的那台机器上：换一台电脑就没法继续跟进；几百 MB 的长会话在哪里打开都很慢；想把工作挪到另一台机器上，只能手动拷文件，对话也就断了。

Hoplet 在你的每台开发机上运行一个 agent，在你自己的主机上运行一个服务端。你可以在任何一台电脑的桌面浏览器里瞬间打开会话，在 Claude Code 工作时远程驱动它，还可以把会话连同未提交的改动一起交接到另一台机器上继续进行。所有数据都只在你自己的机器和服务器之间流转。

## 功能

- **瞬间打开**：会话从最新的消息开始加载，更早的历史、大块的工具输出和图片在你滚动或需要时才加载。几百 MB 的会话和小会话打开得一样快。
- **远程控制**：发送提示、实时查看流式输出、回答权限请求、中断当前这一轮。控制的是 Hoplet 在会话所在机器上启动的 Claude Code 进程。
- **跨机器交接**：把会话连同未提交和未跟踪的改动，一起移到目标机器上它自己的仓库克隆里，以 git worktree 的形式继续。源机器离线时也可以，会使用最后一次的快照。
- **工作区视图**：在浏览器里查看会话改动了哪些文件、具体的 diff，以及文件树。
- **搜索**：可以在全部会话或单个会话里搜索。
- **自托管、私密**：你的主机上只需要一个容器镜像和一个数据卷；不依赖任何外部服务，没有遥测；服务端以增量同步的方式，为每个会话保留一份完全一致的副本。

## 工作原理

```
 development machines                        your host                     any desktop browser
┌───────────────────────┐                 ┌──────────────────┐
│ Claude Code           │                 │  Hoplet core     │   HTTPS / WSS   ┌──────────────┐
│   ▲ runs, reads       │   WebSocket     │  (one container) │◀───────────────▶│  web UI      │
│ hoplet-agent ─────────┼────────────────▶│  sessions,       │  via your TLS   └──────────────┘
└───────────────────────┘  sync, control, │  snapshots,      │  reverse proxy
┌───────────────────────┐  handoff        │  SQLite index    │
│ hoplet-agent ─────────┼────────────────▶│                  │
└───────────────────────┘                 └──────────────────┘
```

**agent**（Go 编写，单个可执行文件）监视你选定的 Claude Code 会话，只把新增的字节发给 core；你在浏览器里发送提示时，它负责运行 Claude Code；交接过来的会话，由它安装到本机仓库克隆的一个 worktree 里。**core**（TypeScript 编写，一个包含网页界面的容器镜像）负责存储会话、分页提供给浏览器，并在浏览器和各个 agent 之间转发控制消息。详细设计见 [docs/architecture.md](docs/architecture.md)。

## 项目状态

**预发布（v0.1）**。目前还没有发布任何现成的产物：镜像和 agent 都需要从本仓库构建；1.0 之前，数据格式和 API 可能随时变化。

- 已经可用：会话同步、打开和搜索会话、带权限回答和中断的远程控制、在线和离线交接、工作区视图，以及在网页界面里添加项目和新建会话。
- 服务端运行在装有 Docker 的 Linux 或 macOS 上（x86_64 或 arm64）。agent 运行在 macOS（已测试）和 Linux 上，需要 Claude Code 2.1.289 或更新的版本。
- 网页界面面向桌面浏览器。手机和 Mac 的原生客户端以后再做。

接下来的计划见 [ROADMAP.md](ROADMAP.md)。

## 快速开始

服务器上需要安装带 Compose 的 Docker；每台开发机上需要 Go 1.27、`git` 和 Claude Code。添加机器要用到主机命令行工具，它需要 Node.js 26 和 pnpm 12。

**1. 启动服务端。**

```bash
git clone https://github.com/yangflow/hoplet.git && cd hoplet
echo "HOPLET_TOKEN=$(openssl rand -hex 32)" > image/.env
docker compose -f image/compose.yaml up -d --build
```

在主机上打开 `http://127.0.0.1:7788`，输入 `image/.env` 里的令牌。端口只绑定在主机的本地回环地址上；要从其他机器访问 Hoplet，请在前面配置一个 TLS 反向代理（见 [docs/hosting.md](docs/hosting.md#reverse-proxy)）。

**2. 添加一台机器。** 在同一个代码目录里运行：

```bash
pnpm install --frozen-lockfile
HOPLET_URL=http://127.0.0.1:7788 HOPLET_TOKEN=<你的令牌> pnpm hoplet devices add my-laptop
```

这会为这台机器生成一个设备令牌，并打印出它的 agent 配置；令牌只显示这一次。把配置里的 `server_url` 改成这台机器能访问到的地址（也就是你的反向代理）。

**3. 在那台机器上运行 agent。**

```bash
git clone https://github.com/yangflow/hoplet.git && cd hoplet/agent
go build -o hoplet-agent ./cmd/hoplet-agent
mkdir -p ~/.hoplet && $EDITOR ~/.hoplet/config.json   # 粘贴上一步打印出的配置
./hoplet-agent daemon
```

这台机器会在网页界面里显示为在线。在它上面选择 **Add project**，挑一个文件夹，Hoplet 就会同步这个文件夹里的 Claude Code 会话；然后打开一个会话，或者用 **New session** 新建一个。

所有配置项、主机上存储的数据、升级和备份，见 [docs/hosting.md](docs/hosting.md)。

## 安全

Hoplet 只运行在你的主机和你的机器上，不调用任何外部服务。网页界面和命令行工具使用主机令牌；每个 agent 有自己的设备令牌，服务端只保存它的哈希值。Claude Code 只在拥有该会话的机器上运行，而且 Hoplet 只控制它自己启动的进程。保护了什么、防范谁、怎么防范，见 [docs/threat-model.md](docs/threat-model.md)。发现漏洞请按 [SECURITY.md](SECURITY.md) 的说明私下报告。

## 文档

以下文档目前只有英文版。

| 文档 | 内容 |
|---|---|
| [docs/hosting.md](docs/hosting.md) | 运行 Hoplet：系统要求、配置项、数据、升级、反向代理 |
| [docs/spec.md](docs/spec.md) | Hoplet 做什么：逐个界面、逐个流程的说明 |
| [docs/architecture.md](docs/architecture.md) | 各部分如何协作，以及它们之间的契约 |
| [docs/api.md](docs/api.md) | 供其他客户端使用的 HTTP API 和浏览器 WebSocket |
| [docs/threat-model.md](docs/threat-model.md) | 保护了什么、怎么保护 |
| [docs/testing.md](docs/testing.md) | 检查、端到端测试和性能基准 |
| [docs/adr/](docs/adr/README.md) | 架构决策及其理由 |
| [CHANGELOG.md](CHANGELOG.md) | 各版本的变更 |

## 参与贡献

欢迎提交 bug、想法和 pull request。动手做新功能之前请先开 issue 讨论；每个提交都需要签名（`git commit -s`，即开发者原创声明 DCO）；如果用了 AI 工具，请在 PR 里说明。详细要求和开发环境搭建见 [CONTRIBUTING.md](CONTRIBUTING.md)；怎么做决定、怎么分诊、怎么发布，见 [GOVERNANCE.md](GOVERNANCE.md)。Hoplet 主要由维护者和 AI 编程 agent 协作开发，AI 遵循的规则写在 [AGENTS.md](AGENTS.md) 里。

## 获取帮助

提问请到 [GitHub Discussions](https://github.com/yangflow/hoplet/discussions)；报告 bug 请开 [issue](https://github.com/yangflow/hoplet/issues)。详见 [SUPPORT.md](SUPPORT.md)。

## 许可证

Hoplet 采用 [Apache License 2.0](LICENSE) 许可证（另见 [NOTICE](NOTICE)）。使用的第三方软件及其许可证列在 [THIRD_PARTY.md](THIRD_PARTY.md) 中。
