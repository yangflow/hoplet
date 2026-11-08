# M4a · PR5 桌面所需的委派、回报与版本数据 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `aac718f`（#42 合并后）加上 #43 设计同步的 `main`。

**目标：** 桌面的伙伴界面要的三样数据由 daemon 给出：伙伴对话里委派与转交的事件、回报的状态、产物的版本列表。界面本身在 PR6、PR7。

**验收：** 委派与转交在父会话日志里各有一条事件；回报消息带状态；客户端能列出一个产物的全部版本；旧日志照常解码。

**依据：** [M4a 总计划](m4a-overview.md)、[0024](../design/0024-desktop-layout.md)（事件行、回报卡、产物标签）、[0025](../design/0025-artifacts.md)、[0012](../design/0012-orchestration-api.md)、[0004](../design/0004-session-data-model.md)。

## 维护者的裁决（2026-11-08）

界面已定，但现有数据画不出三处；三处都按“采用”一列做：

| 缺口 | 界面要什么 | 采用 | 未采用 |
|---|---|---|---|
| 委派 / 转交事件行 | 伙伴对话里在委派发生的位置有一行，点开是子会话 | 新事件 `session.delegated`（v1），`session_create` 与 `handoff` 成功后写进**父会话**日志，和 `ArtifactPublished` 一样 | 桌面按目录里 `Parent` 与建立时间插进对话：不改后端，但位置按时间猜，和事件序号对不齐 |
| 回报卡状态 | 完成 / 出错了 / 等你审批 | `Message` 加可选的 `Report`（`done` / `error` / `approval`），回报时由 daemon 填；旧消息没有该字段，有 `From` 时按 `done` 显示 | 桌面看来源会话当前状态：卡片会随来源变化而变，不是回报那一刻的状态 |
| 产物版本菜单 | 每行“v3 · 10:24” | 新方法 `artifacts.versions`（产物 ID → 全部版本，从新到旧），配对设备可用 | 读每个版本的入口文件拿时间：不可行，读结果里没有时间 |

三项都是只增不改：新事件类型带版本号，`Message` 新字段可省略、事件版本不变，新方法不影响旧客户端。

## 全局约束

- 不新增依赖；不改已有表，事件只增类型。
- 测试不碰真实的 `~/.hoplet`、不启动真实 harness、不用 sleep 同步，全部 `-race`。
- 设计变化同一 PR 写进 0004、0012、0025。

## 设计细节

### `session.delegated`（v1）

| 字段 | 说明 |
|---|---|
| `session` | 新建的会话 |
| `kind` | `delegate`（`session_create` 建的托管子会话）或 `handoff`（转交的 Code 会话） |
| `title` | 建立时的标题；桌面优先用目录里该会话的当前标题 |

写在父会话日志里，时间是建立成功的时刻；建立失败不写。

### 回报状态

| 来源 | `Report` |
|---|---|
| 子会话调用 `report` | `done` |
| 转交的 Code 会话轮次正常结束 | `done` |
| 转交的 Code 会话轮次以错误结束 | `error` |
| 转交的 Code 会话等待审批 | `approval` |

daemon 投递回报时把状态随消息写入；排队中的回报同样带状态。

### `artifacts.versions`

参数 `{artifact}`，返回 `{versions: [Artifact]}`，每项是该版本的 `Artifact`（版本号、时间、文件数、来源），从新到旧。不存在的产物返回 `not_found`。

## 任务

### 任务 1：协议

- [x] `proto/payloads.go`：`SessionDelegated`（`TypeSessionDelegated = "session.delegated"`，v1，校验 `session` 非空、`kind` 合法）；`Message.Report ReportStatus`（`omitempty`）与三个常量
- [x] `proto/artifact.go` / `proto/api.go`：`MethodArtifactsVersions`、`ArtifactsVersionsParams`、`ArtifactsVersions`
- [x] `proto/testdata/events.jsonl` 加新事件与带 `report` 的消息；旧样例照常解码
- [x] 测试：新事件与新类型的 JSON 往返；缺字段校验失败
- [x] commit：`feat(proto): add delegation events, report status and versions`

### 任务 2：daemon 与客户端

- [x] `internal/daemon`：`session_create`、`handoff` 成功后向父会话追加 `SessionDelegated`；回报投递时写 `Report`（`toolSource.Report` 为 `done`，`handoffs.report` 按上表）
- [x] `internal/daemon`：`artifacts.versions`，本机与配对设备可用；存储用已有的 `ArtifactVersions`
- [x] `internal/client`：`ArtifactVersions(ctx, id)`
- [x] 测试（daemon）：托管父会话经工具建子会话、转交 Code 后父会话日志各有一条事件；失败的建立不写；回报消息带对的状态（含排队后投递）；配对设备列出版本，不存在的产物 `not_found`
- [x] commit：`feat(daemon): record delegations and report status`

### 任务 3：CLI 与文档

- [x] `cmd/hoplet`：事件输出加委派行与回报状态（不加新命令）
- [x] 0004：新事件与 `Message.report`；0012：回报带状态；0025：`artifacts.versions`
- [x] `make check`
- [x] commit：`docs: record delegation events and artifact versions`

PR 标题：`feat(session): add delegation events, report status and artifact versions`。

## 实施记录

- 委派事件写在存储建会话的同一个事务里（`createSession`），不在工具层另写：建立与事件一起成功或一起回滚，重复建立不重写。
- 回报状态要跨重启随排队消息保存，队列表加了 `report` 列（迁移 15）；与“不改已有表”不同，只加一列、默认为空，旧行照常读。
- 存储方法名为 `ArtifactHistory`（`ArtifactVersions` 留给客户端）。
- CLI `session watch`：回报显示为 `< report from <会话> (<状态>)`，委派显示为 `delegated to` / `handed off to <会话> <标题>`。

## 风险

| 风险 | 处理 |
|---|---|
| 父会话日志在委派时正在写一轮的事件 | 追加走同一个日志写入，序号自然落在调用工具的那一轮里 |
| PR3 之前已有的委派没有事件 | 不补写；这些子会话只在回报卡上出现 |
