# M2b · PR2 跨语言向量 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [x]` 勾选；每个任务结束时 `make check` 通过。基于 PR1 合并后的 `main`。

**目标：** 由 Go 的真实实现生成确定性的向量，写入 `testdata/vectors/`，供 PR3 的 Swift 实现逐条回放。本 PR 只产出向量和 Go 侧自验，不写 Swift 的 Noise。

**验收：** `make gen` 两次输出相同；Go 测试用 `internal/noise` 回放全部向量通过；负面向量全部被拒绝；向量里只有专为测试生成的密钥。

**依据：** [M2b 总计划](m2b-overview.md)、[M2a PR1](m2a-pr1-noise.md)、[0006](../design/0006-connectivity-security.md)、[0013](../design/0013-testing-release.md)。

## 向量

| 文件 | 内容 |
|---|---|
| `noise-ik.json` | 日常握手：固定静态密钥、临时密钥、prologue，每条消息的密文，之后每个方向一帧（能解密即证明传输密钥一致） |
| `noise-ikpsk1.json` | 配对握手：另加 PSK 与首条载荷（显示名） |
| `noise-negative.json` | 发起方必须拒绝的响应（篡改、截断、错误 prologue、错误 PSK、其他会话的响应）；通道必须拒绝的消息序列 |
| `channel.json` | 通道帧：单片、恰好 65518 字节、跨片、空帧、ping、pong；重放、乱序、未知标志、超过帧上限 |
| `pairing.json` | 配对码解析（合法、前缀错误、损坏 base64，并给出解析出的过期时间；过期由调用方比较时钟，不在解析内）与主机 ID 推导 |

XN 只用于主机向中继注册，手机不发起，不出向量。手机只做发起方，负面向量只覆盖发起方会遇到的情况，未知类型字节等响应方检查不出向量。续传向量需要会话订阅的场景，消费方是 PR3 的同步逻辑，随 PR3 交付。

## 改动

生成放在包内测试里：`go generate` 带 `-update` 写文件，平时 `go test` 重新生成并逐字节比对。这样能直接用包内的管道、随机源和密码状态，生产代码不改。

| 文件 | 内容 |
|---|---|
| `internal/noise/vectors_test.go` | 用真实握手与通道生成并比对 Noise、通道向量，负面向量逐条确认错误 |
| `internal/pair/vectors_test.go` | 用真实解析器生成并比对配对码、主机 ID 向量 |
| `internal/noise/doc.go`、`internal/pair/code.go` | `//go:generate` |
| `testdata/vectors/README.md` | 格式说明，声明其中的密钥仅供测试 |
| `docs/design/0011-client-stack.md`、`docs/design/0013-testing-release.md` | 更新现状 |

## 任务

- [x] 握手向量与 Go 回放
- [x] 通道帧向量与 Go 回放
- [x] 配对码与主机 ID 向量
- [x] 负面向量，逐条确认错误原因
- [x] 接入 `make gen`，更新 0013 的现状
- [x] `make check` 通过，并报告真实输出
