# M3s · PR2 辅助进程与输入工具 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。

**目标：** Code 会话的 harness 能点击、滑动、键入、按 Home，并用截图坐标验证结果；私有接口失效时，输入工具报 `input_unsupported`，其余能力不受影响。

**验收：** harness 经工具完成“装 App → 启动 → 取截图坐标 → 点击 → 截图确认变化”；探测失败或辅助进程缺失时返回 `input_unsupported`，截图、安装、启动正常；手势中途杀掉上游，设备上没有残留的按下；两个会话抢同一设备，后者得到 `simulator_busy`。

**依据：** [0028](../design/0028-ios-simulator.md)、[PR1 计划](m3s-pr1-simctl.md)。

## 关卡结果

公证通过（Accepted）的探针，Developer ID 加 hardened runtime、无例外授权，能加载 CoreSimulator 与 SimulatorKit。任务 5 再确认公证后的辅助进程实际注入触摸。

## 全局约束

- 不新增第三方依赖，不新增持久化事件。辅助进程只用系统框架和运行时加载的 Xcode 私有框架。
- 本 PR 不做帧流、面板和“人”的占用，这些属于 PR3。
- 真机验证只用**专用的新建模拟器**，结束后删除。
- Go 测试用假辅助进程，不启动真实模拟器，不用 sleep 同步，全部 `-race`；时间用注入的 `now`。
- Swift 6 严格并发，警告视为错误，`swift-format`，测试用 Swift Testing。
- 代码、标识符、注释、commit 用英文；不写本机路径、用户名和设备 UDID。

## 设计细节

### 辅助进程（`apple/SimulatorHelper`）

SwiftPM 包，产物 `hoplet-simulator-helper`。纯逻辑（协议编解码、参数校验、键码映射、手势插值）与私有接口分开：私有接口藏在 `DeviceInput` 协议后面，纯逻辑用假实现测试。

| 启动方式 | 行为 |
|---|---|
| `hoplet-simulator-helper --probe` | 加载框架、检查所需类与符号，输出一行 JSON `{ok, xcode, missing}`，退出 |
| `hoplet-simulator-helper --device <udid>` | 进入命令循环；第一行输出同样的探测结果，失败后退出 |

命令走 stdio，每行一个 JSON，请求带 `id`，响应为 `{id, ok, code?, message?}`。

| `op` | 字段 | 行为 |
|---|---|---|
| `tap` | `x`、`y`、`duration` | 比例坐标；按下，保持 `duration`，抬起 |
| `swipe` | `x`、`y`、`x2`、`y2`、`duration`、`edge` | 每 16 毫秒一个按下事件插值，最后抬起；`edge` 为 `bottom` 时所有消息带边缘参数 3 |
| `keys` | `usages` | 依次按下、抬起 HID 键码 |
| `paste` | 无 | 发送 Command+V |
| `button` | `name` | 只接受 `home` |

| 项 | 规则 |
|---|---|
| 串行 | 一个进程同时只执行一条命令，按到达顺序 |
| 所需符号 | 类 `SimServiceContext`、`_TtC12SimulatorKit24SimDeviceLegacyHIDClient`；函数 `IndigoHIDMessageForMouseNSEvent`、`IndigoHIDMessageForKeyboardArbitrary`、`IndigoHIDMessageForHIDArbitrary` |
| 开发者目录 | `xcode-select -p` 的结果，由进程自己读取，不写死 |
| 抬起保证 | 标准输入关闭、收到 SIGTERM 或单条命令超过 15 秒时，若有进行中的触摸先发抬起，再退出 |

### Go 侧（`internal/simulator`）

| 名称 | 规格 |
|---|---|
| `Helper` | 接口：`Do(ctx, req) error`、`Close() error`；生产实现启动子进程并按 `id` 配对响应 |
| `HelperStarter` | `func(ctx, udid) (Helper, error)`，测试注入假实现 |
| `Probe` | 运行 `--probe`，结果按 Xcode 路径缓存；缺文件或失败都是不可用 |
| 新方法 | `Tap`、`Swipe`、`Text`、`Button`，都先 `acquire`，操作结束重新计 60 秒 |
| 坐标 | 工具收设备点，服务按 `Device.Width/Height` 换算成比例；超出屏幕时失败 |
| 校验 | `duration` 在 0–10 秒；`edge` 只接受 `bottom`，其他返回 `unsupported`；`button` 只接受 `home` |
| `Text` | 全 ASCII 时映射 HID 键码走 `keys`；否则 `simctl pbcopy` 写入后 `paste` |
| 进程查找 | 与当前 `hoplet` 可执行文件同目录；找不到时 `InputAvailable` 为假 |
| 生命周期 | 每台设备一个进程，按需启动；空闲 60 秒由周期回收关闭标准输入。回收函数收注入的时间，可直接测试 |
| 崩溃 | 进行中的命令返回失败，不重试；下一条命令重新拉起 |
| 时限 | 单条命令 30 秒 |

## 任务

### 任务 1：辅助进程的协议与纯逻辑

- [ ] 建 `apple/SimulatorHelper` 包，接入 `make check` 的 Swift 检查（lint、警告视为错误、测试）
- [ ] 命令编解码、参数校验、键码映射、手势插值、命令循环、`DeviceInput` 协议
- [ ] 测试（假 `DeviceInput`）：每种命令的事件序列；未知 `op`；坐标越界；手势首尾与步数；命令按序执行；标准输入关闭时补发抬起；单条超时时补发抬起
- [ ] commit：`feat(simulator): add helper protocol and command loop`

### 任务 2：私有接口实现

- [ ] 动态加载、`--probe`、HID 客户端、触摸 / 键盘 / Home 消息、SIGTERM 处理
- [ ] 在专用模拟器上确认 `home` 的 HID 用法，并记入 0028 验证基线
- [ ] 真机验证：探测结果；点击位置；滑动驱动滚动；ASCII 键入；Home 回桌面；SIGTERM 时无残留按下
- [ ] commit：`feat(simulator): inject input through the helper`

### 任务 3：Go 侧进程管理与服务方法

- [ ] `Helper`、生产实现、`Probe`、回收；`Tap`、`Swipe`、`Text`、`Button`；`InputAvailable`；错误码 `input_unsupported`、`unsupported`
- [ ] 测试（假 `Helper`）：响应按 `id` 配对；未持有占用时 `simulator_busy` 且不发命令；点到比例的换算与越界；参数校验；ASCII 与非 ASCII 的路径；探测失败和文件缺失都是 `input_unsupported`；探测结果缓存；崩溃时返回失败、下一条重启；恰好 60 秒与超过 60 秒的回收边界；服务关闭时子进程退出
- [ ] commit：`feat(simulator): manage helpers and input operations`

### 任务 4：工具接线

- [ ] `simulator_tap`、`simulator_swipe`、`simulator_text`、`simulator_button`；工具说明告诉 harness 优先用没有占用者的设备，回桌面用 `simulator_button`
- [ ] 测试（tools）：Personal 会话和非 macOS 不可见；缺必填参数；未知参数被拒；`simulator_busy`、`input_unsupported`、`unsupported` 作为工具失败返回
- [ ] 测试（daemon）：假 harness 经 `hoplet mcp` 调输入工具并拿到结果
- [ ] commit：`feat(tools): add simulator input tools`

### 任务 5：真机验收与文档

- [ ] 公证一份辅助进程，用它在专用新建模拟器上走完验收，确认注入正常，结束后删除设备
- [ ] 手势中途杀掉上游，确认设备上没有残留的按下
- [ ] 移走辅助进程，确认输入工具返回 `input_unsupported`，截图、安装、启动正常
- [ ] 更新状态：0028“范围与现状”与验证基线、0012 工具表、0019 已交付内容
- [ ] 运行 `make check`，贴出真实结果
- [ ] commit：`docs: mark simulator input delivered`

## 不在本 PR

桌面打包时把辅助进程放进 app 并统一签名公证，随发布流程一起做，当前仓库还没有这条流程。本 PR 只手动验证一份公证产物。
