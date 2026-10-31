# M1 · PR4 harness 接口与假 harness · 详细计划

> **执行方式：** 按任务顺序逐步执行；每一步用 `- [ ]` 勾选。每个任务结束时 `go test -race ./...` 和 `golangci-lint run ./...` 都应通过。

**目标：** 定义 Hoplet 驱动任意 harness 的统一接口，并提供一个可编写剧本的假 harness，让 PR5 的守护进程不依赖任何真实模型，就能稳定复现流式输出、工具调用、审批、提问、插话、中断、慢输出和中途崩溃。

**架构：** `proto` 增加会话权限模式（四档）和能力位，供客户端使用。`internal/harness` 定义 `Harness` / `StdioHarness` / `Run` 接口、归一化事件 `Event{Payload, Raw}`、按行的 `Stdio` 抽象，以及直接执行进程的 `ExecStdio`（用于探测和测试；会话在生产中经 shim 运行）。`internal/harness/fake` 是一个按 JSON 剧本运行的小二进制和驱动它的适配器，协议是 Hoplet 自定的简单 JSONL。

**与总计划的差异（裁决）：** 总计划写的是"按剧本输出 Claude stream-json 子集的假 harness"。Claude 的真实格式要到 PR6 录制后才能确认，现在模仿只能靠猜，所以假 harness 改用 Hoplet 自定的协议；Claude 格式在 PR6 用录制数据做契约测试。

**技术栈：** Go 1.27 标准库；不新增依赖。

**依据：** [M1 总计划](m1-overview.md)、[0003 Agent 驱动](../design/0003-agent-driving.md)（统一抽象、能力位、硬规则）、[0007 命令与审批](../design/0007-commands-approvals.md)（会话模式、审批选项透传）、[0013 测试](../design/0013-testing-release.md)（假 harness）。

## 全局约束

- 不新增依赖；代码、注释、commit 用英文；导出标识符有文档注释；库代码不 panic；不用 lint 抑制注释（`.golangci.yml` 中按路径和规则号的排除需写明理由）。
- 适配器遵守 0003 的硬规则：轮次结束只认协议信号，进程中途退出产生 `TurnEnded{Reason: aborted}`；Hoplet 会话 ID 是身份，harness 自己的会话 ID 只作为 `NativeSession` 指针；审批选项原样透传。
- 每个归一化事件都附带原始行（`Event.Raw`）；无法识别的行变成 `proto.Error` 事件，不丢弃。
- 假 harness 只用于测试：它的二进制由测试用 `go build` 构建，不进入 `hoplet` 二进制。
- 分支 `feat/harness`；commit 带 DCO 签名（`git commit -s`）；完成前 `make check` 通过。

## 文件

| 文件 | 职责 |
|---|---|
| `proto/harness.go`、`proto/harness_test.go` | `PermissionMode`（read_only / confirm / auto_edit / full_auto）、`Capabilities` |
| `internal/harness/harness.go` | `Harness`、`StdioHarness`、`Run`、`Spec`、`Info`、`Event`、`Stdio`、`Line`、`Launch`、`ErrUnsupported` |
| `internal/harness/execstdio.go`、`execstdio_test.go` | `StartExec`：直接执行进程并以 `Stdio` 暴露 |
| `internal/harness/fake/protocol.go` | 假 harness 的 `Op` / `Ev` / `Script` / `Turn` / `Step` |
| `internal/harness/fake/fakeharness/main.go` | 按剧本运行的假 harness 二进制 |
| `internal/harness/fake/adapter.go` | 驱动假 harness 的 `StdioHarness` 实现 |
| `internal/harness/fake/build.go` | `BuildBinary`、`WriteScript`（测试用） |
| `internal/harness/fake/fake_test.go` | 流式、审批与提问、插话与中断、崩溃、原始行与错误、探测与启动 |
| `.golangci.yml` | G204 排除扩展到 `execstdio.go`；`internal/harness/fake/` 排除 gosec（测试工具） |

## 接口（后续 PR 依赖）

- `type Spec struct { Session proto.SessionID; WorkDir string; Env []string; Mode proto.PermissionMode; Model string; Resume string }`
- `type Event struct { Payload proto.Payload; Raw []byte }`（`Payload` 为 nil 表示该行只保留原始内容）
- `type Stdio interface { WriteLine([]byte) error; Lines() <-chan Line; Close() error }`；`Line{Kind Stdout|Stderr|Exit; Data []byte; Code int}`
- `type StdioHarness interface { Kind() string; Probe(ctx) (Info, error); Launch(Spec) (Launch, error); Attach(ctx, Spec, Stdio) (Run, error) }`
- `type Run interface { Send; Steer; Interrupt; Respond(approvalID, optionID); Answer(questionID, answer); Events() <-chan Event; NativeSession() string; Err() error; Close() error }`
- `fake.Harness{Binary, Script}`、`fake.BuildBinary(ctx, dir)`、`fake.WriteScript(dir, Script)`

---

### 任务 0：建分支

- [ ] **步骤 1：**

```bash
git switch -c feat/harness
```

### 任务 1：权限模式与能力位（proto）

**文件：** 创建 `proto/harness.go`、`proto/harness_test.go`

**接口：** `PermissionMode` 及四个常量、`Capabilities`

- [ ] **步骤 1：写失败的测试**

`proto/harness_test.go`

```go
package proto

import (
	"encoding/json"
	"testing"
)

func TestCapabilitiesJSON(t *testing.T) {
	b, err := json.Marshal(Capabilities{Steer: true, PartialStream: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"steer":true,"partial_stream":true}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	var back Capabilities
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Steer || !back.PartialStream || back.Resume {
		t.Fatalf("round trip %+v", back)
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./proto/`
预期：编译失败，`undefined: Capabilities`

- [ ] **步骤 3：实现** `proto/harness.go`

```go
package proto

// PermissionMode is a session's permission level, mapped by each harness
// adapter onto that harness's native permission settings. A harness lacking
// a level uses the nearest stricter one.
type PermissionMode string

// Permission modes, from strictest to most permissive.
const (
	ModeReadOnly PermissionMode = "read_only" // read only; no file changes
	ModeConfirm  PermissionMode = "confirm"   // ask before file changes and commands
	ModeAutoEdit PermissionMode = "auto_edit" // edit inside the workdir freely; ask before commands
	ModeFullAuto PermissionMode = "full_auto" // never ask; the harness's own forced checks still surface
)

// Capabilities says which optional operations a harness supports; clients
// hide controls for the rest.
type Capabilities struct {
	Steer         bool `json:"steer,omitempty"`
	Resume        bool `json:"resume,omitempty"`
	Fork          bool `json:"fork,omitempty"`
	Rewind        bool `json:"rewind,omitempty"`
	PlanMode      bool `json:"plan_mode,omitempty"`
	PartialStream bool `json:"partial_stream,omitempty"`
	MCPInject     bool `json:"mcp_inject,omitempty"`
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./proto/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(proto): add permission modes and harness capabilities"
```

### 任务 2：harness 接口与 ExecStdio

**文件：** 创建 `internal/harness/harness.go`、`internal/harness/execstdio.go`、`internal/harness/execstdio_test.go`；修改 `.golangci.yml`

**接口：** 见上文“接口”；`StartExec(ctx, command, env, dir) (*ExecStdio, error)`

`.golangci.yml` 下面给出修改后的完整文件（含任务 3 需要的 fake 目录排除）。

- [ ] **步骤 1：写失败的测试**

`internal/harness/execstdio_test.go`

```go
package harness

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExecStdio(t *testing.T) {
	ctx := context.Background()
	e, err := StartExec(ctx, []string{"/bin/sh", "-c", "echo a; echo b >&2; read x; echo got:$x; exit 4"}, os.Environ(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.WriteLine([]byte("z")); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []string
	var exit *Line
	timeout := time.After(5 * time.Second)
	for exit == nil {
		select {
		case l, ok := <-e.Lines():
			if !ok {
				t.Fatal("lines closed before exit")
			}
			switch l.Kind {
			case Stdout:
				stdout = append(stdout, string(l.Data))
			case Stderr:
				stderr = append(stderr, string(l.Data))
			case Exit:
				exit = &l
			}
		case <-timeout:
			t.Fatal("no exit")
		}
	}
	if len(stdout) != 2 || stdout[0] != "a" || stdout[1] != "got:z" {
		t.Fatalf("stdout %q", stdout)
	}
	if len(stderr) != 1 || stderr[0] != "b" {
		t.Fatalf("stderr %q", stderr)
	}
	if exit.Code != 4 {
		t.Fatalf("exit code %d", exit.Code)
	}
	if _, ok := <-e.Lines(); ok {
		t.Fatal("lines not closed after exit")
	}
	if _, err := StartExec(ctx, nil, nil, ""); err == nil {
		t.Fatal("empty command accepted")
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/harness/`
预期：编译失败，`undefined: StartExec`

- [ ] **步骤 3：实现** `internal/harness/harness.go`

```go
// Package harness defines how Hoplet drives an agent harness (Claude Code,
// Codex, Hermes, ...) through a structured protocol. Each adapter turns the
// harness's native messages into normalized proto payloads, keeping the raw
// line, and turns Hoplet commands into native messages.
//
// Rules every adapter follows:
//   - A turn ends only on the harness protocol's own signal; a harness that
//     exits mid-turn produces proto.TurnEnded{Reason: proto.TurnAborted}.
//   - The Hoplet session ID is the identity; the harness's own session ID is
//     reported through NativeSession and kept as a pointer.
//   - Approval options are the harness's native options, passed through.
package harness

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/proto"
)

// ErrUnsupported is returned by operations the harness's Capabilities lack.
var ErrUnsupported = errors.New("harness: operation not supported")

// Info describes an installed harness.
type Info struct {
	Kind         string
	Version      string
	Capabilities proto.Capabilities
}

// Spec is what a session needs from its harness.
type Spec struct {
	Session proto.SessionID
	WorkDir string
	Env     []string // base environment; adapters add only what they need
	Mode    proto.PermissionMode
	Model   string // empty: the harness default
	Resume  string // native session ID to resume; empty starts fresh
}

// Event is one normalized event. Payload is nil for lines that carry
// nothing Hoplet shows (they are still kept raw). Raw is the native line.
type Event struct {
	Payload proto.Payload
	Raw     []byte
}

// LineKind says where a stdio line came from.
type LineKind int

// Line kinds.
const (
	Stdout LineKind = iota
	Stderr
	Exit
)

// Line is one line of a child's output, or its exit.
type Line struct {
	Kind LineKind
	Data []byte
	Code int // exit code when Kind is Exit; -1 if unknown
}

// Stdio is a line-oriented connection to a harness process: in production
// a shim client, in tests a directly executed process.
type Stdio interface {
	WriteLine(line []byte) error
	Lines() <-chan Line // closed after the Exit line or when the connection ends
	Close() error
}

// Launch is how to start a stdio harness.
type Launch struct {
	Command []string
	Env     []string
}

// Harness is one kind of harness.
type Harness interface {
	Kind() string
	Probe(ctx context.Context) (Info, error)
}

// StdioHarness is a harness spoken to over a child process's stdio.
type StdioHarness interface {
	Harness
	Launch(spec Spec) (Launch, error)
	Attach(ctx context.Context, spec Spec, io Stdio) (Run, error)
}

// Run is a live harness session.
type Run interface {
	Send(ctx context.Context, text string) error
	Steer(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	Respond(ctx context.Context, approvalID, optionID string) error
	Answer(ctx context.Context, questionID, answer string) error
	// Events delivers normalized events in order and is closed when the run
	// ends; NativeSession and Err are valid after it is closed.
	Events() <-chan Event
	NativeSession() string
	Err() error
	Close() error
}
```

- [ ] **步骤 4：实现** `internal/harness/execstdio.go`

```go
package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// ExecStdio runs a process directly and exposes its stdio as Lines. It is
// for probes and tests; sessions run through a shim instead.
type ExecStdio struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan Line
	wmu   sync.Mutex
}

// StartExec starts command in dir with env.
func StartExec(ctx context.Context, command []string, env []string, dir string) (*ExecStdio, error) {
	if len(command) == 0 {
		return nil, errors.New("harness: no command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env, cmd.Dir = env, dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("harness: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("harness: start %s: %w", command[0], err)
	}
	e := &ExecStdio{cmd: cmd, stdin: stdin, lines: make(chan Line, 64)}
	var wg sync.WaitGroup
	wg.Add(2)
	go e.read(&wg, stdout, Stdout)
	go e.read(&wg, stderr, Stderr)
	go func() {
		wg.Wait()
		code := -1
		var ee *exec.ExitError
		if err := cmd.Wait(); err == nil {
			code = 0
		} else if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		e.lines <- Line{Kind: Exit, Code: code}
		close(e.lines)
	}()
	return e, nil
}

func (e *ExecStdio) read(wg *sync.WaitGroup, r io.Reader, kind LineKind) {
	defer wg.Done()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		e.lines <- Line{Kind: kind, Data: append([]byte(nil), sc.Bytes()...)}
	}
}

// WriteLine writes line plus a newline to the process's stdin.
func (e *ExecStdio) WriteLine(line []byte) error {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	if _, err := e.stdin.Write(append(append([]byte(nil), line...), '\n')); err != nil {
		return fmt.Errorf("harness: write stdin: %w", err)
	}
	return nil
}

// Lines delivers output lines, then the Exit line, then closes.
func (e *ExecStdio) Lines() <-chan Line { return e.lines }

// Close closes stdin and kills the process group.
func (e *ExecStdio) Close() error {
	e.wmu.Lock()
	err := e.stdin.Close()
	e.wmu.Unlock()
	if e.cmd.Process != nil {
		_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL) // may already be gone
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}
```

- [ ] **步骤 5：实现** `.golangci.yml`

```yaml
version: "2"

linters:
  default: standard
  enable:
    - errorlint
    - gosec
    - misspell
    - nolintlint
    - revive
    - unconvert
    - unparam
  settings:
    revive:
      rules:
        - name: exported
        - name: context-as-argument
        - name: errorf
        - name: unused-parameter
    gosec:
      excludes:
        - G104
  exclusions:
    presets:
      - std-error-handling
    rules:
      - path: _test\.go
        linters: [gosec, unparam]
      # The shim and ExecStdio exist to run the harness command the daemon chose.
      - path: internal/(shim/shim|harness/execstdio)\.go
        linters: [gosec]
        text: G204
      # The fake harness is test tooling: it runs `go build` and reads the
      # script file a test points it at.
      - path: internal/harness/fake/
        linters: [gosec]

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/yangflow/hoplet
```

- [ ] **步骤 6：运行，确认通过**

运行：`go test -race ./internal/harness/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 7：commit**

```bash
git add -A
git commit -s -m "feat(harness): add harness interfaces and a direct-exec stdio"
```

### 任务 3：假 harness 与适配器

**文件：** 创建 `internal/harness/fake/protocol.go`、`internal/harness/fake/fakeharness/main.go`、`internal/harness/fake/adapter.go`、`internal/harness/fake/build.go`、`internal/harness/fake/fake_test.go`

**接口：** `fake.Harness`、`fake.Kind`、`BuildBinary`、`WriteScript`、`Script` / `Turn` / `Step` / `Ev` / `Op` / `Option`

- [ ] **步骤 1：写失败的测试**

`internal/harness/fake/fake_test.go`

```go
package fake

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeharness")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary, err = BuildBinary(context.Background(), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func intp(n int) *int { return &n }

// start runs the fake harness with s and returns the attached run.
func start(t *testing.T, s Script) harness.Run {
	t.Helper()
	dir := t.TempDir()
	path, err := WriteScript(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: binary, Script: path}
	spec := harness.Spec{Session: "s1", WorkDir: dir, Env: os.Environ(), Mode: proto.ModeConfirm}
	l, err := h.Launch(spec)
	if err != nil {
		t.Fatal(err)
	}
	io, err := harness.StartExec(context.Background(), l.Command, l.Env, dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Attach(context.Background(), spec, io)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// next returns the next event that has a payload.
func next(t *testing.T, r harness.Run) proto.Payload {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-r.Events():
			if !ok {
				t.Fatal("events closed")
			}
			if ev.Payload != nil {
				return ev.Payload
			}
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
}

// until collects payloads up to and including the first TurnEnded.
func until(t *testing.T, r harness.Run) []proto.Payload {
	t.Helper()
	var got []proto.Payload
	for {
		p := next(t, r)
		got = append(got, p)
		if _, ok := p.(proto.TurnEnded); ok {
			return got
		}
	}
}

func TestStreamingTurn(t *testing.T) {
	r := start(t, Script{Session: "native-1", Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "hel"}},
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "lo"}},
		{Emit: &Ev{Ev: "tool", ID: "t1", Kind: "exec", Name: "Bash", Summary: "ls", Status: "done"}},
		{Emit: &Ev{Ev: "message", ID: "m1", Text: "hello"}},
	}}}})
	if err := r.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	want := []proto.Payload{
		proto.TurnStarted{},
		proto.MessageDelta{MessageID: "m1", Text: "hel"},
		proto.MessageDelta{MessageID: "m1", Text: "lo"},
		proto.ToolCall{ID: "t1", Kind: proto.ToolExec, Name: "Bash", Summary: "ls", Status: proto.ToolDone},
		proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "hello"},
		proto.TurnEnded{Reason: proto.TurnCompleted},
	}
	if !slices.EqualFunc(got, want, func(a, b proto.Payload) bool { return fmt.Sprint(a) == fmt.Sprint(b) }) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if r.NativeSession() != "native-1" {
		t.Fatalf("native session %q", r.NativeSession())
	}
}

func TestApprovalAndQuestion(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []Option{{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "deny", Label: "Deny", Scope: "deny"}}}},
		{AwaitApproval: "a1"},
		{Emit: &Ev{Ev: "question", ID: "q1", Text: "Which repo?"}},
		{AwaitAnswer: "q1"},
	}}}})
	ctx := context.Background()
	if err := r.Send(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	req, ok := next(t, r).(proto.ApprovalRequested)
	if !ok || req.ID != "a1" || len(req.Options) != 2 || req.Options[0].Scope != proto.ScopeOnce || req.Kind != proto.ApprovalCommand {
		t.Fatalf("approval %#v", req)
	}
	if err := r.Respond(ctx, "a1", "once"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "approved:once" {
		t.Fatalf("after approval: %#v", m)
	}
	if q := next(t, r).(proto.Question); q.ID != "q1" {
		t.Fatalf("question %#v", q)
	}
	if err := r.Answer(ctx, "q1", "api"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "answered:api" {
		t.Fatalf("after answer: %#v", m)
	}
	if e := next(t, r).(proto.TurnEnded); e.Reason != proto.TurnCompleted {
		t.Fatalf("end %#v", e)
	}
}

func TestSteerAndInterruptDuringSlowOutput(t *testing.T) {
	r := start(t, Script{Turns: []Turn{
		{Steps: []Step{{SleepMS: 60_000}, {Emit: &Ev{Ev: "message", ID: "never", Text: "never"}}}},
		{Steps: []Step{{EchoPrompt: true}}},
	}})
	ctx := context.Background()
	if err := r.Send(ctx, "slow"); err != nil {
		t.Fatal(err)
	}
	next(t, r) // turn started
	if err := r.Steer(ctx, "faster"); err != nil {
		t.Fatal(err)
	}
	if m := next(t, r).(proto.Message); m.Text != "steer:faster" {
		t.Fatalf("steer: %#v", m)
	}
	begin := time.Now()
	if err := r.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if e := next(t, r).(proto.TurnEnded); e.Reason != proto.TurnInterrupted {
		t.Fatalf("interrupt: %#v", e)
	}
	if time.Since(begin) > 2*time.Second {
		t.Fatal("interrupt was not prompt")
	}
	if err := r.Send(ctx, "again"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	if m, ok := got[1].(proto.Message); !ok || m.Text != "again" {
		t.Fatalf("next turn: %#v", got)
	}
}

func TestCrashMidTurnAbortsTurn(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Emit: &Ev{Ev: "delta", ID: "m1", Text: "partial"}},
		{Exit: intp(3)},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	got := until(t, r)
	end := got[len(got)-1].(proto.TurnEnded)
	if end.Reason != proto.TurnAborted || end.Message != "harness exited with code 3" {
		t.Fatalf("end %#v", end)
	}
	for range r.Events() {
	}
	if r.Err() != nil {
		t.Fatalf("err %v", r.Err())
	}
}

func TestRawLinesAndErrors(t *testing.T) {
	r := start(t, Script{Turns: []Turn{{Steps: []Step{
		{Raw: "not json"},
		{Stderr: "warning on stderr"},
		{Emit: &Ev{Ev: "mystery"}},
	}}}})
	if err := r.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// stderr is a separate pipe, so its line may arrive after turn_ended.
	var sawStderr, ended bool
	var errs []string
	timeout := time.After(5 * time.Second)
	for !sawStderr || !ended {
		select {
		case ev := <-r.Events():
			switch p := ev.Payload.(type) {
			case nil:
				if string(ev.Raw) == "warning on stderr" {
					sawStderr = true
				}
			case proto.Error:
				errs = append(errs, p.Message)
				if len(ev.Raw) == 0 {
					t.Fatal("error event without raw line")
				}
			case proto.TurnEnded:
				ended = true
			}
		case <-timeout:
			t.Fatal("timed out")
		}
	}
	if !slices.Equal(errs, []string{"unparseable harness line", "unknown harness event mystery"}) {
		t.Fatalf("stderr %v errors %q", sawStderr, errs)
	}
	if err := r.Send(context.Background(), "extra"); err != nil {
		t.Fatal(err)
	}
	if e := next(t, r).(proto.Error); e.Message != "no more scripted turns" {
		t.Fatalf("exhausted script: %#v", e)
	}
}

func TestProbeAndLaunch(t *testing.T) {
	h := &Harness{Binary: binary, Script: "/x.json"}
	info, err := h.Probe(context.Background())
	if err != nil || info.Kind != Kind || !info.Capabilities.Steer {
		t.Fatalf("probe %+v %v", info, err)
	}
	l, err := h.Launch(harness.Spec{Env: []string{"A=1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Env, []string{"A=1", "HOPLET_FAKE_SCRIPT=/x.json"}) || l.Command[0] != binary {
		t.Fatalf("launch %+v", l)
	}
	if _, err := (&Harness{}).Launch(harness.Spec{}); err == nil {
		t.Fatal("launch without binary accepted")
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/harness/...`
预期：编译失败，`undefined: BuildBinary`

- [ ] **步骤 3：实现** `internal/harness/fake/protocol.go`

```go
// Package fake is a scriptable harness for tests: a small binary that
// follows a JSON script and speaks a simple line protocol, plus the adapter
// that drives it. It exercises everything the daemon does with a real
// harness — streaming, tool calls, approvals, questions, steering,
// interrupts, slow output and crashes — without a model.
package fake

// Op is one line Hoplet writes to the fake harness.
type Op struct {
	Op     string `json:"op"` // prompt, steer, interrupt, respond, answer
	Text   string `json:"text,omitempty"`
	ID     string `json:"id,omitempty"`
	Option string `json:"option,omitempty"`
}

// Ev is one line the fake harness writes.
type Ev struct {
	Ev      string   `json:"ev"` // session, turn_started, delta, message, thinking, tool, approval, question, turn_ended, error
	ID      string   `json:"id,omitempty"`
	Text    string   `json:"text,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Name    string   `json:"name,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Status  string   `json:"status,omitempty"`
	Title   string   `json:"title,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Options []Option `json:"options,omitempty"`
}

// Option is an approval choice the fake harness offers.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scope string `json:"scope,omitempty"`
}

// Script drives the fake harness: Session is its native session ID; each
// prompt runs the next turn.
type Script struct {
	Session string `json:"session"`
	Turns   []Turn `json:"turns"`
}

// Turn is the steps of one turn, run between turn_started and turn_ended.
type Turn struct {
	Steps []Step `json:"steps"`
}

// Step is one scripted action; exactly one field is set.
type Step struct {
	Emit          *Ev    `json:"emit,omitempty"`           // write this event
	Raw           string `json:"raw,omitempty"`            // write this line verbatim
	Stderr        string `json:"stderr,omitempty"`         // write this line to stderr
	EchoPrompt    bool   `json:"echo_prompt,omitempty"`    // write a message with the prompt's text
	SleepMS       int    `json:"sleep_ms,omitempty"`       // pause; an interrupt ends the turn early
	AwaitApproval string `json:"await_approval,omitempty"` // block until respond with this ID, then write a message "approved:<option>"
	AwaitAnswer   string `json:"await_answer,omitempty"`   // block until answer with this ID, then write a message "answered:<text>"
	Exit          *int   `json:"exit,omitempty"`           // exit immediately with this code
}
```

- [ ] **步骤 4：实现** `internal/harness/fake/fakeharness/main.go`

```go
// Command fakeharness is the scriptable harness used by tests. It reads the
// script named by HOPLET_FAKE_SCRIPT and follows it; see package fake.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness/fake"
)

func main() {
	b, err := os.ReadFile(os.Getenv("HOPLET_FAKE_SCRIPT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeharness:", err)
		os.Exit(2)
	}
	var script fake.Script
	if err := json.Unmarshal(b, &script); err != nil {
		fmt.Fprintln(os.Stderr, "fakeharness: bad script:", err)
		os.Exit(2)
	}
	h := &harness{out: json.NewEncoder(os.Stdout), ops: make(chan fake.Op)}
	h.emit(fake.Ev{Ev: "session", ID: script.Session})
	go h.readOps()
	next := 0
	for op := range h.ops {
		switch op.Op {
		case "prompt":
			if next >= len(script.Turns) {
				h.emit(fake.Ev{Ev: "error", Text: "no more scripted turns"})
				continue
			}
			h.runTurn(script.Turns[next], op.Text)
			next++
		case "steer", "interrupt", "respond", "answer":
			h.emit(fake.Ev{Ev: "error", Text: op.Op + " outside a turn"})
		}
	}
}

type harness struct {
	mu  sync.Mutex
	out *json.Encoder
	ops chan fake.Op
}

func (h *harness) emit(ev fake.Ev) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_ = h.out.Encode(ev) // stdout closing means the shim is gone; nothing to do
}

func (h *harness) raw(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Println(line)
}

func (h *harness) readOps() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var op fake.Op
		if err := json.Unmarshal(sc.Bytes(), &op); err != nil {
			h.emit(fake.Ev{Ev: "error", Text: "bad op: " + err.Error()})
			continue
		}
		h.ops <- op
	}
	close(h.ops)
}

// runTurn runs one turn's steps, handling ops that arrive meanwhile.
func (h *harness) runTurn(turn fake.Turn, prompt string) {
	h.emit(fake.Ev{Ev: "turn_started"})
	for _, st := range turn.Steps {
		switch {
		case st.Emit != nil:
			h.emit(*st.Emit)
		case st.Raw != "":
			h.raw(st.Raw)
		case st.Stderr != "":
			fmt.Fprintln(os.Stderr, st.Stderr)
		case st.EchoPrompt:
			h.emit(fake.Ev{Ev: "message", ID: "echo", Text: prompt})
		case st.Exit != nil:
			os.Exit(*st.Exit)
		case st.SleepMS > 0:
			if !h.wait(time.After(time.Duration(st.SleepMS)*time.Millisecond), nil) {
				return
			}
		case st.AwaitApproval != "":
			if !h.wait(nil, func(op fake.Op) bool {
				if op.Op == "respond" && op.ID == st.AwaitApproval {
					h.emit(fake.Ev{Ev: "message", ID: "approval-" + op.ID, Text: "approved:" + op.Option})
					return true
				}
				return false
			}) {
				return
			}
		case st.AwaitAnswer != "":
			if !h.wait(nil, func(op fake.Op) bool {
				if op.Op == "answer" && op.ID == st.AwaitAnswer {
					h.emit(fake.Ev{Ev: "message", ID: "answer-" + op.ID, Text: "answered:" + op.Text})
					return true
				}
				return false
			}) {
				return
			}
		}
	}
	h.emit(fake.Ev{Ev: "turn_ended", Reason: "completed"})
}

// wait blocks until timer fires or done accepts an op. Steers are answered
// with a message; an interrupt ends the turn and wait returns false.
func (h *harness) wait(timer <-chan time.Time, done func(fake.Op) bool) bool {
	for {
		select {
		case <-timer:
			return true
		case op, ok := <-h.ops:
			if !ok {
				os.Exit(0)
			}
			switch {
			case op.Op == "interrupt":
				h.emit(fake.Ev{Ev: "turn_ended", Reason: "interrupted"})
				return false
			case op.Op == "steer":
				h.emit(fake.Ev{Ev: "message", ID: "steer", Text: "steer:" + op.Text})
			case done != nil && done(op):
				return true
			default:
				h.emit(fake.Ev{Ev: "error", Text: "unexpected " + op.Op})
			}
		}
	}
}
```

- [ ] **步骤 5：实现** `internal/harness/fake/adapter.go`

```go
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

// Kind is the fake harness's kind.
const Kind = "fake"

// Harness drives the fakeharness binary at Binary with the script at Script.
type Harness struct {
	Binary string
	Script string
}

var _ harness.StdioHarness = (*Harness)(nil)

// Kind reports "fake".
func (h *Harness) Kind() string { return Kind }

// Probe reports the fake harness's capabilities.
func (h *Harness) Probe(context.Context) (harness.Info, error) {
	return harness.Info{
		Kind:         Kind,
		Version:      "1",
		Capabilities: proto.Capabilities{Steer: true, PartialStream: true},
	}, nil
}

// Launch returns the command and environment for the binary.
func (h *Harness) Launch(spec harness.Spec) (harness.Launch, error) {
	if h.Binary == "" || h.Script == "" {
		return harness.Launch{}, errors.New("fake: binary and script are required")
	}
	env := append(append([]string(nil), spec.Env...), "HOPLET_FAKE_SCRIPT="+h.Script)
	return harness.Launch{Command: []string{h.Binary}, Env: env}, nil
}

// Attach starts translating io's lines into events.
func (h *Harness) Attach(_ context.Context, _ harness.Spec, io harness.Stdio) (harness.Run, error) {
	r := &run{io: io, events: make(chan harness.Event, 64)}
	go r.loop()
	return r, nil
}

type run struct {
	io     harness.Stdio
	events chan harness.Event

	mu     sync.Mutex
	native string
	err    error
}

func (r *run) loop() {
	defer close(r.events)
	inTurn := false
	for line := range r.io.Lines() {
		switch line.Kind {
		case harness.Stderr:
			r.events <- harness.Event{Raw: line.Data}
		case harness.Exit:
			if inTurn {
				r.events <- harness.Event{Payload: proto.TurnEnded{
					Reason:  proto.TurnAborted,
					Message: fmt.Sprintf("harness exited with code %d", line.Code),
				}}
			}
			return
		case harness.Stdout:
			p, native := translate(line.Data)
			if native != "" {
				r.mu.Lock()
				r.native = native
				r.mu.Unlock()
			}
			switch p.(type) {
			case proto.TurnStarted:
				inTurn = true
			case proto.TurnEnded:
				inTurn = false
			}
			r.events <- harness.Event{Payload: p, Raw: line.Data}
		}
	}
	r.mu.Lock()
	r.err = errors.New("fake: stdio closed before the harness exited")
	r.mu.Unlock()
}

// translate maps one stdout line to a payload; the second result is the
// native session ID when the line announces it.
func translate(line []byte) (proto.Payload, string) {
	var ev Ev
	if err := json.Unmarshal(line, &ev); err != nil {
		return proto.Error{Message: "unparseable harness line"}, ""
	}
	switch ev.Ev {
	case "session":
		return nil, ev.ID
	case "turn_started":
		return proto.TurnStarted{}, ""
	case "turn_ended":
		return proto.TurnEnded{Reason: proto.TurnEndReason(ev.Reason)}, ""
	case "delta":
		return proto.MessageDelta{MessageID: ev.ID, Text: ev.Text}, ""
	case "message":
		return proto.Message{ID: ev.ID, Role: proto.RoleAssistant, Text: ev.Text}, ""
	case "thinking":
		return proto.Thinking{Text: ev.Text}, ""
	case "tool":
		return proto.ToolCall{ID: ev.ID, Kind: proto.ToolKind(ev.Kind), Name: ev.Name, Summary: ev.Summary, Status: proto.ToolStatus(ev.Status)}, ""
	case "approval":
		opts := make([]proto.ApprovalOption, len(ev.Options))
		for i, o := range ev.Options {
			opts[i] = proto.ApprovalOption{ID: o.ID, Label: o.Label, Scope: proto.ApprovalScope(o.Scope)}
		}
		return proto.ApprovalRequested{ID: ev.ID, Kind: proto.ApprovalKind(ev.Kind), Title: ev.Title, Detail: ev.Text, Options: opts}, ""
	case "question":
		return proto.Question{ID: ev.ID, Text: ev.Text}, ""
	case "error":
		return proto.Error{Message: ev.Text}, ""
	}
	return proto.Error{Message: "unknown harness event " + ev.Ev}, ""
}

func (r *run) send(op Op) error {
	b, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("fake: encode op: %w", err)
	}
	return r.io.WriteLine(b)
}

func (r *run) Send(_ context.Context, text string) error {
	return r.send(Op{Op: "prompt", Text: text})
}

func (r *run) Steer(_ context.Context, text string) error {
	return r.send(Op{Op: "steer", Text: text})
}

func (r *run) Interrupt(context.Context) error { return r.send(Op{Op: "interrupt"}) }

func (r *run) Respond(_ context.Context, approvalID, optionID string) error {
	return r.send(Op{Op: "respond", ID: approvalID, Option: optionID})
}

func (r *run) Answer(_ context.Context, questionID, answer string) error {
	return r.send(Op{Op: "answer", ID: questionID, Text: answer})
}

func (r *run) Events() <-chan harness.Event { return r.events }

func (r *run) NativeSession() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.native
}

func (r *run) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *run) Close() error { return r.io.Close() }
```

- [ ] **步骤 6：实现** `internal/harness/fake/build.go`

```go
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// BuildBinary compiles the fakeharness binary into dir and returns its
// path. It needs the Go toolchain and is meant for tests.
func BuildBinary(ctx context.Context, dir string) (string, error) {
	out := filepath.Join(dir, "fakeharness")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "github.com/yangflow/hoplet/internal/harness/fake/fakeharness")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("fake: build fakeharness: %w: %s", err, b)
	}
	return out, nil
}

// WriteScript writes s as JSON into dir and returns the file's path.
func WriteScript(dir string, s Script) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("fake: encode script: %w", err)
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("fake: write script: %w", err)
	}
	return path, nil
}
```

- [ ] **步骤 7：运行，确认通过**

运行：`go test -race ./internal/harness/...` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 8：commit**

```bash
git add -A
git commit -s -m "feat(harness): add a scriptable fake harness and its adapter"
```

### 任务 4：全量检查

- [ ] **步骤 1：** 运行 `make check` 和 `go test -race -count=10 ./internal/harness/...`
预期：lint `0 issues.`；所有包 `ok`；重复 10 次无偶发失败。

- [ ] **步骤 2：推送并开 PR（需维护者同意）**

```bash
git push -u origin feat/harness
gh pr create --title "feat(harness): add harness interfaces and scriptable fake harness" --fill
```

## 完成标准

- 适配器把剧本中的每种事件映射成正确的 `proto` 载荷，并保留原始行；harness 会话 ID 通过 `NativeSession` 报告。
- 审批与提问能被回应；插话立即生效；中断在慢输出期间立即结束本轮；中途退出产生 `aborted` 的轮次结束。
- 无法识别的行变成 `proto.Error`；stderr 行作为只含原始内容的事件保留。
- `make check` 本地和 CI 通过。
