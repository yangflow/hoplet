# M4b PR4 后台提炼 · 实施计划

**目标：** 会话阶段结束时，daemon 在后台从有新证据的会话里提炼记忆，经 PR2 的写入服务提交；不阻塞前台，崩溃后不重复提交，预算用尽时材料保留。

**PR 标题：** `feat(memory): extract candidates in the background`

**依据：** [总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)“提炼”“更新与纠错”、[0021](../design/0021-claude-adapter.md)“隔离提炼”。

**前置：** PR2 已合入；PR3 已合入（提炼复用会话范围）。

## 实测结论（2026-11-08，Claude Code 2.1.319）

| 假设 | 结果 |
|---|---|
| `disableAllHooks` 关闭 hook | 对照组（`false`）hook 触发；`true` 连跑 3 次都不触发。hook 由 `--settings` 传入，见取舍 4 |
| 模型稳定输出操作 JSON | 3 次都是合法 JSON，无多余文字；材料里的“忽略指令”没有被执行 |
| 证据引用 | 3 次里有 1 次把 `event:s1/3` 写成 `s1/3`，见取舍 3 |
| 闲置 30 分钟可判断 | 可以：会话状态加最后一条事件的时间 |

## 待维护者确认的取舍

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| 1 | 0016 的“换 harness”触发在代码里没有入口 | 这一期不做这个触发，0016 注明等换 harness 功能落地时接上 | 现在会话创建后 harness 不能换，新建另一 harness 的会话不算结束旧会话，旧会话仍由闲置或归档触发 |
| 2 | 闲置怎么判断 | 每分钟扫一次：状态为空闲、最后一条事件距今满 30 分钟、有新证据 | 不用给每个会话挂定时器；daemon 重启后自然恢复；测试注入时钟，不用 sleep |
| 3 | 模型怎么引用证据 | 材料每行标序号 `[12]`，输出写 `{"kind":"user_stated","event":12}`，会话和前缀由代码补全；第一期不让模型引用产物 | 实测模型会写错引用格式；模型也无从引用别的会话。产物只作背景 |
| 4 | 用户级和插件 hook 的验证 | 接受用 `--settings` 传入 hook 的对照实测，0021 写明这一范围 | 真正装一个用户级 hook 要改 `~/.claude`，项目规则不允许 |
| 5 | 开关放哪 | 新表存一行，默认开；CLI `hoplet profile extraction on/off/status`，Mac 设置项随 PR6 | 现在没有通用设置表，不为一个开关造通用机制 |
| 6 | 来源会话还没有模型 | 不建运行，等 harness 报告模型后下次扫描再来 | 0016 要求显式传模型，不用默认值代替 |
| 7 | 哪些会话提炼 | 非 `Ephemeral` 的 Personal（主、子）和 Code，harness 是 Claude 2.1.319 | 跟随来源会话；Hermes、Codex 和其他版本不建运行，状态里说明原因 |

## 数据

迁移 `0019_memory_extractions.sql`，时间用整数毫秒：

| 表 | 字段 |
|---|---|
| `memory_extractions` | `id`、`session_id`、`harness`、`model`、`from_seq`、`to_seq`、`trigger`（archive、handoff、report、idle）、`state`（pending、running、parsed、done、failed）、`attempts`、`output`、`usage`、`error`、`created_at`、`updated_at` |
| `memory_extraction_progress` | `session_id` 主键、`through_seq` |
| `memory_extraction_state` | 单行：`enabled` |

`output` 在提交前落库：`parsed` 之后崩溃只重放提交，不再调用模型。每日次数按 harness 和本地日期数 `started` 过的运行，查询得出，不另存计数。

## 接口

```go
// internal/harness
type ExtractRequest struct{ Env []string; Dir, Model, System, Input string }
type ExtractResult struct{ Output string; Usage proto.Usage }
// Extractor is a harness able to run one isolated extraction.
type Extractor interface {
    // ExtractSupport says why this installed version cannot, "" when it can.
    ExtractSupport(info Info) string
    Extract(ctx context.Context, r ExtractRequest) (ExtractResult, error)
}
```

只有 Claude 实现，且只认 2.1.319，本地自验的版本也不算。参数和环境严格按 0021，工作目录用一个空的临时目录，提示词走 stdin，不额外传凭据变量。

`internal/memory/extract`（新包，依赖 `memory`、`store`、`harness`）：

| 文件 | 内容 |
|---|---|
| `material.go` | 选材：用户消息（`From == ""` 且 `Agent == ""`）、助手最终回复、工具调用的摘要行、产物名；其他会话的回报、思考、增量、提炼自身不进；累计到 32KB 在事件边界停下，单条过长截断并标明 |
| `prompt.go` | 提炼指令（英文常量）、现有条目（范围内最多 8KB，带 ID 和版本） |
| `output.go` | 严格解析：未知字段、非法动作、缺字段都算整次失败，不放宽 |
| `worker.go` | 循环：恢复 → 扫描建运行 → 一次执行一个；通过 `store.Changes()` 和每分钟的定时唤醒；时钟可注入 |

提交：操作 ID `extract_<运行ID>_<序号>`，`Actor=extraction`，`Batch{Session,From,To}`，范围种类的值取自 `ProfileScopes`（该会话没有这个范围时这一条记 rejected）。提炼提出的更新带模型读到的版本，版本不符由服务转候选。全部提交后在一个事务里把运行置 `done` 并推进 `through_seq`。

触发：`Manager.Archive`、转交、子会话回报调用 `Extractor.Enqueue(session, trigger)`；提炼关闭或 harness 不支持时为空操作，并记录原因供状态显示。

失败：调用或解析失败最多重试 2 次（共 3 次），之后 `failed`，进度不前进，下次触发时从同一位置重新建运行；预算用尽时不建运行，材料留在队列，次日继续。

## 任务

### 任务 1：存储与隔离运行

- [ ] 迁移和 `store` 方法；`harness.Extractor`；Claude 实现
- [ ] 测试：参数和环境逐项按 0021 核对；其他版本和 Hermes 不支持并给出原因；非零退出和 `is_error` 转错误
- [ ] commit：`feat(memory): add extraction runs and isolated harness calls`

### 任务 2：选材、指令与解析

- [ ] `material.go`、`prompt.go`、`output.go`
- [ ] 测试（表驱动）：哪些事件进材料；32KB 边界和超长单条；回报和提炼事件不进；刚好满和超 1 字节；非法输出的各种形态
- [ ] commit：`feat(memory): select extraction material and parse output`

### 任务 3：运行器

- [ ] `worker.go`；daemon 接入；三个触发点；每分钟扫描；开关和 CLI
- [ ] 测试（假 Extractor、注入时钟）：闲置前后；预算用尽材料保留、次日继续；非法输出重试 2 次后失败；`running`、`parsed` 崩溃后恢复不重复调用、不重复提交；用户纠正后回放旧材料不复活；不支持的 harness 不建运行；模型缺失时等待；开关关闭；前台 `Send` 不被运行阻塞
- [ ] commit：`feat(memory): run extraction in the background`

### 任务 4：实测与收尾

- [ ] 真实 Claude 2.1.319 跑一次完整提炼：无工具调用，无残留会话，条目按分级进入生效或候选
- [ ] 更新 0016（取舍 1、3、5）、0021（取舍 4）；`make check`；计划写“结果”
- [ ] commit：`docs(memory): record extraction results`

## 风险

| 风险 | 处理 |
|---|---|
| 每次提炼花用户的额度 | 默认每日 20 次、单批 32KB，可关闭；用量随运行记录 |
| 材料里的内容诱导模型 | 指令声明材料只是数据；`user_stated` 由服务核对必须是真实用户消息；模型写不出别的会话的证据 |
| 同一事实反复提炼 | 提供现有条目去重；服务按证据时间拦截旧材料；进度只前进 |
