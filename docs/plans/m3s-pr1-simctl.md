# M3s · PR1 simctl 工具与设备占用 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。本计划按任务列出文件、接口、要测的情况与 commit，代码在实现时写，基于 `1670cad`（#46 合并后）的 `main`。

**目标：** Code 会话的 harness 经工具桥列出、启动 iOS 模拟器，安装并启动 App，截图和打开深链；输入类操作受设备占用约束。

**验收：** harness 经工具完成“启动设备 → 装 App → 启动 → 截图”；两个会话抢同一设备，后者得到 `simulator_busy`；Personal 会话和非 macOS 主机看不到工具；越出工作目录的 `app_path` 失败。

**依据：** [0028](../design/0028-ios-simulator.md)、[0012](../design/0012-orchestration-api.md)。

## 分期

| PR | 内容 | 开始前的关卡 |
|---|---|---|
| PR1（本计划） | `simctl` 工具、设备占用、工具结果带图片 | 无 |
| PR2 | Swift 辅助进程，`tap`、`swipe`、`text`、`button` | 先验证公证后的辅助进程能加载 Xcode 内的私有框架 |
| PR3 | 桌面实时面板、帧流、人的占用 | 面板在布局画布上审核通过，并同步到设计系统；`ui.Video` 帧率实测 |

PR2、PR3 的详细计划在各自关卡通过后再写。占用放在 PR1，因为 `simulator_launch` 属于输入类操作，必须持有占用。

## 全局约束

- 不新增依赖，不新增持久化事件。占用只在内存里，daemon 重启后清空。
- 只调用 `xcrun simctl` 和 `plutil`，本 PR 不碰私有框架。
- 测试用假 `Runner`，不启动真实模拟器，不用 sleep 同步，全部 `-race`；时间用注入的 `now`。
- 代码、标识符、注释、commit 用英文；不写本机路径、用户名和设备 UDID。
- 生成代码用 `make gen` 重新生成，不手改。

## 设计细节

### 包接口（`internal/simulator`）

| 名称 | 规格 |
|---|---|
| `Runner` | `Run(ctx context.Context, name string, args ...string) ([]byte, error)`；生产实现 `ExecRunner` 用 `exec.CommandContext`，错误里带 stderr |
| `Error` | `struct{ Code, Message string }`，实现 `error` |
| 错误码常量 | `CodeUnavailable = "simulator_unavailable"`、`CodeNotFound = "device_not_found"`、`CodeNotBooted = "device_not_booted"`、`CodeBusy = "simulator_busy"` |
| `Device` | `Name`、`UDID`、`State`、`Runtime`、`Width`、`Height`（设备点，未知为 0）、`Scale`、`Holder`（`proto.SessionID`，空为无）、`InputAvailable`（PR1 恒为 false） |
| `Service` | `New(r Runner, now func() time.Time, alive func(context.Context, proto.SessionID) bool) *Service` |
| 方法 | `Devices(ctx)`、`Boot(ctx, device)`、`Launch(ctx, caller, device, bundleID, appPath)`、`Screenshot(ctx, caller, device) (Shot, error)`、`OpenURL(ctx, caller, device, url)` |
| `Shot` | `PNG []byte`、`Width`、`Height`（设备点） |

### 规则

| 项 | 规则 |
|---|---|
| 设备解析 | 先按 UDID 精确匹配，再按名称；名称重名时失败并列出 UDID。省略时用 `caller` 持有的设备，没有则失败并要求指定 |
| 列表 | `simctl list -j devices`，只取 `isAvailable` 为真的设备；运行时取自外层键 |
| 启动 | `simctl bootstatus <udid> -b`，已启动时也成功；上限 120 秒 |
| 倍率 | `simctl list -j devicetypes` 取 `bundlePath`，用 `plutil -convert json -o -` 读 `Contents/Resources/capabilities.plist` 里的 `main-screen-scale`，按设备类型缓存 |
| 截图 | `simctl io <udid> screenshot --type=png <临时文件>`，解码后按整数倍率做盒式缩小，重新编码；临时目录用完删除 |
| 占用 | `acquire(caller, udid)`：空闲、已是本人、持有者 60 秒无输入、`alive(holder)` 为假时获得并刷新时间；否则 `CodeBusy`，消息写明持有会话 |
| 需要占用 | `Launch`。`Boot`、`Screenshot`、`OpenURL`、`Devices` 不需要 |
| `alive` | daemon 提供：会话存在且未归档 |
| 不可用 | `xcrun` 不存在或 `simctl` 启动失败映射为 `CodeUnavailable` |

### 工具结果

| 项 | 规则 |
|---|---|
| `proto.ToolImage` | `MIME string`（`mime`）、`Data []byte`（`data`） |
| `proto.ToolsCallResult` | 增加 `Images []ToolImage`（`images,omitempty`）；旧结果解码不变 |
| `tools.imageResult` | `struct{ Value any; Image proto.ToolImage }`；`Set.Call` 识别后，`Value` 编码进 `Text`，图片进 `Images` |
| `hoplet mcp` | `Images` 逐个转成 `mcp.ImageContent`，排在文本之后 |

## 任务

### 任务 1：确认事实并建设备层

- [x] 先在真机上确认两点，结果写进测试夹具：`plutil -convert json` 能转换 `capabilities.plist`；`main-screen-scale` 所在的键路径。转换失败时停下来提出设计修改
- [x] `internal/simulator`：`Runner`、`ExecRunner`、`Error`、`Device`、列表解析、设备解析、`Boot`
- [x] 测试（假 `Runner`）：多运行时列表解析；`isAvailable` 为假被过滤；按 UDID、按名称、名称重名、不存在；省略设备时有 / 无占用；`xcrun` 缺失映射为 `simulator_unavailable`；`Boot` 的参数
- [x] commit：`feat(simulator): list and boot simulator devices`

### 任务 2：截图与倍率

- [x] `Screenshot`、倍率读取与缓存、盒式缩小
- [x] 测试：3 倍图缩小后尺寸等于设备点，颜色为区块均值；倍率 1 时不变；倍率读不到时失败；像素尺寸不能被倍率整除时失败；临时目录在成功和失败后都被删除；未启动设备返回 `device_not_booted`
- [x] commit：`feat(simulator): capture screenshots in device points`

### 任务 3：占用、启动 App 与深链

- [x] `acquire`、`Launch`（有 `app_path` 时先 `simctl install`）、`OpenURL`；`Device.Holder` 过滤过期占用
- [x] 测试（表驱动，注入 `now`）：空闲获得；本人重入并刷新；他人持有得到 `simulator_busy` 且消息含会话 ID；恰好 60 秒与超过 60 秒的边界；持有者 `alive` 为假时可获得；`Launch` 被拒时不执行 `install`；安装失败时不执行 `launch`；并发抢占只有一个成功
- [x] commit：`feat(simulator): lease devices and launch apps`

### 任务 4：工具结果带图片

- [x] `proto`：`ToolImage`、`ToolsCallResult.Images`；`make gen`
- [x] `internal/tools`：`imageResult`；`cmd/hoplet/mcp.go`：转成 `mcp.ImageContent`
- [x] 测试：不带 `images` 的旧结果照常解码；`Set.Call` 返回文本和图片；`forward` 产出文本加图片，失败结果仍是 `IsError`；一张 1 MiB 的图片能走完 daemon 连接
- [x] commit：`feat(tools): let tool results carry images`

### 任务 5：工具与 daemon 接线

- [x] `internal/tools/simulator.go`：`simulator_list`、`simulator_boot`、`simulator_launch`、`simulator_screenshot`、`simulator_open_url`；`Source` 增加 `Simulator() Simulator`，返回 nil 表示本机不支持
- [x] `Error` 转成 `Failure`，文本以错误码开头；`app_path` 用已有的 `inside` 校验
- [x] `internal/daemon`：`darwin` 上用 `ExecRunner` 建 `Service` 并放进 `toolSource`，其他平台返回 nil；`alive` 用 `store` 判断
- [x] 测试（tools，假 `Simulator`）：Personal 会话和 `Simulator()` 为 nil 时工具不可见、硬调用得到“no tool named”；缺少必填参数；`app_path` 含 `..` 或符号链接越出目录时被拒；未知参数被拒；`simulator_busy` 作为工具失败返回
- [x] 测试（daemon）：假 harness 经 `hoplet mcp` 调 `simulator_list` 和 `simulator_screenshot`，收到文本和图片
- [x] commit：`feat(tools): add simulator tools for Code sessions`

### 任务 6：真机验收与文档

- [x] 用一台**专用的新建模拟器**手动走完验收，结束后删除；不操作其他会话可能在用的设备
- [ ] 分别确认 Claude Code 和 Hermes 收到的截图模型能看到；任何一家不能时记入 0028 待验证并停下来提出
- [x] 更新状态：0028“范围与现状”、0012 工具表（去掉“目标”）、0019 已交付内容
- [x] 运行 `make check`，贴出真实结果
- [x] commit：`docs: mark simulator tools delivered`
