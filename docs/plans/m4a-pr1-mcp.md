# M4a · PR1 会话工具桥 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `858a75f`（#32 合并后）的 `main`。

**目标：** Hoplet 启动的 harness 会话带上 Hoplet 自己的 MCP 服务 `hoplet mcp`；它以本会话的 token 连 daemon，模型能调 `sessions_list` 与 `session_get`。

**验收（M4a 总计划）：** 假 harness 经 `hoplet mcp` 调通两个工具；别的会话的 token、归档后的 token 被拒；daemon 重启后 token 仍有效；token 不出现在 harness 的环境与命令行里；两家适配器的启动参数与请求按录制向量一致。

**依据：** [M4a 总计划](m4a-overview.md)、[0012](../design/0012-orchestration-api.md)、[0003](../design/0003-agent-driving.md)、[0020](../design/0020-hermes-adapter.md)、[0021](../design/0021-claude-adapter.md)。

## 维护者的裁决（2026-11-07）

1. **Hermes 的 token 随会话不变。** Hermes 把 MCP 配置连同 token 存在自己的会话里，不带 `editor` 续接（新连接也一样）照样按存下的配置重启服务。新会话在 `session.create` 里带上；之前建的会话第一次挂载时经空闲时的 `editor_mcp` 挂上；之后续接一律不带 `editor`。Claude Code 仍每次启动新生成。
2. **Claude Code 的 MCP 配置写文件。** 每次启动写 `<会话目录>/mcp.json`（0600），`--mcp-config` 传路径；token 不进命令行。
3. **daemon 持有工具表。** hello 增加 `session_token`；这样的连接绑定到该会话，只能调 `tools.list`、`tools.call`。工具定义、参数校验与范围检查在 daemon，`hoplet mcp` 只转发。

## 全局约束

- 新增依赖只有 `github.com/modelcontextprotocol/go-sdk` v1.8.0，只在 `hoplet mcp` 里用。
- 工具调用落到与 API 相同的服务方法；不另写一份会话查询。
- 不新增持久化事件；token 只存 SHA-256。
- 测试不碰真实的 `~/.hoplet`、不启动真实 harness、不用 sleep 同步，全部 `-race`。
- 提炼任务（`Ephemeral`）不注入 MCP。

## 设计细节

### token

| 项 | 规则 |
|---|---|
| 生成 | `crypto/rand.Text()`（128 位）；存 SHA-256 |
| 存储 | 新迁移 `0011_session_tokens.sql`：`session_tokens(session_id TEXT PRIMARY KEY REFERENCES sessions(id), hash BLOB NOT NULL UNIQUE, issued_at INTEGER NOT NULL) STRICT`；每个会话最多一行，新签发覆盖旧的 |
| 校验 | 按哈希查行，且会话未归档；查不到报 `unauthorized` |
| 作废 | `ArchiveSession` 在同一事务里删掉该行 |
| Claude Code | 每次 Launch 签发；Attach（daemon 重启后接回运行中的 harness）不签发，旧 token 照常有效 |
| Hermes | 会话没有 token 行、或还没有原生会话 ID（要走 `session.create`）时签发；否则不签发、不注入 |
| 不注入 | `Ephemeral`；适配器的 `Capabilities.MCPInject` 为假 |

### harness 接口

| 项 | 规则 |
|---|---|
| `harness.MCPServer` | 改为 stdio：`Name`、`Command []string`、`Env []string`（`K=V`）；去掉 `URL`、`Token` |
| `Spec.MCP` | 有值即注入；空即不注入。daemon 只放一项：`Name: "hoplet"`、`Command: [<daemon 的二进制>, "mcp", "--home", <home>]`、`Env: ["HOPLET_SESSION_TOKEN=<token>"]` |
| `Spec.Dir` | 新增：会话的私有目录（`<root>/<会话 ID>`），适配器在这里放文件 |
| `Launch.Files` | 新增：`map[string][]byte`，文件名（不含路径）→ 内容；manager 在启动 shim 前以 0600 写进 `Spec.Dir`，写失败即启动失败 |
| `harness.PersistsMCP` | 新增可选接口 `PersistsMCP() bool`：Hermes 实现并返回真；manager 据此按上表决定是否签发 |
| `Capabilities.MCPInject` | 两家适配器都声明为真；能启动就说明版本已通过自检 |

### Claude Code 适配

- `Spec.MCP` 有值时，Launch 返回 `Files["mcp.json"]`：`{"mcpServers":{"hoplet":{"type":"stdio","command":…,"args":[…],"env":{"HOPLET_SESSION_TOKEN":…}}}}`，参数加 `--mcp-config <Spec.Dir>/mcp.json` 与 `--allowedTools mcp__hoplet__*`。
- 不加 `--strict-mcp-config`；`Spec.MCP` 为空时参数与现在一字不差。
- 自检（`check.go`）不变：仍是空 MCP。

### Hermes 适配

- 新会话：`session.create` 的 `editor` 带 `mcp_servers: [{name, command, args, env: [{name, value}]}]`，`command` 取 `Command[0]`，`args` 取其余。
- 续接（重连、daemon 重启、`session.replay_gap`）：不带 `editor`，与现在相同。
- 已有原生会话且 `Spec.MCP` 有值：挂上后把 `editor_mcp`（`payload: {mcp_servers: [...]}`）放进现有的修改队列，空闲时发出；被拒时沿用现有的修改被拒处理（`onMutateRefused`）。
- 重发 `session.create`（创建已发出、原生 ID 还没记下时 daemon 崩溃）带的是新 token；Hermes 若按配置不同拒绝，按现有的挂载被拒处理。录制时确认实际行为。

### 连接与工具

| 项 | 规则 |
|---|---|
| hello | `HelloParams` 加 `session_token`；有它时不看本机 token 与设备 ID，查到会话就把连接绑定到该会话；远程连接带它直接拒绝 |
| 方法 | 新增 `tools.list`（无参数 → `{tools: [{name, description, input_schema}]}`）、`tools.call`（`{name, arguments}` → `{text, is_error}`） |
| 隔离 | 会话连接调其他方法报 `unauthorized`；普通连接调 `tools.*` 报 `unauthorized` |
| 工具表 | 新包 `internal/tools`：每个工具有名字、说明、JSON Schema、调用方是否可见、调用函数；`tools.list` 只列调用方可见的 |
| 错误 | 参数不对、目标不存在等写成 `is_error: true` 的文本，不作协议错误 |
| `sessions_list` | 无参数；返回未归档会话，内容同 `sessions.list` 结果的 JSON |
| `session_get` | `{id}`，可用至少 4 字符的唯一前缀；返回同 `sessions.get` 结果的 JSON |

### `hoplet mcp`

- `hoplet mcp --home <dir>`：从 `HOPLET_SESSION_TOKEN` 读 token，连 `<home>/daemon.sock`，发 hello；失败写 stderr 并以 1 退出。
- 用 go-sdk 在 stdio 上提供 MCP 服务：启动时取一次 `tools.list` 注册工具；每次调用转成 `tools.call`，结果以文本内容返回，`is_error` 原样带回。
- 与 daemon 断开即退出；harness 下次调用时自己重启服务。
- 不读本机 token 文件，不读 harness 的任何配置。

## 任务

### 任务 1：协议

- [x] `proto/api.go`：`HelloParams.SessionToken`；`MethodToolsList`、`MethodToolsCall`；`ToolInfo`、`ToolsListResult`、`ToolsCallParams`、`ToolsCallResult`
- [x] `make gen` 重新生成 Swift
- [x] 测试：新类型的 JSON 往返与字段名
- [x] commit：`feat(proto): add session tokens and tool calls`

### 任务 2：存储

- [x] `internal/store/migrations/0011_session_tokens.sql`
- [x] `internal/store/tokens.go`：`SetSessionToken(ctx, id, hash, t)`、`SessionByToken(ctx, hash) (proto.SessionID, error)`、`HasSessionToken(ctx, id) (bool, error)`；`ArchiveSession` 一并删行
- [x] 测试：签发后查得到；重签后旧哈希查不到；归档后查不到；别的会话的哈希不串；从 0010 的库迁移后数据不变
- [x] commit：`feat(store): store session token hashes`

### 任务 3：daemon 的工具连接

- [x] `internal/tools/`：工具表与 `sessions_list`、`session_get`
- [x] `internal/daemon/conn.go`：hello 的会话分支；连接记下调用方会话；按调用方过滤方法
- [x] `internal/daemon/methods.go`：`tools.list`、`tools.call`
- [x] 测试：带有效 token 的连接列出两个工具并调通；错误 token、归档会话的 token 被拒；会话连接调 `sessions.list` 被拒；普通连接调 `tools.call` 被拒；远程连接带 `session_token` 被拒；重开 store 后 token 仍有效；`session_get` 的前缀与不存在
- [x] commit：`feat(daemon): serve tools to session connections`

### 任务 4：`hoplet mcp`

- [x] `go get github.com/modelcontextprotocol/go-sdk@v1.8.0`
- [x] `cmd/hoplet/mcp.go`：命令与转发
- [x] 测试：起测试 daemon，管道接 `hoplet mcp`，用 go-sdk 客户端完成握手、列工具、调 `sessions_list`；没有 token、token 错误时退出 1 且 stderr 说明；daemon 断开后退出
- [x] 测试：录一份 Claude Code 2.1.316 对 MCP 服务的握手（实录：`server/discover` 协议 2026-07-28 探测后直接 `tools/list`，不发 `initialize`），在测试里回放给 `hoplet mcp`，应答与录制一致
- [x] commit：`feat(cli): add hoplet mcp bridge`

### 任务 5：注入

- [x] `internal/harness/harness.go`：`MCPServer`、`Spec.Dir`、`Launch.Files`、`PersistsMCP`
- [x] `internal/session/manager.go`：按上表签发 token、填 `Spec.MCP` 与 `Spec.Dir`、写 `Launch.Files`；`Config` 加 MCP 命令（daemon 的二进制与 home）
- [x] `internal/harness/claude/claude.go`：`mcp.json`、`--mcp-config`、`--allowedTools`；`MCPInject`
- [x] `internal/harness/hermes/requests.go`：create 带 `mcp_servers`；已有会话排 `editor_mcp`；`PersistsMCP`；`MCPInject`
- [x] 测试（manager，用假 harness）：Claude 型每次 Launch 换 token、Attach 不换；Hermes 型只在没有 token 或没有原生 ID 时签发；`Ephemeral` 与 `MCPInject` 为假时不注入；文件 0600；token 不在 `Launch.Env`、`Launch.Command` 与 shim 日志里
- [x] 测试（Claude）：带 MCP 的参数与 `mcp.json` 按 golden；不带 MCP 的参数不变
- [x] 测试（Hermes）：create 参数按录制向量；续接不带 `editor`；已有会话忙时 `editor_mcp` 排队、空闲后发一次
- [x] commit：`feat(harness): inject the Hoplet MCP server`

### 任务 6：实测与文档

- [x] 本机实测：Claude Code 2.1.316 与 Hermes 0.22.5 各新建一个会话，让模型调 `sessions_list`；Hermes 再测 daemon 重启后续接仍可调用、已有会话第一次挂载
- [x] 按实测结果更新 0012、0003、0020、0021 的现状（“尚未实现” → 已有），0019 的基线
- [x] `make check`
- [x] commit：`docs: record the session tool bridge`

实测结果（2026-11-07）：Claude Code 2.1.316 新会话调 `mcp__hoplet__sessions_list` 不经审批；Hermes 0.22.5 新会话经 `editor.mcp_servers` 挂上（工具名 `mcp__editor_hoplet_<hash>__sessions_list`），daemon 重启后续接用同一 token 照常调用；没有 token 的已有会话接回时签发新 token、空闲时发一次 `editor_mcp`，停掉重接后新 token 生效。

PR 标题：`feat(mcp): add session tool bridge`。

## 风险

| 风险 | 在哪发现 | 处理 |
|---|---|---|
| go-sdk v1.8.0 不能应答 Claude Code 的 `server/discover` 探测 | 任务 4 回放 | 停下讨论：换版本或在转发前自己应答探测 |
| Hermes 拒绝带新 token 重发的 `session.create` | 任务 6 实测 | 按挂载被拒报错；如确实会发生，再讨论是否改成先查原生会话 |
| Claude Code 对 `--allowedTools` 的通配写法换了 | 任务 6 实测 | 改成逐个工具名列出 |
