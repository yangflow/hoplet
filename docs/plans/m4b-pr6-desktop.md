# M4b PR6 桌面成长记忆 · 实施计划

**目标：** 补齐条目关联、使用记录和记忆设置，再把审定的成长记忆页、聊天内一行、详情记忆块和成长图做进桌面端。

**依据：** [总计划](m4b-overview.md)、[0016](../design/0016-growth-memory.md)“结构”“界面”“注入与迁移”、[0014](../design/0014-desktop-ui.md)、[0024](../design/0024-desktop-layout.md)“成长记忆”，设计系统的 `PMemory`、`MMemory*`、`CMemoryRow`、`CEventLine`、`MDetailsCard`。

**前置：** PR1–5 已合入；画板已审定，并已同步进设计系统、0014 和 0024。

## 拆分

总计划里 PR6 只有桌面视图。0016 后来加了关联、使用记录和设置，后端要先落地；一个 PR 放不下，拆三个，按顺序合入。

| PR | 标题 | 内容 | 验收 |
|---|---|---|---|
| 6a | `feat(memory): add links, usage and settings` | 关联、使用记录、设置表及其生效逻辑、协议、CLI 不变 | 0016 验收表“设置生效”“关联”用假 harness 逐条有测试 |
| 6b | `feat(desktop): add growth memory views` | 设置页壳、列表、详情、候选、编辑、记忆设置、聊天内一行、详情记忆块 | 与审定画板一致（浅色、深色）；`ui.Tester` 覆盖编辑、撤回、确认、拒绝、移除关联、开关设置 |
| 6c | `feat(desktop): draw the growth graph` | 成长图、时间轴回放、动效 | 布局和命中是纯函数测试；减少动态效果时直接是最终状态；与画板一致 |

## 取舍（2026-11-09 维护者已确认，全部按建议）

| # | 问题 | 建议 | 理由 |
|---|---|---|---|
| 1 | 拆成 6a、6b、6c | 拆 | 后端可独立测试和审阅；成长图风险最高，晚一步不挡列表和候选的使用。代价：6b 合入后、6c 合入前，页面暂时没有图 |
| 2 | 无向关联存在哪 | 只写在建立关联的那一条的文件里，读取时取双向并集 | 一次操作只改一个文件，沿用现有的原子提交；存两边要处理两个文件的崩溃恢复。条目规模小，读取时遍历够用，不建派生表 |
| 3 | 移除关联不升版本，审计怎么记 | `memory_ops` 加 `kind`（`content` / `links`）；`links` 操作版本不变，走同一条意图 → 写文件 → 标记提交；版本历史只列 `content` | 0016 要求入审计且不升内容版本，又要保持崩溃可恢复。唯一索引相应改为只约束 `content` |
| 4 | “用过几次”“本次用到”算什么 | 完整进入摘要、或被 `profile_read` 读过的算；只在索引里出现一行标题、或只被 `profile_list` 列出的不算 | 只看到标题谈不上用过。旧的注入记录用迁移补进使用表 |
| 5 | 打开“包含 Code 会话”后，之前的 Code 记录算不算 | 不算。关闭期间扫描时把 Code 会话的进度推到最新 | 用户是从打开那一刻开始同意的，不应把存量记录一次性发给模型并占满每日预算。代价：打开前的 Code 经验不会被提炼 |
| 6 | 指定 harness 时用哪个模型 | 该 harness 探测结果里标记为默认的模型；没有默认模型就不建运行，状态里说明 | 0016 写“用它的默认模型”；不猜模型 |
| 7 | 关闭“使用”时，运行中的会话 | 工具列表和调用都按当前设置；摘要只影响之后启动的会话 | 运行中的上下文收不回来，0016 也写明对新会话生效；工具顺带立刻停用，不额外处理 |
| 8 | 详情记忆块没有内容时 | 没有用到任何条目（含 Hermes Code 的 `unsupported`）就整块不显示 | 画板只有“有条目”和“注入失败”两种状态，界面没设计的不做 |
| 9 | 聊天内一行取不到条目时 | 这一行不显示 | PR5 已定：事件没有正文，取不到就没有可写的内容；远程连接和 iOS 同样如此 |
| 10 | 成长图里的“新保存”动效 | 只在本页确认候选、产生新节点时播放；其余靠进入页面的整图动画 | 设计系统写明打开页面时取一次数据、不做推送，因此页面开着时后台新增的条目下次进入才出现 |
| 11 | 设置页范围 | 只做画板画的：侧栏变成“返回 + 成长记忆”，底部加设置按钮 | 现在代码里没有设置页，其他设置项没有设计。不加快捷键，画板没有 |

## 协议（6a）

`proto/profile.go`，加完 `make gen`：

```go
type ProfileEntry struct {
	// existing fields...
	Links []string `json:"links,omitempty"` // both directions
	Uses  int      `json:"uses,omitempty"`  // distinct sessions
}

type ProfileVersion struct {
	// existing fields...
	Body     string            `json:"body"`
	Evidence []ProfileEvidence `json:"evidence"`
}

// ProfileSettings are the memory settings. An empty ExtractionHarness
// follows each session's own harness.
type ProfileSettings struct {
	UsePersonal       bool   `json:"use_personal"`
	UseCode           bool   `json:"use_code"`
	Extraction        bool   `json:"extraction"`
	ExtractionHarness string `json:"extraction_harness,omitempty"`
	IncludeCode       bool   `json:"include_code"`
	AutoSave          bool   `json:"auto_save"`
}

type ProfileUnlinkParams struct {
	ID    string `json:"id"`
	Other string `json:"other"`
	OpID  string `json:"op_id"`
}

type ProfileUsageParams struct {
	Session SessionID `json:"session"`
}

// ProfileUsage is what one session used. Hidden means no block is shown;
// Failed means its digest could not be delivered.
type ProfileUsage struct {
	Hidden  bool           `json:"hidden,omitempty"`
	Failed  bool           `json:"failed,omitempty"`
	Count   int            `json:"count"`
	Entries []ProfileEntry `json:"entries,omitempty"` // at most 3, narrowest scope first
}
```

新方法：`profile.settings`、`profile.settings.set`（整体替换；指定不支持的 harness 返回 `ErrInvalid`）、`profile.unlink`、`profile.usage`，全部加进 `localOnly`。`profile.extraction.set` 保留，CLI 继续用，读写新表的同一字段。

6c 再加 `profile.graph`，返回全部节点（ID、标题、范围、状态、版本、用过几次、各版本时间）和关联边；它只读，不暴露正文。

## 存储（6a）

迁移 `0021_memory_links_settings.sql`：

```sql
ALTER TABLE memory_ops ADD COLUMN kind TEXT NOT NULL DEFAULT 'content';
DROP INDEX memory_ops_committed;
CREATE UNIQUE INDEX memory_ops_committed ON memory_ops (entry_id, new_version)
	WHERE state = 'committed' AND kind = 'content';

CREATE TABLE memory_settings (
	id                 INTEGER PRIMARY KEY CHECK (id = 1),
	use_personal       INTEGER NOT NULL,
	use_code           INTEGER NOT NULL,
	extraction         INTEGER NOT NULL,
	extraction_harness TEXT NOT NULL,
	include_code       INTEGER NOT NULL,
	auto_save          INTEGER NOT NULL
) STRICT;
INSERT INTO memory_settings
	SELECT 1, 1, 1, enabled, '', 0, 1 FROM memory_extraction_state;
DROP TABLE memory_extraction_state;

CREATE TABLE memory_uses (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	entry_id   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	way        TEXT NOT NULL,    -- injected | read
	created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX memory_uses_entry ON memory_uses (entry_id, session_id);
CREATE INDEX memory_uses_session ON memory_uses (session_id, id);
INSERT INTO memory_uses (session_id, entry_id, version, way, created_at)
	SELECT i.session_id, json_extract(e.value, '$.id'), json_extract(e.value, '$.version'), 'injected', i.created_at
	FROM memory_injections i, json_each(i.entries) e
	WHERE i.status = 'delivered' AND NOT coalesce(json_extract(e.value, '$.indexed'), 0);
```

`links`、`unlinked` 为空时不写进 frontmatter，现有条目的文件和哈希不变。

## 6a 步骤

### 1. 设置表

**文件：** `internal/store/migrations/0021_memory_links_settings.sql`、`internal/store/memory_settings.go`（新）及测试、`internal/store/memory_extractions.go`、`proto/profile.go`、`proto/api.go`、`internal/daemon/profile.go`、`internal/daemon/methods.go`。

`MemoryExtractionEnabled`、`SetMemoryExtractionEnabled` 改读写 `memory_settings`，调用方不变。新增 `MemorySettings`、`SetMemorySettings`。

测试：迁移保留原开关值；默认值与 0016 的表一致；`profile.settings.set` 拒绝不支持的 harness；远程调用四个新方法被拒。

Commit：`feat(memory): store the memory settings`

### 2. 设置生效

**文件：** `internal/session/profile.go`、`internal/tools/profile.go`、`internal/tools/tools.go`、`internal/daemon/methods.go`、`internal/memory/write.go`、`internal/memory/extract/worker.go` 及各自测试。

- `addDigest` 开头：该模式关闭使用时返回 nil，不注入、不记录。读设置失败按开启处理并记日志，和摘要失败时一样不阻止启动。
- `ProfileSource` 增加 `Usable(ctx, mode) bool`。`visible` 现在没有 `ctx`，三个 `profile_*` 工具的 `visible` 改用 `context.Background()` 调它；调用入口已按 `visible` 拦截，实现时加测试确认。
- `Write` 增加 `Propose bool`：为 true 时创建和更新走 `propose`，不进 `applyNow`。worker 在提炼提交时按“自动保存”设置。
- `scan`（`worker.go:191`）：Code 会话在“包含 Code 会话”关闭时，`AdvanceMemoryExtraction` 到 `s.LastSeq` 后跳过（取舍 5）。
- `plan` 和 `scan` 用同一函数 `harnessFor(ctx, s, settings)` 决定执行方和模型；指定时不要求 `s.Model` 非空，`Count` 和预算按实际执行方。

测试（表驱动）：两个模式各关闭使用，新会话无摘要、无工具、无注入记录；关闭自动保存，`user_stated` 的提炼操作成为候选，且伙伴的明确写入仍生效；Code 默认不建运行，进度前移，打开后只有之后的事件进入；指定 harness 后运行记录为该 harness 和默认模型；无默认模型时不建运行。

Commit：`feat(memory): apply the memory settings`

### 3. 条目关联

**文件：** `internal/memory/entry.go`、`frontmatter.go`（及测试）、`service.go`、`write.go`、`candidate.go`、`scan.go`、新文件 `links.go`、`internal/memory/extract/output.go`、`prompt.go`、`worker.go`、`internal/store/memory.go`。

- `Entry` 增加 `Links`、`Unlinked []string`，frontmatter 严格解析 ID 列表，不认识的格式按不合法处理。
- `Op` 增加 `Kind`；`Kind == links` 时 `write` 不改版本和 `UpdatedAt`；`History` 只返回 `content`。崩溃恢复、重复 OpID、扫描哈希沿用原路径。
- `extract.Op` 增加可选 `links`（只允许 create、update），已存的旧回复照常解析。`input` 同时返回实际展示给模型的条目 ID；`writeOf` 丢弃不在其中的、双方 `unlinked` 里有的，每条最多 5 个，丢弃记警告，操作照常提交。
- 候选接受时，修改类把候选的关联并入目标条目。
- `Service.Unlink(ctx, id, other, opID)`：在持有这条关联的文件里移到 `unlinked`；两边都持有时各走一次，操作 ID 分别加后缀。
- `Service.Links(entries)` 算双向并集。

测试：frontmatter 往返，无关联时字节不变；移除后版本不变、审计有一行、历史没有新版本；每个失败点崩溃后恢复正确；重复 OpID 不重复写；移除后提炼从任一方向提议都不重建；幻觉 ID 被丢弃而操作仍提交；旧回复解析通过。

Commit：`feat(memory): link related entries`

### 4. 使用记录

**文件：** `internal/store/memory_injections.go`、新文件 `internal/store/memory_uses.go` 及测试、`internal/tools/profile.go`、`internal/daemon/profile.go`。

`AddMemoryInjection` 在同一事务里写 `memory_uses`。`profile_read` 成功后写 `read`，失败只记警告，不影响读取结果。`profile.list`、`profile.get` 填 `Uses`、`Links`；`profile.get` 的历史填 `Body`、`Evidence`，由 `Decode(Content)` 得出。`profile.usage` 按取舍 4、8 计算：关闭使用 → `Hidden`；最近一次注入失败 → `Failed`；总数按不同条目计，列表去掉已撤回的条目，最多 3 条，范围由近到远。

测试：注入和读取在同一会话算一次；只在索引里的不算；迁移补入旧记录；失败和隐藏状态；读取记录失败时工具仍返回内容。

Commit：`feat(memory): record which entries sessions use`

### 5. 文档

`docs/plans/m4b-overview.md`（PR6 改成三行）、本文“结果”。

Commit：`docs(memory): record the settings and links`

## 6b 步骤

新文件放在 `desktop/internal/shell/`，一个文件管一块；`Host` 增加 `Profile*` 方法，全部不阻塞，结果经 `done` 回到 UI 线程，实现在 `desktop/main.go`。

### 1. 设置页壳

**文件：** `app.go`（`pageKind` 增加 `pageSettings`，`page` 增加 `entry string`）、`sidebar.go`（底部设置按钮；在设置页时侧栏换成“返回”和“成长记忆”，待确认数量作徽标）、`content.go`、`memory.go`（新）、`desktop/internal/icons`（从 `lucide-static` 1.66.0 原样复制缺少的 `settings`）。

`OpenMemory(entry string)` 进入页面，记入前进后退。每次进入拉取 `profile.list`（`All`）、`profile.candidates`、`profile.extraction`、`profile.settings`，不订阅。

测试：进入、返回、前进后退；徽标数字；读取中、读取失败、空状态各一行字。

Commit：`feat(desktop): add the settings page`

### 2. 列表与详情

**文件：** `memory.go`、`memorydetail.go`（新）、`memoryedit.go`（新）及测试。

按 `MMemoryList`、`CMemoryRow`、`MMemoryDetail`、`MMemorySheet`、`MMemoryScopeMenu` 实现：行是 `ui.Row`，展开用 `Collapse`，撤回确认和范围菜单用 `ui.PopoverBase`，编辑用 `ui.DialogBase` + `ui.TextArea`。写操作的 `OpID` 在点击时生成一次，失败重试沿用；版本冲突时提示并重新拉取。

测试（`ui.Tester`）：分组和待确认置顶；不超过 5 条时单列；编辑保存带期望版本；撤回要确认；移除关联；三类候选的确认、拒绝和改范围，提议撤回时按钮写“撤回”且无范围下拉；从聊天进入已撤回条目只显示一行。

Commit：`feat(desktop): list and edit memory entries`

### 3. 记忆设置

**文件：** `memorysettings.go`（新）及测试。

按 `MMemoryExtract`：`ui.PopoverBase` + `ui.Switch`，提炼使用的菜单嵌套 `ui.PopoverBase`，不支持的 harness 置灰并写“不支持”；有失败时后台提炼一行和标题栏按钮都显示错误状态点。开关先改本地，失败时还原并提示。

Commit：`feat(desktop): add the memory settings`

### 4. 聊天内一行与详情记忆块

**文件：** `desktop/internal/convo/convo.go`（`MemorySaved` 转成 `Event{Memory, Version}`）及测试、`delegation.go`（`eventRow`、`openEvent`）、`content.go` 里详情的“会话”块之后。

`eventRow` 第一次遇到某条目时取 `profile.get` 并按 ID 缓存，图标 `brain`、动词“记住了”，没有版本号；已撤回写“已撤回”、不可点；取不到不显示。本页撤回后更新缓存。详情每次打开取一次 `profile.usage`，`Hidden` 不画，`Failed` 显示状态点和“本次没有用上记忆”。

测试：两种模式都有这一行；撤回后的样子；点击进入设置页并展开该条；详情三种状态。

Commit：`feat(desktop): show memory in chats and details`

## 6c 步骤

### 1. 布局与命中

**文件：** `desktop/internal/shell/memorygraph_layout.go`（新）及测试。

纯函数 `layoutGraph(nodes, edges, width, height) graphLayout`：中心“你”，范围枢纽均匀排一圈，条目按 ID 排序后围着自己的枢纽，同样的输入得到同样的位置，不做力导向。`hitNode(layout, x, y)` 做命中；`frameAt(layout, t)` 给出回放进度 `t` 时可见的节点和线。

测试：确定性；节点不出画布；半径随版本 1、2、3 及以上；命中优先最近节点；`t` 为 0 和 1 的边界。

Commit：`feat(desktop): lay out the growth graph`

### 2. 绘制与交互

**文件：** `memorygraph.go`（新）、`memory.go`、`proto` 与 daemon 的 `profile.graph` 及测试。

`Draw` + `Painter` / `Path` 绘制，全部用现有 token；`Pressed` + `PointerPosition` 命中，选中时高亮相邻节点并在列表展开该条，再点取消；范围筛选只改视图。入场和回放用元素的 `Animate*` 驱动一个 0→1 的相位，曲线取 `motion` token，`c.Preferences().ReduceMotion` 时直接到 1。超过 5 条才显示。

测试：不超过 5 条没有图；点节点展开；筛选只显示该组；减少动态效果时相位为 1。实机对照画板的全图、选中、筛选、回放四种状态，浅色和深色各一次。

Commit：`feat(desktop): draw the growth graph`

## 验证

每个 PR：

```bash
make gen
make check
```

合并最新 `main` 后再跑一次 `make check`，并扫描机器信息。桌面视图在真实 app 里截图对照画板。

## 手动验收（6c 合入后）

按总计划，用真实 Claude Code 和 Hermes 跑 0016 验收表，结果写进 0016。

## 不做

- 设置页里成长记忆之外的内容、设置快捷键。
- 记忆变更推送、远程设备访问、iOS 管理界面。
- 单条暂停、按范围永久删除、力导向布局和图形库。

## 结果

### 6a

11 条取舍中属于后端的按建议实现。与上面的差异：

- 指定 harness 时的默认模型优先取 `ModelLister` 的结果，没有该能力时才用探测结果。
- `profile.usage` 在最近一次注入失败时直接返回 `Failed`，不再统计该会话读取过的条目，与界面“本次没有用上记忆”一致。
- 使用记录为一次读取写一行，“用过几次”在查询时按不同会话去重。
- 迁移 `0021` 之后，旧版本迁移测试不能再调用记忆操作的存储方法，改为直接写旧库。

### 6b

按上面的步骤实现。与上面和设计的差异：

- 范围下拉的选项由后端给出：`ProfileDetail`、`ProfileCandidate` 增加 `Scopes`，由守护进程按条目来源算出，界面不自己推断。
- 待确认一组按组件说明写组头“待确认”和条数；画板这一组没有组头。
- 分组顺序按画板：全局、伙伴、仓库、主机。
- 打开的条目按画板替换原来那一行，不是接在行下面；展开块用 `selected` 底、`radius-row`。
- 从对话点进已撤回的条目时，按组件说明只显示这一张卡片，下面不接列表。
- 只有一个范围时，超过 5 条也保持单列；两列要有两组才能分。
- 版本来源的链接打开原会话，不定位到具体消息：聊天里还没有跳到某条事件的能力。除 `user_stated` 外的来源不加新文案。
- 记忆块在条数为 0 或读取失败时不画。
- 聊天内一行沿用现有事件行的外形（`row-step`、`space-3`、`radius-card`），和画板的 CEventLine 有差别，所有事件行都一样；Code 模式下这一行贴左，并在此处分开前后的工作组。
- `0024` 的“暂不出现”列表去掉“记住了”。

6c 待实现后填写。
