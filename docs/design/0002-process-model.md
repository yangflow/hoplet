# 0002 进程模型与部署

状态：当前设计 · 更新：2026-11-04

## 范围与现状

范围：单二进制角色、进程托管、部署与重启恢复。

| 状态 | 边界 |
|---|---|
| ✓ 代码已有 | 本机 daemon、stdio shim、macOS LaunchAgent 与偏移续接 |
| ○ 目标 | Linux 服务安装、PTY、电源断言、中继与配对 |

## 决策

| 项 | 结论 |
|---|---|
| 二进制 | 当前有 `daemon`、`shim`、CLI 与 `hermes-bridge`；`relay` 是后续目标 |
| 托管 | 当前 `daemon install` 只支持 macOS LaunchAgent，锁占用时按 30s 节奏重试；Linux systemd 安装是目标，不是现有命令能力 |
| 会话进程 | 每个活跃会话一个 shim，当前托管 harness / bridge stdio；PTY 托管未实现 |
| 单例 | flock；socket 用“新进程监听临时路径 → 原子 rename”接管，清理逻辑永不删 socket（误删会让新守护进程“能连不能用”） |
| 版本 | 一次握手：主版本不一致即拒绝并提示升级；shim ↔ daemon 协议带版本 |
| 休眠 | `IOPMAssertion` 与“接电源时常亮”均为规划，当前没有电源断言实现 |
| 多机 | 目标为逐台对等配对；当前 daemon 仅有本机服务入口 |
| 平台 | macOS（Apple Silicon）、Linux（amd64 / arm64）；不做 Windows |

## 进程关系

```mermaid
flowchart TB
    LD["launchd / systemd"] --> D["hoplet daemon<br/>（可随时重启、升级）"]
    D <-- "Unix socket<br/>断开后按字节偏移续读" --> S1["hoplet shim · 会话 A"]
    D <--> S2["hoplet shim · 会话 B"]
    S1 --> C1["claude（stdio）"]
    S1 -. "规划" .-> T1["终端 PTY"]
    S2 --> C2["codex（stdio）"]
    S1 -. "守护进程不在时<br/>写 spool.jsonl" .-> F[("本地暂存")]
```

## 守护进程重启时会发生什么

```mermaid
sequenceDiagram
    participant D as 旧 daemon
    participant N as 新 daemon
    participant S as shim
    participant H as harness
    H->>S: 事件持续输出
    S->>D: 转发事件
    D--xS: 崩溃 / 升级退出
    H->>S: 事件持续输出
    S->>S: 追加写 spool.jsonl
    N->>N: 原子接管 socket
    N->>S: 重连，报告已收到的偏移
    S->>N: 补发缺失事件，恢复实时转发
```

shim 和原生进程保持运行时，daemon 重启不终止正在进行的轮次；补发按已提交偏移续读。

## shim 协议（已实现）

| 项 | 当前行为 |
|---|---|
| 帧 | 4 字节大端长度 + JSON，payload 最大 16 MiB，协议版本 1 |
| spool | JSONL 记录 start / stdout / stderr / stdin / stdin_error / exit，按字节偏移续读 |
| 分片 | 每记录 1 MiB，`More` 拼接；非 UTF-8 使用 base64 字节 |
| 写入 | stdin 接受不等于 harness 已收到；失败写入记录为 stdin_error |
| 排队与路径 | stdin 待写队列 64 条；Unix socket 路径按 103 字节上限检查，daemon 包含临时后缀 |
| 时限 | hello 5s、发送 30s、停止宽限 5s、退出输出排空 2s；退出后默认保留 shim 24h 等待补读 |

## 部署形态

| 主机 | 运行 | 作用 |
|---|---|---|
| Mac | `hoplet daemon` + 桌面 app | Code 会话主力；合盖即离线 |
| 海外 VPS（规划） | relay + headless daemon + systemd 用户服务 | 常驻主宿主；relay 与 systemd 安装尚未落地 |

## 恢复边界

| 情况 | 行为 |
|---|---|
| daemon 重启 | shim 继续托管，按偏移补发 |
| spool 损坏 | 当前记录接入错误；原生历史只读降级尚未实现 |
| 无历史协议 | 保留 Hoplet 已存记录，不解析 harness 私有文件补造内容 |
| harness / gateway 重启 | 按该适配器的续接能力核对，不套用 daemon 重启保证 |

当前超长原生行跨 spool 记录时，提交偏移停在完整行边界；中途重启可重复归一化片段，交错轮次重复会结清为失败。shim 崩溃后可能先重放旧尾部再启动新进程；不能对这些边界承诺“所有事件恰好一次”。

待验证：损坏降级、原生历史入口与服务接管；目标回放不得解析 harness 私有文件。

## 验收

| 范围 | 条件 |
|---|---|
| 重启 | 正常完整行按偏移接续，超长 / 交错重放边界可解释 |
| 服务 | macOS 安装与锁接管符合代码；Linux 不宣称已支持安装 |

## 待验证

尚未落地的目标按原生协议、故障恢复与平台约束分别验证。
