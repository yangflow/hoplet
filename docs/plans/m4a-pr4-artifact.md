# M4a · PR4 产物服务与 CLI · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `94eabd8`（#40 合并后）的 `main`。

**目标：** 会话能发布带版本的产物，产物落在 daemon 的存储里，对话里有一条发布事件；客户端（本机与配对设备）能列出产物并按块读出任一版本的文件。桌面查看另起 PR。

**验收（M4a 总计划）：** 0025 验收表中不依赖界面的各项：发布与版本、来源、读取、事件。

**依据：** [M4a 总计划](m4a-overview.md)、[0025](../design/0025-artifacts.md)、[0012](../design/0012-orchestration-api.md)、[0004](../design/0004-session-data-model.md)、[0005](../design/0005-sync-protocol.md)。

## 维护者的裁决（2026-11-08）

1. **WebView 原型的结论。** 内容规则挡住了除 WebRTC 以外的全部外部请求；WebRTC 记为已知缺口，等 MyGo 提供关闭选项。协议处理器只认调用页面自己的产物与版本，其他返回 404。结论已写进 0025。
2. **页面能力暂缓。** `db` / `publish` / `files` / `ask` 等产物能查看并实际使用后再定；PR4 不存能力声明，不做 `artifact_db_*`，不注入桥接脚本。
3. **按路径发布。** daemon 自己读文件，请求里不带内容。工具桥的路径相对会话工作目录；CLI 只对本机 daemon，`artifacts.publish` 是仅本机的方法。
4. **分块读取。** `artifacts.read` 按偏移每次最多返回 1 MiB。
5. **SVG 不作入口。** `image` 只收 png / jpg / jpeg / gif / webp；HTML 内部引用的 SVG 照常发布。

## 全局约束

- 不新增依赖；Content-Type 用标准库 `mime`。
- 迁移只加表，不改已有数据；新事件类型带版本号。
- 测试不碰真实的 `~/.hoplet`、不启动真实 harness、不用 sleep 同步，全部 `-race`。
- 来源与路径检查在服务里做，不靠提示词。

## 设计细节

### 存储

| 项 | 规则 |
|---|---|
| 迁移 | `0014_artifacts.sql`：`artifacts(id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), name TEXT NOT NULL, kind TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE (session_id, name)) STRICT`；`artifact_versions(artifact_id TEXT NOT NULL REFERENCES artifacts(id), version INTEGER NOT NULL, entry TEXT NOT NULL, by TEXT NOT NULL, files INTEGER NOT NULL, size INTEGER NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY (artifact_id, version)) STRICT, WITHOUT ROWID` |
| 发布记录 | `Store.PublishArtifact(ctx, v)`：一个事务里按 `(session, name)` 找或建产物（kind 不同返回 `ErrInvalid`）、版本号取已有最大值 + 1、写版本行、在所属会话日志追加 `ArtifactPublished`；会话不存在 `ErrNotFound`，已归档 `ErrInvalid` |
| 事件轮次 | 记在会话当前打开的轮次；没有打开的轮次时为 0 |
| 读 | `Store.Artifacts(ctx, session)`（每个产物带最新版本）、`Store.ArtifactVersion(ctx, id, n)`（`n = 0` 取最新） |
| 文件 | `<home>/artifacts/<session>/<id>/v<n>/`，由产物服务写；存储只记行 |

### 协议

| 项 | 规则 |
|---|---|
| `proto.Artifact` | `id`、`session`、`name`、`kind`、`version`、`entry`、`by`、`files`、`size`、`created_at`、`updated_at`（最新版本的时间） |
| `proto.ArtifactPublished` | 事件 `artifact.published` v1：`artifact`、`name`、`kind`、`version`、`by` |
| `artifacts.publish` | 参数 `session`、`name`、`entry`（绝对路径）、`files`（绝对路径，可省略）；返回 `Artifact`；仅本机连接 |
| `artifacts.list` | 参数 `session`；返回 `[]Artifact`，按最新版本时间倒序 |
| `artifacts.read` | 参数 `artifact`、`version`（0 = 最新）、`path`（空 = 入口）、`offset`；返回 `{data, size, content_type}`，`data` 最多 1 MiB（JSON 里为 base64）；`offset` 超出大小返回 `invalid` |
| 错误 | 用现有 API 错误码：不存在 `not_found`；超限、不支持的类型、路径越界、名字不合法 `invalid`；远端调用 `artifacts.publish` 沿用仅本机方法的拒绝 |

`make gen` 重新生成 Swift 类型；新事件与三个方法的参数、返回进 `testdata/vectors/`。

### 产物服务（`internal/artifact`）

| 项 | 规则 |
|---|---|
| 名字 | 去掉首尾空白后非空，≤ 128 字节，不含控制字符与 `/` |
| kind | 按入口扩展名（不分大小写），见 0025；其他返回“不支持的类型” |
| 附带文件 | 只有 HTML 能带；须在入口所在目录之下（解析符号链接后），按相对该目录的路径保存，不得与入口重名、彼此重名 |
| 文件 | 只收普通文件；入口加附带文件 ≤ 64 个，合计 ≤ 16 MiB（按读到的字节计，读的过程中超出即停） |
| 写入 | 同一服务内的发布串行。文件先写进 `artifacts/<session>/<id>/.tmp-*` 并 fsync，再改名为 `v<n>`，然后 `Store.PublishArtifact`；记录失败就删掉 `v<n>` |
| 启动清理 | daemon 启动时删掉 `.tmp-*`，以及没有版本行的 `v<n>`（改名后、记录前崩溃留下的） |
| 版本号 | 写目录前从存储读出下一版本号；串行发布保证与记录时一致 |
| 读 | 路径清理后须在 `v<n>` 之下；按偏移读 ≤ 1 MiB；Content-Type 按扩展名，取不到时为 `application/octet-stream` |

### 工具

| 工具 | 谁能看见 | 参数 | 规则 |
|---|---|---|---|
| `artifact_publish` | 所有会话 | `name`（必填）、`entry`（必填）、`files` | 路径相对调用方会话的工作目录，解析符号链接后不能越出；以 `by = harness` 发布到调用方会话；返回 `{id, name, kind, version}`；失败原因作为工具失败返回给模型 |

### CLI

| 命令 | 规则 |
|---|---|
| `hoplet artifact publish SESSION PATH [--name NAME]` | `PATH` 是文件：它就是入口，名字默认取文件名。`PATH` 是目录：入口为其中的 `index.html`，目录下全部普通文件随版本发布（跳过以 `.` 开头的文件与目录），名字默认取目录名。以 `by = cli` 发布；`--host` 时报“发布只支持本机” |
| `hoplet artifact list SESSION` | 列出名字、kind、最新版本、更新时间与 ID |
| 会话事件 | `hoplet session watch` 的输出把 `ArtifactPublished` 显示为一行：`published <name> v<n> (<kind>)` |

两个命令都接受 `--json`；`SESSION` 支持至少 4 个字符的唯一前缀。

## 任务

### 任务 1：协议与存储

- [x] `proto/`：`Artifact`、`ArtifactPublished`、三个方法的参数与返回；`make gen`
- [x] `internal/store/migrations/0014_artifacts.sql`；`internal/store/artifacts.go`：`PublishArtifact`、`Artifacts`、`ArtifactVersion`
- [x] 测试：新类型的 JSON 与向量；从 0013 迁移后会话与事件不变；同名再发布版本递增、kind 不同被拒；不同会话同名互不影响；会话不存在、已归档被拒；事件写进所属会话日志并记在打开的轮次
- [x] commit：`feat(store): record artifacts and versions`

### 任务 2：产物服务

- [x] `internal/artifact`：名字、kind、路径与上限检查；临时目录写入、改名、记录；启动清理；分块读
- [x] 测试：单文件与带附带文件的 HTML；超 64 个文件、超 16 MiB、不支持的扩展名（含 `.svg`）、附带文件在入口目录之外、符号链接指出目录、非普通文件，均整次失败且不留目录；记录失败时删掉 `v<n>`；启动清理删掉孤立目录、保留有记录的版本；分块读完与原文件一致，越界路径与不存在的版本 `not_found`；旧版本发布新版本后不变
- [x] commit：`feat(artifact): publish and read artifact versions`

### 任务 3：工具与 API

- [x] `internal/tools`：`artifact_publish`；`Source` 增加发布方法
- [x] `internal/daemon`：建产物服务并在启动时清理；`artifacts.publish`（加入仅本机方法）、`artifacts.list`、`artifacts.read`；工具发布按调用方会话工作目录解析路径
- [x] 测试（tools，假 Source）：参数校验；路径越出工作目录（含 `..` 与符号链接）被拒
- [x] 测试（daemon）：假 harness 经 `hoplet mcp` 调 `artifact_publish`，会话日志里有发布事件、`artifacts.list` 列出它；远端连接调用 `artifacts.publish` 被拒，`artifacts.list` / `artifacts.read` 可用；分块读大文件
- [x] commit：`feat(daemon): serve artifacts over the API and tool bridge`

### 任务 4：CLI

- [x] `cmd/hoplet/artifact.go`：`publish`、`list`，`--json`；事件输出加发布行
- [x] 测试：文件与目录发布（目录跳过点文件、无 `index.html` 报错）、默认名字、`--host` 报错、`list` 输出
- [x] commit：`feat(cli): publish and list artifacts`

### 任务 5：实测与文档

- [x] 本机实测：Claude Code 与 Hermes 会话各让 harness 写一页带脚本与图片的 HTML 并 `artifact_publish`，再改一版发布；用 CLI 发布一个目录；`artifacts.read` 读回内容与原文件一致；从配对设备列出并读取
- [x] 0025：实现的部分从目标改为现状；0004：`artifacts` / `artifact_versions` 表与文件布局；0012：`artifact_publish` 改为现状；0019 的基线
- [x] 本计划勾选并记下实测结果
- [x] `make check`
- [x] commit：`docs: record the artifact service`

PR 标题：`feat(artifact): add artifact service and CLI`。

## 结果（2026-11-08）

### 与计划不同之处

| 项 | 计划 | 实际 |
|---|---|---|
| Swift 类型与向量 | `make gen` 重新生成，新类型进 `testdata/vectors/` | 仓库里还没有 `make gen` 与 `testdata/vectors/`；新事件进 `proto/testdata/events.jsonl` 的解码样例，新类型的 JSON 在 `proto/api_test.go` 里测 |
| daemon 测试 | 假 harness 经 `hoplet mcp` 调 `artifact_publish` | 假 harness 不会调 MCP 工具；daemon 测试用会话 token 的工具连接直接调用，真实的 `hoplet mcp` 转发在 `cmd/hoplet/mcp_test.go` 里测 |
| 工具路径 | 相对会话工作目录 | 同时接受工作目录内的绝对路径（harness 常给绝对路径），越出仍被拒 |
| 存储读方法 | `Artifacts`、`ArtifactVersion` | 另加 `ArtifactByName`（发布前取下一版本号）与 `ArtifactVersions`（启动清理） |
| `--host` | 报“发布只支持本机” | `artifact` 命令加入 `--host` 可用的命令；`list` 可对配对主机运行，`publish` 报错 |
| CLI 默认名字 | 文件名 | 文件名带扩展名（如 `notes.md`），目录取目录名 |

### 本机实测

临时 home、本机 Claude Code 2.1.316 与 Hermes 0.22.5：

| 项 | 结果 |
|---|---|
| Claude Code 会话写 `report/`（index.html 引 app.js 与 4×4 PNG）并 `artifact_publish` | v1 发布，会话日志出现 `published report v1 (html)`；改 app.js 再发布得 v2，v1 的 app.js 不变，两版 PNG 与原文件一致 |
| Hermes 会话（模型 `zcode/GLM-5.3`）同样写 `page/` 并发布、再改一版 | v1、v2 都发布，内容与原文件一致；Hermes 默认模型对该账号不可用，第一轮失败，换模型后正常，与产物无关 |
| CLI 发布目录（index.html、3 MB 的 `assets/app.js`、`.git/`、`.env`） | `published site v1 (html, 2 files)`，点文件与点目录未发布 |
| `artifacts.read` 读回 | 本机：3 MB 文件分 3 次读完，与原文件一致，Content-Type `text/javascript` |
| 配对设备 | 另一 home 经直连配对后 `--host live artifact list` 列出产物；读 3 MB 文件（3 次）、PNG（`image/png`）、Hermes 页面入口，均与原文件一致；`--host live artifact publish` 报“publish only works on this machine” |

两页都没有引用外部资源，路径都在工作目录内，大小远低于上限；风险表前三项这次没有出现。

## 风险

| 风险 | 在哪发现 | 处理 |
|---|---|---|
| harness 写的 HTML 引用外部 CDN，桌面查看时显示不全 | 任务 5 实测（看生成的页面） | 记下比例；`artifact_publish` 的说明里写明外部资源会被挡、须随版本发布；是否放行 CDN 白名单按 0025 待验证项另议 |
| 模型给出工作目录外的路径（如 `/tmp`） | 任务 5 实测 | 工具失败说明路径须在工作目录内，模型据此改写到工作目录 |
| 16 MiB / 64 个文件不够 | 任务 5 实测 | 记下实际大小，按 0025 待验证项另议 |
| 发布期间 daemon 崩溃 | 任务 2 测试 | 临时目录与孤立版本目录在启动时清理，记录与事件同一事务 |
