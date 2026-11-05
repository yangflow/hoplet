# 0008 工作区生命周期

状态：当前设计 · 更新：2026-11-04

## 范围与现状

范围：目录发现、隔离、脚本、端口、检查点与 PR。

| 状态 | 边界 |
|---|---|
| ✓ 代码已有 | 仓库发现、原地目录、基线与累计 diff |
| ○ 目标 | 真正 worktree 隔离、PR、脚本、检查点、端口与文件接口 |

## 以会话为中心

```mermaid
flowchart LR
    S["会话"] --> W["工作目录 ~/work/product<br/>（不必是 git 仓库）"]
    W --> A["api/（git）"]
    W --> B["web/（git）"]
    W --> C["shared/（git）"]
    W --> N["notes/（非 git）"]
    B -. "package.json file:../shared" .-> C
    A -. "go.mod replace ../shared" .-> C
```

- 不是“先选一个仓库再开会话”，而是**会话选目录，仓库是目录里被发现的资源**。
- 当前扫描深度 ≤ 3，跳过 `node_modules`、`vendor`、`.git` 和符号链接目录；子模块挂在父仓库下。创建基线 / 请求 diff 时发现仓库，没有文件监听重扫。
- `go.work`、`go.mod replace`、workspaces / `file:`、Cargo path 关系推断尚未实现。

## 当前目录与隔离边界

| 项 | 代码行为 |
|---|---|
| Code | 使用传入的真实目录，累计 diff 已实现 |
| Personal | 未指定目录时在 `~/.hoplet/sessions/<id>/` 工作 |
| `Isolation` | 接受并存储枚举，但 `workDir` 未创建 worktree 或镜像；不能当作现有隔离保证 |
| 文件 / diff | 有 `sessions.diff`，没有会话文件树 / 文件内容读取 API |

## 原地 vs 隔离（规划）

| | 原地（默认） | 隔离（开会话时勾选） |
|---|---|---|
| 位置 | 真实目录 | `~/.hoplet/mirrors/<session>/`，结构与原目录一致 |
| 仓库 | 不切分支 | 每个仓库 `git worktree add --no-track -b hoplet/<slug>` |
| 非 git 文件 | — | COW 复制（APFS `clonefile`），默认不用软链（防止经软链改到原文件） |
| gitignore 文件 | — | 只复制 `[isolate].include` 列出的 |
| 冲突 | 两会话改同一仓库 → 横幅提醒，建议转隔离 | 天然隔离 |
| 归档 | — | 跑 archive 脚本 → 未提交改动做备份 commit → 移除 worktree，保留未合并分支 |

原地模式只在“创建 PR 且仓库停在默认分支”时新建 `hoplet/<slug>` 分支。

界面上这个选项是新建页的“Worktree”勾选框（Git 的术语，不译）：不勾 = 原地，勾上 = 隔离。

## 配置 `hoplet.toml`（规划）

```toml
[setup]                       # 隔离镜像创建后执行（原地可选：首次开会话时）
[archive]                     # 归档前执行
[run.web]                     # 具名运行命令
ports = ["web"]
[env]                         # 注入环境变量
[isolate]
include = [".env", "config/*.local"]
```

- 个人覆盖写在 `hoplet.local.toml`（不提交）。
- 脚本按内容 hash 记录信任：首次或内容变化时需我确认。

## 端口与环境变量（规划）

| 变量 | 含义 |
|---|---|
| `HOPLET_SESSION_ID` | 会话 ID |
| `HOPLET_WORKDIR` | 实际工作目录 |
| `HOPLET_ROOT` | 原始目录（隔离时与上者不同） |
| `HOPLET_PORT` … `+9` | 每会话分配的 10 个端口（端口池 40000–49999） |
| `HOPLET_PORT_<NAME>` | `[run.x]` 声明的具名端口 |

另按进程树把监听端口归属到会话；以后可经加密通道代理到手机预览。

## 检查点与回滚（M3）

```mermaid
sequenceDiagram
    participant D as daemon
    participant G as git（私有 index）
    participant O as Hoplet 对象目录
    participant DB as hoplet.db
    D->>G: 轮次开始：对被改动仓库快照（Hoplet 自己的 index，write-tree）
    G->>O: 对象写入 Hoplet 自己的对象目录（不读用户的 index，不写用户仓库；只读 HEAD 的提交与树）
    D->>DB: 记录 before SHA（同一轮编成一组）
    Note over D: harness 工作……
    D->>G: 轮次结束：同样快照
    D->>DB: 记录 after SHA
```

| 规则 | 说明 |
|---|---|
| 不写用户仓库 | 无 ref、无对象落进 `.git`（快照大文件会在用户仓库里堆积大量对象） |
| 范围 | 遵守 `.gitignore`；单文件 > 50MiB 跳过并标注；非 git 目录只快照被改文件到 blobs |
| 发现改动 | FileChange 事件 + FSEvents + 轮末对所有仓库 `git status` 兜底 |
| 回滚 | 同组仓库一起回退；文件被会话外修改过 → 拒绝还原并列出 |

## diff 与 PR

| diff 视图 | 范围 | 阶段 |
|---|---|---|
| 本轮改动 | 本轮 before → after | M3 |
| 会话累计 | 建会话时的基线 → 现在 | M1 |
| 分支改动 | 每个仓库 merge-base → HEAD | M3 |

刷新：M1 由客户端按需请求；以后再加 FSEvents 防抖 + 每 2 分钟补漏的推送。单个仓库的 patch ≤ 3MiB，带 sha，无变化不重绘。

```mermaid
flowchart LR
    Btn["创建 PR"] --> H["harness 按模板写 commit / PR 描述"]
    H --> P1["api 的 PR"]
    H --> P2["web 的 PR"]
    H --> P3["shared 的 PR"]
    P1 <-. 互相链接 .-> P2
    P2 <-. 互相链接 .-> P3
```

- 用我已登录的 `gh`；Hoplet 不存 token。
- 状态轮询：活跃会话 20s，其余 5 分钟；可选合并后自动归档。

## Personal 的工作区

当前默认目录是 `sessions/<id>/`。按事情组织到 `~/Hoplet/Personal/<事情>/`、复制目录并转交 Code 的行为均未实现，工作区迁移规则待定。

## 验收

| 范围 | 条件 |
|---|---|
| 当前 | 累计 diff 不修改用户 `.git`、index、ref 或对象库 |
| 隔离目标 | worktree / COW 真正生效后才能声明隔离 |
| 回滚目标 | 检查外部修改，拒绝覆盖冲突内容 |

## 待验证

能捕捉到一轮改了哪些仓库。轮末全量 `git status` 兜底；仓库多时后台进行，界面显示“正在核对”。
