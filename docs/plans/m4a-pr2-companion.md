# M4a · PR2 伙伴与人格 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `9937267`（#35 合并后）的 `main`。

**目标：** Personal 有独立的伙伴实体；每个伙伴有自己的目录与 `persona.md`，人格在启动主会话时交给 harness。

**验收（M4a 总计划）：** 迁移前后会话与事件不变；新建伙伴得到目录与空人格；两家的启动参数和生成的文件与人格一致；改人格后新主会话生效。

**依据：** [M4a 总计划](m4a-overview.md)、[0010](../design/0010-personal-mode.md)、[0004](../design/0004-session-data-model.md)、[0020](../design/0020-hermes-adapter.md)、[0021](../design/0021-claude-adapter.md)、[0012](../design/0012-orchestration-api.md)。

## 维护者的裁决（2026-11-07）

1. **迁移不打断已有的原生会话。** Claude Code 的 `--resume` 按工作目录找原生会话。所以已经有原生会话 ID 的 Personal 会话，迁成主会话后仍在原来的会话目录里工作。还没有原生会话的、以及之后新建伙伴的主会话，都在伙伴目录里工作。
2. **新建 Personal 会话即建伙伴。** `sessions.create` 建没有父会话的 Personal 会话时，在同一事务里建伙伴，并把这个会话设为主会话。桌面和 CLI 现有的入口不改。PR3 的委派子会话带父会话，不建伙伴。
3. **名字就是主会话的标题。** 伙伴不另存名字，读的时候取主会话标题；改名沿用 `session set --title`。
4. **PR2 不提供换主会话。** 已有的 Hermes 主会话改人格后不保证生效，`companion persona` 改完时会说明这一点。换主会话以后另行讨论。

## 全局约束

- 不新增依赖、不新增持久化事件。
- 迁移只加表、加行，不改 `sessions` 与 `events` 里已有的数据。
- 测试不碰真实的 `~/.hoplet`、不启动真实 harness、不用 sleep 同步，全部 `-race`。
- 已有会话的 `Directory` 不动：桌面把它当项目目录（Finder、变更面板、分组），所以裁决 1 的旧目录记在伙伴行上，不写进 `Directory`。

## 设计细节

### 存储

| 项 | 规则 |
|---|---|
| 迁移 | `0012_companions.sql`：`companions(id TEXT PRIMARY KEY, main_session TEXT NOT NULL UNIQUE REFERENCES sessions(id), in_session_dir INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL) STRICT` |
| 迁入 | 每个 `mode = 'personal'` 的会话（含已归档的）插一行：`id = lower(hex(randomblob(8)))`，`created_at` 取会话的创建时间；`native_id != ''` 的 `in_session_dir = 1` |
| 新建 | `CreateSession` 增加可选参数：建伙伴时在同一事务里插行；ID 用与会话 ID 同样的 8 字节随机十六进制 |
| 查询 | `Companions(ctx)`、`Companion(ctx, id)`（支持唯一前缀，与会话一致）、`CompanionByMain(ctx, sessionID)`；返回值联表取主会话的标题、harness、归档时间 |

字段只存 ID、主会话、旧目录标记和创建时间。名字和 harness 读的时候从主会话取；主机由 daemon 返回时填自己的主机 ID。

### 协议

| 项 | 规则 |
|---|---|
| `proto.Companion` | `id`、`name`（主会话标题）、`host`、`harness`（`HarnessRef`）、`main`（主会话 ID）、`archived_at`（主会话归档时间） |
| 方法 | `companions.list`、`companions.get`、`companions.create`、`companions.persona`（读）、`companions.set_persona` |
| `companions.create` | 参数与 `sessions.create` 相同，只是不带 `mode`、`directory`、`isolation`；返回伙伴和主会话 |
| `companions.set_persona` | `{id, text}`；返回 `{applies: "next_launch" \| "new_main_session"}`：Claude Code 是前者，Hermes 的主会话已有原生会话时是后者 |
| 远程 | 与 `sessions.*` 相同的权限：已配对的设备可调 |

### 目录与人格

| 项 | 规则 |
|---|---|
| 伙伴目录 | `<home>/companions/<id>/`，0700；新建伙伴时建好，并写一个空的 `persona.md`（0600）；迁移来的伙伴在第一次用到时再建 |
| 读 | 文件不存在就当作空；不是 UTF-8 或超过 64 KiB 时启动失败，说明原因 |
| 写 | `set_persona` 写临时文件再改名，0600；超过 64 KiB 或不是 UTF-8 的拒绝；直接改文件也算数 |
| 主会话的工作目录 | `Directory` 非空用它；否则主会话的 `in_session_dir = 0` 用伙伴目录，为 1 用会话目录；不是主会话的 Personal 会话（PR3 的子会话）仍用会话目录 |
| 交给 harness | manager 在 Launch 主会话前读 `persona.md`，放进新的 `Spec.Persona`；不是主会话的为空 |

### harness 接口

| 项 | 规则 |
|---|---|
| `Spec.Persona` | 新增：人格全文；空即不传 |
| `Spec.Companion` | 新增：这是伙伴的主会话 |
| `Launch.WorkFiles` | 新增：`map[string][]byte`，文件名到内容；manager 在启动 shim 前以 0600 写进 `Spec.WorkDir`（先写临时文件再改名），写失败即启动失败 |

### Claude Code 适配

- `Spec.Persona` 非空时，参数加 `--append-system-prompt <人格>`；为空时参数与现在一字不差。
- 自检（`check.go`）不变。

### Hermes 适配

- 主会话（`Spec.Companion` 为真）Launch 时返回 `WorkFiles["HERMES.md"]`：第一行注明由 Hoplet 生成、直接修改会被覆盖、改人格请用 `hoplet companion persona`，空一行后接人格全文。人格为空时只写这一行，用来盖掉旧的人格。
- 不是主会话的（PR3 子会话）和自检不写 `HERMES.md`。

### CLI

```
hoplet companion list [--all]
hoplet companion show ID
hoplet companion new --harness KIND [--name NAME] [--permission MODE] [--model M] [--effort E]
hoplet companion persona ID [--set FILE|-]
```

- `list`：列 ID、名字、harness、主会话；默认不含主会话已归档的，`--all` 才列出。
- `new`：`--name` 作主会话标题；打印伙伴 ID、目录与主会话 ID。
- `persona`：不带 `--set` 打印人格；带 `--set` 时替换，并按 `applies` 说明什么时候生效。
- 支持 `--host`，与 `session` 命令一致。

## 任务

### 任务 1：协议

- [ ] `proto/companion.go`：`Companion`；`proto/api.go`：五个方法与它们的参数和结果
- [ ] `make gen` 重新生成 Swift
- [ ] 测试：新类型的 JSON 往返与字段名
- [ ] commit：`feat(proto): add companions`

### 任务 2：存储

- [ ] `internal/store/migrations/0012_companions.sql`
- [ ] `internal/store/companions.go`：查询与建行；`CreateSession` 的建伙伴参数
- [ ] 测试：从 0011 的库迁移后，会话与事件逐行不变；每个 Personal 会话恰好一个伙伴，Code 会话没有；有原生 ID 的 `in_session_dir = 1`；前缀查询与不存在；同一主会话不能有两个伙伴；建会话失败时伙伴行也没有
- [ ] commit：`feat(store): add companions`

### 任务 3：会话层与适配器

- [ ] `internal/harness/harness.go`：`Spec.Persona`、`Spec.Companion`、`Launch.WorkFiles`
- [ ] `internal/session/manager.go`：`Config` 加伙伴根目录；`Create` 对没有父会话的 Personal 建伙伴、建目录和空人格；按上表算工作目录；Launch 前读人格、写 `WorkFiles`；`Persona`、`SetPersona`
- [ ] `internal/harness/claude/claude.go`：`--append-system-prompt`
- [ ] `internal/harness/hermes/hermes.go`：`HERMES.md`
- [ ] 测试（manager，用假 harness）：新建 Personal 会话得到伙伴、目录和空 `persona.md`；主会话在伙伴目录里启动；`in_session_dir = 1` 的仍在会话目录；改人格后下一次 Launch 的 `Spec.Persona` 是新内容；超长或非 UTF-8 的人格启动失败并说明原因；`WorkFiles` 是 0600
- [ ] 测试（Claude）：带人格的参数按 golden；不带人格的参数不变
- [ ] 测试（Hermes）：`HERMES.md` 的内容按 golden，空人格只有注明那一行；不是主会话的不写
- [ ] commit：`feat(companion): give companions a directory and persona`

### 任务 4：daemon 与 CLI

- [ ] `internal/daemon/methods.go`：五个方法；`companions.get` 填 `host`
- [ ] `cmd/hoplet/companion.go`：四个子命令
- [ ] 测试（daemon）：五个方法的往返；`set_persona` 按 harness 返回 `applies`；远程设备可调
- [ ] 测试（CLI）：`new` → `persona --set -` → `persona` 读回一致；`list` 默认不含已归档的
- [ ] commit：`feat(cli): add companion commands`

### 任务 5：实测与文档

- [ ] 本机实测：Claude Code 与 Hermes 各新建一个伙伴，写一句可验证的人格（如“回答以‘好的，伙伴’开头”），确认模型照做；Claude Code 伙伴改人格、停掉再发，确认新人格生效
- [ ] 0010：名字取主会话标题；迁移来的有原生会话的主会话留在会话目录；“伙伴实体”“人格”两节从目标改为现状
- [ ] 0004：伙伴表；0012：五个方法；0020、0021：人格的传法从目标改为现状；0019 的基线
- [ ] 本计划勾选并记下实测结果
- [ ] `make check`
- [ ] commit：`docs: record companions and personas`

PR 标题：`feat(companion): add companions and personas`。

## 风险

| 风险 | 在哪发现 | 处理 |
|---|---|---|
| 人格太长，超过命令行参数的上限 | 任务 3 | 64 KiB 的上限远低于 macOS 与 Linux 的参数上限；实测再确认 |
| Hermes 的注入扫描丢掉了人格 | 任务 5 实测 | 按 0010，Hoplet 不另做检测；在计划里记下 |
| 迁移来的伙伴之后换 harness 或重建原生会话 | — | PR2 不提供；仍在会话目录里工作，等换主会话的设计再处理 |
