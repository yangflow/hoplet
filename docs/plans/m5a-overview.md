# M5a Codex 适配 · 总计划

**目标：** Codex 成为和 Claude Code、Hermes 同级的 harness：能新建、对话、审批、续接，能调用 Hoplet 工具，为语音通话打好基础。

**验收（交付路线）：** Codex 单独通过协议契约，不依赖 Personal 其余部分。

**依据：** [设计总览](../README.md)，以及 [0028](../design/0028-codex-adapter.md)、[0003](../design/0003-agent-driving.md)、[0007](../design/0007-commands-approvals.md)、[0023](../design/0023-harness-lifecycle.md)、[0027](../design/0027-voice-calls.md)、[0019](../design/0019-delivery-roadmap.md)。

**前置：** M1 已合入。工具注入依赖的 M4a 也已合入。

## 维护者的裁决（2026-11-08）

1. **程序来源。** 下载官方完整独立包到 Hoplet 自己的目录，固定版本；登录用用户自己的，不碰 `CODEX_HOME` 内的文件。
2. **权限映射。** 按 0028 的表。
3. **令牌。** 新增 `hoplet mcp --token-file`，0600 文件，每次启动轮换。
4. **Go 类型。** 手写用到的协议子集，由录制契约测试保证。
5. **人格。** 用 `developerInstructions`。
6. **版本门控。** 录制加本机自检。

## 实测结论（2026-11-08，Codex 0.167.0，订阅账号）

| 项 | 结果 |
|---|---|
| 需确认 | 命令触发 `item/commandExecution/requestApproval`，选项在 `availableDecisions` |
| 自动编辑 | 工作区内不问；工作区外（真实用户目录）触发审批，同意后执行成功 |
| 智能审批 | 工作区外的命令未问用户，未执行，模型回复被拒；未见审核事件（只跑一次） |
| 全自动 | 不问，直接执行 |
| 沙箱范围 | `workspace-write` 下系统临时目录可写 |
| Plan | 产生 `plan` 条目；`collaborationMode` 必须打开 `experimentalApi` |
| 语音接口 | `thread/realtime/start` 必须打开 `experimentalApi` |
| 人格 | `developerInstructions` 生效；`thread/resume` 传新值后仍是旧人格 |
| MCP 注入 | `-c mcp_servers.<name>.*` 可用；默认调用会弹 `mcpServer/elicitation/request`，设 `default_tools_approval_mode="approve"` 后不弹 |
| 用户配置 | 用户自己的 MCP 服务器随 thread 启动 |
| Fast | 当前账号的模型 `serviceTiers` 全空 |

## 全局约束

- 不新增依赖。解包用标准库；`.tar.gz` 包优先。
- 不读 `CODEX_HOME`，不传额外凭据变量；录制文件清掉账号和本机路径。
- 持久化的新事件带版本号，旧解码器保留。
- 测试不启动真实 Codex、不用 sleep 同步；真实 Codex 只在 `hoplet harness verify` 和录制命令里运行。
- 桌面新建入口若写死了 harness 列表，在 PR1 改为读 `harnesses.list`，不新增界面元素。
- 代码、注释、commit 用英文；本目录下的计划用中文。

## PR 拆分与顺序

```mermaid
flowchart LR
    P1["PR1 安装与基本回合"] --> P2["PR2 审批与设置"]
    P2 --> P3["PR3 工具、人格与自检"]
```

| PR | 标题 | 主要内容 | 验收 |
|---|---|---|---|
| 1 | `feat(codex): install and run basic turns` | 新包 `internal/harness/codex`；手写 wire；`harness install / upgrade codex`（官方包、SHA256 校验、检查语音组件并记录）；Probe、Launch、Attach；消息、推理、命令、文件、用量事件映射；Send、Interrupt、续接；`hoplet dev record` 录制 | 录制回放金样通过；校验失败不切换副本；daemon 重启后接回；未知条目类型只记原始行、不报错 |
| 2 | `feat(codex): add approvals, questions and settings` | 命令、文件、权限审批，`requestUserInput` 问题；四档映射与 Plan；Steer；`model/list`；SetModel、SetEffort、SetMode、SetPlan；能力位 | 四档参数按 0028 表驱动测试；选项原样透传；首答生效，迟到回答被拒；Fast 不声明；录制覆盖审批同意与拒绝、Plan、steer、中断 |
| 3 | `feat(codex): inject tools and persona, add self-check` | `hoplet mcp --token-file`；`-c` 注入与免审批项；令牌轮换；`developerInstructions`，`KeepsPersona`；本机自检与跟进策略；`doctor` 展示 | 令牌不出现在进程参数、spool、日志、harness 环境；环境变量方式对现有 harness 不变；人格按新建与续接分别测试；自检失败留在旧副本 |

## 不做

Fork、回退、Fast、原生多代理的子会话视图、语音（语音由交付路线的下一里程碑负责）。
