# M1 · PR5a 会话管理器 · 详细计划

> **执行方式：** 按任务顺序逐步执行；每一步用 `- [ ]` 勾选。每个任务结束时 `go test -race ./...` 和 `golangci-lint run ./...` 都应通过。

**目标：** 把存储、shim 和 harness 串起来：创建会话、按需拉起 shim、把 harness 的事件入库（spool 偏移与事件同一事务提交）、审批先答者生效、守护进程走开后由新的管理器从已提交偏移续接。这是守护进程的核心，5b 把它包成进程和本机 API，5c 加 CLI。

**架构：** 新包 `internal/session`：
- `ShimStdio`：基于 `shim.Client` 的 `harness.Stdio`，把分块记录拼回整行，并报告最后一块之后的 spool 偏移；`Ack` 用于确认 exit 记录。
- `ExecSpawner`：以独立会话（setsid）启动 `hoplet shim`，stdin 指向 `/dev/null`，输出追加到 `<会话目录>/shim.log`，环境只用 launch 给的完整环境；`dialWhenReady` 等 shim 回 welcome。
- `Manager`：`Create` / `Send` / `Respond` / `Interrupt` / `Recover` / `Close`。`Send` 先以幂等键记录用户消息（轮次号取下一轮），再发给 harness；轮次进行中返回 `ErrBusy`。`Respond` 用存储的 `ResolveApproval` 做比较并交换，先答者生效。`Close` 只脱离、不停止 harness（等同守护进程退出）。
- 入库循环：把连续的同轮事件合并成一次追加，带上最高偏移；`TurnStarted` 时分配轮次号；看到 `Native` 立即写入会话；Exit 时提交、若存储里本轮仍开着则补写 `TurnEnded{aborted}`、再 ack exit 记录；连接断开（`ErrDetached`）时重新连接，连不上就中止未结束的轮次；存储拒绝的无效条目逐条重试并替换成 `proto.Error`。
- 存储补两处：`TurnState`（最后一轮的轮次号与是否未结束），以及会话的权限模式（`0002` 迁移、`proto.Session.Permission`、`SessionUpdate.Permission`）。

**技术栈：** Go 1.27；不新增依赖。

**依据：** [M1 总计划](m1-overview.md)、[0002 进程模型](../design/0002-process-model.md)（守护进程重启时会发生什么）、[0004 会话数据模型](../design/0004-session-data-model.md)（偏移与事件同一事务、事实来源）、[0007 命令与审批](../design/0007-commands-approvals.md)（先答者生效、命令可靠执行）、[0003 Agent 驱动](../design/0003-agent-driving.md)（续接、原生会话 ID）。

**本计划的裁决：**
- 原始行（raw）留在会话的 spool 里，shim 会保留到归档；5a 不为每个事件单独存 raw blob（逐行 fsync 的 blob 太贵）。归档时把 spool 转存成 blob、补上引用，放到 M3 的归档功能里。
- 轮次进行中再发消息返回 `ErrBusy`；0007 的持久化队列放到 5b（命令协议）实现。
- 幂等重试：消息已记录、而它的轮次已开始，就不再重发；守护进程恰好死在"记录之后、发送之前"时，消息会留在库里但没有发出，用户重发即可（5b 的命令回执会让客户端看到这种状态）。

## 全局约束

- 不新增依赖；代码、注释、commit 用英文；导出标识符有文档注释；库代码不 panic；不用 lint 抑制注释（`.golangci.yml` 中按路径和规则号的排除需写明理由）。
- 存储是事实来源：轮次状态以 `TurnState` 为准；退出或断开时存储里本轮仍开着，由管理器补写 `TurnEnded{aborted}`。
- 偏移与它产生的事件同一事务提交；偏移只前进；exit 记录在提交之后才 ack。
- shim 由 `hoplet shim` 以独立会话启动，不继承守护进程的标准输入输出和环境。
- 测试不泄漏进程：每个测试结束时终止其目录下的所有 shim；测试里除了"轮询存储直到条件成立"不用 sleep 同步。
- 分支 `feat/session`；commit 带 DCO 签名（`git commit -s`）；完成前 `make check` 通过。

## 文件

| 文件 | 职责 |
|---|---|
| `proto/session.go`、`proto/session_test.go` | `Session.Permission` |
| `internal/store/migrations/0002_session_permission.sql` | 会话权限模式列 |
| `internal/store/session.go`、`session_test.go`、`store_test.go` | 权限模式的默认值、校验、更新；迁移版本号测试改为按迁移数计算 |
| `internal/store/turns.go`、`turns_test.go` | `TurnState` |
| `internal/session/shimstdio.go`、`shimstdio_test.go` | `ShimStdio` |
| `internal/session/spawn.go` | `Spawner`、`ExecSpawner`、`dialWhenReady` |
| `internal/session/manager.go` | `Manager`、`Config`、`CreateRequest`、`ErrBusy`、`ErrNotRunning`、`ErrUnknownHarness` |
| `internal/session/ingest.go` | 入库循环、容错追加、重连 |
| `internal/session/helpers_test.go`、`manager_test.go`、`recover_test.go` | 构建 `hoplet` 与假 harness，端到端测试 |
| `.golangci.yml` | G204 排除扩展到 `internal/session/spawn.go`（以 shim 身份运行 hoplet 自身） |

## 接口（后续 PR 依赖）

- `session.New(Config{Store, Root, Harnesses map[string]harness.StdioHarness, Spawner, Env, Logger, OnAppend func(proto.SessionID)}) (*Manager, error)`
- `(*Manager).Create(ctx, CreateRequest{Mode, Harness, Directory, Isolation, Permission, Title, Origin}) (proto.Session, error)`
- `(*Manager).Send(ctx, id, text, key string) error`（`ErrBusy`）；`Respond(ctx, id, approvalID, optionID, decidedBy, key string) error`（`store.ErrConflict`）；`Interrupt(ctx, id) error`（`ErrNotRunning`）
- `(*Manager).Recover(ctx) error`；`Close() error`（只脱离）
- `(*store.Store).TurnState(ctx, id) (last uint32, open bool, err error)`；`store.SessionUpdate.Permission`
- `session.ExecSpawner{Exe}`；`session.NewShimStdio(*shim.Client)`、`(*ShimStdio).Ack(off)`

---

### 任务 0：建分支

- [ ] **步骤 1：**

```bash
git switch -c feat/session
```

### 任务 1：存储：轮次状态与会话权限模式

**文件：** 修改 `proto/session.go`、`proto/session_test.go`、`internal/store/session.go`、`internal/store/session_test.go`、`internal/store/store_test.go`；创建 `internal/store/migrations/0002_session_permission.sql`、`internal/store/turns.go`、`internal/store/turns_test.go`

**接口：** `TurnState`、`Session.Permission`、`SessionUpdate.Permission`

修改过的文件下面都给出完整内容。

- [ ] **步骤 1：写失败的测试**

`internal/store/turns_test.go`

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestTurnState(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	check := func(wantLast uint32, wantOpen bool) {
		t.Helper()
		last, open, err := s.TurnState(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if last != wantLast || open != wantOpen {
			t.Fatalf("turn state (%d, %v), want (%d, %v)", last, open, wantLast, wantOpen)
		}
	}
	check(0, false)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	check(1, true)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnCompleted}}}})
	check(1, false)
	appendOK(t, s, AppendRequest{Session: "s", Turn: 2, Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	check(2, true)
	if _, _, err := s.TurnState(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
}
```

`internal/store/session_test.go`

```go
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func newSession(t testing.TB, s *Store, id proto.SessionID) proto.Session {
	t.Helper()
	sess, err := s.CreateSession(context.Background(), proto.Session{
		ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe,
		CreatedAt: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := newSession(t, s, "a")
	if a.Status != proto.StatusIdle || !a.UpdatedAt.Equal(t0) {
		t.Fatalf("defaults not applied: %+v", a)
	}
	newSession(t, s, "b")
	got, err := s.Session(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeq != 0 || got.Harness.Kind != "claude" || got.Directory != "/work" || !got.CreatedAt.Equal(t0) {
		t.Fatalf("session a: %+v", got)
	}
	if err := s.ArchiveSession(ctx, "a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Session(ctx, "a")
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(t0.Add(time.Hour)) || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("archive not recorded: %+v", got)
	}
	list, err := s.Sessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "a" {
		t.Fatalf("want a first (most recently updated), got %+v", list)
	}
	if _, err := s.Session(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session: %v", err)
	}
	if err := s.ArchiveSession(ctx, "missing", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive missing: %v", err)
	}
	if _, err := s.CreateSession(ctx, proto.Session{ID: "c"}); err == nil {
		t.Fatal("incomplete session accepted")
	}
}

func TestCreateSessionValidation(t *testing.T) {
	valid := proto.Session{
		ID: "v", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe,
	}
	cases := []struct {
		name   string
		mutate func(*proto.Session)
	}{
		{"empty id", func(s *proto.Session) { s.ID = "" }},
		{"empty harness", func(s *proto.Session) { s.Harness.Kind = "" }},
		{"bad mode", func(s *proto.Session) { s.Mode = "chat" }},
		{"bad owner", func(s *proto.Session) { s.Owner = "you" }},
		{"bad origin", func(s *proto.Session) { s.Origin = "cron" }},
		{"bad isolation", func(s *proto.Session) { s.Isolation = "vm" }},
		{"personal with isolation", func(s *proto.Session) { s.Mode = proto.ModePersonal; s.Isolation = proto.Isolated }},
		{"bad status", func(s *proto.Session) { s.Status = "paused" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			sess := valid
			tc.mutate(&sess)
			if _, err := s.CreateSession(context.Background(), sess); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	t.Run("personal without isolation", func(t *testing.T) {
		s := newStore(t)
		sess := valid
		sess.Mode = proto.ModePersonal
		if _, err := s.CreateSession(context.Background(), sess); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCreateSessionDuplicate(t *testing.T) {
	s := newStore(t)
	newSession(t, s, "dup")
	_, err := s.CreateSession(context.Background(), proto.Session{
		ID: "dup", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe,
	})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
}

func TestSessionPermission(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	sess := newSession(t, s, "p")
	if sess.Permission != proto.ModeConfirm {
		t.Fatalf("default permission %q", sess.Permission)
	}
	auto := proto.ModeAutoEdit
	got, err := s.UpdateSession(ctx, "p", SessionUpdate{Permission: &auto})
	if err != nil {
		t.Fatal(err)
	}
	if got.Permission != proto.ModeAutoEdit {
		t.Fatalf("updated permission %q", got.Permission)
	}
	if again, _ := s.Session(ctx, "p"); again.Permission != proto.ModeAutoEdit {
		t.Fatalf("stored permission %q", again.Permission)
	}
	bogus := proto.PermissionMode("bogus")
	if _, err := s.UpdateSession(ctx, "p", SessionUpdate{Permission: &bogus}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bogus update: %v", err)
	}
	if _, err := s.CreateSession(ctx, proto.Session{ID: "q", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "fake"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe, Permission: bogus}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bogus create: %v", err)
	}
}
```

`internal/store/store_test.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yangflow/hoplet/proto"
)

var t0 = time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)

func newStore(t testing.TB) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenReopenAndRejectNewerSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if want := len(mustMigrations(t)); v != want {
		t.Fatalf("schema version %d, want %d", v, want)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("open newer schema: got %v", err)
	}
}

func TestBackupKeepsNewestThree(t *testing.T) {
	s := newStore(t)
	for v := 1; v <= 4; v++ {
		if err := s.backup(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := filepath.Glob(filepath.Join(s.dir, DBName+".v*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d backups, want 3: %v", len(got), got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, DBName+".v1.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest backup still present: %v", err)
	}
}

func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a?b#c%d")
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.CreateSession(context.Background(), proto.Session{
		ID: "s", Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Owner: proto.OwnerMe, Origin: proto.OriginMe,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DBName)); err != nil {
		t.Fatalf("database not at the requested path: %v", err)
	}
}

func initSQL(t *testing.T) string {
	t.Helper()
	b, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateTwoSteps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	one := fstest.MapFS{"0001_init.sql": {Data: []byte(initSQL(t))}}
	two := fstest.MapFS{
		"0001_init.sql":  {Data: []byte(initSQL(t))},
		"0002_extra.sql": {Data: []byte("ALTER TABLE sessions ADD COLUMN test_col TEXT")},
	}
	s, err := open(ctx, dir, one)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = open(ctx, dir, two)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != 2 {
		t.Fatalf("user_version %d, %v; want 2", v, err)
	}
	if _, err := s.db.ExecContext(ctx, "SELECT test_col FROM sessions"); err != nil {
		t.Fatalf("migration 2 not applied: %v", err)
	}
	bak, err := sql.Open("sqlite", dsn(filepath.Join(dir, DBName+".v1.bak"), url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer bak.Close()
	var bv int
	if err := bak.QueryRowContext(ctx, "PRAGMA user_version").Scan(&bv); err != nil || bv != 1 {
		t.Fatalf("backup user_version %d, %v; want 1", bv, err)
	}
	var n int
	if err := bak.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'sessions'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("backup is not a valid database: %d, %v", n, err)
	}
}

func TestMigrationGapRejected(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte("SELECT 1")},
		"0003_skip.sql": {Data: []byte("SELECT 1")},
	}
	if _, err := open(context.Background(), t.TempDir(), fsys); err == nil || !strings.Contains(err.Error(), "without gaps") {
		t.Fatalf("gap accepted: %v", err)
	}
}

func TestNoTransactionMigration(t *testing.T) {
	ctx := context.Background()
	fsys := fstest.MapFS{
		"0001_init.sql": {Data: []byte(initSQL(t))},
		// PRAGMA foreign_keys is a no-op inside a transaction, so seeing it
		// take effect proves the migration ran outside one.
		"0002_pragma.sql": {Data: []byte("-- hoplet:no-transaction\nPRAGMA foreign_keys = OFF;\nCREATE TABLE extra (a INTEGER);")},
	}
	s, err := open(ctx, t.TempDir(), fsys)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, fk int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != 2 {
		t.Fatalf("user_version %d, %v; want 2", v, err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign_keys = %d, %v; migration did not run outside a transaction", fk, err)
	}
	if _, err := s.db.ExecContext(ctx, "SELECT a FROM extra"); err != nil {
		t.Fatal(err)
	}
}

func TestReadsProceedDuringWrite(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.tx(ctx, false, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET title = 'writing' WHERE id = 's'"); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sess, err := s.Session(readCtx, "s")
	close(release)
	if err != nil {
		t.Fatalf("read blocked by an open write transaction: %v", err)
	}
	if sess.Title != "" {
		t.Fatalf("read saw uncommitted data: %q", sess.Title)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReaderIsReadOnly(t *testing.T) {
	s := newStore(t)
	if _, err := s.rdb.ExecContext(context.Background(), "DELETE FROM sessions"); err == nil {
		t.Fatal("read pool accepted a write")
	}
}

func TestOpenRemovesStaleTempBlobs(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "blobs", ".tmp-123")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp blob survived Open: %v", err)
	}
}

func mustMigrations(t *testing.T) []migration {
	t.Helper()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations(sub)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
```

`proto/session_test.go`

```go
package proto

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestSessionJSONRoundTrip(t *testing.T) {
	archived := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	in := Session{
		ID: "s1", Mode: ModeCode, Harness: HarnessRef{Kind: "claude", Version: "2.1.312", NativeID: "n1"}, Permission: ModeAutoEdit,
		Directory: "/work", Isolation: InPlace, Owner: OwnerMe, Origin: OriginMe, Status: StatusIdle,
		Title: "sync", CreatedAt: archived.Add(-time.Hour), UpdatedAt: archived, ArchivedAt: &archived, LastSeq: 7, Version: 9,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Session
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/ ./proto/`
预期：编译失败，`s.TurnState undefined` / `unknown field Permission`

- [ ] **步骤 3：实现** `internal/store/migrations/0002_session_permission.sql`

```sql
ALTER TABLE sessions ADD COLUMN permission TEXT NOT NULL DEFAULT 'confirm';
```

- [ ] **步骤 4：实现** `proto/session.go`

```go
package proto

import "time"

// Mode says what a session delivers: a reviewable change or a result.
type Mode string

// Session modes.
const (
	ModeCode     Mode = "code"
	ModePersonal Mode = "personal"
)

// Isolation says whether a Code session works in the real directory.
type Isolation string

// Isolation kinds.
const (
	InPlace  Isolation = "in_place"
	Isolated Isolation = "isolated"
)

// Owner says who drives a session.
type Owner string

// Session owners.
const (
	OwnerMe        Owner = "me"
	OwnerDelegated Owner = "delegated"
)

// Origin says what started a session.
type Origin string

// Session origins.
const (
	OriginMe       Origin = "me"
	OriginIM       Origin = "im"
	OriginSchedule Origin = "schedule"
	OriginEvent    Origin = "event"
)

// SessionStatus is derived from the session's events.
type SessionStatus string

// Session states.
const (
	StatusIdle    SessionStatus = "idle"
	StatusRunning SessionStatus = "running"
	StatusWaiting SessionStatus = "waiting"
	StatusErrored SessionStatus = "errored"
)

// HarnessRef names the harness behind a session. NativeID is the harness's
// own session ID, kept as a pointer; the Hoplet session ID is the identity.
type HarnessRef struct {
	Kind     string `json:"kind"`
	Version  string `json:"version,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// Session is a conversation with one harness, optionally bound to a
// working directory.
type Session struct {
	ID      SessionID  `json:"id"`
	Mode    Mode       `json:"mode"`
	Harness HarnessRef `json:"harness"`
	// Permission is the session's permission mode; empty means confirm.
	Permission PermissionMode `json:"permission,omitempty"`
	Directory  string         `json:"directory,omitempty"`
	Isolation  Isolation      `json:"isolation,omitempty"`
	Owner      Owner          `json:"owner"`
	Origin     Origin         `json:"origin"`
	Status     SessionStatus  `json:"status"`
	Title      string         `json:"title,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ArchivedAt *time.Time     `json:"archived_at,omitempty"`
	LastSeq    uint64         `json:"last_seq"`
	// Version is the store's catalog counter value at this session's last
	// change; it grows with every change to the session record.
	Version uint64 `json:"version"`
}
```

- [ ] **步骤 5：实现** `internal/store/session.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/yangflow/hoplet/proto"
)

const sessionColumns = `id, mode, harness_kind, harness_version, native_id, directory, isolation,
	owner, origin, status, title, created_at, updated_at, archived_at, last_seq, version, permission`

// CreateSession stores a new session. Status defaults to idle and
// CreatedAt to now; UpdatedAt equals CreatedAt. The write is durable.
func (s *Store) CreateSession(ctx context.Context, sess proto.Session) (proto.Session, error) {
	if sess.Status == "" {
		sess.Status = proto.StatusIdle
	}
	if sess.Permission == "" {
		sess.Permission = proto.ModeConfirm
	}
	if err := validateSession(sess); err != nil {
		return proto.Session{}, err
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	sess.CreatedAt = sess.CreatedAt.UTC().Truncate(time.Millisecond)
	sess.UpdatedAt = sess.CreatedAt
	sess.ArchivedAt = nil
	sess.LastSeq = 0
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (`+sessionColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0, 0, ?)`,
			sess.ID, sess.Mode, sess.Harness.Kind, sess.Harness.Version, sess.Harness.NativeID,
			sess.Directory, sess.Isolation, sess.Owner, sess.Origin, sess.Status, sess.Title,
			sess.CreatedAt.UnixMilli(), sess.UpdatedAt.UnixMilli(), sess.Permission)
		var serr *sqlite.Error
		if errors.As(err, &serr) && serr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
			return fmt.Errorf("%w: session %s", ErrExists, sess.ID)
		}
		if err != nil {
			return fmt.Errorf("store: create session %s: %w", sess.ID, err)
		}
		if err := bumpVersion(ctx, tx, sess.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT version FROM sessions WHERE id = ?`, sess.ID).Scan(&sess.Version); err != nil {
			return fmt.Errorf("store: read session version: %w", err)
		}
		return nil
	})
	if err != nil {
		return proto.Session{}, err
	}
	return sess, nil
}

func validateSession(sess proto.Session) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: session %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if sess.ID == "" {
		return bad("needs an id")
	}
	if sess.Harness.Kind == "" {
		return bad("needs a harness kind")
	}
	if sess.Mode != proto.ModeCode && sess.Mode != proto.ModePersonal {
		return bad("mode %q", sess.Mode)
	}
	if sess.Owner != proto.OwnerMe && sess.Owner != proto.OwnerDelegated {
		return bad("owner %q", sess.Owner)
	}
	switch sess.Origin {
	case proto.OriginMe, proto.OriginIM, proto.OriginSchedule, proto.OriginEvent:
	default:
		return bad("origin %q", sess.Origin)
	}
	switch sess.Isolation {
	case "", proto.InPlace, proto.Isolated:
	default:
		return bad("isolation %q", sess.Isolation)
	}
	if sess.Mode == proto.ModePersonal && sess.Isolation != "" {
		return bad("isolation %q on a personal session", sess.Isolation)
	}
	switch sess.Status {
	case proto.StatusIdle, proto.StatusRunning, proto.StatusWaiting, proto.StatusErrored:
	default:
		return bad("status %q", sess.Status)
	}
	if !validPermission(sess.Permission) {
		return bad("permission %q", sess.Permission)
	}
	return nil
}

func validPermission(m proto.PermissionMode) bool {
	switch m {
	case proto.ModeReadOnly, proto.ModeConfirm, proto.ModeAutoEdit, proto.ModeFullAuto:
		return true
	}
	return false
}

// Session returns one session, or ErrNotFound.
func (s *Store) Session(ctx context.Context, id proto.SessionID) (proto.Session, error) {
	return getSession(ctx, s.rdb, id)
}

func getSession(ctx context.Context, q queryer, id proto.SessionID) (proto.Session, error) {
	row := q.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Session{}, fmt.Errorf("%w: session %s", ErrNotFound, id)
	}
	return sess, err
}

// Sessions returns all sessions, most recently updated first.
func (s *Store) Sessions(ctx context.Context) ([]proto.Session, error) {
	return s.listSessions(ctx, `SELECT `+sessionColumns+` FROM sessions ORDER BY updated_at DESC, id`)
}

// SessionsSince returns the sessions changed after catalog version v, oldest
// change first. Sessions are never deleted yet, so no tombstones exist;
// deletion must add them or clients would never learn of removals.
func (s *Store) SessionsSince(ctx context.Context, v uint64) ([]proto.Session, error) {
	return s.listSessions(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE version > ? ORDER BY version`, v)
}

func (s *Store) listSessions(ctx context.Context, query string, args ...any) ([]proto.Session, error) {
	rows, err := s.rdb.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	defer rows.Close()
	var out []proto.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list sessions: %w", err)
	}
	return out, nil
}

// CatalogVersion returns the current value of the catalog counter, the
// highest Version any session carries.
func (s *Store) CatalogVersion(ctx context.Context) (uint64, error) {
	var v uint64
	if err := s.rdb.QueryRowContext(ctx, `SELECT version FROM catalog WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read catalog version: %w", err)
	}
	return v, nil
}

// bumpVersion advances the catalog counter and stamps it on the session.
func bumpVersion(ctx context.Context, tx *sql.Tx, id proto.SessionID) error {
	if _, err := tx.ExecContext(ctx, `UPDATE catalog SET version = version + 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("store: bump catalog: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET version = (SELECT version FROM catalog WHERE id = 1) WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: stamp session version: %w", err)
	}
	return nil
}

// SessionUpdate lists the session fields UpdateSession may change; nil
// fields are left alone.
type SessionUpdate struct {
	NativeID   *string
	Title      *string
	Permission *proto.PermissionMode
}

// UpdateSession changes a session's harness-native ID, title and/or
// permission mode and
// returns the updated session. The write is durable; with nothing to change
// it writes nothing and returns the current session.
func (s *Store) UpdateSession(ctx context.Context, id proto.SessionID, u SessionUpdate) (proto.Session, error) {
	if u.NativeID == nil && u.Title == nil && u.Permission == nil {
		return s.Session(ctx, id)
	}
	if u.Permission != nil && !validPermission(*u.Permission) {
		return proto.Session{}, fmt.Errorf("%w: permission %q", ErrInvalid, *u.Permission)
	}
	var out proto.Session
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET native_id = COALESCE(?, native_id), title = COALESCE(?, title),
			permission = COALESCE(?, permission) WHERE id = ?`, u.NativeID, u.Title, u.Permission, id)
		if err != nil {
			return fmt.Errorf("store: update session %s: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: update session %s: %w", id, err)
		} else if n == 0 {
			return fmt.Errorf("%w: session %s", ErrNotFound, id)
		}
		if err := bumpVersion(ctx, tx, id); err != nil {
			return err
		}
		out, err = getSession(ctx, tx, id)
		return err
	})
	if err != nil {
		return proto.Session{}, err
	}
	return out, nil
}

// ArchiveSession marks a session archived at t. The write is durable.
func (s *Store) ArchiveSession(ctx context.Context, id proto.SessionID, t time.Time) error {
	ms := t.UTC().Truncate(time.Millisecond).UnixMilli()
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET archived_at = ?, updated_at = ? WHERE id = ?`, ms, ms, id)
		if err != nil {
			return fmt.Errorf("store: archive session %s: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("store: archive session %s: %w", id, err)
		} else if n == 0 {
			return fmt.Errorf("%w: session %s", ErrNotFound, id)
		}
		return bumpVersion(ctx, tx, id)
	})
}

type scanner interface{ Scan(dest ...any) error }

func scanSession(r scanner) (proto.Session, error) {
	var (
		sess             proto.Session
		created, updated int64
		archived         sql.NullInt64
	)
	err := r.Scan(&sess.ID, &sess.Mode, &sess.Harness.Kind, &sess.Harness.Version, &sess.Harness.NativeID,
		&sess.Directory, &sess.Isolation, &sess.Owner, &sess.Origin, &sess.Status, &sess.Title,
		&created, &updated, &archived, &sess.LastSeq, &sess.Version, &sess.Permission)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return proto.Session{}, err
		}
		return proto.Session{}, fmt.Errorf("store: scan session: %w", err)
	}
	sess.CreatedAt = time.UnixMilli(created).UTC()
	sess.UpdatedAt = time.UnixMilli(updated).UTC()
	if archived.Valid {
		t := time.UnixMilli(archived.Int64).UTC()
		sess.ArchivedAt = &t
	}
	return sess, nil
}
```

- [ ] **步骤 6：实现** `internal/store/turns.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// TurnState reports a session's highest turn number (0 when it has none)
// and whether that turn is still open (started, not yet ended).
func (s *Store) TurnState(ctx context.Context, id proto.SessionID) (last uint32, open bool, err error) {
	err = s.readTx(ctx, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		var end sql.NullInt64
		row := tx.QueryRowContext(ctx, `SELECT n, end_seq FROM turns WHERE session_id = ? ORDER BY n DESC LIMIT 1`, id)
		switch err := row.Scan(&last, &end); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return fmt.Errorf("store: read turn state: %w", err)
		}
		open = !end.Valid
		return nil
	})
	return last, open, err
}
```

- [ ] **步骤 7：运行，确认通过**

运行：`go test -race ./internal/store/ ./proto/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 8：commit**（用明确的路径 `git add`，不要 `git add -A`）

```bash
git commit -s -m "feat(store): add turn state and session permission mode"
```

### 任务 2：shim 版 Stdio 与 shim 启动器

**文件：** 创建 `internal/session/shimstdio.go`、`internal/session/spawn.go`、`internal/session/shimstdio_test.go`；修改 `.golangci.yml`

**接口：** `ShimStdio`、`NewShimStdio`、`(*ShimStdio).Ack`、`Spawner`、`ExecSpawner`、`dialWhenReady`

- [ ] **步骤 1：写失败的测试**

`internal/session/shimstdio_test.go`

```go
package session

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// TestShimStdioLines runs a real shim in-process and checks that chunked
// records come back as whole lines with increasing offsets, non-UTF-8 bytes
// survive, stdin is echoed as a Stdin line, and the exit carries its code.
func TestShimStdioLines(t *testing.T) {
	dir, err := os.MkdirTemp("", "ss")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := `read x; head -c 3145728 /dev/zero | tr '\0' a; echo; printf '\377\376\n'; echo oops >&2; echo "got:$x"; exit 7`
	done := make(chan error, 1)
	go func() {
		done <- shim.Run(ctx, shim.Config{Dir: dir, WorkDir: dir, Command: []string{"/bin/sh", "-c", script}, Env: []string{}})
	}()
	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	defer dcancel()
	c, err := dialWhenReady(dctx, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewShimStdio(c)
	defer s.Close()
	if err := s.WriteLine(ctx, []byte("z")); err != nil {
		t.Fatal(err)
	}
	var got []harness.Line
	timeout := time.After(15 * time.Second)
	for {
		select {
		case l, ok := <-s.Lines():
			if !ok {
				goto checked
			}
			got = append(got, l)
		case <-timeout:
			t.Fatal("timed out")
		}
	}
checked:
	var last int64
	var kinds []harness.LineKind
	var stdout [][]byte
	for _, l := range got {
		if l.Offset <= last {
			t.Fatalf("offset %d not after %d", l.Offset, last)
		}
		last = l.Offset
		kinds = append(kinds, l.Kind)
		if l.Kind == harness.Stdout {
			stdout = append(stdout, l.Data)
		}
	}
	if len(stdout) != 3 || len(stdout[0]) != 3<<20 || bytes.Count(stdout[0], []byte("a")) != 3<<20 {
		t.Fatalf("stdout lines %d, first %d bytes", len(stdout), len(stdout[0]))
	}
	if !bytes.Equal(stdout[1], []byte{0xff, 0xfe}) || string(stdout[2]) != "got:z" {
		t.Fatalf("stdout tail %q %q", stdout[1], stdout[2])
	}
	exit := got[len(got)-1]
	if exit.Kind != harness.Exit || exit.Code != 7 {
		t.Fatalf("exit %+v", exit)
	}
	if !strings.Contains(kindString(kinds), "stdin") || !strings.Contains(kindString(kinds), "stderr") {
		t.Fatalf("kinds %v", kinds)
	}
	if err := s.Ack(exit.Offset); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shim did not exit after the ack")
	}
}

func kindString(ks []harness.LineKind) string {
	names := map[harness.LineKind]string{harness.Stdout: "stdout", harness.Stderr: "stderr", harness.Stdin: "stdin", harness.StdinError: "stdin_error", harness.Exit: "exit"}
	var b strings.Builder
	for _, k := range ks {
		b.WriteString(names[k] + " ")
	}
	return b.String()
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/session/`
预期：编译失败，`undefined: NewShimStdio`

- [ ] **步骤 3：实现** `internal/session/shimstdio.go`

```go
package session

import (
	"context"
	"errors"
	"sync"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// ShimStdio is a harness.Stdio over a shim connection. It reassembles
// chunked records into whole lines and reports each line with the spool
// offset just after its last chunk, so the daemon can commit offsets with
// the events they produced.
type ShimStdio struct {
	c     *shim.Client
	lines chan harness.Line
	done  chan struct{}
	wg    sync.WaitGroup
	once  sync.Once
}

var _ harness.Stdio = (*ShimStdio)(nil)

// NewShimStdio starts reading records from c.
func NewShimStdio(c *shim.Client) *ShimStdio {
	s := &ShimStdio{c: c, lines: make(chan harness.Line, 64), done: make(chan struct{})}
	s.wg.Add(1)
	go s.read()
	return s
}

func (s *ShimStdio) read() {
	defer s.wg.Done()
	defer close(s.lines)
	partial := map[shim.RecordKind][]byte{}
	for {
		rec, off, err := s.c.Recv()
		var serr *shim.Error
		if errors.As(err, &serr) {
			// A rejected frame (for example stdin_busy): the connection stays
			// usable. It has no spool position, so Offset is 0.
			if !s.send(harness.Line{Kind: harness.StdinError, Data: []byte(serr.Error())}) {
				return
			}
			continue
		}
		if err != nil {
			return // the connection ended: the run reports ErrDetached
		}
		data := []byte(rec.Data)
		if len(rec.B) > 0 {
			data = rec.B
		}
		kind, ok := lineKind(rec.Kind)
		if !ok {
			continue // a start record carries nothing for the harness adapter
		}
		if rec.More {
			partial[rec.Kind] = append(partial[rec.Kind], data...)
			continue
		}
		if p := partial[rec.Kind]; p != nil {
			data = append(p, data...)
			delete(partial, rec.Kind)
		}
		line := harness.Line{Kind: kind, Data: data, Offset: off}
		if kind == harness.Exit {
			line.Code, line.Signal = rec.Code, rec.Signal
			if rec.Signal != "" && rec.Code == 0 {
				line.Code = -1
			}
		}
		if !s.send(line) || kind == harness.Exit {
			return
		}
	}
}

func lineKind(k shim.RecordKind) (harness.LineKind, bool) {
	switch k {
	case shim.KindStdout:
		return harness.Stdout, true
	case shim.KindStderr:
		return harness.Stderr, true
	case shim.KindStdin:
		return harness.Stdin, true
	case shim.KindStdinError:
		return harness.StdinError, true
	case shim.KindExit:
		return harness.Exit, true
	}
	return 0, false
}

func (s *ShimStdio) send(l harness.Line) bool {
	select {
	case s.lines <- l:
		return true
	case <-s.done:
		return false
	}
}

// WriteLine sends one line to the harness's stdin. A nil error means the
// shim accepted the frame; delivery failures arrive as StdinError lines.
func (s *ShimStdio) WriteLine(ctx context.Context, line []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.c.Stdin(string(line))
}

// Lines delivers lines until the exit record or the end of the connection.
func (s *ShimStdio) Lines() <-chan harness.Line { return s.lines }

// Signal sends INT, TERM, HUP or KILL to the harness's process group.
func (s *ShimStdio) Signal(name string) error { return s.c.Signal(name) }

// Ack tells the shim the daemon has committed everything up to off; acking
// the exit record lets the shim exit.
func (s *ShimStdio) Ack(off int64) error { return s.c.Ack(off) }

// Close detaches from the shim; the harness keeps running.
func (s *ShimStdio) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		err = s.c.Close()
		s.wg.Wait()
	})
	return err
}
```

- [ ] **步骤 4：实现** `internal/session/spawn.go`

```go
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
)

// Spawner starts a shim for a session.
type Spawner interface {
	Spawn(ctx context.Context, dir, workDir string, l harness.Launch) error
}

// ExecSpawner runs `<Exe> shim` as a detached process: its own session
// (setsid), stdin from /dev/null, stdout and stderr appended to
// <dir>/shim.log, and the launch environment as its whole environment, so
// it survives the daemon and inherits nothing else.
type ExecSpawner struct {
	Exe string
}

// Spawn starts the shim and returns once it is started; use Dial to wait
// until it accepts connections.
func (e ExecSpawner) Spawn(_ context.Context, dir, workDir string, l harness.Launch) error {
	if len(l.Command) == 0 {
		return errors.New("session: launch has no command")
	}
	logf, err := os.OpenFile(filepath.Join(filepath.Clean(dir), "shim.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("session: open shim log: %w", err)
	}
	defer logf.Close()
	args := append([]string{"shim", "--dir", dir, "--cwd", workDir, "--"}, l.Command...)
	cmd := exec.Command(e.Exe, args...)
	cmd.Env = l.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("session: start shim: %w", err)
	}
	go func() { _ = cmd.Wait() }() // reap; the shim's exit status lives in its spool
	return nil
}

// dialWhenReady dials the shim in dir, retrying until it accepts
// connections or ctx ends; a shim is ready once it answers with welcome.
func dialWhenReady(ctx context.Context, dir string, offset int64) (*shim.Client, error) {
	delay := 10 * time.Millisecond
	for {
		c, err := shim.Dial(ctx, dir, offset)
		if err == nil {
			return c, nil
		}
		var serr *shim.Error
		if errors.As(err, &serr) {
			return nil, err // the shim answered: retrying won't help
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("session: shim in %s not ready: %w", dir, errors.Join(ctx.Err(), err))
		case <-time.After(delay):
		}
		delay = min(2*delay, 200*time.Millisecond)
	}
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
      # The shim and ExecStdio exist to run the harness command the daemon chose;
      # the session spawner runs the hoplet binary itself as the shim.
      - path: internal/(shim/shim|harness/execstdio|session/spawn)\.go
        linters: [gosec]
        text: G204
      # BuildBinary runs `go build` with the output path a test chose.
      - path: internal/harness/fake/build\.go
        linters: [gosec]
        text: G204
      # fakeharness reads the script file named by its test-controlled environment.
      - path: internal/harness/fake/fakeharness/main\.go
        linters: [gosec]
        text: G703

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

运行：`go test -race ./internal/session/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 7：commit**（用明确的路径 `git add`，不要 `git add -A`）

```bash
git commit -s -m "feat(session): add a shim-backed stdio and a detached shim spawner"
```

### 任务 3：会话管理器与入库

**文件：** 创建 `internal/session/manager.go`、`internal/session/ingest.go`、`internal/session/helpers_test.go`、`internal/session/manager_test.go`

**接口：** 见上文“接口”

- [ ] **步骤 1：写失败的测试**

`internal/session/helpers_test.go`

```go
package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

var hopletExe, fakeExe string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sessbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hopletExe = filepath.Join(dir, "hoplet")
	if out, err := exec.Command("go", "build", "-o", hopletExe, "github.com/yangflow/hoplet/cmd/hoplet").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build hoplet: %v\n%s", err, out)
		os.Exit(1)
	}
	if fakeExe, err = fake.BuildBinary(context.Background(), dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is a deterministic test environment for harnesses.
type env struct {
	t      *testing.T
	root   string // holds hoplet.db and sessions/
	work   string
	script string
	store  *store.Store
}

func newEnv(t *testing.T, s fake.Script) *env {
	t.Helper()
	root, err := os.MkdirTemp("", "hs") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	// Detached shims outlive their manager by design; stop every shim under
	// this root (SIGTERM terminates its harness too) so tests leak nothing.
	t.Cleanup(func() { stopShims(t, root) })
	script, err := fake.WriteScript(root, s)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, root: root, work: work, script: script, store: st}
}

// manager returns a new Manager over the env's store, as a fresh daemon
// would create one. The caller closes it.
func (e *env) manager() *Manager {
	e.t.Helper()
	m, err := New(Config{
		Store: e.store, Root: filepath.Join(e.root, "s"), Spawner: ExecSpawner{Exe: hopletExe},
		Harnesses: map[string]harness.StdioHarness{"fake": &fake.Harness{Binary: fakeExe, Script: e.script}},
		Env:       []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) create(m *Manager) proto.Session {
	e.t.Helper()
	sess, err := m.Create(context.Background(), CreateRequest{Mode: proto.ModeCode, Harness: "fake", Directory: e.work})
	if err != nil {
		e.t.Fatal(err)
	}
	return sess
}

// waitFor polls the store until cond holds; the store, not the harness, is
// what the test observes.
func (e *env) waitFor(id proto.SessionID, what string, cond func([]proto.Event) bool) []proto.Event {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		evs, err := e.store.Events(context.Background(), id, 0, 10000)
		if err != nil {
			e.t.Fatal(err)
		}
		if cond(evs) {
			return evs
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s; events: %s", what, describe(evs))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func payloads(t *testing.T, evs []proto.Event) []proto.Payload {
	t.Helper()
	out := make([]proto.Payload, len(evs))
	for i, ev := range evs {
		p, err := ev.Decode()
		if err != nil {
			t.Fatal(err)
		}
		out[i] = p
	}
	return out
}

func describe(evs []proto.Event) string {
	s := ""
	for _, ev := range evs {
		s += fmt.Sprintf("\n  %d t%d %s %s", ev.Seq, ev.Turn, ev.Type, ev.Payload)
	}
	return s
}

func turnEnded(evs []proto.Event) bool {
	for _, ev := range evs {
		if ev.Type == proto.TypeTurnEnded {
			return true
		}
	}
	return false
}

func count(evs []proto.Event, typ proto.EventType) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// waitShimGone waits until no shim process owns the session's directory.
func waitShimGone(t *testing.T, m *Manager, id proto.SessionID) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := exec.Command("pgrep", "-f", "shim --dir "+m.dir(id)).Run(); err != nil {
			return // pgrep exits non-zero when nothing matches
		}
		if time.Now().After(deadline) {
			t.Fatal("shim still running")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stopShims terminates every shim whose directory is under root and waits
// for them to exit.
func stopShims(t *testing.T, root string) {
	t.Helper()
	pattern := "shim --dir " + root
	_ = exec.Command("pkill", "-TERM", "-f", pattern).Run() // exits 1 when nothing matches
	deadline := time.Now().Add(15 * time.Second)
	for exec.Command("pgrep", "-f", pattern).Run() == nil {
		if time.Now().After(deadline) {
			_ = exec.Command("pkill", "-KILL", "-f", pattern).Run()
			t.Error("shims did not exit after SIGTERM")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

`internal/session/manager_test.go`

```go
package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

func intp(n int) *int { return &n }

func TestSendStreamsIntoStore(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "native-1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "hel"}},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "lo"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "hello"}},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if err := m.Send(context.Background(), sess.ID, "hi", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] || ev.Turn != 1 || ev.Seq != uint64(i+1) {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	ps := payloads(t, evs)
	if u := ps[0].(proto.Message); u.Role != proto.RoleUser || u.Text != "hi" {
		t.Fatalf("user message %+v", u)
	}
	got, err := e.store.Session(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != proto.StatusIdle || got.Harness.NativeID != "native-1" {
		t.Fatalf("session %+v", got)
	}
	if off, _ := e.store.IngestOffset(context.Background(), sess.ID); off == 0 {
		t.Fatal("ingest offset not committed")
	}
}

func TestSendIsIdempotent(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{EchoPrompt: true}}}, {Steps: []fake.Step{{EchoPrompt: true}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "once", "k"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn end", turnEnded)
	if err := m.Send(ctx, sess.ID, "once", "k"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "settled", func(evs []proto.Event) bool { return turnEnded(evs) })
	if n := count(evs, proto.TypeTurnStarted); n != 1 {
		t.Fatalf("retry started %d turns: %s", n, describe(evs))
	}
	if err := m.Send(ctx, sess.ID, "", ""); err == nil {
		t.Fatal("send without key accepted")
	}
}

func TestSendWhileRunningIsBusy(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{AwaitApproval: "a"}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := m.Send(ctx, sess.ID, "more", "k2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("send during turn: %v", err)
	}
}

func TestApprovalFirstAnswerWins(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "approval", ID: "a1", Kind: "command", Title: "Run go test", Options: []fake.Option{{ID: "once", Label: "Allow once", Scope: "once"}, {ID: "no", Label: "Deny", Scope: "deny"}}}},
		{AwaitApproval: "a1"},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "approval", func(evs []proto.Event) bool { return count(evs, proto.TypeApprovalRequest) == 1 })
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, opt := range []string{"once", "no"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = m.Respond(ctx, sess.ID, "a1", opt, "device:"+opt, "r-"+opt)
		}()
	}
	wg.Wait()
	conflicts := 0
	for _, err := range errs {
		if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("errors %v: want exactly one conflict", errs)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeApprovalResolved); n != 1 {
		t.Fatalf("%d resolutions: %s", n, describe(evs))
	}
}

func TestInterruptEndsTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	ctx := context.Background()
	if err := m.Send(ctx, sess.ID, "slow", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := m.Interrupt(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnInterrupted {
		t.Fatalf("end %+v", end)
	}
}

func TestHarnessCrashAbortsTurnAndShimExits(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "partial"}},
		{Exit: intp(3)},
	}}}})
	m := e.manager()
	defer m.Close()
	sess := e.create(m)
	if err := m.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	if n := count(evs, proto.TypeTurnEnded); n != 1 {
		t.Fatalf("%d turn ends: %s", n, describe(evs))
	}
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnAborted {
		t.Fatalf("end %+v", end)
	}
	got, _ := e.store.Session(context.Background(), sess.ID)
	if got.Status != proto.StatusErrored {
		t.Fatalf("status %s", got.Status)
	}
	waitShimGone(t, m, sess.ID) // the exit record was acked
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/session/`
预期：编译失败，`undefined: Config` / `undefined: New`

- [ ] **步骤 3：实现** `internal/session/manager.go`

```go
// Package session runs Hoplet sessions: it creates them in the store,
// starts each harness behind a shim, ingests the harness's events into the
// store (committing the shim's spool offset in the same transaction), and
// reattaches to running shims after the daemon restarts.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/shim"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Errors.
var (
	// ErrBusy is returned by Send while the session's turn is running.
	ErrBusy = errors.New("session: a turn is running")
	// ErrNotRunning is returned for operations that need a live harness.
	ErrNotRunning = errors.New("session: harness is not running")
	// ErrUnknownHarness is returned for a harness kind the manager lacks.
	ErrUnknownHarness = errors.New("session: unknown harness")
)

// readyTimeout bounds how long a freshly spawned shim may take to accept
// connections.
const readyTimeout = 10 * time.Second

// Config configures a Manager.
type Config struct {
	Store     *store.Store
	Root      string // parent of the per-session directories
	Harnesses map[string]harness.StdioHarness
	Spawner   Spawner
	Env       []string // base environment for harnesses
	Logger    *slog.Logger
	// OnAppend, when set, is called after events are committed for a session.
	OnAppend func(proto.SessionID)
}

// Manager runs sessions. Its methods are safe for concurrent use.
type Manager struct {
	cfg Config
	log *slog.Logger
	// ctx scopes background work (ingestion, reattaching); it outlives
	// the requests that start it and ends with the manager.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	live    map[proto.SessionID]*live
	closing bool
	wg      sync.WaitGroup
}

// live is a session whose harness the manager is attached to.
type live struct {
	id    proto.SessionID
	h     harness.StdioHarness
	stdio *ShimStdio
	run   harness.Run
	done  chan struct{} // closed when ingestion stops
}

// New returns a manager; call Recover to reattach to running shims.
func New(cfg Config) (*Manager, error) {
	if cfg.Store == nil || cfg.Root == "" || cfg.Spawner == nil {
		return nil, errors.New("session: store, root and spawner are required")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, log: log, ctx: ctx, cancel: cancel, live: map[proto.SessionID]*live{}}, nil
}

// CreateRequest describes a new session.
type CreateRequest struct {
	Mode       proto.Mode
	Harness    string
	Directory  string // working directory; required for code sessions
	Isolation  proto.Isolation
	Permission proto.PermissionMode
	Title      string
	Origin     proto.Origin
}

// Create stores a new session. The harness starts on the first Send.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (proto.Session, error) {
	h, ok := m.cfg.Harnesses[req.Harness]
	if !ok {
		return proto.Session{}, fmt.Errorf("%w: %q", ErrUnknownHarness, req.Harness)
	}
	info, err := h.Probe(ctx)
	if err != nil {
		return proto.Session{}, fmt.Errorf("session: probe %s: %w", req.Harness, err)
	}
	if !info.Supported {
		return proto.Session{}, fmt.Errorf("session: %s %s is outside the supported versions", req.Harness, info.Version)
	}
	if req.Mode == proto.ModeCode && req.Directory == "" {
		return proto.Session{}, errors.New("session: a code session needs a directory")
	}
	if req.Mode == proto.ModeCode && req.Isolation == "" {
		req.Isolation = proto.InPlace
	}
	if req.Origin == "" {
		req.Origin = proto.OriginMe
	}
	id, err := newID()
	if err != nil {
		return proto.Session{}, err
	}
	sess, err := m.cfg.Store.CreateSession(ctx, proto.Session{
		ID: id, Mode: req.Mode, Harness: proto.HarnessRef{Kind: req.Harness, Version: info.Version},
		Directory: req.Directory, Isolation: req.Isolation, Permission: req.Permission,
		Owner: proto.OwnerMe, Origin: req.Origin, Title: req.Title,
	})
	if err != nil {
		return proto.Session{}, err
	}
	if err := os.MkdirAll(m.dir(id), 0o700); err != nil {
		return proto.Session{}, fmt.Errorf("session: create session dir: %w", err)
	}
	return sess, nil
}

func newID() (proto.SessionID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: new id: %w", err)
	}
	return proto.SessionID(hex.EncodeToString(b[:])), nil
}

func (m *Manager) dir(id proto.SessionID) string { return filepath.Join(m.cfg.Root, string(id)) }

// Send records the user's message and sends it to the harness, starting the
// harness if needed. key makes the call idempotent: a retry with the same
// key does not record or send the message again once its turn has started.
func (m *Manager) Send(ctx context.Context, id proto.SessionID, text, key string) error {
	if key == "" {
		return errors.New("session: send needs an idempotency key")
	}
	l, err := m.ensure(ctx, id)
	if err != nil {
		return err
	}
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil {
		return err
	}
	if open {
		return ErrBusy
	}
	res, err := m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Turn: last + 1, ReceiptKey: "send:" + key,
		Entries: []store.Entry{{Payload: proto.Message{ID: key, Role: proto.RoleUser, Text: text}}},
	})
	if err != nil {
		return err
	}
	m.notify(id)
	if res.Duplicate {
		// The message is recorded; it was sent unless the daemon died in
		// between, which shows as its turn never having started.
		return nil
	}
	return l.run.Send(ctx, text)
}

// Respond answers an approval with one of the harness's options. The first
// answer wins; later ones get store.ErrConflict.
func (m *Manager) Respond(ctx context.Context, id proto.SessionID, approvalID, optionID, decidedBy, key string) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	pending, err := m.cfg.Store.PendingApprovals(ctx, id)
	if err != nil {
		return err
	}
	outcome := proto.OutcomeAllowed
	found := false
	for _, p := range pending {
		if p.Request.ID != approvalID {
			continue
		}
		found = true
		for _, o := range p.Request.Options {
			if o.ID == optionID && o.Scope == proto.ScopeDeny {
				outcome = proto.OutcomeDenied
			}
		}
	}
	last, _, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil {
		return err
	}
	res, err := m.cfg.Store.ResolveApproval(ctx, id, last, time.Now(), proto.ApprovalResolved{
		ID: approvalID, Outcome: outcome, OptionID: optionID, DecidedBy: decidedBy,
	}, key)
	if err != nil {
		return err
	}
	m.notify(id)
	if res.Duplicate || !found {
		return nil
	}
	return l.run.Respond(ctx, approvalID, optionID)
}

// Interrupt stops the running turn.
func (m *Manager) Interrupt(ctx context.Context, id proto.SessionID) error {
	l, err := m.attached(id)
	if err != nil {
		return err
	}
	return l.run.Interrupt(ctx)
}

func (m *Manager) attached(id proto.SessionID) (*live, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.live[id]
	if !ok {
		return nil, ErrNotRunning
	}
	return l, nil
}

func (m *Manager) notify(id proto.SessionID) {
	if m.cfg.OnAppend != nil {
		m.cfg.OnAppend(id)
	}
}

// ensure returns the live session, attaching to its shim or spawning one.
func (m *Manager) ensure(ctx context.Context, id proto.SessionID) (*live, error) {
	if l, err := m.attached(id); err == nil {
		return l, nil
	}
	sess, err := m.cfg.Store.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.ArchivedAt != nil {
		return nil, fmt.Errorf("session: %s is archived", id)
	}
	if l, err := m.attach(ctx, sess, false); err == nil {
		return l, nil
	}
	if err := m.spawn(ctx, sess); err != nil {
		return nil, err
	}
	return m.attach(ctx, sess, true)
}

func (m *Manager) spec(sess proto.Session, inTurn bool) harness.Spec {
	return harness.Spec{
		Session: sess.ID, WorkDir: m.workDir(sess), Env: m.cfg.Env, Mode: sess.Permission,
		Resume: sess.Harness.NativeID,
		State:  harness.State{InTurn: inTurn, Native: sess.Harness.NativeID},
	}
}

func (m *Manager) workDir(sess proto.Session) string {
	if sess.Directory != "" {
		return sess.Directory
	}
	return m.dir(sess.ID) // personal sessions work in their own directory
}

func (m *Manager) spawn(ctx context.Context, sess proto.Session) error {
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	spec := m.spec(sess, false)
	if spec.Resume != "" {
		if info, err := h.Probe(ctx); err != nil || !info.Capabilities.Resume {
			spec.Resume = "" // the harness can't resume; start fresh
		}
	}
	l, err := h.Launch(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir(sess.ID), 0o700); err != nil {
		return fmt.Errorf("session: create session dir: %w", err)
	}
	return m.cfg.Spawner.Spawn(ctx, m.dir(sess.ID), spec.WorkDir, l)
}

// attach connects to the session's shim from the committed offset and
// starts ingesting. With wait it retries until a just-spawned shim is ready;
// without, it fails at once if no shim is listening.
func (m *Manager) attach(ctx context.Context, sess proto.Session, wait bool) (*live, error) {
	h, ok := m.cfg.Harnesses[sess.Harness.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownHarness, sess.Harness.Kind)
	}
	off, err := m.cfg.Store.IngestOffset(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	var c *shim.Client
	if wait {
		dctx, cancel := context.WithTimeout(ctx, readyTimeout)
		c, err = dialWhenReady(dctx, m.dir(sess.ID), off)
		cancel()
	} else {
		c, err = shim.Dial(ctx, m.dir(sess.ID), off)
	}
	if err != nil {
		return nil, err
	}
	last, open, err := m.cfg.Store.TurnState(ctx, sess.ID)
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	stdio := NewShimStdio(c)
	run, err := h.Attach(ctx, m.spec(sess, open), stdio)
	if err != nil {
		return nil, errors.Join(err, stdio.Close())
	}
	l := &live{id: sess.ID, h: h, stdio: stdio, run: run, done: make(chan struct{})}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, errors.Join(errors.New("session: manager is closing"), run.Close())
	}
	if existing, ok := m.live[sess.ID]; ok {
		m.mu.Unlock()
		return existing, run.Close()
	}
	m.live[sess.ID] = l
	m.wg.Add(1)
	m.mu.Unlock()
	go m.ingest(m.ctx, l, last, open)
	return l, nil
}

// Recover reattaches to every running shim and closes turns whose harness
// is gone. Call it once at daemon start.
func (m *Manager) Recover(ctx context.Context) error {
	sessions, err := m.cfg.Store.Sessions(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, sess := range sessions {
		if sess.ArchivedAt != nil {
			continue
		}
		if _, err := m.attach(ctx, sess, false); err == nil {
			continue
		}
		if err := m.abortOpenTurn(ctx, sess.ID, "the harness was not running when the daemon started"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// abortOpenTurn records TurnEnded{aborted} if the store still has an open
// turn; the store is the source of truth for turn state.
func (m *Manager) abortOpenTurn(ctx context.Context, id proto.SessionID, why string) error {
	last, open, err := m.cfg.Store.TurnState(ctx, id)
	if err != nil || !open {
		return err
	}
	_, err = m.cfg.Store.Append(ctx, store.AppendRequest{
		Session: id, Turn: last,
		Entries: []store.Entry{{Payload: proto.TurnEnded{Reason: proto.TurnAborted, Message: why}}},
	})
	if err == nil {
		m.notify(id)
	}
	return err
}

// Close detaches from every harness without stopping it, as a daemon that
// exits does; running turns continue in their shims.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	lives := make([]*live, 0, len(m.live))
	for _, l := range m.live {
		lives = append(lives, l)
	}
	m.mu.Unlock()
	var errs []error
	for _, l := range lives {
		errs = append(errs, l.run.Close())
	}
	m.wg.Wait()
	m.cancel()
	return errors.Join(errs...)
}
```

- [ ] **步骤 4：实现** `internal/session/ingest.go`

```go
package session

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// maxBatch bounds how many events one append commits.
const maxBatch = 256

// group is consecutive events that belong to the same turn.
type group struct {
	turn    uint32
	entries []store.Entry
	offset  int64 // highest spool offset in the group; 0 if none
}

// ingest commits the run's events until it ends. Raw native lines stay in
// the session's spool, which the shim keeps until the session is archived.
func (m *Manager) ingest(ctx context.Context, l *live, last uint32, open bool) {
	turn, inTurn := last, open
	committed, _ := m.cfg.Store.IngestOffset(ctx, l.id)
	var detached bool
	defer func() {
		m.mu.Lock()
		if m.live[l.id] == l {
			delete(m.live, l.id)
		}
		closing := m.closing
		m.mu.Unlock()
		close(l.done)
		m.wg.Done()
		if detached && !closing {
			go m.reattach(ctx, l.id)
		}
	}()

	flush := func(g *group) {
		if len(g.entries) == 0 && g.offset <= committed {
			return
		}
		req := store.AppendRequest{Session: l.id, Turn: g.turn, Entries: g.entries}
		if g.offset > committed {
			off := g.offset
			req.IngestOffset = &off
		}
		if err := m.appendTolerant(ctx, req); err != nil {
			m.log.Error("ingest append failed", "session", l.id, "err", err)
			return
		}
		if req.IngestOffset != nil {
			committed = *req.IngestOffset
		}
		m.notify(l.id)
	}

	events := l.run.Events()
	for {
		ev, ok := <-events
		if !ok {
			detached = errors.Is(l.run.Err(), harness.ErrDetached)
			return
		}
		g := &group{}
		for n := 0; ; n++ {
			t := uint32(0)
			switch ev.Payload.(type) {
			case proto.TurnStarted:
				turn++
				inTurn = true
				t = turn
			case proto.TurnEnded:
				t = turn
				inTurn = false
			default:
				if inTurn {
					t = turn
				}
			}
			if len(g.entries) > 0 && t != g.turn {
				flush(g)
				g = &group{}
			}
			if len(g.entries) == 0 {
				g.turn = t
			}
			if ev.Native != "" {
				native := ev.Native
				if _, err := m.cfg.Store.UpdateSession(ctx, l.id, store.SessionUpdate{NativeID: &native}); err != nil {
					m.log.Error("record native session", "session", l.id, "err", err)
				}
			}
			if ev.Payload != nil {
				g.entries = append(g.entries, store.Entry{Payload: ev.Payload})
			}
			if ev.Offset > g.offset {
				g.offset = ev.Offset
			}
			if ev.Exit != nil {
				flush(g)
				if err := m.abortOpenTurn(ctx, l.id, "the harness exited"); err != nil {
					m.log.Error("abort open turn", "session", l.id, "err", err)
				}
				if ev.Offset > 0 {
					if err := l.stdio.Ack(ev.Offset); err != nil {
						m.log.Error("ack exit record", "session", l.id, "err", err)
					}
				}
				g = &group{}
				break
			}
			if n+1 >= maxBatch {
				break
			}
			select {
			case next, more := <-events:
				if !more {
					flush(g)
					detached = errors.Is(l.run.Err(), harness.ErrDetached)
					return
				}
				ev = next
				continue
			default:
			}
			break
		}
		flush(g)
	}
}

// appendTolerant appends req; if the store rejects it as invalid, it
// retries entry by entry and replaces each invalid entry with a proto.Error,
// so one bad harness line never stalls ingestion.
func (m *Manager) appendTolerant(ctx context.Context, req store.AppendRequest) error {
	_, err := m.cfg.Store.Append(ctx, req)
	if !errors.Is(err, store.ErrInvalid) || len(req.Entries) == 0 {
		return err
	}
	for i, e := range req.Entries {
		one := store.AppendRequest{Session: req.Session, Turn: req.Turn, Entries: []store.Entry{e}}
		if i == len(req.Entries)-1 {
			one.IngestOffset = req.IngestOffset
		}
		if _, err := m.cfg.Store.Append(ctx, one); errors.Is(err, store.ErrInvalid) {
			one.Entries = []store.Entry{{Payload: proto.Error{Message: "invalid harness event: " + err.Error()}}}
			one.Turn = 0
			if _, err := m.cfg.Store.Append(ctx, one); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

// reattach reconnects to a session whose connection ended without an exit.
func (m *Manager) reattach(ctx context.Context, id proto.SessionID) {
	sess, err := m.cfg.Store.Session(ctx, id)
	if err != nil {
		return
	}
	if _, err := m.attach(ctx, sess, false); err != nil {
		if err := m.abortOpenTurn(ctx, id, "lost the connection to the harness"); err != nil {
			m.log.Error("abort open turn", "session", id, "err", err)
		}
	}
}
```

- [ ] **步骤 5：运行，确认通过**

运行：`go test -race ./internal/session/` 和 `golangci-lint run ./...`
预期：`ok`；`0 issues.`

- [ ] **步骤 6：commit**（用明确的路径 `git add`，不要 `git add -A`）

```bash
git commit -s -m "feat(session): run sessions and ingest harness events into the store"
```

### 任务 4：验证关键假设——守护进程中途走开后续接

**文件：** 创建 `internal/session/recover_test.go`

只加测试，**不改实现**：测试应当直接通过。失败说明 0002 的前提不成立，停下来报告。

- [ ] **步骤 1：写测试** `internal/session/recover_test.go`

```go
package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/yangflow/hoplet/internal/harness/fake"
	"github.com/yangflow/hoplet/proto"
)

// TestRecoverMidTurn checks the property the shim exists for: the daemon
// goes away mid-turn, a new one attaches from the committed offset, and the
// turn completes in the store with every event exactly once.
func TestRecoverMidTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Session: "n1", Turns: []fake.Turn{{Steps: []fake.Step{
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "one "}},
		{SleepMS: 1500},
		{Emit: &fake.Ev{Ev: "delta", ID: "m1", Text: "two"}},
		{Emit: &fake.Ev{Ev: "message", ID: "m1", Text: "one two"}},
	}}}})
	first := e.manager()
	sess := e.create(first)
	if err := first.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "first delta", func(evs []proto.Event) bool { return count(evs, proto.TypeMessageDelta) == 1 })
	if err := first.Close(); err != nil { // the daemon goes away; the shim and harness keep running
		t.Fatal(err)
	}

	second := e.manager()
	defer second.Close()
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	want := []proto.EventType{proto.TypeMessage, proto.TypeTurnStarted, proto.TypeMessageDelta, proto.TypeMessageDelta, proto.TypeMessage, proto.TypeTurnEnded}
	if len(evs) != len(want) {
		t.Fatalf("events: %s", describe(evs))
	}
	for i, ev := range evs {
		if ev.Type != want[i] {
			t.Fatalf("event %d: %s", i, describe(evs))
		}
	}
	if end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded); end.Reason != proto.TurnCompleted {
		t.Fatalf("end %+v", end)
	}
}

func TestRecoverWithoutShimAbortsOpenTurn(t *testing.T) {
	e := newEnv(t, fake.Script{Turns: []fake.Turn{{Steps: []fake.Step{{SleepMS: 60_000}}}}})
	first := e.manager()
	sess := e.create(first)
	if err := first.Send(context.Background(), sess.ID, "go", "k1"); err != nil {
		t.Fatal(err)
	}
	e.waitFor(sess.ID, "turn start", func(evs []proto.Event) bool { return count(evs, proto.TypeTurnStarted) == 1 })
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	killShim(t, first.dir(sess.ID))
	waitShimGone(t, first, sess.ID)

	second := e.manager()
	defer second.Close()
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	evs := e.waitFor(sess.ID, "turn end", turnEnded)
	end := payloads(t, evs)[len(evs)-1].(proto.TurnEnded)
	if end.Reason != proto.TurnAborted || !strings.Contains(end.Message, "not running") {
		t.Fatalf("end %+v", end)
	}
}

// killShim kills the shim that owns dir and its harness's process group,
// as if the machine had lost both.
func killShim(t *testing.T, dir string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "spool.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(b), "\n")
	var start struct {
		Pid int `json:"pid"`
	}
	if err := json.Unmarshal([]byte(first), &start); err != nil || start.Pid == 0 {
		t.Fatalf("no start record in %q: %v", first, err)
	}
	out, err := exec.Command("pgrep", "-f", "shim --dir "+dir).Output()
	if err != nil {
		t.Fatalf("find shim: %v", err)
	}
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	}
	_ = syscall.Kill(-start.Pid, syscall.SIGKILL) // the harness may already be gone
}
```

- [ ] **步骤 2：运行，确认通过并重复 10 次**

运行：`go test -race ./internal/session/ && go test -race -count=10 -run Recover ./internal/session/`
预期：`ok`

- [ ] **步骤 3：确认没有泄漏进程**

运行：`pgrep -f 'sessbin[0-9]*/(hoplet shim|fakeharness)' | wc -l`
预期：`0`

- [ ] **步骤 4：commit**

```bash
git add internal/session/recover_test.go
git commit -s -m "test(session): verify a new manager resumes a turn mid-flight"
```

### 任务 5：全量检查

- [ ] **步骤 1：** `make check`，以及 `go test -race -count=10 ./internal/session/`
预期：lint `0 issues.`；所有包 `ok`；没有泄漏的 shim 或假 harness 进程。

- [ ] **步骤 2：推送并开 PR（需维护者同意）**

```bash
git push -u origin feat/session
gh pr create --title "feat(session): run sessions behind shims and ingest into the store" --fill
```

## 完成标准

- 发一条消息：用户消息、轮次开始、增量、完整消息、轮次结束按顺序入库，seq 连续，原生会话 ID 和偏移已提交。
- 两个设备同时回答一个审批：恰好一个成功，另一个得到 `store.ErrConflict`，只有一条决定事件。
- 中断立即结束本轮；harness 崩溃时本轮记为 aborted、会话为 errored，exit 记录被 ack、shim 退出。
- 管理器在轮次中途关闭，新管理器 `Recover` 后本轮完整入库，每个事件恰好一次；shim 也不在了时，未结束的轮次被记为 aborted。
- `make check` 本地和 CI 通过；测试不泄漏进程。

## 实现与计划的差异（评审后追加）

任务评审、整分支终审和五轮复审之后，合并前在计划之外补了以下内容；后续 PR 以 `main` 上的真实代码为准：

| 方面 | 变化 |
|---|---|
| 不丢事件 | 批次在追加前用 `store.ValidateEntry` 逐条校验，无效条目替换成 `proto.Error`，再连同偏移一次追加；任何追加失败都结束本次入库、不 ack，从已提交偏移重连（指数退避，上限 5 秒，不限次数）；exit 的 abort 与"轮次之间退出"的错误和 exit 偏移同批提交；轮次外的 TurnEnded 只保留偏移，轮次内又来 TurnStarted 时先把旧轮记为 failed；存储的 TurnEnded 投影只更新未结束的轮次 |
| 偏移 | 新增 `harness.Position` 行：shim 的 start 记录等也产生只带偏移的事件；分块长行未拼完时，所有行的偏移都封顶在它的起点（包括恰好是 MaxLine 整数倍的行）；start / exit 记录先冲刷未完成的行 |
| 并发 | 每会话一把锁串行 ensure / attach / spawn / reattach / stop；`pending` + `sendMu` + 先读 pending 再读轮次状态，使 `ErrBusy` 原子；新 attach 的会话在入库追上 shim 的 spool 大小（welcome 帧携带）之前算"正在追平"；Exit 先摘掉会话句柄再 ack |
| 连接 | 只有"没有 shim 在监听"才中止轮次；`bad_offset`、协议版本、未知 harness 记录一次错误后停止重连；其他错误带退避重连 |
| Send / Respond | `Send` 返回 `SendResult`，消息已记录但轮次未开始时重发（新增 `store.Receipt`）；`Respond` 校验选项（`ErrInvalidOption`），用请求所在的轮次，重复时投递库里记录的选项 |
| 生命周期 | `Stop`（TERM → 5 秒 → KILL）、`Archive`（会话锁内 Stop + 归档）；Recover 停掉已归档会话仍在运行的 shim；开新轮次前做版本门控（`ErrUnsupportedVersion`） |
| API | `Config.Harnesses` 改为 `harness.Harness`，支持 `DirectHarness`（Hermes）；通知改为 `store.Changes()`；新增 `Steer`、`Answer`、`SetPermission`；`harness.Event.StdinError` |
| 其他 | 50ms 合批窗口；原生会话 ID 变化才写；shim 起不来时附带 `shim.log` 末尾；轮次之间非零退出记一条错误；假 harness 新增 `quiet_start`、重复审批静默忽略 |
| 已接受并写入包文档 | 未写 exit 就崩溃的 shim 重新拉起后会重放旧尾部；长行封顶期间交错的记录、MaxLine 分块产生的条目在重放后可能重复；长 stderr / stdin 行跨记录时重放可能重复 stdout 的轮次事件；长行写到一半时追平会等待 |
