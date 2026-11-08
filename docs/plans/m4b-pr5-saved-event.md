# M4b PR5 “记住了”事件 · 实施计划

**目标：** 伙伴或后台提炼自动保存记忆后，来源会话的日志里多一条事件，供聊天内显示“记住了：…”；这条事件不产生新的提炼。

**PR 标题：** `feat(memory): report saves in the session log`

**依据：** [总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)“界面”“提炼”。

**前置：** PR3、PR4 已合入。

## 与总计划的差异

总计划里 PR5 还要做列表、详情、版本历史、候选、编辑、撤回、确认、拒绝、提炼状态这些方法。它们已随 PR2、PR4 落地，生成的 Swift 类型也在，并且都限本机客户端（`localOnly`）。PR5 只剩事件，总计划的这一行随本 PR 改掉。

## 待维护者确认的取舍

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| 1 | 总计划写“配对设备按权限可读写”，0016 写“iOS 不提供管理界面”，代码现在只允许本机 | 维持只限本机，总计划改成与 0016 一致 | 记忆是用户最私密的数据；0016 已把 iOS 管理列为后续，放开远程要另外定权限。代价：Mac 连远程机器时，看不到那台机器的成长记忆 |
| 2 | 事件里放什么 | 只放条目 ID、版本、动作（`created`、`updated`）、来源（`companion`、`extraction`），不放正文和标题；桌面显示时用 `profile.get` 取 | 日志长期保留，正文进日志后，撤回也删不掉；撤回后那一行能如实显示已撤回。代价：PR6 每一行要多取一次，远程连接和 iOS 不显示这一行 |
| 3 | 哪些写入发事件 | 只有结果为 `applied`、动作是创建或更新、来源是伙伴或提炼；候选、拒绝、撤回、用户自己的编辑都不发 | 0016 写的是“自动保存后显示”。用户自己改的不需要告诉用户；候选有自己的入口 |
| 4 | 事件写失败 | 记忆已落盘就算保存成功，只记 `slog` 警告，不向调用方报错 | 0016 要求落盘后才说已保存，写入本身已成功；这一行丢了只影响显示，条目仍在成长记忆页 |
| 5 | 崩溃后重放 | `ReceiptKey` 用 `memory_saved:<OpID>` | 提炼崩溃后按原 OpID 重放，`Apply` 再次返回 `applied`，回执保证只记一条 |
| 6 | 这条事件会不会触发提炼 | 会，必须挡住：闲置扫描判断“新证据”和“最后一条事件时间”时跳过 `memory.saved` | 现在只比较事件序号。不挡的话，提炼写入事件 → 30 分钟后看到新事件 → 再建一次运行，占每日预算，还会把闲置计时往后推 |
| 7 | 未读标记和会话排序 | 这条事件不把会话标成未读，不改预览 | 提炼可能在你离开很久后才落下，不应让旧会话冒到“有新消息” |
| 8 | iOS | 协议层能解码，界面不显示 | 0026 没有这个状态；界面没设计的不做 |

## 协议

`proto/payloads.go` 新增类型 `TypeMemorySaved = "memory.saved"`，版本 1：

```go
// MemorySaved records, in the source session's log, that a memory entry was
// saved without the user editing it. It carries no entry text, so that
// withdrawing the entry leaves nothing of it in the log.
type MemorySaved struct {
	Entry   string `json:"entry"`
	Version int    `json:"version"`
	Action  string `json:"action"` // created, updated
	Actor   string `json:"actor"`  // companion, extraction
}
```

`validate` 拒绝空 `Entry`、`Version <= 0`、未知 `Action` 和 `Actor`。`proto/testdata/events.jsonl` 追加一行，`make gen` 更新 Swift。

## 步骤

### 1. 事件类型

**文件：** `proto/payloads.go`、`proto/event_test.go`、`proto/testdata/events.jsonl`、生成的 Swift。

先写表驱动的合法、非法用例，再加类型。

Commit：`feat(proto): add the memory saved event`

### 2. 写入后发事件

**文件：** `internal/memory/write.go`、`internal/memory/write_test.go`，以及 `internal/store/append.go`（第 7 条需要时）。

`Apply` 在 `applied` 的两条路径上（新提交、幂等重放）都调用同一个 `reportSaved`。来源会话取 `w.source()`，没有来源会话时不发。写入时取会话当前未结束的轮次，取不到为 0，与产物发布事件相同。

测试（表驱动）：伙伴创建、更新各一条；提炼创建一条；候选、拒绝、撤回、用户编辑没有事件；同一 OpID 重放只有一条；事件写失败时 `Apply` 仍返回 `applied`；未读标记和预览不变。

Commit：`feat(memory): report automatic saves in the session log`

### 3. 不让事件触发提炼

**文件：** `internal/store/memory_extractions.go`、`internal/memory/extract/worker.go`、`internal/memory/extract/select_test.go`、`internal/memory/extract/worker_test.go`。

“有新证据”和“最后一条事件时间”改为忽略 `memory.saved`。材料白名单不变，它本来就不收这个类型，加一条测试锁住。

测试：一次提炼落下事件后，把时钟拨过 30 分钟，不建新运行；之后来一条真实消息，正常建运行，范围覆盖中间的 `memory.saved`，材料里没有它。

Commit：`fix(memory): keep saved events out of extraction triggers`

### 4. 文档

**文件：** `docs/design/0016-growth-memory.md`（事件内容、只限本机、不算新证据）、`docs/plans/m4b-overview.md`（PR5 这一行）、本文“结果”。

Commit：`docs(memory): record the saved event`

## 验证

```bash
make gen
make check
```

合并最新 `main` 后再跑一次 `make check`，写入机器信息扫描。真实 harness 的验收放在 PR6 合入后，按总计划一起做。

## 不做

- 新的订阅或记忆变更推送：桌面进入页面时拉取，PR6 需要时再提。
- 远程设备访问成长记忆。
- 聊天内这一行的界面，属于 PR6，先过画板。
