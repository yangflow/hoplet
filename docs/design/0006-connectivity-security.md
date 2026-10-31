# 0006 跨设备连接与安全

状态：已采纳 · 2026-10-31

前提：iPhone 常开代理 VPN（占用唯一的 VPN 位，所以 Tailscale 不做主路由）；VPS 在海外。

## 一层安全，多条路由

```mermaid
flowchart LR
    C["客户端"] == "Noise IK 加密会话<br/>（与路由无关）" ==> D["守护进程"]
    subgraph routes["路由：按序尝试"]
        R1["① 局域网直连<br/>Bonjour"]
        R2["② IPv6 直连<br/>可选，默认关"]
        R3["③ 海外中继<br/>WSS:443"]
        R4["备用：中继第二域名<br/>经 Cloudflare 代理"]
    end
```

- 连在后面的路由时，后台持续探测前面的路由，能通就切回（回家自动走局域网）。
- 加密与认证全在应用层；中继、Cloudflare、Tailscale 都只搬运字节。

## 配对与日常连接

```mermaid
sequenceDiagram
    participant M as Mac / VPS
    participant P as iPhone
    M->>M: hoplet pair：二维码<br/>主机 ID · 主机公钥 · 路由 · 一次性 PSK（10 分钟）
    P->>M: 扫码，以 PSK 完成 Noise 握手
    M->>M: 记录设备公钥
    Note over P,M: 之后每次连接
    P->>M: Noise IK，双方用已知公钥互相认证
```

| 项 | 结论 |
|---|---|
| 密钥 | 每个守护进程、每台设备一对长期 X25519 密钥 |
| 吊销 | 任一已配对设备上操作，或 `hoplet device revoke` |
| 多主机 | 每台主机各配对一次，不做信任传递；守护进程互调也走配对 |
| 防重放 / 降级 | Noise 计数器；只有一个协议版本 |

## 中继

| 项 | 结论 |
|---|---|
| 形态 | `hoplet relay`，部署在海外 VPS，哑转发，不缓存 |
| 协议 | 只用 TCP 443 上的 WSS；域名 + Let's Encrypt，流量像普通网站（国内到海外的 UDP 常被限速、干扰） |
| 认证 | 守护进程用主机密钥签名注册；客户端按主机 ID 连接 |
| 可见性 | 只见密文与元数据 |
| 保活 | 每段心跳 + 端到端回声探测 |
| 不做 | UDP / WebRTC 打洞、Cloudflare Workers 中继 |

## 代理（守护进程拿不到 shell 的代理变量）

```mermaid
flowchart LR
    Cfg["network.proxy<br/>默认读 macOS 系统代理"] --> D["daemon 出站<br/>（连中继）"]
    Cfg --> Env["注入 harness 环境<br/>HTTPS_PROXY · ALL_PROXY · NO_PROXY"]
    Env --> H["claude / codex 访问 API"]
    Pre["启动前连通性预检"] --> H
```

## 本机与终端设备

| 场景 | 措施 |
|---|---|
| 本机访问 | Unix socket 0600 + 本机 token；回环 WebSocket 拒绝带 `Origin` 的握手 |
| 推送 | 正文用设备公钥加密，iOS 通知扩展解密 |
| iOS 缓存 | 系统 Data Protection |
| 手机丢失 | 修改 Hoplet 规则与授权必须 Face ID；可选 app 启动锁；任意设备可吊销 |

## 风险与兜底

海外中继在国内的线路稳定。不稳时切 Cloudflare 备用域名；以实测延迟和丢包决定主备。
