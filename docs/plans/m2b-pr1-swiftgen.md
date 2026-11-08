# M2b · PR1 Swift 类型生成 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。基于 `98ac60e`（#49 合并后）的 `main`。

**目标：** 由 `proto/` 生成 Swift Codable 类型，放进新的 Swift 包 `apple/HopletKit`，并证明 Swift 与 Go 对同一份 JSON 的解读一致。不含 Noise、网络和界面。

**验收：** `make gen` 输出稳定；`proto/` 改动而未重新生成时 `gen-check` 失败；Swift 解码 `proto/testdata/events.jsonl` 每一行，重新编码后与原行语义相等；未知枚举值、未知事件不报错。

**依据：** [M2b 总计划](m2b-overview.md)、[0011](../design/0011-client-stack.md)、[0013](../design/0013-testing-release.md)。

## 维护者的裁决（2026-11-08）

| # | 问题 | 结论 |
|---|---|---|
| 1 | 枚举遇到新值 | 生成 `enum` 带 `case unknown(String)`：手机版本可能旧于守护进程，新值不能让整条消息解码失败；`switch` 仍然穷尽 |
| 2 | 字段缺失 | 与 Go 的 `encoding/json` 一致：缺失或 `null` 取零值，只有 Go 里是指针的字段才生成 Optional。协议规定加可选字段不升版本，旧客户端必须能读 |
| 3 | 未知事件 | `Event` 的载荷解码为 `EventPayload` 枚举，未知的类型或版本落到 `.unknown(type:v:)`，不报错；已知类型但内容损坏时抛错 |
| 4 | macOS CI | 新增 macOS 任务跑 `swift test`，只在 `apple/`、`proto/`、`tools/swiftgen/`、`testdata/vectors/` 变动时触发；现有 CI 只有 Ubuntu，生成的 Swift 没有别处验证 |

## 映射规则

| Go | Swift |
|---|---|
| `type X struct` | `public struct X: Codable, Hashable, Sendable`，带全默认值的 `init` |
| `type X string` 且有 `X` 类型常量 | 枚举，规则见裁决 1 |
| `type X string` 无常量（`SessionID` 等） | `RawRepresentable` 的单值结构体，编码为字符串 |
| `string`、`bool`、`int`、`int64`、`uint16`、`uint32`、`uint64` | `String`、`Bool`、`Int`、`Int64`、`UInt16`、`UInt32`、`UInt64` |
| `time.Time` | `Date`；解码接受 0 到 9 位小数秒，编码固定毫秒 UTC |
| `[]byte` | `Data`，base64 |
| `json.RawMessage` | `JSONValue` 枚举，手写于 `HopletKit` |
| `[]T` | `[T]`；`null` 解码为 `[]` |
| `*T` | `T?` |
| `json:"name,omitempty"` | 零值时不编码；`time.Time` 与结构体按 Go 的行为不省略 |
| `json:"-"`、未导出字段 | 跳过 |
| 字段名 | 取 JSON 名转 lowerCamelCase，Swift 关键字加反引号 |

事件载荷由 `eventType()` / `eventVersion()` 方法的返回值确定对应关系，生成 `EventPayload`。旧版本的升级函数不进 Swift：当前没有 `addLegacy` 登记；将来出现时，由守护进程在发送前升级还是 Swift 按未知处理，到时单独决定。

生成器只用标准库 `go/parser` 与 `go/ast`，不加依赖。遇到映射表之外的类型形状直接失败并报告源码位置，不静默跳过。输出按文件名和声明顺序，不含时间戳。

## 文件

| 文件 | 内容 |
|---|---|
| `tools/swiftgen/main.go` | 入口：`-proto`、`-out` |
| `tools/swiftgen/parse.go` | 读 AST，得到类型、常量、事件映射 |
| `tools/swiftgen/emit.go` | 输出 Swift |
| `tools/swiftgen/*_test.go` | 映射规则表驱动测试；不支持的形状报错；两次输出字节相同 |
| `proto/doc.go` | 加 `//go:generate` |
| `apple/HopletKit/Package.swift` | Swift 6、iOS 26、macOS 目标（仅用于 `swift test`），无第三方依赖 |
| `apple/HopletKit/Sources/HopletProto/Proto.generated.swift` | 生成结果 |
| `apple/HopletKit/Sources/HopletProto/JSONValue.swift` | `JSONValue` |
| `apple/HopletKit/Sources/HopletProto/ProtoCoding.swift` | 零值协议、线上日期编解码 |
| `apple/HopletKit/Tests/HopletProtoTests/` | Swift Testing：回放 golden、未知枚举、未知事件、缺失字段、`null` 数组 |
| `Makefile` | `swift-test` 目标：本机有 `swift` 时才跑，并入 `check` |
| `.github/workflows/ci.yml` | macOS 任务（裁决 4） |
| `docs/design/0011-client-stack.md`、`docs/design/0013-testing-release.md`、`docs/design/0019-delivery-roadmap.md` | 现状改为“生成器已有” |

## 任务

- [x] 生成器解析：结构体、字符串类型、常量、事件映射；不支持的形状报错
- [x] 生成器输出：按映射表输出，确定性测试
- [x] 建 `HopletKit`，写 `JSONValue` 与日期处理，接 `go:generate`，提交生成结果
- [x] Swift 测试回放 golden 与四类容错情况
- [x] `Makefile` 与 CI，更新设计文档现状
- [x] 改一个 `proto` 字段确认 `gen-check` 失败，改回
- [x] `make check` 通过，并报告真实输出
