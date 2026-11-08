# M2b · PR3 Swift 协议客户端 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [x]` 勾选。基于 PR2 合并后的 `main`。

**目标：** `apple/HopletKit` 能用 Noise 与主机建立单条连接，完成 hello、请求、订阅、心跳，并用游标续传。

**验收：** Swift 回放 PR2 全部向量与本 PR 的续传向量；内存传输层上的连接测试通过；不引入第三方依赖。

**依据：** [M2b 总计划](m2b-overview.md)、[PR2](m2b-pr2-vectors.md)、[0005](../design/0005-sync-protocol.md)、[0006](../design/0006-connectivity-security.md)、[0011](../design/0011-client-stack.md)。

## 范围

只做单条连接。多路由切换、自动重连、Bonjour 属于 PR4；`URLSessionWebSocketTask` 只有一层薄适配，连真实主机的验证放在 PR4（M2a 实测之后）。

## 结构

新 target `HopletClient`，依赖 `HopletProto` 与 CryptoKit：

| 文件 | 内容 |
|---|---|
| `NoiseHandshake.swift` | IK / IKpsk1 发起方，临时密钥可由测试注入 |
| `NoiseChannel.swift` | 分片、重组、ping / pong，纯值类型 |
| `PairingCode.swift` | 配对码解析、主机 ID |
| `Connection.swift` | `actor`：hello、请求、订阅（`AsyncThrowingStream`）、心跳 15s / 10s |
| `SyncState.swift` | `SessionLog`、`CatalogView`：把订阅项合并成本地状态，给出下次订阅的游标 |
| `WebSocketTransport.swift` | `MessageTransport` 的 URLSession 实现 |

所有待发消息在 actor 内一次加密并进入单一写队列，保证 Noise 计数和分片顺序。

## 续传向量

`resume.json` 由 `internal/daemon` 的包内测试用真实存储和追赶逻辑生成，`go test` 逐字节比对。覆盖：小缺口补发、已是最新、大缺口转快照、首次订阅、游标越界，之后都接一批实时事件；目录的首次、增量、版本越界。为此把目录订阅的首项提取成函数，行为不变。

## 任务

- [x] Noise 握手，回放 `noise-ik`、`noise-ikpsk1` 与握手负面向量
- [x] 通道，回放 `channel.json` 与通道负面向量
- [x] 配对码与主机 ID，回放 `pairing.json`
- [x] 连接：hello、请求、订阅、取消、心跳、断开
- [x] 续传状态与 `resume.json`
- [x] 更新 0011，`make check` 通过
