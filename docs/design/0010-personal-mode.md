# 0010 Personal 模式

状态：已采纳 · 2026-10-31

## 定位

Personal 是一种**模式**，不是某个 harness。默认 harness 是一个设置项，随时可换；模型支持**订阅登录和 API key** 两种方式（凭证都留在 harness 自己的配置里）。

## 运行位置

```mermaid
flowchart LR
    TG["Telegram"] --> V["VPS daemon<br/>Personal 主宿主 · 7×24"]
    App["iOS / 桌面"] --> V
    V -- "run_on：需要 Mac 的子任务" --> M["Mac daemon"]
    M -. "Mac 休眠：排队，并告诉我要等" .-> V
    GH["GitHub webhook"] -- "/hook/{id}" --> V
```

## 个人档案（可移植记忆）

```
~/.hoplet/profile/          （位于主宿主）
  PROFILE.md                 索引
  persona.md                 人格（只注入 Personal）
  about-me.md · preferences.md · facts/*.md · people.md …（带 frontmatter）
```

```mermaid
flowchart LR
    Prof[("个人档案")] -- "会话开始：压缩到约 8KB<br/>经 harness 原生入口注入" --> H["当前 Personal harness"]
    H -- "profile_write（带版本检查，记日志）" --> Prof
    Me["我"] -- "在 app 里查看 / 编辑" --> Prof
    C["Code 会话"] -. 只读查询 .-> Prof
```

- Hoplet 只负责**存放与注入**；“学什么、怎么整理”交给 harness。
- harness 自带的记忆照常运行；“关于我的事实”约定写进档案，换 harness 不丢。

## 触发器

```mermaid
flowchart LR
    subgraph sources["来源"]
        Sch["定时<br/>cron / interval / once"]
        IM["Telegram 消息"]
        Evt["事件<br/>会话等待 / 出错 / 卡住<br/>PR · CI 变化 · webhook"]
    end
    Sch --> T["Trigger<br/>预算 · 启用"]
    IM --> T
    Evt --> T
    T --> A1["开新 Personal 会话"]
    T --> A2["发给已有会话"]
    T --> A3["只通知我"]
```

| 规则 | 说明 |
|---|---|
| 主机离线错过 | 默认跳过并通知；可设为恢复后补跑一次 |
| 并发 | 同一触发器同时只跑一个 |
| 创建 / 修改 | 默认问我（Hoplet 规则） |

## Telegram

| 项 | 结论 |
|---|---|
| 运行 | VPS 上 bot 长轮询，无需公网入口 |
| 授权 | app 里生成一次性配对码，Telegram 用户 ID 进白名单，其他人消息一律忽略 |
| 线程 | 每个（通道，聊天）一条线程；空闲 N 小时或 `/new` 切新会话，带摘要 + 档案 |
| 发消息 | 发给我自己：直接发；替我发给别人：必须我本人确认 |

## 首批琐事：GitHub

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant P as Personal
    participant Me as 我
    participant C as Code 会话
    GH->>P: CI 失败（webhook，轮询兜底）
    P->>P: 读日志，归纳失败原因
    P->>Me: Telegram / 推送：“web#42 的 CI 挂了，原因是…，要我开会话修吗？”
    Me->>P: 修
    P->>C: 转交：开 Code 会话（对应工作目录 + 失败摘要）
    C-->>P: 修完一轮 → 通知
    P->>Me: 汇报结果，等我审 diff
```

- 能力来源：GitHub MCP / `gh`，登记在 `mcp.toml`（按模式启用）。
- 其他琐事（邮件、日历、浏览器……）以后通过接入现成 MCP 扩展，Hoplet 不自己造工具。

## Personal 可用的 Hoplet 工具

`sessions_list/read/status` · `session_create` · `session_send` · `handoff` · `delegate` · `profile_*` · `artifact_save` · `notify_me` · `trigger_create` · `terminal_*` · `run_on`

## 护栏

| 护栏 | 说明 |
|---|---|
| 预算 | 每个触发器、每次自主运行有轮次上限和 token 预算；另有每日总预算；超出即停并通知 |
| 循环检测 | 重复同一调用或无进展 → 自动暂停 |
| 代批 | Personal 代答 harness 审批：v1 不做，数据模型已预留 |

## 风险与兜底

harness 能遵守“事实写进档案”的约定。若不遵守，档案会变薄；可加一个低频后台任务，从会话摘要里提炼候选事实，交给我确认后写入。
