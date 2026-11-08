# M4b · PR1 条目存储与提交 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `53a2b3c`（#51 合并后）的 `main`。

**目标：** daemon 能把记忆条目可靠地存成 Markdown：每次写入都有审计，崩溃后能判定是否已提交并恢复；索引和 `PROFILE.md` 可重建；手改的文件能被发现。本 PR 只有存储层，不含写入规则、CLI、工具和协议。

**验收（M4b 总计划）：** 每个失败点崩溃后状态正确且不重复写入；期望版本不符被拒；重建索引不动审计；手改合法的记一次用户编辑，不合法的不进索引且文件不动；未知字段被拒。

**依据：** [M4b 总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)。

## 维护者的裁决（2026-11-08）

1. **frontmatter 用自己的严格解析器。** 不引入 YAML 库，只认 0016 定义的固定字段。
2. **检索用子串匹配。** 实测（SQLite 3.54.1）：FTS5 的 `unicode61` 和 `trigram` 都查不到两字中文词。条目量小，索引表上做 `LIKE` 即可，0016 随本计划修正。
3. **提炼运行和注入记录的表不在本 PR。** 它们的形状由 PR4、PR3 决定，各自带迁移。

## 全局约束

- 不新增依赖。
- 迁移只加表，不改已有数据。
- 测试用 `t.TempDir()`，不碰真实的 `~/.hoplet`，不用 sleep 同步，全部 `-race`。
- 崩溃用注入失败点模拟：失败后丢弃 `Service`，在同一目录重新打开。

## 设计细节

### 文件

路径：`<home>/profile/entries/<category>/<id>.md`，权限 `0600`，目录 `0700`。`home.Profile(home)` 返回 `<home>/profile`。

ID：`mem_` 加 32 位小写十六进制（`crypto/rand`）。类别和 ID 都按白名单校验后才拼路径；文件所在目录必须与 `category` 一致，文件名必须与 `id` 一致。

### frontmatter 语法

文件以 `---\n` 开始，下一行单独的 `---` 结束，其后是正文。字段固定，每个恰好一次，顺序不限，编码时按 0016 示例的顺序输出。

| 字段 | 形式 | 规则 |
|---|---|---|
| `id` | 标量 | 符合 ID 格式 |
| `version` | 整数 | ≥ 1 |
| `category` | 标量 | `fact`、`preference`、`convention`、`experience` |
| `status` | 标量 | `candidate`、`active`、`stale`、`superseded`、`withdrawn`，行尾 `#` 注释允许 |
| `scope` | `{ kind: K, value: V }` | `kind` 为 `global` 时 `value` 为 `""`，其余非空 |
| `evidence` | 下一行起每行 `  - { kind: K, ref: R }`；无证据写 `evidence: []` | `kind` 取 0016 的四种，`ref` 非空 |
| `valid_until` | RFC 3339 或 `null` | UTC |
| `replaced_by` | ID 或 `null` | `status` 为 `superseded` 时必填，其余必须为 `null` |
| `updated_at` | RFC 3339 | UTC |

标量是 `[A-Za-z0-9_:./@-]+`，其他内容写成双引号字符串，用 JSON 的转义规则。未知字段、重复字段、缺字段、多余的缩进都是错误。正文不超过 16 KiB，去掉首尾空白后不能为空。`Decode(Encode(e)) == e`。

### 存储（`internal/store/memory.go`）

迁移 `0016_memory.sql`：

```sql
-- memory_ops is the audit log: one row per attempted write, holding the
-- whole new file, so it also holds every old version. Not derivable.
CREATE TABLE memory_ops (
    id               TEXT PRIMARY KEY,
    entry_id         TEXT NOT NULL,
    expected_version INTEGER NOT NULL,   -- 0 creates the entry
    new_version      INTEGER NOT NULL,
    actor            TEXT NOT NULL,      -- user | companion | extraction | file
    reason           TEXT NOT NULL,
    content          TEXT NOT NULL,
    hash             TEXT NOT NULL,      -- SHA-256 of content
    state            TEXT NOT NULL,      -- pending | committed | failed
    created_at       INTEGER NOT NULL,
    finished_at      INTEGER
) STRICT;
CREATE UNIQUE INDEX memory_ops_committed ON memory_ops (entry_id, new_version) WHERE state = 'committed';
CREATE INDEX memory_ops_pending ON memory_ops (state) WHERE state = 'pending';

-- memory_index is derived from the Markdown files and can be rebuilt.
CREATE TABLE memory_index (
    id          TEXT PRIMARY KEY,
    version     INTEGER NOT NULL,
    category    TEXT NOT NULL,
    status      TEXT NOT NULL,
    scope_kind  TEXT NOT NULL,
    scope_value TEXT NOT NULL,
    title       TEXT NOT NULL,   -- first body line, at most 80 runes
    body        TEXT NOT NULL,
    hash        TEXT NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- memory_problems lists files the last scan could not accept. Derived.
CREATE TABLE memory_problems (
    path TEXT PRIMARY KEY,
    err  TEXT NOT NULL
) STRICT, WITHOUT ROWID;
```

| 方法 | 规则 |
|---|---|
| `BeginMemoryOp(ctx, op)` | 持久事务写 `pending` 行；同 ID 已存在且 `hash` 相同返回已有行和 `ErrExists`，`hash` 不同返回 `ErrConflict` |
| `FinishMemoryOp(ctx, id, row)` | 持久事务内标记 `committed` 并写入索引行 |
| `FailMemoryOp(ctx, id)` | 标记 `failed` |
| `PendingMemoryOps(ctx)` | 按 `created_at` 升序 |
| `MemoryOps(ctx, entryID)` | 该条目的 `committed` 行，版本降序 |
| `MemoryIndexEntry(ctx, id)`、`SearchMemoryIndex(ctx, q)` | 走读连接；`q` 有 `Category`、`Status`、`ScopeKind`、`ScopeValue`、`Text`、`Limit`；`Text` 对 `title`、`body` 做 `LIKE ... ESCAPE`，转义 `%` `_` `\`；按 `updated_at` 降序 |
| `ReplaceMemoryIndex(ctx, rows, problems)` | 一个事务内替换索引和问题两张表，不碰 `memory_ops` |

### 服务（`internal/memory`）

```go
type Config struct {
    Root  string          // <home>/profile
    Store *store.Store
    Log   *slog.Logger
}

type Op struct {
    ID              string
    Entry           Entry // Version and UpdatedAt are set by Commit
    ExpectedVersion int   // 0 creates the entry
    Actor           Actor
    Reason          string
}

func Open(ctx context.Context, cfg Config) (*Service, error) // recovers, then scans
func (s *Service) Commit(ctx context.Context, op Op) (Entry, error)
func (s *Service) Get(ctx context.Context, id string) (Entry, error)
func (s *Service) Search(ctx context.Context, q store.MemoryQuery) ([]Entry, error)
func (s *Service) History(ctx context.Context, id string) ([]Version, error)
func (s *Service) Problems(ctx context.Context) ([]store.MemoryProblem, error)
func (s *Service) Scan(ctx context.Context) error
```

`Commit` 用一把 mutex 串行，daemon 是唯一写入方。步骤：

1. 校验条目；读索引得当前版本，不等于 `ExpectedVersion` 返回 `store.ErrConflict`。
2. 设 `Version = ExpectedVersion + 1`，编码，`BeginMemoryOp`。同 ID 重试：`committed` 的直接返回当时的结果，`failed` 的返回错误。
3. 同目录临时文件 `.tmp-*`，写入、`Sync`、rename、`Sync` 目录。失败时 `FailMemoryOp` 并返回错误。
4. `FinishMemoryOp`，然后写 `PROFILE.md`。`PROFILE.md` 写失败只记日志，提交已成立，下次提交或扫描重新生成。

失败点：`type hook func(point string) error`，不导出的字段，点位为 `after-begin`、`after-temp`、`after-rename`、`after-finish`。返回错误时立即中止，不做清理，以模拟崩溃。

### 恢复（`Open` 第一步）

删除 `entries/` 下的 `.tmp-*`，然后逐条处理 `pending`：

| 磁盘文件 | 处理 |
|---|---|
| `hash` 等于操作的 `hash` | `FinishMemoryOp`，补完提交 |
| 其他（旧版本、不存在、被改过） | `FailMemoryOp`，后续扫描按实际内容处理 |

### 扫描（`Open` 第二步，以及 `Scan`）

没有文件监听，扫描只在启动和显式调用时发生。

| 情况 | 处理 |
|---|---|
| 哈希与索引相同 | 不变 |
| 合法且内容变了，或索引里没有 | 走 `Commit`，`Actor = file`，`ExpectedVersion` 为索引版本，版本加一后写回；操作 ID 由“条目 ID + 文件哈希”派生 |
| 不合法 | 不进索引，已有索引行移除，记入 `memory_problems`，文件不动 |
| 索引有、文件不在 | 移除索引行，记入 `memory_problems`，审计保留 |
| 路径和文件内 `category` / `id` 不一致 | 按不合法处理 |

`Scan` 结束时重新生成 `PROFILE.md`，只列 `active` 条目，按类别分组，每行 ID 和标题，开头注明由程序生成。索引全部丢失时，`Scan` 从文件重建，版本号取文件中的值，这种情况不记编辑：判断依据是 `memory_ops` 中已有相同 `entry_id`、`hash` 的 `committed` 行。

## 任务

### 任务 1：条目编解码

- [ ] `internal/memory/entry.go`：`Entry`、`Category`、`Status`、`Scope`、`Evidence`、`Actor` 及校验；`internal/memory/frontmatter.go`：`Encode`、`Decode`
- [ ] 测试（表驱动）：往返相等；含中文、冒号、引号、换行的 `value` 与 `ref`；上表每种错误各一例；正文上限；`superseded` 与 `replaced_by` 的约束；`FuzzDecode`，种子含合法文件，只断言不 panic 且成功时往返相等
- [ ] commit：`feat(memory): encode entries as Markdown with frontmatter`

### 任务 2：存储

- [ ] `internal/store/migrations/0016_memory.sql`；`internal/store/memory.go`
- [ ] 测试：从 0015 迁移后会话与事件不变；同 ID 重复 begin 的两种结果；同条目同版本第二次 `committed` 被唯一索引拒绝；`ReplaceMemoryIndex` 不动 `memory_ops`；`LIKE` 中 `%`、`_`、`\` 按字面匹配；两字中文词能查到；过滤条件组合
- [ ] commit：`feat(store): record memory operations and index`

### 任务 3：提交与恢复

- [ ] `internal/home`：`Profile`；`internal/memory/service.go`：`Open`、`Commit`、`Get`、`Search`、`History`、`Problems`；`internal/memory/recover.go`
- [ ] 测试：新建和更新后文件、审计、索引一致；`ExpectedVersion` 过期被拒且文件不变；同 ID 重试不产生新版本；4 个失败点各崩溃一次，重开后：`after-begin` 和 `after-temp` 为旧版本且操作 `failed`，临时文件被清除，`after-rename` 和 `after-finish` 为新版本且操作 `committed`；重开后用同一操作 ID 重试，结果一致且不重复写入；rename 失败时返回错误且不是已提交；`-race` 下并发提交同一条目只有一个成功；`History` 返回旧版本内容
- [ ] commit：`feat(memory): commit entries with crash recovery`

### 任务 4：扫描与重建

- [ ] `internal/memory/scan.go`、`internal/memory/profile.go`
- [ ] 测试：手改正文后版本加一、`actor = file`、文件版本号被写回；再次扫描无新操作；不合法文件不进索引、不被改动、出现在 `Problems`，修好后恢复；已索引条目改坏后从检索消失；删除文件后的处理；路径与内容不一致；清空索引后 `Scan` 恢复且不产生新操作；`PROFILE.md` 只含 `active`；`PROFILE.md` 写失败时 `Commit` 仍成功且下次提交重新生成
- [ ] commit：`feat(memory): scan entry files and rebuild the index`

### 任务 5：接入 daemon

- [ ] `internal/daemon/daemon.go`：在 `arts.Clean` 之后、监听之前 `memory.Open`，服务放到 `server`；此 PR 不暴露方法
- [ ] 测试：daemon 启动时恢复一个 `pending` 操作（预置数据）；`profile/` 不存在时启动会创建
- [ ] `make check`，把结果写入本计划的“结果”一节，更新 0016 的状态
- [ ] commit：`feat(daemon): open the memory service at startup`

## 风险

| 风险 | 处理 |
|---|---|
| 用户用编辑器保存时恰好扫描，读到半截文件 | 扫描只在启动和显式调用时发生；读到不合法内容也只是记问题，下次扫描恢复 |
| 迁移编号被并行工作占用 | 实现时取当时 `main` 的下一个编号 |
| `after-rename` 崩溃后恢复前用户改了文件 | 恢复按哈希判定为失败，扫描再按用户编辑处理，不丢用户内容 |
