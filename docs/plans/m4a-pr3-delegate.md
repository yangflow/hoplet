# M4a · PR3 委派、回报与转交 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `e22bf46`（#37 合并后）的 `main`。

**目标：** Personal 会话把事情委派给子会话、继续交代、收回结果；需要改代码时转交给归属于我的 Code 会话。结果持久回到父会话：父会话空闲时投递，忙时排队。

**验收（M4a 总计划）：** Code 会话调用 `session_create` / `session_send` / `handoff` 被拒；`session_send` 到非托管会话被拒；父会话忙时回报排队、空闲后投递一次；daemon 重启后未投递的回报不丢不重。

**依据：** [M4a 总计划](m4a-overview.md)、[0010](../design/0010-personal-mode.md)、[0012](../design/0012-orchestration-api.md)、[0007](../design/0007-commands-approvals.md)、[0004](../design/0004-session-data-model.md)。

## 维护者的裁决（2026-11-08）

1. **回报走现有的消息队列。** 回报是发给父会话的一条消息，记下来自哪个会话；父会话空闲就发，忙就入队，持久、按键幂等，与我的消息同一条路。回报在父会话的队列里，我可以编辑或删除。
2. **任何 Personal 会话都能委派。** 子会话也能再建子会话、继续交代、转交；回报交给直接的父会话。
3. **转交立即开工。** `handoff` 建归属于我的 Code 会话，权限为 confirm，摘要作为第一条消息发出；审批都到我这里。它每个轮次结束、每次等待审批时自动回报父会话。
4. **`session_create` 的参数。** 任务（第一条消息）必填；标题、harness（默认与调用方相同）、目录（不给就用会话目录）可选；权限沿用调用方，模型与 effort 在 harness 相同时沿用。

## 全局约束

- 不新增依赖。
- 迁移只加列、加表，不改已有数据；`Message` 新增的字段可省略，旧事件照常解码。
- 测试不碰真实的 `~/.hoplet`、不启动真实 harness、不用 sleep 同步，全部 `-race`。
- 范围检查在 `internal/tools` 与服务里做，不靠提示词。

## 设计细节

### 存储

| 项 | 规则 |
|---|---|
| 迁移 | `0013_delegation.sql`：`sessions` 加 `parent TEXT NOT NULL DEFAULT ''`；`queue` 加 `from_session TEXT NOT NULL DEFAULT ''`；新表 `reports(parent TEXT NOT NULL REFERENCES sessions(id), key TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY (parent, key)) STRICT, WITHOUT ROWID` |
| 父会话 | 建会话时写入，之后不改；Personal 子会话 `owner = delegated`，转交的 Code 会话 `owner = me`，两者都有 `parent` |
| 回报入队 | `Store.Report(ctx, parent, from, key, text)`：一个事务里先查 `reports`，键已在就什么都不做（返回已有）；否则插 `reports` 行并入队（`from_session` 记来源）。所以一个键只会入队一次，我删掉的回报也不会再出现 |
| 队列 | `QueuedMessage` 加 `from`；编辑保留 `from` |

### 协议

| 项 | 规则 |
|---|---|
| `proto.Session` | 加 `parent`（可省略） |
| `proto.Message` | 加 `from`（可省略）：这条消息是哪个会话的回报 |
| `proto.QueuedMessage` | 加 `from`（可省略） |
| API | 不加新方法；子会话与转交只经工具桥建；`sessions.list` / `get` 带出 `parent` |

### 投递

| 项 | 规则 |
|---|---|
| 路径 | 回报入队后由现有 queuer 投递：父会话空闲就发，忙就等；daemon 重启后照常从队列继续，发送按回执幂等 |
| 记录 | 父会话日志里的 `Message` 记回报原文和 `from`，角色 user |
| 交给 harness 的文本 | 原文前加一行来源：`[Report from Hoplet session <id> "<标题>"]`，空一行接原文；模型据此知道这不是我说的 |
| 父会话已归档 | `report` 返回失败给模型；转交的自动回报跳过 |
| 队列满 | 沿用 `MaxQueue`，入队失败把原因返回给模型 |

### 工具

| 工具 | 谁能看见 | 参数 | 规则 |
|---|---|---|---|
| `session_create` | Personal | `prompt`（必填）、`title`、`harness`、`directory` | 建 Personal 子会话：`owner = delegated`、`parent = 调用方`；权限沿用调用方，harness 相同时沿用模型与 effort；`directory` 须是已存在的绝对路径；`prompt` 立即发出（键 `create:<子会话 ID>`）；返回子会话 |
| `session_send` | Personal | `id`、`text` | 目标须是调用方的直接子会话且 `owner = delegated`，否则拒绝；经 queuer 发送或入队；返回 `{seq, turn}` 或 `{queued}` |
| `handoff` | Personal | `directory`（必填）、`summary`（必填）、`title`、`harness` | 建 Code 会话：`owner = me`、`parent = 调用方`、权限 confirm、harness 默认与调用方相同；`summary` 立即发出；返回 Code 会话 |
| `report` | 有父会话的会话 | `text` | 以随机键 `report:<会话 ID>:<随机>` 经 `Store.Report` 交给父会话；返回 `{queued}` 或 `{seq, turn}` |

- Code 会话看不见 `session_create` / `session_send` / `handoff`；硬调用得到“没有这个工具”。
- 不是子会话的会话看不见 `report`。
- ID 参数支持唯一前缀，与 `session_get` 一致。

### 转交的自动回报

| 项 | 规则 |
|---|---|
| 范围 | `owner = me` 且有 `parent`、未归档的会话 |
| 触发 | daemon 在每次存储变化后检查这些会话：最近结束的轮次、正在等待的审批 |
| 轮次结束 | 键 `handoff:<会话 ID>:turn:<轮次>`；文本是该轮最后一条助手消息，没有时写“轮次结束，没有回复” |
| 等待审批 | 键 `handoff:<会话 ID>:approval:<审批 ID>`；文本说明在等哪项审批 |
| 不重复 | 键是确定的，`Store.Report` 只入队一次；daemon 重启后重新检查也不会重复 |
| 中间轮次 | daemon 停着的期间结束了几轮，只回报最近的一轮 |

## 任务

### 任务 1：协议与存储

- [x] `proto/session.go`、`proto/payloads.go`、`proto/api.go`：`Session.Parent`、`Message.From`、`QueuedMessage.From`
- [x] `internal/store/migrations/0013_delegation.sql`
- [x] `internal/store`：建会话写 `parent`；`Store.Report`；队列读写 `from_session`，编辑保留
- [x] 测试：新字段的 JSON（省略时与现在一字不差）；从 0012 迁移后会话、事件、队列不变；`Report` 同一键只入队一次，删掉后再报也不再入队；父会话不存在时报 `ErrNotFound`
- [x] commit：`feat(store): record parents and reports`

### 任务 2：会话层与投递

- [x] `internal/session`：`CreateRequest.Parent`、`Owner`；`Send` 接受来源，日志记原文与 `from`，交给 harness 的文本带来源行
- [x] `internal/daemon/queue.go`：投递队列消息时带上 `from`
- [x] 测试：带来源的消息日志与 harness 收到的文本；父会话空闲时回报直接投递；忙时入队，轮次结束后投递一次；daemon 重启（新 manager 与 queuer 读同一个库）后未投递的回报照常投递、不重复
- [x] commit：`feat(session): deliver reports through the queue`

### 任务 3：工具

- [x] `internal/tools`：`session_create`、`session_send`、`handoff`、`report`；`Source` 增加所需的服务方法
- [x] `internal/daemon`：实现这些方法（建会话、经 queuer 发送、`Store.Report`）
- [x] 测试（tools，假 Source）：可见性按调用方；参数校验；`session_send` 到别人的子会话、到我开的会话、到转交的 Code 会话都被拒；嵌套：子会话建的孙会话回报给子会话
- [x] 测试（daemon，假 harness 经 `hoplet mcp`）：伙伴建子会话、子会话 `report`、父会话收到带 `from` 的消息；Code 会话调用三个工具被拒
- [x] commit：`feat(tools): delegate, report and hand off`

### 任务 4：转交的自动回报

- [x] `internal/daemon/handoff.go`：随存储变化检查转交会话，按确定键回报
- [x] 测试：轮次结束回报最后一条助手消息；等待审批回报一次；同一轮次、同一审批 daemon 重启后不再回报；父会话归档后不回报
- [x] commit：`feat(daemon): report handoff progress`

### 任务 5：实测与文档

- [x] 本机实测：Claude Code 伙伴建一个子会话查一件事并 `report`，确认结果回到伙伴；伙伴忙时子会话回报，确认排队后投递；Hermes 伙伴同样走一遍；伙伴 `handoff` 到一个临时 git 目录，确认 Code 会话开工、等待审批与轮次结束都回报伙伴
- [x] 0010：委派与回报、转交从目标改为现状；0012：四个工具与投递规则；0004：`parent`、`reports`、`queue.from_session`；0007：工具默认规则；0019 的基线
- [x] 本计划勾选并记下实测结果
- [x] `make check`
- [x] commit：`docs: record delegation, reports and handoff`

PR 标题：`feat(session): delegate child sessions and report back`。

## 结果（2026-11-08）

### 与计划不同之处

| 计划 | 实际 | 原因 |
|---|---|---|
| `report` 返回 `{queued}` 或 `{seq, turn}` | 总是返回 `{queued}` | 回报一律经父会话的队列投递，空闲时也由 queuer 立即发出；工具不等投递结果 |
| `Store.Report` 键已在时返回已有的回报 | 返回“没有入队”，不返回内容 | 回报可能已投递或被我删掉，调用方只需要知道这次没有入队 |
| 轮次结束没有回复时写“轮次结束，没有回复” | 写 `The turn ended without a reply.` | 交给模型的文本与来源行一样用英文，代码里不写中文 |
| 等待审批的回报“说明在等哪项审批” | `Waiting for the user to approve: <标题>`，有说明时空一行接说明 | 同上 |
| `handoff` 的 harness 默认与调用方相同 | 同样默认，但不沿用调用方的模型与 effort | 转交的 Code 会话用 harness 的默认模型；要换由我在 Code 会话里改 |

### 本机实测

临时 home、本机 Claude Code 2.1.316 与 Hermes 0.22.5：

| 项 | 结果 |
|---|---|
| Claude Code 伙伴 `session_create` 一个子会话算 17×23 并 `report` | 子会话在会话目录工作、权限沿用伙伴；回报“17 × 23 = 391”进伙伴的下一轮，伙伴回复“收到：391” |
| 伙伴忙时回报 | 伙伴建子会话后前台跑约 40 秒的命令；子会话的回报在伙伴队列里（`from_session` 为子会话），伙伴轮次结束后投递一次 |
| Hermes 伙伴（模型 `zcode/GLM-5.3`）同样走一遍 | 伙伴经 `session_create` 建子会话；子会话第一轮失败（见下）；给子会话设好模型重发后，它经 `report` 交回“399”，Hermes 伙伴回复“收到：399” |
| Claude Code 伙伴 `handoff` 到临时 git 目录 | Code 会话权限 confirm 并立即开工；两次等待审批各回报伙伴一次，我批准后轮次结束回报最后一条助手消息；README 加上了 hello |
| daemon 重启 | 重启后 `reports` 行数不变、队列为空，伙伴没有新轮次 |
| 模型是否把回报当成我的指示 | 两家伙伴都按来源行把它当作子会话的回报处理 |
| 转交会话回报的频率 | 每次等待审批和每个轮次结束都让伙伴多跑一轮；这次实测可以接受 |

### 实测发现的问题

Hermes 用自定义 provider 时，`model.changed` 报回的 provider 是 `custom`，存储按设计以报告为准记下它。之后用这个记录新开 Hermes 进程（`session_create` 沿用调用方的模型）时，Hermes 解析不了 `custom`，第一轮以 `auth_permanent` 失败。已有会话重启续接不受影响。这是 Hermes 适配报告 provider 的问题，不在本 PR 里改，另行处理。

## 风险

| 风险 | 在哪发现 | 处理 |
|---|---|---|
| 模型把带来源行的回报当成我的指示 | 任务 5 实测 | 来源行写明是会话回报；不够时再讨论改用别的投递形式 |
| 子会话一直不 `report`，伙伴不知道结果 | 任务 5 实测 | PR3 不自动回报 Personal 子会话；伙伴可用 `session_get` 查；实测后再定 |
| 嵌套过深或循环委派 | — | 父会话建会话时写入、之后不改，不会成环；深度不设限，按需另议 |
| 转交会话的回报太频繁 | 任务 5 实测 | 只在轮次结束和等待审批时回报；记下实测感受 |
