# M4b · PR2 写入服务与 CLI · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时相关测试通过，最后 `make check` 通过。本计划列出文件、接口、要测的情况与 commit，基于 PR1 合并后的 `main`（`55a139e`）。

**目标：** 用户、伙伴、后台提炼三个来源经同一个服务方法写入记忆；证据校验、凭据拒绝、生效门槛、候选、撤回和旧材料拦截都在服务里完成；用户能用 CLI 查看、修改、撤回和确认候选。

**验收（M4b 总计划）：** 0016 “更新与纠错”表逐条有表驱动测试；纠正后回放旧材料不复活；撤回内容重现被拦截，晚于撤回的 `user_stated` 可恢复；写入失败不报已保存。

**依据：** [M4b 总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)、[PR1 计划](m4b-pr1-storage.md)。

## 需要维护者确认的取舍

0016 没有写到这一层，以下是本计划的选择：

1. **候选的存放。** 每个候选都是一个独立条目文件，`status: candidate`，有自己的 ID；新表 `memory_candidates` 记它针对哪个已有条目、期望版本和动作。这样不会覆盖生效中的条目，也不用扩展 frontmatter。
2. **拒绝候选等于撤回这个候选。** 同样内容再次出现时按“已撤回内容重现”拦截。
3. **“已撤回内容重现”的判定。** 服务只比较规范化后的正文（去首尾空白、连续空白压成一个）。意思相同、措辞不同的内容由模型判断，经 `target` 指向已撤回条目后再走拦截，服务不猜语义。
4. **PR2 的 daemon 方法只开放给本机。** 配对设备的访问在 PR5 打开。
5. **CLI 不加 `add`。** 0016 的命令列表里没有；用户新增条目靠伙伴写入或手写文件。

## 全局约束

- 不新增依赖。迁移只加表。
- `proto` 新增类型后运行 `make gen`，生成代码不手改。
- 测试用 `t.TempDir()`，不碰真实的 `~/.hoplet`，不用 sleep 同步，全部 `-race`。
- 被拒内容疑似凭据时，审计不保存正文。

## 设计细节

### 写入请求

新文件 `internal/memory/write.go`：

```go
type Action string // create | update | withdraw

type Write struct {
	OpID            string
	Actor           Actor           // user | companion | extraction
	Action          Action
	Target          string          // update / withdraw 的条目 ID
	ExpectedVersion int             // update / withdraw 必填
	Category        Category
	Scope           Scope
	Body            string
	Evidence        []Evidence
	Reason          string
	Session         proto.SessionID // companion：发起写入的会话
	Batch           *Batch          // extraction：本批材料
}

type Batch struct {
	Session  proto.SessionID
	From, To uint64 // 事件序号，闭区间
}

type Outcome string // applied | candidate | rejected

type Result struct {
	Outcome Outcome
	Entry   Entry  // applied 为目标条目，candidate 为候选条目，rejected 为空
	Code    string // rejected 时的原因代码
	Reason  string
}

func (s *Service) Apply(ctx context.Context, w Write) (Result, error)
func (s *Service) Candidates(ctx context.Context) ([]Candidate, error)
func (s *Service) Accept(ctx context.Context, id, opID string, scope *Scope) (Entry, error)
func (s *Service) Reject(ctx context.Context, id, opID string) (Entry, error)
```

规则被拒是业务结果，返回 `Result{Outcome: rejected}`，并写入审计；`error` 只表示版本冲突和基础设施失败。**只有 `Outcome == applied` 才能对用户说“已保存”。**

### 校验顺序

| 步骤 | 规则 | 结果 |
|---|---|---|
| 1. 重试 | 同一 `OpID` 已有记录 | 返回当时的结果，不再写入 |
| 2. 形状 | 动作、字段不完整 | `store.ErrInvalid` |
| 3. 权限 | `companion` 的会话不是 Personal | `store.ErrInvalid`，不走审计 |
| 4. 凭据 | 正文命中凭据模式 | rejected，代码 `credential`，审计不存正文 |
| 5. 证据 | 见下表（`user` 跳过） | rejected，代码 `bad_evidence` |
| 6. 版本 | `Target` 不是 `ExpectedVersion` | `user`、`companion` 返回 `store.ErrConflict`；`extraction` 转候选 |
| 7. 旧材料 | 见“旧材料拦截”（`user` 跳过） | rejected，代码 `stale_evidence` 或 `withdrawn_replay` |
| 8. 门槛 | 见“生效门槛” | applied 或 candidate |

凭据模式：常见密钥前缀（`sk-`、`ghp_`、`gho_`、`github_pat_`、`AKIA`、`AIza`、`xox[abp]-`）加足够长度、PEM 私钥头，以及 `password|passwd|token|secret|api[_-]?key|密码|口令|密钥` 后接 `:`、`=`、`：`、`是`、`为` 和至少 6 个非空白字符。

### 证据

引用格式：`event:<session>/<seq>`、`artifact:<id>/<version>`。其他格式一律不通过。

| 规则 | 说明 |
|---|---|
| 至少一条 | `companion`、`extraction` 必须带证据 |
| 必须存在 | 新增 `Store.EventBySeq(ctx, session, seq)`；产物用已有的 `ArtifactVersion` |
| 归属 | `companion`：事件属于 `Session`，产物属于该会话；`extraction`：事件属于 `Batch.Session` 且序号在区间内 |
| `user_stated` | 必须指向事件，角色为用户且 `From` 为空（子会话回报不算），`Agent` 为空 |
| `user` 来源 | 不传证据；服务写入一条 `{user_stated, op:<OpID>}`，指向审计记录本身 |

证据时间取事件的 `Time`，或产物版本的 `CreatedAt`。

### 生效门槛

| 条件 | 结果 |
|---|---|
| `user` | 生效 |
| `extraction` 的 `withdraw` | 候选 |
| 至少一条 `user_stated`，且范围不扩大 | 生效 |
| 类别为 `experience`，至少一条 `verified`，范围不是 `global`，且范围不扩大 | 生效 |
| 其余 | 候选 |

范围不扩大：`create` 视为满足；`update` 时新范围必须与原范围相同，或原范围是 `global`。

### 旧材料拦截

只对 `companion`、`extraction` 生效。“用户变更”指 `actor` 为 `user` 或 `file` 的已提交操作。

| 情况 | 结果 |
|---|---|
| `Target` 最近一次用户变更晚于全部证据时间 | `stale_evidence` |
| 正文等于某条目被用户变更替换掉的旧版本，且全部证据早于那次变更 | `stale_evidence` |
| 正文等于已撤回条目的正文（含 `Target` 已撤回），且没有晚于撤回的 `user_stated` 证据 | `withdrawn_replay` |

条目量小，比较时遍历已提交操作并解码，不加索引。有晚于撤回的 `user_stated` 证据时，`update` 把已撤回条目恢复为 `active`；`create` 新建条目。

### 候选

迁移 `internal/store/migrations/0017_memory_candidates.sql`（实现时取当时 `main` 的下一个编号）：

```sql
CREATE TABLE memory_candidates (
	entry_id         TEXT PRIMARY KEY,
	action           TEXT NOT NULL,    -- create | update | withdraw
	target_id        TEXT NOT NULL,    -- create 时为空字符串
	expected_version INTEGER NOT NULL,
	actor            TEXT NOT NULL,
	reason           TEXT NOT NULL,
	state            TEXT NOT NULL,    -- open | accepted | rejected
	created_at       INTEGER NOT NULL,
	decided_at       INTEGER
) STRICT, WITHOUT ROWID;

CREATE INDEX memory_candidates_open ON memory_candidates (created_at) WHERE state = 'open';
```

这张表持久、不可重建，扫描和 `ReplaceMemoryIndex` 不碰它。`withdraw` 候选的正文写撤回理由。

| 操作 | 过程 |
|---|---|
| 确认 `create` | 候选条目改为 `active`，可调整范围；证据不变 |
| 确认 `update` | 目标条目用候选的正文、范围和证据写新版本；候选改为 `superseded`，`replaced_by` 指向目标。目标版本已变时返回 `store.ErrConflict`，候选保持打开 |
| 确认 `withdraw` | 目标条目改为 `withdrawn`；候选改为 `superseded`，`replaced_by` 指向目标 |
| 拒绝 | 候选改为 `withdrawn` |

确认和拒绝的 `actor` 都是 `user`。每一步提交的操作 ID 由 `opID` 加步骤号组成，中途崩溃后用同一 `opID` 重试能走完。

### 被拒的审计

`store.RecordMemoryRejection(ctx, op)` 写一行 `state = 'rejected'`，`reason` 为 `<code>: <说明>`，`new_version = 0`。`MemoryOps` 只返回 `committed`，不受影响。原因是 `credential` 时 `content`、`hash` 为空字符串，说明里也不引用正文。重试同一 `OpID` 时从这一行还原结果。

`memory_ops.state` 没有 CHECK 约束，不需要迁移。

### daemon 方法与 CLI

`proto/profile.go` 定义类型，方法都加入 `localOnly`：

| 方法 | 参数 | 返回 |
|---|---|---|
| `profile.list` | 类别、状态、范围、文本、上限 | 条目列表，默认只有 `active` |
| `profile.get` | ID | 条目和版本历史 |
| `profile.update` | ID、期望版本、正文、范围、操作 ID | 条目 |
| `profile.withdraw` | ID、期望版本、原因、操作 ID | 条目 |
| `profile.candidates` | 无 | 候选和各自的目标 |
| `profile.accept` | ID、范围（可选）、操作 ID | 条目 |
| `profile.reject` | ID、操作 ID | 条目 |

错误沿用现有映射：`ErrConflict` 对应冲突，`ErrInvalid` 对应错误请求，`ErrNotFound` 对应未找到。ID 只接受完整 ID。

```text
hoplet profile list [--category C] [--status S] [--scope KIND[:VALUE]] [--text T] [--all]
hoplet profile show ID
hoplet profile edit ID --body FILE|- [--scope KIND[:VALUE]]
hoplet profile withdraw ID [--reason R]
hoplet profile candidates
hoplet profile accept ID [--scope KIND[:VALUE]]
hoplet profile reject ID
```

都支持 `--json`。`edit` 与 `companion persona --set` 的用法一致，先取当前版本作为期望版本；`withdraw` 同样。操作 ID 由 CLI 随机生成。`--all` 列出所有状态。`--host` 不支持 `profile`。

## 任务

### 任务 1：存储支持

- [x] 迁移 `memory_candidates`
- [x] `internal/store/memory.go`：`MemoryOpRejected` 常量、`RecordMemoryRejection`、`AllCommittedMemoryOps`
- [x] `internal/store/memory_candidates.go`：`AddMemoryCandidate`、`MemoryCandidate`、`OpenMemoryCandidates`、`DecideMemoryCandidate`
- [x] `internal/store/events.go`：`EventBySeq`，未知会话或序号返回 `ErrNotFound`
- [x] 测试：从上一版迁移后数据不变；被拒记录不出现在 `MemoryOps`，同 ID 再次记录返回 `ErrExists`；候选状态只能从 `open` 变一次；`ReplaceMemoryIndex` 不动候选；`EventBySeq` 的三种结果
- [x] commit：`feat(store): record memory candidates and rejections`

### 任务 2：写入服务

- [x] `internal/memory/credential.go`、`write.go`、`evidence.go`、`replay.go`、`gate.go`
- [x] `Service` 增加配置项 `Sessions`（只读接口，取会话模式）；`Apply` 全程持有写锁，把 PR1 的 `Commit` 拆成加锁的外层和不加锁的内层
- [x] 测试（全部表驱动，一行对应 0016 表中的一条）：
  - 用户写入带期望版本，立即生效，证据为 `op:`
  - 伙伴：Code 会话被拒；别的会话的证据被拒；`user_stated` 指向助手消息、子会话回报、子代理记录时被拒；不存在的事件被拒
  - 提炼：区间外的证据被拒
  - 凭据：每种模式各一条被拒，审计无正文；普通提到“密码”两字的句子不误拦
  - 门槛：门槛表每行各一条，含范围扩大、`verified` 非 `experience`、`verified` 全局范围、`observed`、`inferred`、提炼撤回
  - 并发：用户和伙伴得到 `ErrConflict`；提炼转候选
  - 重试：`applied`、`candidate`、`rejected` 三种结果都不重复写入
  - 旧材料：拦截表每行各一条；晚于纠正的 `observed` 仍进候选；晚于撤回的 `user_stated` 恢复（`update`、`create` 各一条）；手改文件视同用户变更
  - 失败：rename 失败时 `Apply` 返回错误，不是 `applied`
- [x] commit：`feat(memory): validate writes from every source`

### 任务 3：候选

- [x] `internal/memory/candidate.go`：`Candidates`、`Accept`、`Reject`
- [x] 测试：三种动作的确认；调整范围；目标版本已变时冲突且候选仍打开；拒绝后同样内容被 `withdrawn_replay` 拦截；每一步之间用失败点崩溃后，同一 `opID` 重试能走完且不重复写入；已决定的候选不能再操作
- [x] commit：`feat(memory): accept and reject candidates`

### 任务 4：daemon 方法与 CLI

- [x] `proto/profile.go`、`proto/api.go`，`make gen`
- [x] `internal/daemon/methods.go`、`localOnly`；`internal/daemon/daemon.go` 传入 `Sessions`
- [x] `internal/client/methods.go`
- [x] `cmd/hoplet/profile.go`，`main.go` 加命令和用法
- [x] 测试：每个方法经 socket 跑通；经 Noise 连接调用被拒；版本冲突如实返回；CLI 表驱动覆盖七个命令的文本与 `--json` 输出、参数错误；`--host` 被拒；写入失败时 CLI 非零退出且不打印已保存
- [x] `make check`，把结果写入本计划的“结果”一节
- [x] commit：`feat(memory): add profile methods and CLI`

## 风险

| 风险 | 处理 |
|---|---|
| 凭据模式误拦普通句子 | 要求关键词后有分隔符和足够长的值，测试包含反例；用户本人写入同样受拦，错误里说明原因 |
| 正文比较漏掉改写过的旧结论 | 已知限制，靠 `update` 的目标时间拦截和 PR4 的模型判断补足 |
| 确认候选的两步之间崩溃 | 操作 ID 确定，重试走完；目标已提交时第一步按重试返回 |
| 迁移编号被并行工作占用 | 实现时取当时 `main` 的下一个编号 |

## 结果

`make check` 通过：`golangci-lint` 0 issues，`go tool mygo vet desktop` 通过，`go test -race ./...` 34 个包全部通过，生成代码无差异。

与计划的出入：

- 任务 2、3 合成一个 commit：`feat(memory): validate writes and decide candidates`。
- 产物版本的证据时间取 `UpdatedAt`：`CreatedAt` 是产物首次发布的时间。
- 提炼写入因版本冲突转候选时，记录目标的当前版本作为期望版本，否则这条候选永远无法确认。
- 旧材料拦截先判撤回规则，再判目标时间和被替换正文，返回更具体的代码。
- `profile.accept` 的范围对 `create`、`update` 都有效，对 `withdraw` 是无效参数。
- `profile.list` 增加 `All` 参数，供 `--all` 使用。
- 确认或拒绝的某一步失败后，用 `<操作 ID>.N` 重试这一步。

已知限制：确认候选崩溃在两步之间时，要用同一个操作 ID 重试才能走完；CLI 每次生成新的操作 ID，这种情况下用户可以拒绝这条候选。
