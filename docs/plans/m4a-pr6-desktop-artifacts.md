# M4a · PR6 桌面产物 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 PR5 合并后的 `main`。

**目标：** 桌面能列出并查看会话的产物：Code 右列多一段“产物”，Personal 的右栏只有产物；查看区的产物标签按类型显示，能切换版本，新版本自动换上。

**验收：** 0025 验收表的“隔离”“远端”两项在桌面成立；0024 “产物（右列）”“产物（查看区标签）”“Personal 右栏”三条按设计系统画出。

**依据：** [M4a 总计划](m4a-overview.md)、[0024](../design/0024-desktop-layout.md)、[0025](../design/0025-artifacts.md)、[0014](../design/0014-desktop-ui.md)、设计系统与布局画板。

## 全局约束

- 不新增依赖；MyGo 固定 v0.7.4，只用它提供的组件：`ui.WebView`、`mygo.Protocol.HandleFunc`、`mygo.CallerPage`、`PageOptions.Untrusted`、`markdown.View`、`ui.Image` + `ui.DecodeImage`、`ui.Table`、`ui.PopoverBase`。
- 图标只用 Lucide 1.66.0 原文件：补 `image.svg`、`table.svg`，其余（`box`、`file-code`、`file-text`、`refresh-cw`、`clock`、`check`）已在。
- 视图保持薄，状态与取数放在 `shell` 的状态里，用 `ui.Tester` 测；`go tool mygo vet` 通过。
- 设计里没有的状态不做；与设计不符时停下提出。

## 设计细节

### 取数（`Host`）

| 方法 | 用途 |
|---|---|
| `Artifacts(id SessionID, done func([]proto.Artifact, error))` | 右列列表 |
| `ArtifactVersions(id ArtifactID, done func([]proto.Artifact, error))` | 版本菜单 |
| `ReadArtifact(p ArtifactsReadParams, done func([]byte, error))` | Markdown、图片、表格、文本：host 分块读完再交回 |
| `ArtifactPage(id ArtifactID, version int) (ui.NativeWebView, error)` | HTML：host 建不可信页面，shell 只摆放 |

刷新：跟随的会话里来了 `ArtifactPublished` 就重取列表；开着的标签在看“最新”时换到新版本，看旧版本时只更新提示里的“最新是 vN”。

伙伴的列表含它委派的子会话发布的：主会话加上目录里 `Parent` 是它的会话，各取一次后按时间合并。子会话的发布不进伙伴日志，改为在右栏开着时、子会话一轮结束（目录里状态离开运行）时重取那个子会话。

### 不可信页面

- 启动时 `mygo.Protocol.HandleFunc("hoplet-artifact", …)` 注册一次。
- 地址 `hoplet-artifact://<id>/v<n>/<path>`；空路径是入口。处理器取 `CallerPage(r.Context())` 的地址，产物或版本与请求不同返回 404，不转给 daemon；相同则按块 `artifacts.read`，Content-Type 用标准库 `mime`。
- 页面 `PageOptions{Untrusted: true}`；导航只留本产物本版本，`http(s)` 链接交给系统浏览器，其他忽略。
- 配对机器上的会话走同一处理器，客户端换成那台机器的连接。

### 视图

| 位置 | 内容 |
|---|---|
| 右列顶栏 | Code：文件树 / 变更 / 产物（`box`）三段；Personal：只写“产物”，没有切换 |
| 列表行 | 类型图标 · 名字 · 最新版本 · 时间；从新到旧；空时“还没有产物”；开着标签的行 `selected` |
| 标签 | 类型图标 · 产物名；顶行版本片“v3 · 最新 ⌄” · 时间 · `refresh-cw` |
| 版本菜单 | `PopoverBase`，窄菜单宽，“v3 · 10:24”，当前的打勾 |
| 旧版本提示 | `clock` · “这是旧版本，最新是 v3” · “回到最新” |
| 打不开 | “打不开这个版本”，一行原因，“重试” |

类型图标：`html` → `file-code`，`markdown` / `text` → `file-text`，`image` → `image`，`csv` → `table`。

## 任务

### 任务 1：取数与状态

- [ ] `desktop/internal/icons`：加 `image.svg`、`table.svg`（lucide-static 1.66.0 原文件）并登记
- [ ] `shell/app.go`：`Host` 加上表四个方法；`panelColumn` 加 `columnArtifacts`；`panelTab` 加产物标签（产物 ID、在看的版本、是否跟最新）
- [ ] `shell/artifacts.go`（新）：列表状态、合并伙伴与子会话、事件触发的重取、标签的版本切换
- [ ] `desktop/main.go`：实现前三个方法（经 `link` 的客户端，分块读完）
- [ ] 测试（`shell`，假 Host）：发布事件到来后列表重取并把那一行移到最前；伙伴列表含子会话的；看最新的标签换版本，看旧版本的标签不换只改提示
- [ ] commit：`feat(desktop): load session artifacts`

### 任务 2：右列与 Personal 右栏

- [ ] `shell/panel.go`：Code 第三段；Personal 的 `panelSession()` 对产物放行，右列只有列表、顶栏写“产物”
- [ ] 列表视图与空状态
- [ ] 测试（`ui.Tester`）：Code 三段切换别处不动；Personal 无切换；点一行开标签、再点切过去；开着的行选中
- [ ] commit：`feat(desktop): list artifacts in the right panel`

### 任务 3：产物标签

- [ ] `shell/artifactview.go`（新）：顶行、版本菜单、旧版本提示、打不开与重试；Markdown `markdown.View`，图片 `ui.Image`，CSV `ui.Table`（标准库 `encoding/csv` 解析），文本同文件视图
- [ ] 测试（`ui.Tester`）：版本菜单顺序与打勾；“回到最新”；读失败显示原因，“重试”再读一次；CSV 成表
- [ ] commit：`feat(desktop): view artifact versions`

### 任务 4：HTML 与协议处理器

- [ ] `desktop/main.go` 或 `desktop/internal/artifactpage`（新）：注册协议、`ArtifactPage`、`CallerPage` 核对、导航规则
- [ ] 测试：处理器对别的产物、别的版本、越出版本目录返回 404 且不调 daemon（假读取函数记调用）；Content-Type 正确
- [ ] 本机实测：PR4 实测用过的那页带脚本与图片的 HTML 能显示；0025 隔离表各项在桌面上复查一遍；配对机器的产物可看
- [ ] commit：`feat(desktop): load html artifacts as untrusted pages`

### 任务 5：文档

- [ ] 0025：桌面查看改为现状；0024：产物各条从目标改为现状
- [ ] 本计划勾选并记下实测
- [ ] `make check`、`go tool mygo vet`
- [ ] commit：`docs: record the desktop artifact viewer`

PR 标题：`feat(desktop): list and view artifacts`。

## 风险

| 风险 | 处理 |
|---|---|
| 大文件分块读完才显示 | 产物有上限；读的时候内容区按设计留空，不加设计里没有的进度 |
| `CallerPage` 在子资源请求里拿不到页面 | 实测确认；拿不到就停下提出，不放宽核对 |
| WebRTC 缺口 | 0025 已记；MyGo 提供关闭选项后跟进 |
