# 0022 工作区快照与累计 diff

状态：当前设计 · 更新：2026-11-04

## 范围与现状

范围：M1 的 git 基线、私有快照、累计 diff 与资源上限。

```mermaid
flowchart LR
    Files["工作树原始字节"] --> Private["Hoplet 私有 index / 对象库"]
    Private --> Baseline["创建基线"]
    Private --> Current["当前快照"]
    Baseline --> Diff["累计 diff"]
    Current --> Diff
    Diff --> Budget["响应预算与 sha"]
```

| 状态 | 边界 |
|---|---|
| ✓ 代码已有 | 私有快照、累计 diff、并发合并、响应裁剪与对象维护 |

## 仓库发现

只支持 git 仓库。会话目录下（深度 ≤ 3）每个有效的仓库各是一个条目；会话目录在某个仓库内部时，还有包含它的那个仓库（路径写成到顶层的相对路径，如 `../..`）。自己的 `.git` 无效（空目录、坏掉的 gitfile）的目录静默忽略。

## 基线与请求

- 创建会话时：先给每个仓库做快照（限时 60 秒），再创建会话（此后别的客户端才看得到它），最后把基线存入 `baselines` / `baseline_repos`。失败或超时不影响创建，原因记入基线。diff 按需请求（`sessions.diff`），不新增事件
- 客户端在 `TurnEnded` / `FileChange` 后重新请求，`sha` 覆盖响应里显示的一切（内容、分支、状态、skipped、父仓库、patch 与列表被截断的程度），不变就不重绘。基线之后出现的仓库按空树对比（`new`），消失的标 `missing`。

## 文件与对象

- Hoplet 自己的对象目录（每个仓库一个，`<home>/objects/<common git dir 路径的哈希>/`）和自己的 index（每个工作树一个，`index-<路径哈希>`，当 stat 缓存）。不读用户的 index，不写用户仓库；用户的对象只读 HEAD 的提交与树（不读文件内容）。 对象目录里没有 alternates，所以用户 `git gc` 不会弄坏旧基线。
- 文件集合 = HEAD 中跟踪的路径 ∪ 未跟踪且未被忽略的路径（`status.showUntrackedFiles=no` 时没有这一项）∪ Hoplet index 里已有的。内容按磁盘上的原始字节：不运行 filter，不做 eol / ident 编码转换；执行位只看属主位。带 filter（如 LFS）的文件只列出、不给 patch。
- 子模块和嵌入的仓库记为它们 `HEAD` 的提交（gitlink）。路径的任何一级父目录是符号链接时，该路径视为不存在，符号链接本身按符号链接记。大于 50MiB 或读不了的文件沿用之前记录的版本，列在 `skipped` 里（基线的和现在的合并）。只打开普通文件（`O_NOFOLLOW|O_NONBLOCK` 加 fstat），FIFO 之类不会卡住。
- 文件内容用 `git fast-import` 写成 pack（在 Hoplet 自己的裸仓库 `fi.git` 里运行，崩溃报告也落在那里）。每 10000 个文件或 256MiB 一轮，每轮存一次 index，所以被 60 秒上限打断的快照下次接着做。单个快照要读超过 4GiB 的仓库为 `error`（“repository too large to snapshot”）。
- 不写用户仓库的做法：去掉环境里所有 `GIT_*` 变量
- `core.hooksPath=/dev/null`
- `core.ignoreStat=false`
- 对配置里每个 filter 驱动，把 clean / smudge / process 覆盖为空命令，每条命令都带上
- `GIT_NO_LAZY_FETCH=1`、`GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0`
- `index.threads=1`
- 关闭 gc / maintenance / fsmonitor / untracked cache / split index
- 在工作目录下发现的仓库用 `GIT_CEILING_DIRECTORIES` 不让 git 越过它的上一级。直接读用户仓库的只有只读的 `rev-parse`（含子模块的 `rev-parse HEAD`）、`config`、`ls-tree HEAD`、`symbolic-ref`。

## 不支持的布局

- （该仓库为 `error`，“unsupported repository layout: …”，不做快照）：`.git` 是符号链接
- `core.worktree` 把工作树放到别处（子模块的 `core.worktree` 指向自己的目录，不算）
- partial clone
- 仓库顶层是用户主目录或其上层（dotfiles 仓库），且是被工作目录下的遍历找到、或就是会话目录本身的顶层
- 只是“包含”会话目录的主目录仓库（所有个人会话都在 `~/.hoplet` 下）被静默忽略，会话不算在那个项目里。git 认得但拒绝打开的仓库（如属主不同）同样为 `error`，带 git 的原话。

## 响应与并发

- 全部仓库按 JSON 编码合计 ≤ 6MiB：超出时先截短后面仓库的 patch（在行尾），再去掉 patch、文件列表、skipped 列表，最后只剩一个很小的条目（路径、父仓库、分支、状态、错误、sha 和 `truncated` / `files_truncated` / `skipped_truncated` 标记）。每个仓库最多 5000 个文件、全部合计 20000 个，skipped 最多 1000 条。每个会话同一时刻最多一次 diff 计算，计算进行中到达的相同请求共用下一次计算（不会拿到早于请求的结果）
- 每个请求的总时限 60 秒，从到达算起
- 没人等待时取消计算。

## 运行代价

- 磁盘：Hoplet 目录里是工作树内容的压缩拷贝，没有清理，除了：daemon 启动后第一次基线或 diff 时（只此一次）清掉 `tmp/` 和所有 `index-*.lock`
- 每次快照前清掉该工作树自己的 `<index>.lock`，以及被杀掉的导入 / repack 留下的 `pack/tmp_*`、`pack/.tmp-*`、`fi.git/fast_import_crash_*`（只在该对象目录没有别的快照或 repack 在写时；`tmp/` 不在快照前清，因为别的工作树的快照可能正在用它）
- pack 超过 50 个时在后台运行 `git repack -a -d -k -q`（该仓库没有 ref，必须带 `-k` 保留“不可达”对象；有自己的 10 分钟时限，不用请求的上下文，Service 关闭时取消并等待）。对象格式变了（同一路径的仓库被删后以另一种格式重建）就删掉该仓库的 Hoplet 目录重来。
- 时间（本机）：第一次快照 6 万个 1KiB 的文件约 5 秒（约 12000 个文件/秒），200 个 10MiB 的文件约 24 秒（约 90MB/s，受压缩限制）；之后靠 stat 缓存，6 万文件无变化的快照约 0.3 秒。
- 基线的对象目录丢了（被重建）时，该仓库为 `error`：“the baseline is no longer available (Hoplet's object store was rebuilt)”。

## 已知限制

大小写不敏感的文件系统上，已提交的只改大小写的重命名显示为重命名（新拼写加入时 Hoplet 的 index 里旧拼写被移除），工作区里未提交的看不到（与 `git status` 一致）；被忽略、`git add -f` 加入但尚未提交的文件不在文件集合里；只改换行符且文件大小和修改时间都不变（`text=auto`）的改动可能看不到；快照期间父目录被换成符号链接的极窄竞态。

## 验收

| 范围 | 条件 |
|---|---|
| 数据安全 | 快照不写用户仓库，特殊文件和不支持布局不阻塞 |
| 预算 | 响应、文件数、时间与读入限制按代码生效 |
| 并发 | 请求不复用早于自身开始的计算结果 |

## 待验证

工作区全量回收和更广泛文件系统布局需另行评估；当前已知限制保留，不据单个快照成功宣称所有布局支持。
