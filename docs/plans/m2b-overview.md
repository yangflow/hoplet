# M2b 移动端 · 总计划

**目标：** 手机经局域网或中继连上自有主机，跟进会话、断线恢复、完成审批。

**验收（交付路线）：** 手机跟进、断线恢复与审批。

**依据：** [设计总览](../README.md)，以及 [0005](../design/0005-sync-protocol.md)、[0006](../design/0006-connectivity-security.md)、[0011](../design/0011-client-stack.md)、[0013](../design/0013-testing-release.md)、[0019](../design/0019-delivery-roadmap.md)、[0026](../design/0026-ios-ui.md)。

**前置：** M2a 代码已合入；两台主机实测须在 PR4 之前完成，否则手机连不上时无法区分是哪一端的问题。

## 分步

| PR | 内容 | 需先审核的界面 | 外部前置 |
|---|---|---|---|
| PR1 | `tools/swiftgen`、`apple/HopletKit`、macOS CI | 无 | 无 |
| PR2 | Noise、通道帧、配对码、主机 ID 的跨语言向量 | 无 | 无 |
| PR3 | Swift 协议客户端：Noise、通道、`/v1` 请求与订阅、续传；续传向量随本 PR | 无 | 无 |
| PR4 | 配对二维码、Bonjour、路由切换与断线重连 | 配对、断线 | M2a 实测 |
| PR5 | 原型换真实数据，加本地缓存 | 无（已定稿） | GRDB 依赖确认 |
| PR6 | APNs、通知扩展解密、Live Activity、锁屏审批、Face ID | App 内 Face ID | Apple 开发者账号、`.p8` 密钥、真机 |
| PR7 | 产物查看 | 产物查看 | 无 |

恢复归档、分组存储、浏览主机目录是后端能力，各自单独 PR 进 `main`，在用到它的 iOS PR 之前合入。

## 全局约束

- 每个 PR 一条从最新 `main` 切出的短分支；Swift 类型只从 `main` 的 `proto/` 生成，iOS 分支不改协议。
- 依赖按 PR 确认。SwiftTerm 用于终端，属于 M3，本里程碑不引入。
- iOS 界面以 0026 为准，未绘制的状态先审核再实现。
- 代码、注释、commit 用英文；本目录下的计划用中文。
