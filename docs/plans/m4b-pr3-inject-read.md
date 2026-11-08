# M4b PR3 启动摘要与读取工具 · 实施计划

**目标：** 新启动的会话拿到同一份按范围选出的有效记忆；工作 harness 经工具桥读取记忆，伙伴经同一服务写入；每个会话收到了哪些条目和版本可以核对。

**PR 标题：** `feat(memory): inject summaries and add read tools`

**依据：** [总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)“注入与迁移”、[0010](../design/0010-personal-mode.md)、[0020](../design/0020-hermes-adapter.md)、[0021](../design/0021-claude-adapter.md)。

**前置：** PR2 已合入（`a86052f`）。

## 待维护者确认的取舍

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| 1 | Hermes 的 Code 会话没有摘要入口 | 第一期不注入，注入记录标 `unsupported`，读取工具照常；实现时改 0016 表格和“待验证” | 已知入口只有工作目录的 `HERMES.md`，Code 的工作目录是用户仓库，写入会覆盖用户文件并弄脏工作区；`session.create` 不接受 `messages`，MCP `instructions` 不进提示词，`personality` 作用于整个配置档 |
| 2 | “转交”出来的 Code 会话算不算“委派的子会话” | 算 Code 会话，注入摘要；只有带父会话的 Personal 会话不注入 | 转交的会话是用户自己要继续审阅的长任务，约定同样要生效 |
| 3 | 仓库范围的值 | `origin` 规范化后的 `host/path`；没有 `origin` 时用 `<主机名>:<仓库根路径>` | 0016 要求跨机用稳定身份，路径注明主机 |
| 4 | 工具里写入的范围 | 只传类别（`global`、`companion`、`repo`、`host`），值由调用方会话决定 | 模型不能写到别的伙伴或仓库 |
| 5 | 读取范围 | `profile_list`、`profile_read` 只看调用方适用的范围，范围外按不存在处理 | 范围由会话决定，不靠提示词 |
| 6 | 需要一次真实 Hermes 运行 | 用 0.22.5 跑一次“人格 + 摘要”的 `HERMES.md`，确认没被注入扫描丢弃，结果写进 0020 | 被扫描命中时内容直接丢弃且不报错，摘要开头的“不覆盖指令”这类措辞可能命中，并连累人格 |

## 适用范围

| 会话 | 范围 | 启动摘要 |
|---|---|---|
| 伙伴主会话 | `global`、本伙伴、当前主机；有目录且在仓库内时加仓库 | 有（Claude、Hermes） |
| Personal 子会话 | 同根伙伴的范围 | 无，只用工具 |
| Code（含转交） | `global`、当前主机、仓库 | Claude 有；Hermes 见取舍 1 |

`Ephemeral` 运行不注入。续接（`spec.Resume != ""`）沿用启动时的上下文，不生成新摘要、不写注入记录；Hermes 主会话每次启动仍重写 `HERMES.md`，内容包含当时的摘要，但只有新会话会读。

## 摘要

新文件 `internal/memory/digest.go`：

```go
type Digest struct {
    Text    string
    Entries []DigestItem // 进正文的条目
    Indexed []DigestItem // 只进索引的条目
}
type DigestItem struct { ID string; Version int }

func (s *Service) Digest(ctx context.Context, scopes []Scope) (Digest, error)
func (s *Service) InScope(ctx context.Context, scopes []Scope, q store.MemoryQuery) ([]Entry, error)
```

| 规则 | 内容 |
|---|---|
| 选择 | 只取 `active`，范围必须与 `scopes` 之一完全相同 |
| 排序 | `convention`、`preference`、`fact`、`experience`；同类中 `repo` / `companion` 先于 `host` 先于 `global`；再按更新时间倒序 |
| 预算 | 正文 8 KiB（`const digestBudget`），放不下的条目进索引，继续尝试后面更短的；索引 4 KiB，再放不下的只统计条数 |
| 为空 | 没有条目时 `Text` 为空，不加任何内容 |
| 开头 | 写明是背景信息，不覆盖当前指令、项目规则和权限，不代表授权；与 harness 原生记忆冲突时以这里为准；查最新版本用 `profile_read` |
| 正文行 | `- [<id> v<版本> <范围类别>] <正文>` |
| 索引行 | `- <id> <类别> <标题>` |

`harness.Spec` 增加 `Profile string`。

| 适配器 | 改动 |
|---|---|
| Claude | `--append-system-prompt` 的值为人格和摘要，空行分隔，两者都空时不传；为 Code 也传 |
| Hermes | `personaContext(persona, profile)` 依次放说明行、人格、摘要；只在 `spec.Companion` 时写文件，Code 不写 |
| 能力 | `harness.Capabilities` 增加 `CodeDigest bool`：Claude 为 true，Hermes 为 false |

## 注入记录

迁移 `0018_memory_injections.sql`（实现时取当时 `main` 的下一个编号）：

```sql
CREATE TABLE memory_injections (
    id         INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    status     TEXT NOT NULL,   -- delivered | failed | unsupported
    entries    TEXT NOT NULL,   -- JSON: [{id, version, indexed}]
    detail     TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX memory_injections_session ON memory_injections(session_id, id);
```

`store.AddMemoryInjection`、`store.MemoryInjections(ctx, session)`。

| 情况 | 记录 | 会话 |
|---|---|---|
| 启动参数或文件已交给 harness | `delivered`，含条目和版本 | 照常 |
| 摘要生成失败 | `failed`，`detail` 写原因，`slog.Warn` | 照常启动，没有摘要 |
| Hermes Code | `unsupported` | 照常 |
| 没有条目 | 不写 | 照常 |

记录在进程启动成功后写；写记录失败只记日志，不影响会话。`delivered` 不代表模型遵守。

## 工具

新文件 `internal/tools/profile.go`，`tools.Source` 增加 `Profile() ProfileSource`：

```go
type ProfileSource interface {
    Scopes(ctx context.Context, caller proto.Session) ([]memory.Scope, error)
    List(ctx context.Context, scopes []memory.Scope, q store.MemoryQuery) ([]memory.Entry, error)
    Get(ctx context.Context, scopes []memory.Scope, id string) (memory.Entry, error)
    Apply(ctx context.Context, w memory.Write) (memory.Result, error)
}
```

| 工具 | 参数 | 行为 |
|---|---|---|
| `profile_list` | `category`、`scope`、`text`、`limit`（默认 50，上限 200） | 只返回 `active`，字段：ID、版本、类别、范围、标题 |
| `profile_read` | `id` | 返回最新生效版本的正文；非 `active` 或范围外返回 `no entry <id>` |
| `profile_write` | `action`、`id`、`expected_version`、`category`、`scope`、`body`、`evidence`、`reason` | `Actor = companion`，`Session = caller.ID` |

- `profile_write` 对 Code 也可见，调用返回 `profile_write is only available in Personal sessions`，走 `Failure`，不进审计。
- 操作 ID：`tool_` 加 `sha256(会话 ID + 规范化参数)` 的前 32 位十六进制，模型重试同一调用不会重复写入。
- 结果原样返回 `outcome`、条目版本、`code`、`reason`；工具描述写明只有 `applied` 才能告诉用户“已保存”。
- `store.ErrConflict`、`store.ErrInvalid` 转成 `Failure`，让模型重读后再试。

`internal/session/profile.go`：`func (m *Manager) profileScopes(ctx, sess) ([]memory.Scope, error)` 和 `repoScope(ctx, dir)`。git 失败或不在仓库内时没有仓库范围，不报错。

`session.Config` 增加 `Memory`（`Digest` 接口），daemon 传入 `s.memory`；为 nil 时不注入，已有测试不用改。

## 任务

### 任务 1：摘要

- [x] `internal/memory/digest.go`
- [x] 测试（表驱动）：类别与范围顺序；更新时间；刚好放满和超出 1 字节；大条目进索引、后面小条目仍进正文；索引超限的条数；`candidate`、`stale`、`superseded`、`withdrawn` 不进；范围外不进；没有条目时为空
- [x] commit：`feat(memory): select a startup digest by scope`

### 任务 2：启动注入与记录

- [x] 迁移和 `store` 方法；`Spec.Profile`；`Capabilities.CodeDigest`；Claude、Hermes 适配；`Manager.startSpec` 取摘要，启动成功后写记录；`profileScopes`、`repoScope`
- [x] 测试：两家启动参数和生成文件按录制向量核对；人格与摘要各自为空的四种组合；Code 走 Claude 有摘要，走 Hermes 没写 `HERMES.md`、记录为 `unsupported`；子会话无摘要、转交的 Code 有；续接不生成、不写记录；摘要失败（注入错误）时会话照常启动且记录 `failed`；`repoScope` 有 `origin`、无 `origin`、不在仓库内
- [x] commit：`feat(session): inject the profile digest at launch`

### 任务 3：工具

- [x] `internal/tools/profile.go`；`Source` 的 daemon 实现
- [x] 测试：Code 调用 `profile_write` 被拒且无审计；Personal 的写入生效、转候选、被拒三种结果原样返回；范围外读取按不存在；非生效条目不可读不可列；重试同一调用不重复写入；版本冲突转 `Failure`；范围值不能由调用方指定
- [x] commit：`feat(tools): add profile read and write tools`

### 任务 4：端到端与设计同步

- [x] `internal/daemon`：假 harness 跑通“伙伴写入 → 新建另一 harness 的会话 → 启动参数里有这条 → `profile_read` 取到”，并核对注入记录的条目和版本
- [x] 按取舍 1、3 更新 0016；真实 Hermes 运行（取舍 6）的结果写进 0020
- [x] `make check`，把真实结果写入本计划“结果”一节
- [x] commit：`docs(memory): record injection decisions and results`

## 风险

| 风险 | 处理 |
|---|---|
| Hermes 扫描丢弃 `HERMES.md` 内容 | 取舍 6 实测；命中就调整开头措辞，不绕过扫描 |
| 记忆正文含指令性文字进系统提示 | 开头声明不代表授权；提炼和写入的证据校验限制来源；残余风险在 0016 已说明 |
| 条目很多时索引过长 | 索引 4 KiB 上限，其余只统计条数，用 `profile_list` 查 |
| 伙伴用长摘要后命令行参数过长 | 人格 64 KiB 加摘要 12 KiB，低于 macOS 和 Linux 的单参数上限 |

## 结果

与计划不同的地方：

| 计划 | 实际 | 原因 |
|---|---|---|
| `memory_injections.created_at` 用 TEXT | INTEGER 毫秒 | 与库里其他时间列一致 |
| `Capabilities.CodeDigest` | 可选接口 `harness.CodeDigester` | 这是 daemon 内部的事，不需要改协议 |
| 续接不生成摘要 | 续接也生成但不写记录 | Hermes 每次启动都重写 `HERMES.md`，不带摘要会把已有内容抹掉；一条路径更简单 |
| `Manager.profileScopes`、`repoScope` | 导出的 `ProfileScopes`，仓库身份由 `Git.RepoIdentity` 提供 | 工具也要用；git 细节留在 `workspace` |

补充：`profile_write` 的修改和撤回先按调用方范围检查目标，范围外返回 `no entry <id>`，不能靠 ID 改别的伙伴的条目。

Hermes 0.22.5 实测：说明、人格加摘要的 `HERMES.md`，模型按人格要求开头并答出摘要里的事实，注入扫描没有丢弃。
