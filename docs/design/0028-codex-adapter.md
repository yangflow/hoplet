# 0028 Codex 适配

状态：目标设计 · 更新：2026-11-08

## 范围与现状

范围：`codex app-server` 的结构化驱动、托管安装、权限映射、工具与人格注入。

| 状态 | 边界 |
|---|---|
| ○ 目标 | 本文全部内容；协议事实在 0.167.0 上实测 |

## 接入

```mermaid
flowchart LR
    Daemon["daemon 适配器"] <--> Shim["shim"]
    Shim -- "stdio JSON-RPC 行" --> Server["Hoplet 托管的 codex app-server"]
    Server --> Thread["原生 thread"]
```

- 一个 Hoplet 会话对应一个 `codex app-server` 进程和一个 thread，放在 shim 后面，daemon 重启不丢事件。
- `initialize` 打开 `experimentalApi`：Plan 和语音只在实验接口下可用。
- Go 类型只手写用到的协议子集，由录制回放的契约测试保证，不从 JSON Schema 生成。
- 回合结束只认 `turn/completed`；进程中途退出记为中止。thread ID 是原生指针，身份仍是 Hoplet 会话 ID。

## 托管范围

| 项 | 规则 |
|---|---|
| 来源 | 官方发布的完整独立包 `codex-package-<target>`，先用同一发布的 `codex-package_SHA256SUMS` 校验 |
| 位置 | `~/.hoplet/harness/codex/`，固定版本，关闭自动更新 |
| 登录与配置 | 沿用用户自己的 Codex 登录和配置；Hoplet 不读、不复制 `CODEX_HOME` 内的任何文件 |
| 用户安装 | 我在终端用的 Codex 不受影响，Hoplet 不替我升级 |
| 环境 | 与其他 harness 相同的清洗规则，不额外传入凭据变量 |

用户配置里的 MCP 服务器会随 thread 启动，这是用户自己的配置，Hoplet 不改；启动失败不影响会话。

## 权限映射

| Hoplet 档位 | `approvalPolicy` | `sandbox` | `approvalsReviewer` |
|---|---|---|---|
| 需确认 | `untrusted` | `workspace-write` | `user` |
| 自动编辑 | `on-request` | `workspace-write` | `user` |
| 智能审批 | `on-request` | `workspace-write` | `auto_review` |
| 全自动 | `never` | `danger-full-access` | `user` |

- Plan 是独立开关，通过 `thread/settings/update` 的 `collaborationMode` 设置，并带上当前模型和 Effort。计划内容以 `plan` 条目或普通回复到达，两者都显示为助手消息。
- 切换权限同样走 `thread/settings/update`。只有进出全自动才改沙箱，其余三档共用一个沙箱，不覆盖用户配置里的网络设置。
- `workspace-write` 的可写范围包含系统临时目录，这是 Codex 自身的沙箱规则。
- 审批选项原样透传 `availableDecisions`（例如 `accept`、带命令规则的 `acceptWithExecpolicyAmendment`、`cancel`），Hoplet 不补造选项。
- 智能审批下被判定不放行的操作不会问我，模型会收到被拒结果并自行说明。

## 能力

| 能力 | 声明 | 说明 |
|---|---|---|
| Steer | 是 | `turn/steer`，需带当前回合 ID |
| 续接 | 是 | `thread/resume` |
| 设置模型 / Effort | 是 | 模型和档位来自 `model/list` |
| 切换权限 / Plan | 是 | 下一回合生效 |
| MCP 注入 | 是 | 见下 |
| Fast | 否 | 实测账号的模型都没有速度档；模型列表出现后再声明 |
| Fork / 回退 | 否 | 协议有，暂不接入 |

Codex 自带的多代理显示为普通工具行，不建 Hoplet 子会话。

## 工具与人格

- 工具通过启动参数 `-c mcp_servers.hoplet.*` 注入，只含命令和令牌文件路径。
- 令牌写在会话目录的 0600 文件，由 `hoplet mcp --token-file` 读取，每次启动轮换。令牌不进命令行、stdin 记录或 harness 环境。
- Hoplet 工具的权限由 Hoplet 自己管，设 `default_tools_approval_mode = "approve"`，否则需确认档下每次调用都会弹 Codex 审批。
- 人格通过 `thread/start` 的 `developerInstructions` 注入。**`thread/resume` 传新值不生效**，所以人格修改只对新的主会话保证生效，保存时如实告知。

## 版本门控

录制过的版本直接可用，没有录制的版本须通过本机自检（命令审批与拒绝、切换档位后免审批、中断、新进程续接）。范围外的版本只读。

Hoplet 固定 Codex 版本，不跟进新发布，守护进程没有每日检查：固定版本随 Hoplet 升级而变。`hoplet harness install codex` 对没有录制的固定版本先在待切换的副本上自检，不过则留在旧副本，`hoplet doctor` 显示原因。`hoplet harness verify codex` 手动自检。

## 验收

- 录制回放覆盖对话、审批与拒绝、中断、续接、Plan、模型和 Effort 切换。
- 假 harness 跑通 daemon 重启后接回。
- 令牌不出现在进程参数、spool、日志和 harness 环境中。
- 安装时校验失败不切换副本。

## 待验证

| 项 | 现状 |
|---|---|
| 完整独立包是否含语音组件 | 安装实现时检查包内容 |
| `auto_review` 的事件形态 | 一次实测中模型直接得到被拒结果，未见审核事件，录制时再确认 |
