# M1 · PR1 骨架与事件类型 · 详细计划

> **执行方式：** 按任务顺序逐步执行；每一步用 `- [ ]` 勾选。

**目标：** 建立 Go 模块和唯一的二进制入口，并定义所有组件共用的事件信封和事件类型，保证事件可以往返编解码，已持久化的旧版本事件永远可以解码。

**架构：** `proto/` 是协议的唯一事实来源。`Event` 是信封：会话 ID、会话内单调递增的 `Seq`、时间、轮次、类型、版本、JSON 载荷、可选的原始事件 blob 引用。每种载荷是一个 Go 类型，用 `(EventType, version)` 注册解码器；载荷接口的方法不导出，所以载荷只能在 `proto` 包里定义。`cmd/hoplet` 先只有 `version` 子命令。

**技术栈：** Go 1.27 标准库；不引入第三方依赖。

**依据：** [M1 总计划](m1-overview.md)、[0004 会话数据模型](../design/0004-session-data-model.md)（事件、演进规则）、[0003 Agent 驱动](../design/0003-agent-driving.md)（归一化事件）。

## 全局约束

- 模块路径 `github.com/yangflow/hoplet`，`go 1.27`。
- 代码、注释、commit 用英文；导出标识符都有文档注释（revive `exported` 规则会检查）。
- 已持久化的事件必须永远能解码：`proto/testdata/events.jsonl` 只增不删。
- 分支 `build/proto-events`；commit 带 DCO 签名（`git commit -s`）；完成前 `make check` 通过。

## 文件

| 文件 | 职责 |
|---|---|
| `go.mod` | 模块定义 |
| `cmd/hoplet/main.go` | 入口；`run(args, stdout, stderr) int` 便于测试 |
| `cmd/hoplet/main_test.go` | 子命令分发测试 |
| `proto/doc.go` | 包文档 |
| `proto/event.go` | `Event`、`Payload`、`BlobRef`、`NewEvent`、`Decode`、解码注册表 |
| `proto/payloads.go` | 12 种载荷及其类型常量、版本、注册 |
| `proto/event_test.go` | 往返、未知类型、注册完整性测试 |
| `proto/golden_test.go`、`proto/testdata/events.jsonl` | 旧版本事件解码的黄金样本 |

## 接口（后续 PR 依赖）

- `type SessionID string`、`type EventType string`
- `type Event struct { Session SessionID; Seq uint64; Time time.Time; Turn uint32; Type EventType; V uint16; Payload json.RawMessage; Raw *BlobRef }`
- `type Payload interface { eventType() EventType; eventVersion() uint16 }`
- `func NewEvent(session SessionID, seq uint64, turn uint32, t time.Time, p Payload) (Event, error)`
- `func (e Event) Decode() (Payload, error)`；未知类型 / 版本返回包装了 `ErrUnknownEvent` 的错误
- 载荷：`TurnStarted`、`TurnEnded{Reason}`、`MessageDelta`、`Message`、`Thinking`、`ToolCall`、`FileChange`、`ApprovalRequested`（带 `[]ApprovalOption`，透传 harness 原生选项）、`ApprovalResolved`、`Question`、`Usage`、`Error`

---

### 任务 1：Go 模块与 `hoplet version`

**文件：** 创建 `go.mod`、`cmd/hoplet/main.go`、`cmd/hoplet/main_test.go`

- [ ] **步骤 1：建分支和模块**

```bash
git switch -c build/proto-events
printf 'module github.com/yangflow/hoplet\n\ngo 1.27\n' > go.mod
```

- [ ] **步骤 2：写失败的测试** `cmd/hoplet/main_test.go`

```go
package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		code     int
		stdout   string
		inStderr string
	}{
		{"version", []string{"version"}, 0, "dev\n", ""},
		{"no args", nil, 2, "", "usage: hoplet"},
		{"unknown", []string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(c.args, &out, &errb); got != c.code {
				t.Fatalf("exit %d, want %d", got, c.code)
			}
			if out.String() != c.stdout {
				t.Fatalf("stdout %q, want %q", out.String(), c.stdout)
			}
			if !bytes.Contains(errb.Bytes(), []byte(c.inStderr)) {
				t.Fatalf("stderr %q, want it to contain %q", errb.String(), c.inStderr)
			}
		})
	}
}
```

- [ ] **步骤 3：运行，确认失败**

运行：`go test ./cmd/hoplet/`
预期：编译失败，`undefined: run`

- [ ] **步骤 4：实现** `cmd/hoplet/main.go`

```go
// Command hoplet is Hoplet's single binary: daemon, shim, relay and CLI.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hoplet <command>\n\ncommands:\n  version  print the version")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	default:
		fmt.Fprintf(stderr, "hoplet: unknown command %q\n", args[0])
		return 2
	}
}
```

- [ ] **步骤 5：运行，确认通过**

运行：`go test -race ./cmd/hoplet/`
预期：`ok`

- [ ] **步骤 6：commit**

```bash
git add go.mod cmd/hoplet
git commit -s -m "build: add Go module and hoplet version command"
```

### 任务 2：事件信封与解码注册表

**文件：** 创建 `proto/doc.go`、`proto/event.go`、`proto/payloads.go`、`proto/event_test.go`

- [ ] **步骤 1：写失败的测试** `proto/event_test.go`

```go
package proto

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestEventRoundTrip(t *testing.T) {
	ts := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	payloads := []Payload{
		TurnStarted{},
		TurnEnded{Reason: TurnCompleted},
		MessageDelta{MessageID: "m1", Text: "hel"},
		Message{ID: "m1", Role: RoleAssistant, Text: "hello"},
		Thinking{Text: "plan"},
		ToolCall{ID: "t1", Kind: ToolExec, Name: "Bash", Summary: "go test ./...", Status: ToolDone, Output: &BlobRef{SHA256: "ab", Size: 3}},
		FileChange{Path: "api/main.go", Op: FileModified},
		ApprovalRequested{ID: "a1", Kind: "command", Title: "Run command", Detail: "go test", Options: []ApprovalOption{{ID: "once", Label: "Allow once"}, {ID: "deny", Label: "Deny"}}},
		ApprovalResolved{ID: "a1", OptionID: "once", DecidedBy: "device:iphone"},
		Question{ID: "q1", Text: "Which repo?", Options: []string{"api", "web"}},
		Usage{InputTokens: 10, OutputTokens: 20},
		Error{Message: "boom"},
	}
	for _, p := range payloads {
		t.Run(string(p.eventType()), func(t *testing.T) {
			e, err := NewEvent("s1", 7, 2, ts, p)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			var back Event
			if err := json.Unmarshal(wire, &back); err != nil {
				t.Fatal(err)
			}
			got, err := back.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, p) {
				t.Fatalf("got %#v, want %#v", got, p)
			}
			if back.Seq != 7 || back.Turn != 2 || !back.Time.Equal(ts) || back.V != 1 {
				t.Fatalf("envelope changed: %+v", back)
			}
		})
	}
}

func TestDecodeUnknown(t *testing.T) {
	cases := []Event{
		{Type: "nope", V: 1, Payload: json.RawMessage(`{}`)},
		{Type: TypeMessage, V: 99, Payload: json.RawMessage(`{}`)},
	}
	for _, e := range cases {
		if _, err := e.Decode(); !errors.Is(err, ErrUnknownEvent) {
			t.Fatalf("%s v%d: got %v, want ErrUnknownEvent", e.Type, e.V, err)
		}
	}
}

func TestEveryEventTypeIsRegistered(t *testing.T) {
	for _, typ := range []EventType{
		TypeTurnStarted, TypeTurnEnded, TypeMessageDelta, TypeMessage, TypeThinking,
		TypeToolCall, TypeFileChange, TypeApprovalRequest, TypeApprovalResolved,
		TypeQuestion, TypeUsage, TypeError,
	} {
		if _, ok := decoders[decoderKey{typ, 1}]; !ok {
			t.Errorf("%s v1 has no decoder", typ)
		}
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test ./proto/`
预期：编译失败（`proto` 包还没有源文件 / `undefined: NewEvent`）

- [ ] **步骤 3：实现包文档** `proto/doc.go`

```go
// Package proto defines the types exchanged between Hoplet's daemon, shims
// and clients. It is the single source of truth for the wire protocol;
// Swift types are generated from it.
package proto
```

- [ ] **步骤 4：实现信封与注册表** `proto/event.go`

```go
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SessionID identifies a session across hosts and restarts.
type SessionID string

// EventType names the kind of an event's payload.
type EventType string

// BlobRef points at a content-addressed blob stored outside the event log.
type BlobRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Event is one entry of a session's event log. Seq increases by one per
// session and never resets. Payload holds the JSON encoding of a Payload
// whose type and version are Type and V.
type Event struct {
	Session SessionID       `json:"session"`
	Seq     uint64          `json:"seq"`
	Time    time.Time       `json:"ts"`
	Turn    uint32          `json:"turn,omitempty"`
	Type    EventType       `json:"type"`
	V       uint16          `json:"v"`
	Payload json.RawMessage `json:"payload"`
	Raw     *BlobRef        `json:"raw,omitempty"`
}

// Payload is implemented by every event payload type. Its methods are
// unexported so that payloads are defined only in this package.
type Payload interface {
	eventType() EventType
	eventVersion() uint16
}

// ErrUnknownEvent is returned when decoding an event whose type and version
// have no registered decoder.
var ErrUnknownEvent = errors.New("proto: unknown event type or version")

type decoderKey struct {
	t EventType
	v uint16
}

var decoders = map[decoderKey]func(json.RawMessage) (Payload, error){}

// register makes payload type P decodable at its current version. Older
// versions stay decodable by registering their own types under the same
// EventType with a lower eventVersion.
func register[P Payload]() {
	var zero P
	k := decoderKey{zero.eventType(), zero.eventVersion()}
	if _, dup := decoders[k]; dup {
		panic(fmt.Sprintf("proto: duplicate decoder for %s v%d", k.t, k.v))
	}
	decoders[k] = func(b json.RawMessage) (Payload, error) {
		var p P
		if err := json.Unmarshal(b, &p); err != nil {
			return nil, err
		}
		return p, nil
	}
}

// NewEvent builds an event carrying p.
func NewEvent(session SessionID, seq uint64, turn uint32, t time.Time, p Payload) (Event, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return Event{}, fmt.Errorf("proto: encode %s: %w", p.eventType(), err)
	}
	return Event{
		Session: session,
		Seq:     seq,
		Time:    t.UTC(),
		Turn:    turn,
		Type:    p.eventType(),
		V:       p.eventVersion(),
		Payload: b,
	}, nil
}

// Decode returns the event's payload as its registered Go type.
func (e Event) Decode() (Payload, error) {
	dec, ok := decoders[decoderKey{e.Type, e.V}]
	if !ok {
		return nil, fmt.Errorf("%w: %s v%d", ErrUnknownEvent, e.Type, e.V)
	}
	p, err := dec(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("proto: decode %s v%d: %w", e.Type, e.V, err)
	}
	return p, nil
}
```

- [ ] **步骤 5：实现载荷** `proto/payloads.go`

```go
package proto

// Event types. Each payload below documents its current version.
const (
	TypeTurnStarted      EventType = "turn.started"
	TypeTurnEnded        EventType = "turn.ended"
	TypeMessageDelta     EventType = "message.delta"
	TypeMessage          EventType = "message"
	TypeThinking         EventType = "thinking"
	TypeToolCall         EventType = "tool.call"
	TypeFileChange       EventType = "file.change"
	TypeApprovalRequest  EventType = "approval.requested"
	TypeApprovalResolved EventType = "approval.resolved"
	TypeQuestion         EventType = "question"
	TypeUsage            EventType = "usage"
	TypeError            EventType = "error"
)

// TurnStarted marks the start of a turn.
type TurnStarted struct{}

// TurnEndReason says why a turn ended.
type TurnEndReason string

// Turn end reasons.
const (
	TurnCompleted   TurnEndReason = "completed"
	TurnInterrupted TurnEndReason = "interrupted"
	TurnAborted     TurnEndReason = "aborted"
	TurnFailed      TurnEndReason = "error"
)

// TurnEnded marks the end of a turn. Only the harness protocol's own signal
// produces it, or a harness crash (Reason TurnAborted).
type TurnEnded struct {
	Reason TurnEndReason `json:"reason"`
}

// MessageDelta is a streamed fragment of a message.
type MessageDelta struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
}

// Role is who wrote a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a complete message; it supersedes the deltas with the same ID.
type Message struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
	Text string `json:"text"`
}

// Thinking is the harness's visible reasoning.
type Thinking struct {
	Text string `json:"text"`
}

// ToolKind groups tool calls for display.
type ToolKind string

// Tool kinds.
const (
	ToolRead  ToolKind = "read"
	ToolEdit  ToolKind = "edit"
	ToolExec  ToolKind = "exec"
	ToolWeb   ToolKind = "web"
	ToolOther ToolKind = "other"
)

// ToolStatus is the state of a tool call.
type ToolStatus string

// Tool call states.
const (
	ToolRunning ToolStatus = "running"
	ToolDone    ToolStatus = "done"
	ToolFailed  ToolStatus = "failed"
)

// ToolCall is a tool invocation. Summary is short; the full output, if any,
// lives in the blob Output points at.
type ToolCall struct {
	ID      string     `json:"id"`
	Kind    ToolKind   `json:"kind"`
	Name    string     `json:"name"`
	Summary string     `json:"summary"`
	Status  ToolStatus `json:"status"`
	Output  *BlobRef   `json:"output,omitempty"`
}

// FileOp is what happened to a file.
type FileOp string

// File operations.
const (
	FileCreated  FileOp = "created"
	FileModified FileOp = "modified"
	FileDeleted  FileOp = "deleted"
)

// FileChange reports a file the harness changed.
type FileChange struct {
	Path string `json:"path"`
	Op   FileOp `json:"op"`
}

// ApprovalOption is one answer the harness offers, passed through as is.
type ApprovalOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scope string `json:"scope,omitempty"`
}

// ApprovalRequested asks for a decision the harness's permissions require.
type ApprovalRequested struct {
	ID      string           `json:"id"`
	Kind    string           `json:"kind"`
	Title   string           `json:"title"`
	Detail  string           `json:"detail,omitempty"`
	Options []ApprovalOption `json:"options"`
}

// ApprovalResolved records the decision; DecidedBy is a device ID or a
// Hoplet rule ID. Expired is set when the harness withdrew the request.
type ApprovalResolved struct {
	ID        string `json:"id"`
	OptionID  string `json:"option_id,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
}

// Question is a question the harness asks.
type Question struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// Usage reports token use.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// Error reports a harness or adapter error that did not end the turn.
type Error struct {
	Message string `json:"message"`
}

func (TurnStarted) eventType() EventType       { return TypeTurnStarted }
func (TurnEnded) eventType() EventType         { return TypeTurnEnded }
func (MessageDelta) eventType() EventType      { return TypeMessageDelta }
func (Message) eventType() EventType           { return TypeMessage }
func (Thinking) eventType() EventType          { return TypeThinking }
func (ToolCall) eventType() EventType          { return TypeToolCall }
func (FileChange) eventType() EventType        { return TypeFileChange }
func (ApprovalRequested) eventType() EventType { return TypeApprovalRequest }
func (ApprovalResolved) eventType() EventType  { return TypeApprovalResolved }
func (Question) eventType() EventType          { return TypeQuestion }
func (Usage) eventType() EventType             { return TypeUsage }
func (Error) eventType() EventType             { return TypeError }

func (TurnStarted) eventVersion() uint16       { return 1 }
func (TurnEnded) eventVersion() uint16         { return 1 }
func (MessageDelta) eventVersion() uint16      { return 1 }
func (Message) eventVersion() uint16           { return 1 }
func (Thinking) eventVersion() uint16          { return 1 }
func (ToolCall) eventVersion() uint16          { return 1 }
func (FileChange) eventVersion() uint16        { return 1 }
func (ApprovalRequested) eventVersion() uint16 { return 1 }
func (ApprovalResolved) eventVersion() uint16  { return 1 }
func (Question) eventVersion() uint16          { return 1 }
func (Usage) eventVersion() uint16             { return 1 }
func (Error) eventVersion() uint16             { return 1 }

func init() {
	register[TurnStarted]()
	register[TurnEnded]()
	register[MessageDelta]()
	register[Message]()
	register[Thinking]()
	register[ToolCall]()
	register[FileChange]()
	register[ApprovalRequested]()
	register[ApprovalResolved]()
	register[Question]()
	register[Usage]()
	register[Error]()
}
```

- [ ] **步骤 6：运行，确认通过**

运行：`go test -race ./proto/`
预期：`ok`

- [ ] **步骤 7：commit**

```bash
git add proto
git commit -s -m "feat(proto): add event envelope and payload types"
```

### 任务 3：旧版本事件的黄金样本

**文件：** 创建 `proto/golden_test.go`、`proto/testdata/events.jsonl`

- [ ] **步骤 1：写测试** `proto/golden_test.go`

```go
package proto

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// TestGoldenEventsDecode keeps every persisted event version decodable:
// testdata/events.jsonl only ever grows, one line per type and version.
func TestGoldenEventsDecode(t *testing.T) {
	f, err := os.Open("testdata/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	n := 0
	for s.Scan() {
		n++
		var e Event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		if _, err := e.Decode(); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no golden events")
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test ./proto/ -run TestGoldenEventsDecode`
预期：FAIL，`open testdata/events.jsonl: no such file or directory`

- [ ] **步骤 3：写黄金样本** `proto/testdata/events.jsonl`（每种类型的 v1 一行；以后只追加，不修改、不删除）

```json
{"session":"s1","seq":1,"ts":"2026-10-31T08:00:00Z","turn":1,"type":"turn.started","v":1,"payload":{}}
{"session":"s1","seq":2,"ts":"2026-10-31T08:00:01Z","turn":1,"type":"message.delta","v":1,"payload":{"message_id":"m1","text":"hel"}}
{"session":"s1","seq":3,"ts":"2026-10-31T08:00:02Z","turn":1,"type":"message","v":1,"payload":{"id":"m1","role":"assistant","text":"hello"}}
{"session":"s1","seq":4,"ts":"2026-10-31T08:00:03Z","turn":1,"type":"thinking","v":1,"payload":{"text":"plan"}}
{"session":"s1","seq":5,"ts":"2026-10-31T08:00:04Z","turn":1,"type":"tool.call","v":1,"payload":{"id":"t1","kind":"exec","name":"Bash","summary":"go test","status":"done"}}
{"session":"s1","seq":6,"ts":"2026-10-31T08:00:05Z","turn":1,"type":"file.change","v":1,"payload":{"path":"a.go","op":"modified"}}
{"session":"s1","seq":7,"ts":"2026-10-31T08:00:06Z","turn":1,"type":"approval.requested","v":1,"payload":{"id":"a1","kind":"command","title":"Run command","options":[{"id":"once","label":"Allow once"}]}}
{"session":"s1","seq":8,"ts":"2026-10-31T08:00:07Z","turn":1,"type":"approval.resolved","v":1,"payload":{"id":"a1","option_id":"once","decided_by":"device:iphone"}}
{"session":"s1","seq":9,"ts":"2026-10-31T08:00:08Z","turn":1,"type":"question","v":1,"payload":{"id":"q1","text":"Which repo?"}}
{"session":"s1","seq":10,"ts":"2026-10-31T08:00:09Z","turn":1,"type":"usage","v":1,"payload":{"input_tokens":10,"output_tokens":20}}
{"session":"s1","seq":11,"ts":"2026-10-31T08:00:10Z","turn":1,"type":"error","v":1,"payload":{"message":"boom"}}
{"session":"s1","seq":12,"ts":"2026-10-31T08:00:11Z","turn":1,"type":"turn.ended","v":1,"payload":{"reason":"completed"}}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./proto/`
预期：`ok`

- [ ] **步骤 5：commit**

```bash
git add proto/golden_test.go proto/testdata
git commit -s -m "test(proto): add golden events that must always decode"
```

### 任务 4：全量检查与 PR

- [ ] **步骤 1：运行全量检查**

运行：`make check`
预期：`golangci-lint run` 输出 `0 issues.`；`go test -race ./...` 两个包都是 `ok`；`gen-check` 无输出

- [ ] **步骤 2：推送并开 PR**

```bash
git push -u origin build/proto-events
gh pr create --title "build: add Go module and protocol event types" --fill
```

预期：CI 中 `Conventional title`、`DCO sign-off`、`make check` 三项通过。

## 完成标准

- `hoplet version` 输出版本号（默认 `dev`）。
- 12 种载荷都能往返编解码；未知类型和版本返回 `ErrUnknownEvent`；黄金样本全部能解码。
- `make check` 本地和 CI 都通过。
