# M2b · PR2 跨语言向量 · 详细计划

> **执行方式：** 按任务顺序执行，每一步用 `- [ ]` 勾选；每个任务结束时 `make check` 通过。基于 PR1 合并后的 `main`。

**目标：** 由 Go 的真实实现生成确定性的向量，写入 `testdata/vectors/`，供 PR3 的 Swift 实现逐条回放。本 PR 只产出向量和 Go 侧自验，不写 Swift 的 Noise。

**验收：** `make gen` 两次输出相同；Go 测试用 `internal/noise` 回放全部向量通过；负面向量全部被拒绝；向量里只有专为测试生成的密钥。

**依据：** [M2b 总计划](m2b-overview.md)、[M2a PR1](m2a-pr1-noise.md)、[0006](../design/0006-connectivity-security.md)、[0013](../design/0013-testing-release.md)。

## 向量

| 文件 | 内容 |
|---|---|
| `noise-ik.json` | 日常握手：固定静态密钥、临时密钥、prologue，每条消息的密文，结束后的两个传输密钥 |
| `noise-ikpsk1.json` | 配对握手：另加 PSK 与首条载荷（显示名） |
| `noise-negative.json` | 错误 PSK、未知类型字节、错误 prologue、被篡改的消息 |
| `channel.json` | 通道帧：单片、恰好 65518 字节、跨片、空帧、ping、pong；重放、乱序、未知标志、超过帧上限 |
| `pairing.json` | 配对码解析（合法、前缀错误、损坏 base64，并给出解析出的过期时间；过期由调用方比较时钟，不在解析内）与主机 ID 推导 |

XN 只用于主机向中继注册，手机不发起，不出向量。续传向量需要会话订阅的场景，消费方是 PR3 的同步逻辑，随 PR3 交付。

## 改动

| 文件 | 内容 |
|---|---|
| `internal/noise/handshake.go` | `random` 字段改为导出的 `Random`，`nil` 时用 `crypto/rand`；注释写明只有向量与测试会设置 |
| `tools/vectorgen/` | 用 `internal/noise`、`internal/pair` 生成上表，随机源为固定种子 |
| `testdata/vectors/README.md` | 格式说明，声明其中的密钥仅供测试 |
| `internal/noise/vectors_test.go` | 用 Go 实现回放向量，防止向量与实现脱节 |
| `proto/doc.go` 或 `tools/vectorgen` | `//go:generate`，并入 `make gen` |

## 任务

- [ ] 导出 `Random`，现有测试改用它
- [ ] 握手向量与 Go 回放
- [ ] 通道帧向量与 Go 回放
- [ ] 配对码与主机 ID 向量
- [ ] 负面向量，逐条确认错误原因
- [ ] 接入 `make gen`，更新 0013 的现状
- [ ] `make check` 通过，并报告真实输出
