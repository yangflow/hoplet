# M2a · PR4 Linux 服务与 doctor · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `fcb07c7`（#29 合并后）的 `main`。

**目标：** 海外 Linux VPS 能长期跑 Hoplet：`hoplet daemon install` 在 Linux 上装成 systemd 用户服务，`hoplet relay install` 以 root 装成系统服务并只拿绑定 443 的能力；`hoplet doctor` 能看出身份密钥、直连监听与每个中继的注册是否正常。

**验收（M2a 总计划）：** Linux 上生成的 unit 文件 golden 一致；未开 linger 有明确提示；macOS 行为不变。

**依据：** [M2a 总计划](m2a-overview.md)、[PR3 计划](m2a-pr3-relay.md)、[0002](../design/0002-process-model.md)、[0006](../design/0006-connectivity-security.md)、[0013](../design/0013-testing-release.md)。

## 维护者的裁决（2026-11-07）

1. **中继服务用 `DynamicUser`。** `relay install` 写 `/etc/systemd/system/hoplet-relay.service`：`DynamicUser=yes`、`StateDirectory=hoplet-relay`（目录即 `/var/lib/hoplet-relay`）、`AmbientCapabilities=CAP_NET_BIND_SERVICE`，不建系统用户。root 执行 `relay allow / deny` 时，新写的白名单改归目录的属主，运行中的中继照常重读。二进制必须对其他用户可执行（如 `/usr/local/bin/hoplet`），install 检查并给出明确错误。
2. **`daemon status --json` 在 Linux 上加 `systemd` 字段。** 形如 `{"installed","unit","program","enabled","state","pid","last_exit","linger"}`；macOS 的 `launch_agent` 输出一字不变。
3. **未开 linger：照装并提示。** install 成功，stderr 说明注销后守护进程会停，给出 `sudo loginctl enable-linger <用户>`；doctor 同样警告。linger 以 `/var/lib/systemd/linger/<用户>` 是否存在判断。不代跑 sudo。
4. **doctor 的中继检查看注册状态。** 守护进程报告每个中继控制连接的现状（何时注册成功，或最近一次失败原因），doctor 不新建连接。身份密钥与直连监听一并检查。

## 全局约束

- 不新增依赖；systemd 只经 `systemctl` 命令操作，不连 D-Bus。
- 测试不碰真实的 `systemctl`、`launchctl`、`~/.config/systemd`、`/etc/systemd` 与 `/var/lib`：路径与命令都经注入；CI 在 Linux 上跑，macOS 本机也要过。
- macOS 的 LaunchAgent 行为、输出与 JSON 不变；现有测试不改断言。
- 不用 sleep 同步；全部 `-race`。

## 设计细节

### 守护进程用户服务

| 项 | 规则 |
|---|---|
| 位置 | `~/.config/systemd/user/hoplet.service`（0644，目录 0700 创建） |
| `[Unit]` | `Description=Hoplet daemon`；`StartLimitIntervalSec=0`（锁被占时一直按间隔重试，不被启动频率限制停掉） |
| `[Service]` | `ExecStart=<二进制> daemon --home <home> --log <home>/logs/daemon.log`；`Restart=on-failure`、`RestartSec=30`（同 LaunchAgent 的 30s 节奏）；`KillMode=process`：停止或重启只结束守护进程本身，shim 与 harness 按设计活过守护进程 |
| `[Install]` | `WantedBy=default.target` |
| 早期输出 | 日志器起来前的输出进 journal（`journalctl --user -u hoplet`），不另写文件 |
| 引号 | `ExecStart` 每个参数按 systemd 规则加双引号，转义 `\`、`"`，`%` 写成 `%%`，`$` 写成 `$$` |
| install | 写 unit（临时文件改名）→ `systemctl --user daemon-reload` → `enable hoplet.service` → `restart hoplet.service`（替换正在运行的守护进程，新进程接管 socket）→ 等守护进程可连，同 macOS |
| uninstall | `systemctl --user disable --now hoplet.service`（未装载不算错）→ 删 unit → `daemon-reload`；运行中的会话保留 shim |
| status | 读 unit 取 `ExecStart` 的程序；`systemctl --user show hoplet.service --property=LoadState,UnitFileState,ActiveState,SubState,MainPID,ExecMainStatus`；`enabled` 取 `UnitFileState == enabled`，`state` 为 `ActiveState`（`SubState`），`last_exit` 为 `ExecMainStatus`；`linger` 看文件 |
| linger | 没有 `/var/lib/systemd/linger/<用户>` 时，install 与 status 文本提示“注销后守护进程会停：sudo loginctl enable-linger <用户>” |
| 平台 | `daemon install / uninstall` 在 macOS 用 LaunchAgent、Linux 用 systemd，其他系统报“不支持” |

### 中继系统服务

| 项 | 规则 |
|---|---|
| 命令 | `hoplet relay install --domain NAME [--domain NAME] [--listen :443]`、`hoplet relay uninstall`；只在 Linux，需要 root（`euid == 0`），否则退出 1 并说明 |
| 位置 | `/etc/systemd/system/hoplet-relay.service`（0644） |
| `[Unit]` | `Description=Hoplet relay`；`Wants=network-online.target`、`After=network-online.target`；`StartLimitIntervalSec=0` |
| `[Service]` | `ExecStart=<二进制> relay --domain … --listen <地址> --dir /var/lib/hoplet-relay`；`DynamicUser=yes`、`StateDirectory=hoplet-relay`、`StateDirectoryMode=0700`；`AmbientCapabilities=CAP_NET_BIND_SERVICE`、`CapabilityBoundingSet=CAP_NET_BIND_SERVICE`、`NoNewPrivileges=yes`；`Restart=on-failure`、`RestartSec=5` |
| `[Install]` | `WantedBy=multi-user.target` |
| 二进制 | 拒绝临时二进制（同 `daemon install`）；解析符号链接后，二进制与它的每一级目录都要对其他用户可执行（`o+x`），否则报错并建议放到 `/usr/local/bin` |
| install | 写 unit → `systemctl daemon-reload` → `enable hoplet-relay.service` → `restart hoplet-relay.service`；打印 unit 路径、目录与下一步 `sudo hoplet relay allow <主机 ID>` |
| uninstall | `systemctl disable --now hoplet-relay.service` → 删 unit → `daemon-reload`；保留 `/var/lib/hoplet-relay`（证书与白名单） |
| 默认目录 | `hoplet relay` 与 `relay allow / deny / list` 不带 `--dir` 时：Linux 上装了中继服务（unit 文件存在）用 `/var/lib/hoplet-relay`，否则仍是 `$HOPLET_HOME/relay` |
| 属主 | 白名单写入时若进程是 root，临时文件改名前 `chown` 成所在目录的属主与组；目录由 systemd 按动态用户维护 |
| 查看 | 不另做 `relay status`：`systemctl status hoplet-relay`、`journalctl -u hoplet-relay` |

### doctor

| 检查 | 在哪 | 规则 |
|---|---|---|
| `identity` | CLI | `noise.LoadIdentity`：不存在 → warn“守护进程启动时创建”；权限过宽或损坏 → fail；否则 ok，显示主机 ID |
| `launch agent` / `systemd` | CLI | 按平台二选一（经注入的服务管理器）；systemd：未安装 → warn；已安装未启用或未运行 → fail；程序不是本二进制 → warn；无 linger → warn 带命令；否则 ok（状态、pid） |
| `listener` | 守护进程 | 配了 `network.listen`：ok“listening on <地址>”，`network.routes` 为空 → warn“配对码需要 network.routes”；没配：ok“off”。配置读不出时守护进程本就不开监听 → warn 带原因 |
| `relay <URL>` | 守护进程 | 每个中继一条：已注册 → ok“registered since <时间>”；未注册且有失败 → fail，带最近一次原因（白名单拒绝或不可达）；尚未完成第一次尝试 → warn“connecting” |

- 守护进程的 `relayer` 记录状态（加锁）：`registeredAt`、`lastErr`；`system.doctor` 经 `daemon` 传入的函数读取，不新建连接。
- 不改 `proto`：仍是 `proto.Check` 列表。

## 文件

| 文件 | 内容 |
|---|---|
| `internal/systemd/systemd.go` | `Unit` 渲染（守护进程与中继两种）、参数引号；`Manager{Scope, Path, User, LingerDir, Run}`：`Install`、`Uninstall`、`Status` |
| `internal/systemd/testdata/*.service` | golden unit 文件 |
| `internal/relay/allow.go` | root 写入时改属主 |
| `internal/daemon/relay.go`、`system.go`、`daemon.go` | 中继注册状态；`listener` 与 `relay` 检查 |
| `cmd/hoplet/service.go` | 守护进程服务的统一接口（安装、卸载、状态、doctor 检查），launchd 与 systemd 两个实现；按平台选择 |
| `cmd/hoplet/daemon.go`、`envdoctor.go` | 改用统一接口；`daemon status` 的 `systemd` 字段；`identity` 检查 |
| `cmd/hoplet/relay.go` | `relay install / uninstall`；默认目录规则 |
| `cmd/hoplet/launchctl.go` | `runCommand` 也跑 `systemctl`（同一个 lint 例外） |

## 任务

### 任务 0：分支

- [x] 从 `main` 建 `feat/linux-services`。

### 任务 1：systemd unit 与管理器

**文件：** `internal/systemd/`

- unit 渲染与引号；用户与系统两种作用域的 `systemctl` 调用；`Status` 解析 `show` 输出；linger 检测。
- **要测的情况：** 两种 unit 与 golden 一致（`-update` 重新生成）；含空格、`"`、`%`、`$` 的路径正确引号；install 的命令顺序（daemon-reload → enable → restart）；uninstall 在未装载时不报错、真实失败时保留 unit；status 解析运行中、已停止、未安装三种 `show` 输出；有无 linger 文件。

**commit：** `feat(systemd): render units and manage them with systemctl`

### 任务 2：`daemon install / uninstall / status` 支持 Linux

**文件：** `cmd/hoplet/service.go`、`daemon.go`、`main.go`、`launchctl.go`；测试 `cli_test.go`、`e2e_test.go`、`shim_test.go`

- `app.agents` 换成统一的服务接口；macOS 走原 launchd 实现，Linux 走 systemd。测试注入假的 `systemctl`（记调用、按 unit 文件是否存在回答 `show`），与现有假 `launchctl` 同一方式。
- **要测的情况：** Linux 实现下 install 写出 unit、按序调用、等到守护进程可连；无 linger 时 stderr 有 `loginctl enable-linger`；uninstall 删 unit；`status --json` 有 `systemd` 且无 `launch_agent`；现有 macOS 用例与输出不变；其他系统报不支持。

**commit：** `feat(cli): install the daemon as a systemd user service`

### 任务 3：`relay install / uninstall`

**文件：** `cmd/hoplet/relay.go`、`relay_test.go`；`internal/relay/allow.go`、`allow_test.go`

- root 检查、二进制可访问检查、默认目录规则、白名单改属主。root、`/etc/systemd/system` 与 `/var/lib/hoplet-relay` 都经 `app` 字段注入，测试不需要 root。
- **要测的情况：** 非 root 拒绝；缺 `--domain` 退出 2；二进制所在目录对其他用户不可进入时报错；unit 内容与 golden 一致、命令顺序；装了服务后 `relay allow` 默认写到服务目录；root 写入时改属主（以注入的 chown 记录验证）；uninstall 保留目录。

**commit：** `feat(relay): install the relay as a system service`

### 任务 4：doctor

**文件：** `internal/daemon/relay.go`、`system.go`、`daemon.go` 及测试；`cmd/hoplet/envdoctor.go` 及测试

- 中继注册状态；`listener`、`relay <URL>`、`identity` 检查；systemd 检查。
- **要测的情况：** 中继已注册 → ok；白名单拒绝 → fail 带原因；中继重启后回到 ok；监听开着、没开、没有路由三种；身份文件缺失 warn、权限过宽 fail；systemd 检查的各状态（含无 linger）。

**commit：** `feat(doctor): check identity, listener and relays`

### 任务 5：文档与全量检查

- [x] 同步 0002（Linux 服务安装落地、`KillMode=process`、linger）、0006（中继服务与 doctor 检查）、0013（doctor 项）、`README.md` 与 `README.zh-CN.md` 的安装说明；实现与本文有出入时先改文档。
- [x] `make check`，报告真实输出。
- [x] 在本文末尾追加“实现与计划的差异”。
- [x] 推送并开 PR：`feat(cli): install daemon and relay as Linux services`。

## 完成标准

- 总计划 PR4 的验收项全部有对应测试。
- macOS 的安装、状态与 doctor 输出不变。
- 不新增依赖；测试不需要 root、不碰真实的 systemd。
- 两台主机实测（Mac 与 VPS，经中继）在本 PR 合并后进行，不属于本 PR。

## 实现与计划的差异

- `relay install` 不提供 `--json`：它只在 VPS 上由人执行一次，输出 unit 路径、域名与监听地址、目录和下一步命令即可。
- `listener` 检查在 ok 时同时列出监听地址与 `network.routes`，便于核对对外地址；未配置监听的 detail 写明 `network.listen is not set`。
- 中继注册失败的 detail 附白名单提示（`hoplet relay allow <id>`），因为白名单外是首次部署最常见的失败原因。
- doctor 中 `listener` 与各 `relay <URL>` 排在 `git` 之后、`network` 之前，先看本机能否被连到，再看出站。
- root 写白名单后改属主由 `TestMatchOwner` 直接测试（以文件属主比对），不另注入 chown 记录。
