# 0004 会话数据模型

状态：已采纳 · 2026-10-31

## 核心原则

| 数据 | 事实来源 | 用途 |
|---|---|---|
| 模型上下文 | harness 自己的存储 | resume、压缩、harness 自带记忆 |
| 事件记录 | Hoplet 的事件日志 | 界面、同步、审计、跨设备追赶 |

界面**从不**通过解析 harness 的私有文件重建（对方格式一变就坏）。触发器和入口归 Hoplet，所以 Hoplet 发起的运行都有完整记录。

## 实体关系

```mermaid
erDiagram
    HOST ||--o{ SESSION : runs
    DIRECTORY ||--o{ REPO : contains
    DIRECTORY ||--o{ SESSION : "workdir（可选）"
    SESSION ||--o{ TURN : has
    SESSION ||--o{ EVENT : "seq 单调递增"
    SESSION ||--o{ APPROVAL : raises
    SESSION ||--o{ ARTIFACT : produces
    SESSION }o--o{ SESSION : links
    TURN ||--o| CHECKPOINT_GROUP : snapshots
    DEVICE }o--|| HOST : "paired with"

    SESSION {
        id id
        enum mode "personal | code"
        json harness "kind version native_id"
        enum isolation "in_place | isolated"
        enum owner "me | delegated"
        enum origin "me | im | schedule | event"
        string status
    }
    EVENT {
        int seq
        string type
        json payload "归一化"
        ref raw "原始事件"
    }
    APPROVAL {
        string kind "命令 / 改文件 / 工具 / Hoplet 工具"
        string status
        string decided_by "设备 / Hoplet 规则"
    }
```

另有 `Receipt`（命令幂等回执）。

## 存储

```mermaid
flowchart LR
    S["shim"] -- "守护进程不在时" --> Sp["sessions/{id}/spool.jsonl"]
    Sp -- "入库：偏移量与事件同一事务" --> DB[("hoplet.db<br/>SQLite WAL")]
    D["daemon"] -- "事件 + 投影 + 回执<br/>一个事务提交" --> DB
    D -- "大载荷（工具输出、文件）" --> B[("blobs/ sha256")]
```

- 纯 Go SQLite 驱动；流式增量每 100ms 合批入库，消息完成后压缩成完整消息。
- 事件里只放摘要 + blob 引用，大内容不进事件表。

## 丢失契约

| 数据 | 持久性 |
|---|---|
| 流式增量 | `synchronous=NORMAL`，掉电最多丢最后一批 |
| 审批决定、命令回执、轮次结束、会话创建 | 落盘后才 ack |
| 写入失败 | 记住并在下一个需确认的点报出，绝不假装成功 |

## 目录布局

```
~/.hoplet/
  hoplet.db              结构化数据
  blobs/                 大载荷（内容寻址）
  sessions/<id>/         spool.jsonl、shim socket
  artifacts/<session>/   产物
  harness/<kind>/        各适配器私有数据与托管的 CLI 副本
  profile/               个人档案（见 0010）
  logs/
```

## 演进规则

- schema 迁移只向前、按编号。
- **已持久化的事件必须永远能解码**：事件类型带版本，旧解码器保留。

## 风险与兜底

Hoplet 看得到 harness 的全部活动。若我在 Hoplet 之外 resume 了同一会话，接管时对比轮次数，插入“外部活动”标记，不伪造内容。
