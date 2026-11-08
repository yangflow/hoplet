# M4a · PR7 桌面伙伴、子会话与人格 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 PR5、PR6 合并后的 `main`。

**目标：** Personal 侧栏只列伙伴；伙伴对话里有委派、转交、发布的事件行和回报卡，点开子会话、Code 会话或产物；伙伴详情有人格块，能编辑人格。

**验收（M4a 总计划）：** 委派结果回到伙伴并可持续跟进——在桌面上从回报卡打开子会话继续对话，回到伙伴的主会话。

**依据：** [M4a 总计划](m4a-overview.md)、[0024](../design/0024-desktop-layout.md)、[0010](../design/0010-personal-mode.md)、[0014](../design/0014-desktop-ui.md)、设计系统与布局画板。

## 全局约束

- 不新增依赖；MyGo 固定 v0.7.4，弹窗用 `ui.DialogBase`，输入用 `ui.TextArea`，不自己做遮罩与按键。
- 图标已全在：`corner-down-right`、`code-xml`、`file-code`、`file-text`、`image`、`table`、`chevron-right`、`check`、`circle-alert`、`pencil`、`info`。
- 视图保持薄，用 `ui.Tester` 测；`go tool mygo vet` 通过。

## 设计细节

### 对话条目

`convo` 加两种条目：

| Kind | 来自 | 内容 |
|---|---|---|
| `KindEvent` | `session.delegated`、`ArtifactPublished` | 动词、目标（会话 ID 或产物 ID + 版本）、标题 |
| `KindReport` | 带 `From` 的用户消息 | 来源会话、状态（缺省 `done`）、时间、正文 |

标题优先取目录里该会话的当前标题。Code 对话里两者照旧按原样显示（Code 会话不会有委派事件；`From` 消息只在伙伴里出现）。

### 视图

| 元素 | 规则 |
|---|---|
| 事件行 | 和伙伴气泡左对齐；细边框、无底色、最宽对话列 78%；图标 · 动词（`muted`）· 标题（500，一行截断）· 发布时版本（等宽 `muted`）· `chevron-right`；整条是链接 |
| 回报卡 | 同对齐与宽度；`surface` 底、`line` 边、`radius-card`；首行状态 · 标题 · “· 完成 / 出错了 / 等你审批” · 右侧“子会话 · 10:21”或“Code · 10:32” · `chevron-right`；正文最多 3 行 |
| 点开 | 委派与子会话回报：Personal 里打开子会话，侧栏伙伴行保持选中；转交与 Code 回报：切到 Code 选中那个会话；发布：右栏打开产物标签（PR6） |
| 侧栏 | Personal 只列 `Parent` 为空的会话 |
| 标题栏 | 子会话的标题前写伙伴名（`muted` 链接，点回主会话）与 `chevron-right` |
| 伙伴详情 | 主会话：人格块（“人格” · 铅笔“编辑人格” · 最多 4 行）；机器（在线加“· 在线”）；目录（伙伴目录，“在 Finder 中显示”，无分支）；运行；会话块同 Code。子会话没有人格块 |
| 编辑人格 | `DialogBase`，`surface-float`、`radius-panel`；标题“编辑人格”，副标题“伙伴名 · 它每次启动都会先读这段话”；固定高度可滚动的 `TextArea`；底行 `info` · 生效说明 · 取消 · 保存 |

生效说明：主会话还没开过（`Harness.NativeID` 为空）写“保存后，下次启动时用上”，否则“保存后，新的主会话才会用上”。保存以 `companions.set_persona` 的结果为准：返回的 `applies` 与预先显示的不同时，按返回值用 toast 再说一次。

### 取数（`Host`）

| 方法 | 用途 |
|---|---|
| `Companions(done func([]proto.Companion, error))` | 主会话 → 伙伴（ID、目录）；连上时取一次，目录里出现未知的 Personal 主会话时再取 |
| `Persona(id CompanionID, done func(string, error))` | 详情与弹窗 |
| `SetPersona(id CompanionID, text string, done func(proto.SetPersonaResult, error))` | 保存 |

## 任务

### 任务 1：对话条目

- [ ] `convo/convo.go`：`KindEvent`、`KindReport` 与条目字段；`Apply` 处理 `session.delegated`、`ArtifactPublished` 与带 `From` 的消息
- [ ] 测试：三种事件进对的条目；带 `From` 的消息不再是我的气泡；缺状态的旧回报按完成
- [ ] commit：`feat(desktop): read delegation and report items`

### 任务 2：事件行与回报卡

- [ ] `shell/chat.go`：两种视图与点开的去向；`shell/session.go` 在 Personal 里打开子会话时保持侧栏选中伙伴
- [ ] 测试（`ui.Tester`）：宽度不超 78%；三种状态的首行文字；点事件行与回报卡分别打开子会话、切到 Code、开产物标签
- [ ] commit：`feat(desktop): show delegations and reports in companion chats`

### 任务 3：侧栏与标题栏

- [ ] `shell/sidebar.go`：Personal 过滤 `Parent`；子会话选中时伙伴行选中
- [ ] `shell/content.go`：子会话标题前的伙伴链接
- [ ] 测试：子会话不出现在侧栏；标题栏链接回主会话；Code 侧栏不变（转交的 Code 会话照常列出）
- [ ] commit：`feat(desktop): open child sessions under their companion`

### 任务 4：伙伴详情与编辑人格

- [ ] `shell/app.go`：`Host` 加上表三个方法；`desktop/main.go` 实现
- [ ] `shell/content.go`：`companionDetails` 按上表重写；`shell/persona.go`（新）：弹窗
- [ ] 测试（`ui.Tester`）：主会话有人格块、子会话没有；目录行无分支；两种生效说明；取消不调 `SetPersona`；保存调一次并关窗；保存失败留在弹窗里并报错
- [ ] commit：`feat(desktop): show and edit companion personas`

### 任务 5：实测与文档

- [ ] 本机实测：Claude Code 与 Hermes 伙伴各委派一个子会话、转交一次 Code、发布一个产物；从回报卡进子会话继续说一句，再点标题栏回主会话；改人格后按说明生效
- [ ] 0024、0010：对应各条从目标改为现状
- [ ] 本计划勾选并记下实测
- [ ] `make check`、`go tool mygo vet`
- [ ] commit：`docs: record the companion desktop views`

PR 标题：`feat(desktop): add companion chats, child sessions and personas`。

## 风险

| 风险 | 处理 |
|---|---|
| 目录还没收到子会话时就来了事件 | 事件行先用事件里的标题，目录到了再换 |
