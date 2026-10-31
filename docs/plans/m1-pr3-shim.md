# M1 · PR3 shim · 详细计划

> **执行方式：** 按任务顺序逐步执行；每一步用 `- [ ]` 勾选。每个任务结束时 `go test -race ./...` 和 `golangci-lint run ./...` 都应通过。

**目标：** 实现 `hoplet shim`：每个活跃会话一个的小进程，托管 harness 子进程的 stdio，把子进程的每一行输出、写给它的每一行输入和退出状态追加到 spool 文件，并在 Unix socket 上按字节偏移续传。守护进程重启后从上次提交的偏移重连，不丢、不重，子进程感知不到。

**架构：** `internal/shim` 分四块：帧编解码（4 字节大端长度 + JSON）、spool（只追加的 JSONL，退出记录强制落盘，读取时跳过尾部不完整的行）、shim 主体（启动子进程并放进独立进程组、两个输出泵、退出记录、同一时间只服务一个客户端，新连接顶替旧连接）、客户端 `Dial`。客户端先发 `hello{v, off}`，版本不符或偏移越界就回 `error` 并关闭。子进程退出后，客户端确认（`ack`）读到退出记录之后的偏移，shim 才退出；没人确认时等待 `linger`（默认 24 小时）后退出。`cmd/hoplet` 增加 `shim` 子命令。

**技术栈：** Go 1.27 标准库；不新增依赖。

**依据：** [M1 总计划](m1-overview.md)（PR3 与关键假设）、[0002 进程模型](../design/0002-process-model.md)（每会话一个 shim、守护进程重启时会发生什么）、[0004 会话数据模型](../design/0004-session-data-model.md)（spool 与入库偏移）。

## 全局约束

- 不新增依赖；代码、注释、commit 用英文；导出标识符有文档注释；库代码不 panic；不用 lint 抑制注释（`.golangci.yml` 中按路径和规则号的排除需写明理由）。
- 协议带版本号 `ProtocolVersion = 1`：shim 比守护进程活得久，守护进程必须能和仍在运行的每个 shim 对话。
- 子进程放进独立进程组（`Setpgid`），信号发给整个进程组。
- spool 的普通追加不强制落盘，退出记录强制落盘；读取方永远不返回尾部不完整的行。
- Unix socket 路径在 macOS 上限 104 字节：测试用 `os.MkdirTemp("", "shim")` 而不是 `t.TempDir()`。
- 分支 `feat/shim`；commit 带 DCO 签名（`git commit -s`）；完成前 `make check` 通过。

## 文件

| 文件 | 职责 |
|---|---|
| `internal/shim/doc.go` | 包文档：shim 的职责与线协议 |
| `internal/shim/frame.go` | `writeFrame` / `readFrame`，`MaxFrame`、`ErrFrameTooLarge` |
| `internal/shim/protocol.go` | `ProtocolVersion`、`Record` / `RecordKind`、`Message` / `MessageType` |
| `internal/shim/spool.go` | `SpoolName`、`SpoolPath`、spool 追加与通知、`readRecords` |
| `internal/shim/shim.go` | `Config`、`Run`、`SocketName`、`SocketPath`、`DefaultLinger`；监听、启动子进程、输出泵、退出 |
| `internal/shim/serve.go` | 单连接处理：hello、从偏移流式发送、stdin / close_stdin / signal / ack |
| `internal/shim/client.go` | `Dial`、`Client.Recv` / `Stdin` / `CloseStdin` / `Signal` / `Ack` / `Close` / `Pid` |
| `internal/shim/*_test.go` | 帧、spool、基本流程、续传与替换等测试；`helper_test.go` 让测试二进制充当子进程 |
| `cmd/hoplet/main.go`、`cmd/hoplet/shim_test.go` | `hoplet shim --dir DIR [--linger D] -- COMMAND [ARGS...]` |
| `.golangci.yml` | 对 `internal/shim/shim.go` 排除 gosec G204（shim 的职责就是运行守护进程选定的命令） |

## 接口（后续 PR 依赖）

- `type Config struct { Dir string; Command []string; Env []string; Linger time.Duration }`；`func Run(ctx context.Context, cfg Config) error`
- `func Dial(ctx context.Context, dir string, offset int64) (*Client, error)`
- `(*Client).Recv() (Record, int64, error)`：返回记录和它之后的 spool 偏移；shim 发来的 error 帧作为 error 返回，连接仍可用
- `(*Client).Stdin(line string) error`、`CloseStdin() error`、`Signal(name string) error`（INT / TERM / HUP / KILL）、`Ack(off int64) error`、`Close() error`、`Pid() int`
- `type Record struct { Kind RecordKind; Data string; Pid int; Code int; Signal string }`；种类 `start` / `stdout` / `stderr` / `stdin` / `exit`
- `SocketPath(dir)`、`SpoolPath(dir)`、`ProtocolVersion`、`DefaultLinger`

---

### 任务 0：建分支

- [ ] **步骤 1：**

```bash
git switch -c feat/shim
```

### 任务 1：帧与协议类型

**文件：** 创建 `internal/shim/doc.go`、`internal/shim/frame.go`、`internal/shim/protocol.go`、`internal/shim/frame_test.go`

**接口：** `writeFrame`、`readFrame`、`MaxFrame`、`ErrFrameTooLarge`、`ProtocolVersion`、`Record`、`Message`

- [ ] **步骤 1：写失败的测试**

`internal/shim/frame_test.go`

```go
package shim

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	in := Message{Type: MsgRecord, Offset: 42, Record: &Record{Kind: KindStdout, Data: "x"}}
	if err := writeFrame(&buf, in); err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := readFrame(&buf, &out); err != nil {
		t.Fatal(err)
	}
	if out.Offset != 42 || out.Record == nil || out.Record.Data != "x" {
		t.Fatalf("round trip %+v", out)
	}
	huge := []byte{0xff, 0xff, 0xff, 0xff}
	if err := readFrame(bytes.NewReader(huge), &out); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized frame: %v", err)
	}
	if err := readFrame(bytes.NewReader([]byte{0, 0, 0, 5, '{'}), &out); err == nil {
		t.Fatal("truncated frame accepted")
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/shim/`
预期：编译失败，`undefined: Message`

- [ ] **步骤 3：实现** `internal/shim/doc.go`

```go
// Package shim runs one session's harness process and outlives the daemon.
//
// The shim owns the child's stdio. Every line the child writes, every line
// written to it and its exit are appended to a spool file, so a daemon that
// restarts reconnects over the shim's Unix socket and resumes from the
// byte offset it last committed: nothing is lost and nothing is repeated,
// and the child never notices the daemon went away.
//
// Wire format: each frame is a 4-byte big-endian length followed by a JSON
// message. The client opens with Hello carrying ProtocolVersion; a shim
// speaking another version answers with an error message and closes.
package shim
```

- [ ] **步骤 4：实现** `internal/shim/protocol.go`

```go
package shim

// ProtocolVersion is the shim protocol version. Shims outlive daemons, so a
// daemon must speak the version of every shim still running.
const ProtocolVersion = 1

// RecordKind says what a spool record holds.
type RecordKind string

// Record kinds.
const (
	KindStart  RecordKind = "start"  // the child started; Pid is set
	KindStdout RecordKind = "stdout" // one line the child wrote to stdout, without the newline
	KindStderr RecordKind = "stderr" // one line the child wrote to stderr
	KindStdin  RecordKind = "stdin"  // one line written to the child's stdin
	KindExit   RecordKind = "exit"   // the child exited; Code or Signal is set
)

// Record is one spool line.
type Record struct {
	Kind   RecordKind `json:"k"`
	Data   string     `json:"d,omitempty"`
	Pid    int        `json:"pid,omitempty"`
	Code   int        `json:"code,omitempty"`
	Signal string     `json:"signal,omitempty"`
}

// MessageType names a frame.
type MessageType string

// Frames. Client → shim: hello, stdin, close_stdin, signal, ack.
// Shim → client: welcome, record, error.
const (
	MsgHello      MessageType = "hello"
	MsgStdin      MessageType = "stdin"
	MsgCloseStdin MessageType = "close_stdin"
	MsgSignal     MessageType = "signal"
	MsgAck        MessageType = "ack"
	MsgWelcome    MessageType = "welcome"
	MsgRecord     MessageType = "record"
	MsgError      MessageType = "error"
)

// Message is one frame. Offset is the spool byte offset: in hello, where to
// resume; in record, the offset just after that record; in ack, how far the
// client has durably consumed.
type Message struct {
	Type    MessageType `json:"type"`
	Version int         `json:"v,omitempty"`
	Offset  int64       `json:"off,omitempty"`
	Data    string      `json:"d,omitempty"`
	Signal  string      `json:"signal,omitempty"`
	Pid     int         `json:"pid,omitempty"`
	Record  *Record     `json:"rec,omitempty"`
	Error   string      `json:"error,omitempty"`
}
```

- [ ] **步骤 5：实现** `internal/shim/frame.go`

```go
package shim

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrame bounds one frame's payload.
const MaxFrame = 16 << 20

// ErrFrameTooLarge is returned for frames over MaxFrame.
var ErrFrameTooLarge = errors.New("shim: frame too large")

// writeFrame encodes v as one length-prefixed JSON frame.
func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("shim: encode frame: %w", err)
	}
	n := len(b)
	if n > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4, 4+n)
	// n <= MaxFrame, so the mask never changes the value; it states the bound.
	binary.BigEndian.PutUint32(buf, uint32(n)&0xffffffff)
	buf = append(buf, b...)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("shim: write frame: %w", err)
	}
	return nil
}

// readFrame decodes one length-prefixed JSON frame into v. It returns
// io.EOF only when the stream ends cleanly between frames.
func readFrame(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return ErrFrameTooLarge
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return fmt.Errorf("shim: read frame: %w", io.ErrUnexpectedEOF)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("shim: decode frame: %w", err)
	}
	return nil
}
```

- [ ] **步骤 6：运行，确认通过**

运行：`go test -race ./internal/shim/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 7：commit**

```bash
git add -A
git commit -s -m "feat(shim): add frame codec and protocol types"
```

### 任务 2：spool

**文件：** 创建 `internal/shim/spool.go`、`internal/shim/spool_test.go`

**接口：** `SpoolName`、`SpoolPath`、`openSpool`、`(*spool).append` / `state` / `close`、`readRecords`

- [ ] **步骤 1：写失败的测试**

`internal/shim/spool_test.go`

```go
package shim

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReadRecordsSkipsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	content := `{"k":"stdout","d":"a"}` + "\n" + `{"k":"stdout","d":"b"}` + "\n" + `{"k":"std`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	var ends []int64
	err := readRecords(path, 0, int64(len(content)), func(r Record, end int64) error {
		got = append(got, r.Data)
		ends = append(ends, end)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b"}) || ends[1] != int64(strings.LastIndex(content, "\n")+1) {
		t.Fatalf("records %q ends %v", got, ends)
	}
}

func TestSpoolAppendNotifyAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	size, notify := sp.state()
	if size != 0 {
		t.Fatalf("new spool size %d", size)
	}
	first, err := sp.append(Record{Kind: KindStart, Pid: 7})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-notify:
	default:
		t.Fatal("append did not notify")
	}
	end, err := sp.append(Record{Kind: KindExit, Code: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if size, _ := reopened.state(); size != end {
		t.Fatalf("reopened size %d, want %d", size, end)
	}
	var kinds []RecordKind
	err = readRecords(path, first, end, func(r Record, _ int64) error {
		kinds = append(kinds, r.Kind)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []RecordKind{KindExit}) {
		t.Fatalf("records after first: %v", kinds)
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/shim/`
预期：编译失败，`undefined: SpoolName`

- [ ] **步骤 3：实现** `internal/shim/spool.go`

```go
package shim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// SpoolName is the spool file inside a session directory.
const SpoolName = "spool.jsonl"

// SpoolPath returns the spool file inside dir.
func SpoolPath(dir string) string { return filepath.Join(dir, SpoolName) }

// spool is the append-only record log. Appends are buffered by the OS; the
// exit record is synced so a finished session's spool is complete on disk.
type spool struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	size   int64
	notify chan struct{} // closed and replaced on every append
}

func openSpool(path string) (*spool, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("shim: open spool: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("shim: stat spool: %w", err), f.Close())
	}
	return &spool{f: f, path: path, size: st.Size(), notify: make(chan struct{})}, nil
}

// append writes one record and returns the offset just after it.
func (s *spool) append(rec Record) (int64, error) {
	b, err := json.Marshal(rec)
	if err != nil {
		return 0, fmt.Errorf("shim: encode record: %w", err)
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.f.Write(b); err != nil {
		return 0, fmt.Errorf("shim: append record: %w", err)
	}
	if rec.Kind == KindExit {
		if err := s.f.Sync(); err != nil {
			return 0, fmt.Errorf("shim: sync spool: %w", err)
		}
	}
	s.size += int64(len(b))
	close(s.notify)
	s.notify = make(chan struct{})
	return s.size, nil
}

// state returns the current size and a channel closed by the next append.
func (s *spool) state() (int64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size, s.notify
}

func (s *spool) close() error { return s.f.Close() }

// readRecords reads complete records from off up to limit (exclusive) and
// calls fn with each record and the offset just after it. A trailing
// partial line is never returned.
func readRecords(path string, off, limit int64, fn func(Record, int64) error) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("shim: open spool for reading: %w", err)
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fmt.Errorf("shim: seek spool: %w", err)
	}
	r := bufio.NewReaderSize(io.LimitReader(f, limit-off), 64<<10)
	pos := off
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil // a trailing partial line is not a record yet
		}
		if err != nil {
			return fmt.Errorf("shim: read spool: %w", err)
		}
		pos += int64(len(line))
		var rec Record
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &rec); err != nil {
			return fmt.Errorf("shim: decode spool record at %d: %w", pos-int64(len(line)), err)
		}
		if err := fn(rec, pos); err != nil {
			return err
		}
	}
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./internal/shim/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(shim): add append-only spool with partial-line safe reads"
```

### 任务 3：shim 主体与客户端

**文件：** 创建 `internal/shim/shim.go`、`internal/shim/serve.go`、`internal/shim/client.go`、`internal/shim/helper_test.go`、`internal/shim/shim_test.go`；修改 `.golangci.yml`

**接口：** `Config`、`Run`、`SocketPath`、`DefaultLinger`、`Dial`、`Client`；测试辅助 `startShim`、`dial`、`recvUntil`、`waitDone`、`shortDir`

`.golangci.yml` 是在现有文件的 `exclusions.rules` 下追加一条规则，下面给出修改后的完整文件。

- [ ] **步骤 1：写失败的测试**

`internal/shim/helper_test.go`

```go
package shim

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMain lets the test binary act as the shim's child: when
// HOPLET_SHIM_HELPER is set it runs helperMain instead of the tests.
func TestMain(m *testing.M) {
	if os.Getenv("HOPLET_SHIM_HELPER") != "" {
		helperMain(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

// helperMain modes:
//
//	echo        echo stdin lines as "echo:<line>"; "err" writes to stderr; "quit" exits 3
//	tick N MS   print "tick <i>" N times, MS milliseconds apart, then exit 0
//	sleep       block until killed
func helperMain(args []string) {
	switch args[0] {
	case "echo":
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			switch line := sc.Text(); line {
			case "quit":
				os.Exit(3)
			case "err":
				fmt.Fprintln(os.Stderr, "oops")
			default:
				fmt.Println("echo:" + line)
			}
		}
		os.Exit(0)
	case "tick":
		n, _ := strconv.Atoi(args[1])
		ms, _ := strconv.Atoi(args[2])
		for i := range n {
			fmt.Printf("tick %d\n", i)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		os.Exit(0)
	case "sleep":
		select {}
	}
	os.Exit(2)
}

// shortDir returns a temp dir short enough for a Unix socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "shim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// startShim runs a shim whose child is this test binary in the given mode.
// It returns the dir and a channel carrying Run's result.
func startShim(ctx context.Context, t *testing.T, linger time.Duration, mode ...string) (string, <-chan error) {
	t.Helper()
	dir := shortDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Dir:     dir,
		Command: append([]string{exe}, mode...),
		Env:     append(os.Environ(), "HOPLET_SHIM_HELPER=1"),
		Linger:  linger,
	}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(SocketPath(dir)); err == nil {
			return dir, done
		}
		if time.Now().After(deadline) {
			t.Fatal("shim socket never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func dial(t *testing.T, dir string, off int64) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, dir, off)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// recvUntil reads records until pred matches one, returning all records
// read and the offset after the matching one.
func recvUntil(t *testing.T, c *Client, pred func(Record) bool) ([]Record, int64) {
	t.Helper()
	var got []Record
	for {
		rec, off, err := c.Recv()
		if err != nil {
			t.Fatalf("recv after %v: %v", got, err)
		}
		got = append(got, rec)
		if pred(rec) {
			return got, off
		}
	}
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shim did not exit")
	}
}
```

`internal/shim/shim_test.go`

```go
package shim

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func isExit(r Record) bool { return r.Kind == KindExit }

func TestEchoStdinStderrAndExit(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "echo")
	c := dial(t, dir, 0)
	if c.Pid() == 0 {
		t.Fatal("welcome without pid")
	}
	for _, line := range []string{"hello", "err", "quit"} {
		if err := c.Stdin(line); err != nil {
			t.Fatal(err)
		}
	}
	got, final := recvUntil(t, c, isExit)
	want := map[RecordKind][]string{
		KindStdin:  {"hello", "err", "quit"},
		KindStdout: {"echo:hello"},
		KindStderr: {"oops"},
	}
	for kind, lines := range want {
		var have []string
		for _, r := range got {
			if r.Kind == kind {
				have = append(have, r.Data)
			}
		}
		if !slices.Equal(have, lines) {
			t.Errorf("%s records %q, want %q", kind, have, lines)
		}
	}
	if got[0].Kind != KindStart || got[0].Pid != c.Pid() {
		t.Errorf("first record %+v, want start with pid %d", got[0], c.Pid())
	}
	if exit := got[len(got)-1]; exit.Code != 3 || exit.Signal != "" {
		t.Errorf("exit record %+v, want code 3", exit)
	}
	if err := c.Stdin("late"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Recv(); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("stdin after exit: %v", err)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if _, err := os.Stat(SocketPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
}

func TestCancelStopsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	cancel()
	waitDone(t, done)
	b, err := os.ReadFile(SpoolPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"k":"exit"`)) {
		t.Fatalf("spool has no exit record:\n%s", b)
	}
}

func TestLingerEndsShimWithoutAck(t *testing.T) {
	_, done := startShim(context.Background(), t, 100*time.Millisecond, "tick", "1", "0")
	waitDone(t, done)
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/shim/`
预期：编译失败，`undefined: Config`

- [ ] **步骤 3：实现** `internal/shim/shim.go`

```go
package shim

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// SocketName is the shim's Unix socket inside a session directory.
const SocketName = "shim.sock"

// DefaultLinger is how long a shim waits, after its child exits, for a
// client to acknowledge the final record before it exits anyway.
const DefaultLinger = 24 * time.Hour

// helloTimeout bounds how long a new connection may take to say hello.
const helloTimeout = 5 * time.Second

// Config describes the child a shim runs.
type Config struct {
	Dir     string   // session directory holding the spool and the socket
	Command []string // program and arguments
	Env     []string // complete child environment
	Linger  time.Duration
}

// SocketPath returns the shim's socket inside dir.
func SocketPath(dir string) string { return filepath.Join(dir, SocketName) }

// shim is one running shim.
type shim struct {
	cfg   Config
	spool *spool
	cmd   *exec.Cmd

	stdinMu sync.Mutex
	stdin   io.WriteCloser

	mu       sync.Mutex
	exited   bool
	final    int64         // spool offset after the exit record
	current  net.Conn      // the one connected client, if any
	exitCh   chan struct{} // closed once the exit record is written
	done     chan struct{}
	doneOnce sync.Once
}

// Run starts the child and serves clients until a client acknowledges the
// exit record, the linger period after exit ends, or ctx is cancelled (which
// terminates the child's process group first).
func Run(ctx context.Context, cfg Config) (err error) {
	if len(cfg.Command) == 0 {
		return errors.New("shim: no command")
	}
	if cfg.Linger <= 0 {
		cfg.Linger = DefaultLinger
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return fmt.Errorf("shim: create dir: %w", err)
	}
	sp, err := openSpool(SpoolPath(cfg.Dir))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sp.close()) }()
	ln, err := listen(cfg.Dir)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, ln.Close())
		if rerr := os.Remove(SocketPath(cfg.Dir)); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			err = errors.Join(err, rerr)
		}
	}()
	s := &shim{cfg: cfg, spool: sp, exitCh: make(chan struct{}), done: make(chan struct{})}
	if err := s.start(); err != nil {
		return err
	}
	go s.accept(ln)
	select {
	case <-s.done:
	case <-ctx.Done():
		s.signal(syscall.SIGTERM)
		<-s.exitCh
	}
	s.mu.Lock()
	if s.current != nil {
		s.current.Close()
	}
	s.mu.Unlock()
	return nil
}

// listen binds the socket under a temporary name and renames it into
// place, so a stale socket file is replaced atomically.
func listen(dir string) (net.Listener, error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".shim-%d.sock", os.Getpid()))
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("shim: remove temp socket: %w", err)
	}
	ln, err := net.Listen("unix", tmp)
	if err != nil {
		return nil, fmt.Errorf("shim: listen: %w", err)
	}
	if err := os.Rename(tmp, SocketPath(dir)); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: publish socket: %w", err), ln.Close())
	}
	return ln, nil
}

func (s *shim) start() error {
	cmd := exec.Command(s.cfg.Command[0], s.cfg.Command[1:]...)
	cmd.Env = s.cfg.Env
	cmd.Dir = s.cfg.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("shim: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("shim: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("shim: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("shim: start %s: %w", s.cfg.Command[0], err)
	}
	s.cmd, s.stdin = cmd, stdin
	if _, err := s.spool.append(Record{Kind: KindStart, Pid: cmd.Process.Pid}); err != nil {
		return err
	}
	var pumps sync.WaitGroup
	pumps.Add(2)
	go s.pump(&pumps, stdout, KindStdout)
	go s.pump(&pumps, stderr, KindStderr)
	go s.wait(&pumps)
	return nil
}

// pump appends each line the child writes as a record.
func (s *shim) pump(wg *sync.WaitGroup, r io.Reader, kind RecordKind) {
	defer wg.Done()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if _, aerr := s.spool.append(Record{Kind: kind, Data: line}); aerr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// wait records the child's exit after its output is drained.
func (s *shim) wait(pumps *sync.WaitGroup) {
	pumps.Wait()
	err := s.cmd.Wait()
	rec := Record{Kind: KindExit}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			rec.Signal = ws.Signal().String()
		} else {
			rec.Code = ee.ExitCode()
		}
	} else if err != nil {
		rec.Code = -1
	}
	off, aerr := s.spool.append(rec)
	s.mu.Lock()
	s.exited = true
	s.final = off
	if aerr != nil {
		s.final = 0 // the exit record is missing; only linger can end the shim
	}
	s.mu.Unlock()
	close(s.exitCh)
	time.AfterFunc(s.cfg.Linger, s.finish)
}

func (s *shim) finish() { s.doneOnce.Do(func() { close(s.done) }) }

func (s *shim) signal(sig syscall.Signal) {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-s.cmd.Process.Pid, sig) // the group may already be gone
}

// accept serves one client at a time; a new connection replaces the old.
func (s *shim) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.current != nil {
			s.current.Close()
		}
		s.current = conn
		s.mu.Unlock()
		go s.serve(conn)
	}
}
```

- [ ] **步骤 4：实现** `internal/shim/serve.go`

```go
package shim

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
)

// signals maps protocol signal names to signals sent to the child's group.
var signals = map[string]syscall.Signal{
	"INT":  syscall.SIGINT,
	"TERM": syscall.SIGTERM,
	"HUP":  syscall.SIGHUP,
	"KILL": syscall.SIGKILL,
}

// conn serializes frame writes to one client.
type conn struct {
	net.Conn
	wmu sync.Mutex
}

func (c *conn) send(m Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeFrame(c.Conn, m)
}

// serve handles one client: hello, then records from the requested offset
// while client frames are applied to the child.
func (s *shim) serve(nc net.Conn) {
	c := &conn{Conn: nc}
	defer c.Close()
	var hello Message
	if err := c.SetReadDeadline(time.Now().Add(helloTimeout)); err != nil {
		return
	}
	if err := readFrame(c, &hello); err != nil || hello.Type != MsgHello {
		return
	}
	if hello.Version != ProtocolVersion {
		_ = c.send(Message{Type: MsgError, Error: fmt.Sprintf("protocol version %d not supported; shim speaks %d", hello.Version, ProtocolVersion)})
		return
	}
	if size, _ := s.spool.state(); hello.Offset < 0 || hello.Offset > size {
		_ = c.send(Message{Type: MsgError, Error: fmt.Sprintf("offset %d outside spool (size %d)", hello.Offset, size)})
		return
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		return
	}
	if err := c.send(Message{Type: MsgWelcome, Version: ProtocolVersion, Pid: s.cmd.Process.Pid}); err != nil {
		return
	}
	stop := make(chan struct{})
	defer close(stop)
	go s.stream(c, hello.Offset, stop)
	for {
		var m Message
		if err := readFrame(c, &m); err != nil {
			return
		}
		if err := s.apply(m); err != nil {
			if c.send(Message{Type: MsgError, Error: err.Error()}) != nil {
				return
			}
		}
	}
}

// stream sends every record from off onward, then follows new appends.
func (s *shim) stream(c *conn, off int64, stop <-chan struct{}) {
	for {
		size, notify := s.spool.state()
		if size > off {
			err := readRecords(s.spool.path, off, size, func(rec Record, end int64) error {
				off = end
				return c.send(Message{Type: MsgRecord, Offset: end, Record: &rec})
			})
			if err != nil {
				c.Close()
				return
			}
			continue
		}
		select {
		case <-notify:
		case <-stop:
			return
		}
	}
}

// apply executes one client frame.
func (s *shim) apply(m Message) error {
	switch m.Type {
	case MsgStdin:
		return s.writeStdin(m.Data)
	case MsgCloseStdin:
		s.stdinMu.Lock()
		defer s.stdinMu.Unlock()
		if err := s.stdin.Close(); err != nil {
			return fmt.Errorf("close stdin: %w", err)
		}
		return nil
	case MsgSignal:
		sig, ok := signals[m.Signal]
		if !ok {
			return fmt.Errorf("unknown signal %q", m.Signal)
		}
		s.signal(sig)
		return nil
	case MsgAck:
		s.mu.Lock()
		finished := s.exited && s.final > 0 && m.Offset >= s.final
		s.mu.Unlock()
		if finished {
			s.finish()
		}
		return nil
	default:
		return fmt.Errorf("unexpected %q frame", m.Type)
	}
}

// writeStdin writes one line to the child and records it.
func (s *shim) writeStdin(line string) error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return errors.New("child has exited")
	}
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	if _, err := s.stdin.Write([]byte(line + "\n")); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	if _, err := s.spool.append(Record{Kind: KindStdin, Data: line}); err != nil {
		return err
	}
	return nil
}
```

- [ ] **步骤 5：实现** `internal/shim/client.go`

```go
package shim

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Client is a connection to a shim. Recv must be called from one goroutine;
// the send methods may be called from any.
type Client struct {
	c   *conn
	pid int
}

// Dial connects to the shim in dir and resumes from offset.
func Dial(ctx context.Context, dir string, offset int64) (*Client, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", SocketPath(dir))
	if err != nil {
		return nil, fmt.Errorf("shim: dial: %w", err)
	}
	c := &conn{Conn: nc}
	if err := c.send(Message{Type: MsgHello, Version: ProtocolVersion, Offset: offset}); err != nil {
		return nil, errors.Join(err, nc.Close())
	}
	var m Message
	if err := readFrame(c, &m); err != nil {
		return nil, errors.Join(fmt.Errorf("shim: read welcome: %w", err), nc.Close())
	}
	switch m.Type {
	case MsgWelcome:
		return &Client{c: c, pid: m.Pid}, nil
	case MsgError:
		return nil, errors.Join(fmt.Errorf("shim: %s", m.Error), nc.Close())
	default:
		return nil, errors.Join(fmt.Errorf("shim: unexpected %q frame", m.Type), nc.Close())
	}
}

// Pid is the child's process ID.
func (c *Client) Pid() int { return c.pid }

// Recv returns the next record and the spool offset just after it. An error
// frame from the shim (for example a rejected stdin write) is returned as an
// error; the connection stays usable.
func (c *Client) Recv() (Record, int64, error) {
	var m Message
	if err := readFrame(c.c, &m); err != nil {
		return Record{}, 0, err
	}
	switch m.Type {
	case MsgRecord:
		if m.Record == nil {
			return Record{}, 0, errors.New("shim: record frame without record")
		}
		return *m.Record, m.Offset, nil
	case MsgError:
		return Record{}, 0, fmt.Errorf("shim: %s", m.Error)
	default:
		return Record{}, 0, fmt.Errorf("shim: unexpected %q frame", m.Type)
	}
}

// Stdin writes one line to the child.
func (c *Client) Stdin(line string) error { return c.c.send(Message{Type: MsgStdin, Data: line}) }

// CloseStdin closes the child's stdin.
func (c *Client) CloseStdin() error { return c.c.send(Message{Type: MsgCloseStdin}) }

// Signal sends INT, TERM, HUP or KILL to the child's process group.
func (c *Client) Signal(name string) error { return c.c.send(Message{Type: MsgSignal, Signal: name}) }

// Ack tells the shim the client has durably consumed the spool up to off.
// Acknowledging the exit record lets the shim exit.
func (c *Client) Ack(off int64) error { return c.c.send(Message{Type: MsgAck, Offset: off}) }

// Close closes the connection; the shim and its child keep running.
func (c *Client) Close() error { return c.c.Close() }
```

- [ ] **步骤 6：实现** `.golangci.yml`

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
      # The shim's whole job is to run the harness command the daemon chose.
      - path: internal/shim/shim\.go
        linters: [gosec]
        text: G204

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/yangflow/hoplet
```

- [ ] **步骤 7：运行，确认通过**

运行：`go test -race ./internal/shim/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 8：commit**

```bash
git add -A
git commit -s -m "feat(shim): run the child and serve one client over a Unix socket"
```

### 任务 4：验证关键假设——客户端消失后续传不丢不重

**文件：** 创建 `internal/shim/resume_test.go`

这一步只加测试，**不改实现**：测试应当直接通过。任何一个失败都说明 0002 的前提不成立，停下来报告，不要改测试去迁就实现。

- [ ] **步骤 1：写测试** `internal/shim/resume_test.go`

```go
package shim

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// TestResumeAfterClientDrop checks the property the daemon relies on: a
// client that disappears without warning reconnects from its last offset
// and receives every later record exactly once, while the child keeps
// running undisturbed.
func TestResumeAfterClientDrop(t *testing.T) {
	const n = 30
	dir, done := startShim(context.Background(), t, time.Minute, "tick", fmt.Sprint(n), "20")
	first := dial(t, dir, 0)
	recs, off := recvUntil(t, first, func(r Record) bool { return r.Data == "tick 4" })
	first.Close() // the daemon vanishes mid-stream

	time.Sleep(150 * time.Millisecond) // the child keeps ticking meanwhile
	second := dial(t, dir, off)
	if second.Pid() != first.Pid() {
		t.Fatalf("child changed: pid %d then %d", first.Pid(), second.Pid())
	}
	more, final := recvUntil(t, second, isExit)
	var ticks []string
	for _, r := range append(recs, more...) {
		if r.Kind == KindStdout {
			ticks = append(ticks, r.Data)
		}
	}
	if len(ticks) != n {
		t.Fatalf("got %d ticks, want %d: %q", len(ticks), n, ticks)
	}
	for i, tk := range ticks {
		if tk != fmt.Sprintf("tick %d", i) {
			t.Fatalf("tick %d is %q: lost, repeated or reordered", i, tk)
		}
	}
	if exit := more[len(more)-1]; exit.Code != 0 {
		t.Fatalf("exit %+v", exit)
	}
	if err := second.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

func TestNewClientReplacesOld(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	old := dial(t, dir, 0)
	dial(t, dir, 0)
	for {
		if _, _, err := old.Recv(); err != nil {
			break // replaced: the old connection is closed
		}
	}
	cancel()
	waitDone(t, done)
}

func TestHelloRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel() }()
	dir, done := startShim(ctx, t, time.Minute, "sleep")
	for _, hello := range []Message{
		{Type: MsgHello, Version: 99},
		{Type: MsgHello, Version: ProtocolVersion, Offset: 1 << 40},
	} {
		nc, err := net.Dial("unix", SocketPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFrame(nc, hello); err != nil {
			t.Fatal(err)
		}
		var m Message
		if err := readFrame(nc, &m); err != nil {
			t.Fatal(err)
		}
		nc.Close()
		if m.Type != MsgError {
			t.Fatalf("hello %+v answered with %+v, want error", hello, m)
		}
	}
	if _, err := Dial(context.Background(), dir, -1); err == nil {
		t.Fatal("negative offset accepted")
	}
	cancel()
	waitDone(t, done)
}

func TestSignalTerminatesChild(t *testing.T) {
	dir, done := startShim(context.Background(), t, time.Minute, "sleep")
	c := dial(t, dir, 0)
	if err := c.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	got, final := recvUntil(t, c, isExit)
	if exit := got[len(got)-1]; exit.Signal != "terminated" {
		t.Fatalf("exit %+v, want signal terminated", exit)
	}
	if err := c.Signal("BOGUS"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Recv(); err == nil || !strings.Contains(err.Error(), "unknown signal") {
		t.Fatalf("bogus signal: %v", err)
	}
	if err := c.Ack(final); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}
```

- [ ] **步骤 2：运行，确认通过，并重复 20 次确认不偶发失败**

运行：`go test -race ./internal/shim/ && go test -race -count=20 -run 'Resume|Replaces|Hello|Signal' ./internal/shim/`
预期：`ok`（重复运行约 1 分钟）

- [ ] **步骤 3：commit**

```bash
git add internal/shim/resume_test.go
git commit -s -m "test(shim): verify resume after client loss and connection handover"
```

### 任务 5：`hoplet shim` 子命令

**文件：** 修改 `cmd/hoplet/main.go`，创建 `cmd/hoplet/shim_test.go`

**接口：** `runShim(args, stderr) int`；SIGTERM / SIGINT 终止子进程组后退出

`cmd/hoplet/main.go` 下面给出修改后的完整文件。

- [ ] **步骤 1：写失败的测试**

`cmd/hoplet/shim_test.go`

```go
package main

import (
	"bytes"
	"testing"
)

func TestShimUsage(t *testing.T) {
	for _, args := range [][]string{
		{"shim"},
		{"shim", "--dir", "/tmp/x"},
		{"shim", "--", "true"},
		{"shim", "--bogus"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%q: unexpected stdout %q", args, out.String())
		}
		if !bytes.Contains(errb.Bytes(), []byte("usage")) && !bytes.Contains(errb.Bytes(), []byte("flag provided but not defined")) {
			t.Errorf("%q: stderr %q", args, errb.String())
		}
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./cmd/hoplet/`
预期：FAIL：`exit 2` 之外的结果，或 stderr 中出现 `unknown command "shim"`

- [ ] **步骤 3：实现** `cmd/hoplet/main.go`

```go
// Command hoplet is Hoplet's single binary: daemon, shim, relay and CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/yangflow/hoplet/internal/shim"
)

// version is set at build time with -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hoplet <command>\n\ncommands:\n  version  print the version\n  shim     run a session's harness process (started by the daemon)")
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "shim":
		return runShim(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "hoplet: unknown command %q\n", args[0])
		return 2
	}
}

// runShim parses `hoplet shim --dir DIR [--linger D] -- COMMAND [ARGS...]`.
// The child inherits the shim's environment; SIGTERM or SIGINT to the shim
// terminates the child's process group and exits.
func runShim(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "session directory for the spool and socket")
	linger := fs.Duration("linger", shim.DefaultLinger, "how long to wait for a final ack after the child exits")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || fs.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: hoplet shim --dir DIR [--linger D] -- COMMAND [ARGS...]")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	cfg := shim.Config{Dir: *dir, Command: fs.Args(), Env: os.Environ(), Linger: *linger}
	if err := shim.Run(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "hoplet shim: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./cmd/hoplet/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(cmd): add hoplet shim subcommand"
```

### 任务 6：全量检查与真实进程验证

- [ ] **步骤 1：全量检查**

运行：`make check`
预期：`golangci-lint run` 输出 `0 issues.`；`cmd/hoplet`、`internal/shim`、`internal/store`、`proto` 四个包 `ok`

- [ ] **步骤 2：真实进程验证（不提交任何文件）**

用一个独立的 `hoplet shim` 进程和一个被 `kill -9` 的客户端进程重复任务 4 的验证（临时客户端写在仓库内以便 import `internal/shim`，构建后立即删除）：

```bash
W=$(mktemp -d /tmp/hoplet-e2e.XXXX)
go build -o $W/hoplet ./cmd/hoplet
mkdir -p internal/shim/e2eclient && cat > internal/shim/e2eclient/main.go <<'GO'
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/yangflow/hoplet/internal/shim"
)

func main() {
	off, _ := strconv.ParseInt(os.Args[2], 10, 64)
	c, err := shim.Dial(context.Background(), os.Args[1], off)
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(1)
	}
	fmt.Println("PID", c.Pid())
	for {
		rec, end, err := c.Recv()
		if err != nil {
			return
		}
		fmt.Println("REC", end, rec.Kind, rec.Data, rec.Code)
		if rec.Kind == shim.KindExit {
			_ = c.Ack(end)
			return
		}
	}
}
GO
go build -o $W/e2eclient ./internal/shim/e2eclient && rm -rf internal/shim/e2eclient
$W/e2eclient /nonexistent 0 >/dev/null 2>&1 || true   # 预热：macOS 上新编译的二进制首次启动较慢
D=$W/s; mkdir -p $D
($W/hoplet shim --dir $D --cwd $D -- sh -c 'for i in $(seq 1 60); do echo tick $i; sleep 0.05; done' >/dev/null 2>&1 &)
for i in $(seq 1 100); do [ -S $D/shim.sock ] && break; sleep 0.05; done
$W/e2eclient $D 0 > $W/c1.out & CP=$!; sleep 0.6; kill -9 $CP; wait $CP 2>/dev/null
LAST=$(grep '^REC' $W/c1.out | tail -1 | awk '{print $2}'); sleep 0.8
$W/e2eclient $D $LAST > $W/c2.out
{ awk '$1=="REC" && $3=="stdout"{print $5}' $W/c1.out; awk '$1=="REC" && $3=="stdout"{print $5}' $W/c2.out; } \
  | awk 'NR!=$1{bad=1} END{print (bad?"FAIL: not contiguous":"OK: 1.."NR" exactly once")}'
grep -h PID $W/c1.out $W/c2.out | awk '{print $2}' | sort -u | wc -l   # 预期 1：同一个子进程
git status --short   # 预期为空：验证没有留下文件
rm -rf $W
```

预期：`OK: 1..60 exactly once`；两次连接看到同一个子进程 PID；`git status` 为空。把输出贴进 PR 描述。

- [ ] **步骤 3：推送并开 PR（需维护者同意）**

```bash
git push -u origin feat/shim
gh pr create --title "feat(shim): add per-session shim with spool and resume" --fill
```

## 完成标准

- 客户端在流中途消失（含 `kill -9`）后，从最后一个偏移重连，收到之后的每一条记录恰好一次、顺序不变；子进程不受影响。
- 新连接顶替旧连接；版本不符、偏移越界的 hello 被拒绝。
- 信号发给子进程组；子进程退出后，确认退出记录才退出 shim，否则等待 linger 后退出；ctx 取消时先终止子进程组。
- spool 读取不返回不完整的行；退出记录落盘。
- `make check` 本地和 CI 通过；真实进程验证输出已记录。
