# Hoplet

[English](README.md)

Hoplet 是一个自用的、跨设备的 AI Agent 壳。**Code 模式帮我做产品，Personal 模式帮我处理琐事。** 两种模式共用一个内核，彼此可以转交工作。

> **状态：** 本机内核与桌面对话已有实现，M1 收尾中。以下概览也包含规划能力。

## 两种模式

| | Code | Personal |
|---|---|---|
| 交付 | 可审阅的变更：diff、检查点、PR | 结果：答案、办完的事、文件 |
| 工作空间 | 我选的目录，可包含多个仓库 | 伙伴的工作区与授权目录 |
| 发起 | 我 | 我、定时、消息、事件 |

两种模式均可选择已支持的 harness。当前有 Claude Code、Hermes 适配，Codex 和 ACP 属于规划。设计中由 Hoplet 持有可迁移成长记忆，harness 提供推理与执行能力。

## 架构概览

```mermaid
flowchart LR
    User["桌面 · iOS · CLI · 通道"] --> Modes["Code / Personal"]
    Modes --> Core["Hoplet 内核<br/>会话 · 审批 · 记忆 · 产物"]
    Core --> Harness["现成 harness<br/>Claude Code · Hermes · Codex · ACP"]
    Harness --> Capabilities["模型 · 工具 · 技能"]
```

- 一个 Go 二进制当前提供 daemon、shim、CLI 、Hermes 桥接和中继；在 Linux 上，`hoplet daemon install` 与 `sudo hoplet relay install` 把它们装成 systemd 服务。
- shim 在 daemon 重启时继续托管本机会话；远程跟进与审批属于规划。
- 远程访问目标为端到端加密，中继只转发字节。
- 桌面采用 MyGo 原生 UI，iOS 使用 SwiftUI。

## 设计导航

| 范围 | 专题 |
|---|---|
| [产品与架构](docs/README.md#总览) | 边界、分层、交付路线 |
| [运行与通信](docs/README.md#运行与通信) | 进程、数据、同步、安全、审批 |
| [Agent 驱动](docs/README.md#agent-驱动) | 通用契约、原生适配、版本管理 |
| [工作区与编排](docs/README.md#工作区与编排) | 隔离、快照、终端、委派 |
| [Personal 与成长](docs/README.md#personal-与成长) | 伙伴、记忆、自主运行、集成 |
| [客户端与交付](docs/README.md#客户端与交付) | 技术栈、设计系统、布局、质量 |

设计文档使用中文。每个专题独立说明当前规则，目录负责导航。

## 交付概览

| 阶段 | 结果 |
|---|---|
| M1 | 收尾本机闭环：收件箱、命令面板、文件 / diff 右栏与回归门禁 |
| M2a / M2b | 先安全网络，再移动访问 |
| M3 | 工作区增强，独立于网络轨道 |
| M4a–M4e | 工具桥与伙伴底座；成长、自主、通道和跨机按依赖推进 |
| M5a–M5c | 新 harness；可写扇出需隔离，手机预览需网络和端口 |

推荐本机主线：**M1 → M4a → M4b**。网络、工作区和新 harness 可独立推进，阶段编号不表示全局串行。

## 参与贡献

贡献前阅读 [CONTRIBUTING.md](CONTRIBUTING.md)。设计变更先开 issue 讨论再写代码；issue 与 PR 均可使用中文或英文。

## 许可证

[Apache License 2.0](LICENSE)
