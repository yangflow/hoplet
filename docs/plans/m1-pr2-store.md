# M1 · PR2 存储 · 详细计划

> **执行方式：** 按任务顺序逐步执行；每一步用 `- [ ]` 勾选。每个任务结束时 `go test -race ./...` 都应通过。

**目标：** 实现 Hoplet 的持久层：会话、事件日志、由事件推导的投影（会话状态、轮次、审批）、命令回执、spool 读取偏移，以及内容寻址的 blob 存储；全部写入在一个事务里完成，并遵守丢失契约。

**架构：** `internal/store` 用纯 Go SQLite（`modernc.org/sqlite`，WAL 模式），一个连接串行写入。`Append` 在一个事务里：查回执（重复则直接返回）→ 分配会话内连续 seq → 写事件 → 更新投影 → 写回执和 spool 偏移。带回执、轮次结束、审批决定的写入，以及创建会话，用 `synchronous=FULL` 提交；其余用 `NORMAL`。迁移按编号只向前，执行前备份数据库，保留最近 3 份。`proto.Session` 等会话类型放进 `proto`，供后续守护进程和客户端共用。

**技术栈：** Go 1.27、`modernc.org/sqlite` v1.61.0（总计划已列入的依赖）。

**依据：** [M1 总计划](m1-overview.md)、[0004 会话数据模型](../design/0004-session-data-model.md)（实体、存储、丢失契约、演进规则）、[0007 命令与审批](../design/0007-commands-approvals.md)（审批状态）、[0005 同步协议](../design/0005-sync-protocol.md)（幂等键）。

## 全局约束

- 只新增 `modernc.org/sqlite` v1.61.0 一个直接依赖（以及它带来的间接依赖）。
- 代码、注释、commit 用英文；导出标识符都有文档注释；库代码不 panic；不用 lint 抑制注释。
- 时间一律以 UTC 毫秒存储（`INTEGER` 毫秒时间戳）。
- 已持久化的事件必须永远能解码：存储层只做 `proto.NewEvent` 时的校验，读取时不校验。
- 事件 seq 会话内单调递增、永不复用；压缩只删除被完整消息取代的增量，允许出现 seq 空洞。
- 分支 `feat/store`；commit 带 DCO 签名（`git commit -s`）；完成前 `make check` 通过。

## 文件

| 文件 | 职责 |
|---|---|
| `proto/session.go`、`proto/session_test.go` | `Session`、`HarnessRef` 及 `Mode` / `Isolation` / `Owner` / `Origin` / `SessionStatus` 枚举 |
| `internal/store/store.go` | `Open`、`Close`、事务辅助 `tx`（含 FULL / NORMAL 切换）、`ErrNotFound`、包文档（丢失契约） |
| `internal/store/migrate.go`、`internal/store/migrations/0001_init.sql` | 嵌入式编号迁移、迁移前备份（保留 3 份）、拒绝更新版本的库 |
| `internal/store/session.go` | `CreateSession`、`Session`、`Sessions`、`ArchiveSession` |
| `internal/store/append.go` | `Entry`、`AppendRequest`、`AppendResult`、`Append`、投影规则 |
| `internal/store/events.go` | `Events`、`PendingApprovals`、`IngestOffset` |
| `internal/store/compact.go` | `Compact`：删除被完整消息取代的增量 |
| `internal/store/blob.go` | `PutBlob`、`OpenBlob`（先 fsync 再 rename，内容去重，校验引用格式） |
| `internal/store/*_test.go`、`bench_test.go` | 各任务测试与追加基准 |

## 接口（后续 PR 依赖）

- `func Open(ctx context.Context, dir string) (*Store, error)`；`func (s *Store) Close() error`
- `func (s *Store) CreateSession(ctx, proto.Session) (proto.Session, error)`；`Session(ctx, id)`；`Sessions(ctx)`；`ArchiveSession(ctx, id, t)`
- `type Entry struct { Payload proto.Payload; Raw *proto.BlobRef }`
- `type AppendRequest struct { Session proto.SessionID; Turn uint32; Time time.Time; Entries []Entry; ReceiptKey string; IngestOffset *int64 }`
- `type AppendResult struct { FirstSeq, LastSeq uint64; Duplicate bool }`；`func (s *Store) Append(ctx, AppendRequest) (AppendResult, error)`
- `Events(ctx, id, after uint64, limit int) ([]proto.Event, error)`；`PendingApprovals(ctx, id) ([]proto.ApprovalRequested, error)`；`IngestOffset(ctx, id) (int64, error)`
- `Compact(ctx, id) (int64, error)`；`PutBlob(io.Reader) (proto.BlobRef, error)`；`OpenBlob(proto.BlobRef) (io.ReadCloser, error)`
- 投影规则：`turn.started` → running；`approval.requested` → waiting；最后一个待审批被决定 → running；`turn.ended` completed / interrupted → idle，failed / aborted → errored

---

### 任务 0：建分支、加依赖

- [ ] **步骤 1：**

```bash
git switch -c feat/store
go get modernc.org/sqlite@v1.61.0
```

预期：`go.mod` 出现 `modernc.org/sqlite v1.61.0`（任务 2 首次 import 后，`go mod tidy` 会把它从 indirect 变为直接依赖；在任务 2 commit 前运行 `go mod tidy`）。

### 任务 1：会话类型（proto）

**文件：** 创建 `proto/session.go`、`proto/session_test.go`

- [ ] **步骤 1：写失败的测试**

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
		ID: "s1", Mode: ModeCode, Harness: HarnessRef{Kind: "claude", Version: "2.1.312", NativeID: "n1"},
		Directory: "/work", Isolation: InPlace, Owner: OwnerMe, Origin: OriginMe, Status: StatusIdle,
		Title: "sync", CreatedAt: archived.Add(-time.Hour), UpdatedAt: archived, ArchivedAt: &archived, LastSeq: 7,
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

运行：`go test -race ./proto/`
预期：编译失败，`undefined: Session`

- [ ] **步骤 3：实现** `proto/session.go`

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
	ID         SessionID     `json:"id"`
	Mode       Mode          `json:"mode"`
	Harness    HarnessRef    `json:"harness"`
	Directory  string        `json:"directory,omitempty"`
	Isolation  Isolation     `json:"isolation,omitempty"`
	Owner      Owner         `json:"owner"`
	Origin     Origin        `json:"origin"`
	Status     SessionStatus `json:"status"`
	Title      string        `json:"title,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	ArchivedAt *time.Time    `json:"archived_at,omitempty"`
	LastSeq    uint64        `json:"last_seq"`
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./proto/`
预期：`ok`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(proto): add session types"
```

### 任务 2：打开数据库与迁移

**文件：** 创建 `internal/store/store.go`、`internal/store/migrate.go`、`internal/store/migrations/0001_init.sql`、`internal/store/store_test.go`；commit 前运行 `go mod tidy`

**接口：** `Open`、`Close`、`(*Store).tx`、`(*Store).backup`、`DBName`、`ErrNotFound`

- [ ] **步骤 1：写失败的测试**

`internal/store/store_test.go`

```go
package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if v != 1 {
		t.Fatalf("schema version %d, want 1", v)
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
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/`
预期：编译失败，`undefined: Open`

- [ ] **步骤 3：实现** `internal/store/migrations/0001_init.sql`

```sql
CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    mode            TEXT NOT NULL,
    harness_kind    TEXT NOT NULL,
    harness_version TEXT NOT NULL DEFAULT '',
    native_id       TEXT NOT NULL DEFAULT '',
    directory       TEXT NOT NULL DEFAULT '',
    isolation       TEXT NOT NULL DEFAULT '',
    owner           TEXT NOT NULL,
    origin          TEXT NOT NULL,
    status          TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    archived_at     INTEGER,
    last_seq        INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE events (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    seq        INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    turn       INTEGER NOT NULL DEFAULT 0,
    type       TEXT NOT NULL,
    v          INTEGER NOT NULL,
    payload    TEXT NOT NULL,
    raw_sha256 TEXT,
    raw_size   INTEGER,
    message_id TEXT,
    PRIMARY KEY (session_id, seq)
) STRICT, WITHOUT ROWID;

CREATE INDEX events_message ON events (session_id, message_id) WHERE message_id IS NOT NULL;

CREATE TABLE turns (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    n          INTEGER NOT NULL,
    start_seq  INTEGER NOT NULL,
    end_seq    INTEGER,
    reason     TEXT,
    PRIMARY KEY (session_id, n)
) STRICT, WITHOUT ROWID;

CREATE TABLE approvals (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(id),
    request_seq  INTEGER NOT NULL,
    kind         TEXT NOT NULL,
    status       TEXT NOT NULL,
    option_id    TEXT NOT NULL DEFAULT '',
    decided_by   TEXT NOT NULL DEFAULT '',
    resolved_seq INTEGER
) STRICT;

CREATE INDEX approvals_pending ON approvals (session_id) WHERE status = 'pending';

CREATE TABLE receipts (
    key        TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    first_seq  INTEGER NOT NULL,
    last_seq   INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE ingest_offsets (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id),
    offset     INTEGER NOT NULL
) STRICT;
```

- [ ] **步骤 4：实现** `internal/store/store.go`

```go
// Package store keeps Hoplet's durable state: sessions, their event logs,
// projections derived from those events, command receipts and blobs.
//
// Loss contract: ordinary appends commit with synchronous=NORMAL, so a power
// loss may drop the latest appends. Appends that carry a receipt, a turn end
// or an approval decision, and session creation, commit with
// synchronous=FULL and are on disk when the call returns.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DBName is the database file inside the store directory.
const DBName = "hoplet.db"

// ErrNotFound is returned when a session does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is Hoplet's database. It is safe for concurrent use; writes are
// serialized over a single connection.
type Store struct {
	dir string
	db  *sql.DB
}

// Open opens or creates the store in dir, migrating the schema forward.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	path := filepath.Join(dir, DBName)
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{dir: dir, db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// tx runs fn in a transaction; durable transactions commit with
// synchronous=FULL.
func (s *Store) tx(ctx context.Context, durable bool, fn func(*sql.Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: conn: %w", err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if durable {
		if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
			return fmt.Errorf("store: set synchronous: %w", err)
		}
		defer func() {
			_, rerr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA synchronous=NORMAL")
			err = errors.Join(err, rerr)
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}
```

- [ ] **步骤 5：实现** `internal/store/migrate.go`

```go
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// keepBackups is how many pre-migration copies of the database are kept.
const keepBackups = 3

// migration is one numbered schema step.
type migration struct {
	version int
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		n, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("store: bad migration name %q", e.Name())
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			return nil, fmt.Errorf("store: bad migration name %q: %w", e.Name(), err)
		}
		b, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{v, string(b)})
	}
	slices.SortFunc(ms, func(a, b migration) int { return a.version - b.version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations must be numbered 1..n without gaps, got %d at position %d", m.version, i+1)
		}
	}
	return ms, nil
}

// migrate applies pending migrations forward, one transaction each, after
// backing up an existing database.
func (s *Store) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var current int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if current > len(ms) {
		return fmt.Errorf("store: database schema version %d is newer than this binary (%d)", current, len(ms))
	}
	if current == len(ms) {
		return nil
	}
	if current > 0 {
		if err := s.backup(ctx, current); err != nil {
			return err
		}
	}
	for _, m := range ms[current:] {
		err := s.tx(ctx, true, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("store: migration %d: %w", m.version, err)
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				return fmt.Errorf("store: set schema version %d: %w", m.version, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// backup copies the database before migrating from version v and prunes
// older copies beyond keepBackups.
func (s *Store) backup(ctx context.Context, v int) error {
	dst := filepath.Join(s.dir, fmt.Sprintf("%s.v%d.bak", DBName, v))
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("store: remove old backup: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("store: backup before migration: %w", err)
	}
	old, err := filepath.Glob(filepath.Join(s.dir, DBName+".v*.bak"))
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	slices.SortFunc(old, func(a, b string) int { return backupVersion(a) - backupVersion(b) })
	for len(old) > keepBackups {
		if err := os.Remove(old[0]); err != nil {
			return fmt.Errorf("store: prune backup: %w", err)
		}
		old = old[1:]
	}
	return nil
}

func backupVersion(path string) int {
	s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), DBName+".v"), ".bak")
	v, _ := strconv.Atoi(s)
	return v
}
```

- [ ] **步骤 6：运行，确认通过**

运行：`go test -race ./internal/store/`
预期：`ok`

- [ ] **步骤 7：commit**

```bash
git add -A
git commit -s -m "feat(store): open SQLite store with forward-only migrations"
```

### 任务 3：会话

**文件：** 创建 `internal/store/session.go`、`internal/store/session_test.go`

**接口：** `CreateSession`、`Session`、`Sessions`、`ArchiveSession`；测试辅助 `newSession`

- [ ] **步骤 1：写失败的测试**

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
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/`
预期：编译失败，`s.CreateSession undefined`

- [ ] **步骤 3：实现** `internal/store/session.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

const sessionColumns = `id, mode, harness_kind, harness_version, native_id, directory, isolation,
	owner, origin, status, title, created_at, updated_at, archived_at, last_seq`

// CreateSession stores a new session. Status defaults to idle and
// CreatedAt to now; UpdatedAt equals CreatedAt. The write is durable.
func (s *Store) CreateSession(ctx context.Context, sess proto.Session) (proto.Session, error) {
	if sess.ID == "" || sess.Mode == "" || sess.Harness.Kind == "" || sess.Owner == "" || sess.Origin == "" {
		return proto.Session{}, errors.New("store: session needs id, mode, harness kind, owner and origin")
	}
	if sess.Status == "" {
		sess.Status = proto.StatusIdle
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
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, 0)`,
			sess.ID, sess.Mode, sess.Harness.Kind, sess.Harness.Version, sess.Harness.NativeID,
			sess.Directory, sess.Isolation, sess.Owner, sess.Origin, sess.Status, sess.Title,
			sess.CreatedAt.UnixMilli(), sess.UpdatedAt.UnixMilli())
		if err != nil {
			return fmt.Errorf("store: create session %s: %w", sess.ID, err)
		}
		return nil
	})
	if err != nil {
		return proto.Session{}, err
	}
	return sess, nil
}

// Session returns one session, or ErrNotFound.
func (s *Store) Session(ctx context.Context, id proto.SessionID) (proto.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Session{}, fmt.Errorf("%w: session %s", ErrNotFound, id)
	}
	return sess, err
}

// Sessions returns all sessions, most recently updated first.
func (s *Store) Sessions(ctx context.Context) ([]proto.Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions ORDER BY updated_at DESC, id`)
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
		return nil
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
		&created, &updated, &archived, &sess.LastSeq)
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

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./internal/store/`
预期：`ok`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(store): add session records"
```

### 任务 4：事务追加、投影与读取

**文件：** 创建 `internal/store/append.go`、`internal/store/events.go`、`internal/store/append_test.go`

**接口：** `Entry`、`AppendRequest`、`AppendResult`、`Append`、`Events`、`PendingApprovals`、`IngestOffset`；测试辅助 `appendOK`

- [ ] **步骤 1：写失败的测试**

`internal/store/append_test.go`

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func appendOK(t testing.TB, s *Store, req AppendRequest) AppendResult {
	t.Helper()
	if req.Time.IsZero() {
		req.Time = t0
	}
	res, err := s.Append(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAppendProjectsStatusTurnsAndApprovals(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	status := func(want proto.SessionStatus) {
		t.Helper()
		got, err := s.Session(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Fatalf("status %s, want %s", got.Status, want)
		}
	}

	res := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "hi"}, Raw: &proto.BlobRef{SHA256: "ab", Size: 2}},
	}})
	if res.FirstSeq != 1 || res.LastSeq != 2 {
		t.Fatalf("seqs %+v", res)
	}
	status(proto.StatusRunning)

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ApprovalRequested{
		ID: "a1", Kind: proto.ApprovalCommand, Title: "Run command",
		Options: []proto.ApprovalOption{{ID: "once", Label: "Allow once", Scope: proto.ScopeOnce}},
	}}}})
	status(proto.StatusWaiting)
	pending, err := s.PendingApprovals(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "a1" {
		t.Fatalf("pending %+v", pending)
	}

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.ApprovalResolved{
		ID: "a1", Outcome: proto.OutcomeAllowed, OptionID: "once", DecidedBy: "device:mac",
	}}}})
	status(proto.StatusRunning)
	if pending, _ := s.PendingApprovals(ctx, "s"); len(pending) != 0 {
		t.Fatalf("approval still pending: %+v", pending)
	}

	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.TurnEnded{Reason: proto.TurnFailed, Message: "harness exited"}}}})
	status(proto.StatusErrored)
	var endSeq uint64
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT end_seq, reason FROM turns WHERE session_id = 's' AND n = 1`).Scan(&endSeq, &reason); err != nil {
		t.Fatal(err)
	}
	if endSeq != 5 || reason != string(proto.TurnFailed) {
		t.Fatalf("turn row end_seq=%d reason=%s", endSeq, reason)
	}

	evs, err := s.Events(ctx, "s", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 5 {
		t.Fatalf("got %d events, want 5", len(evs))
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) || ev.Session != "s" || !ev.Time.Equal(t0) {
			t.Fatalf("event %d envelope: %+v", i, ev)
		}
		if _, err := ev.Decode(); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	if evs[1].Raw == nil || evs[1].Raw.SHA256 != "ab" {
		t.Fatalf("raw ref lost: %+v", evs[1].Raw)
	}
	if after, _ := s.Events(ctx, "s", 3, 100); len(after) != 2 || after[0].Seq != 4 {
		t.Fatalf("events after 3: %+v", after)
	}
}

func TestAppendReceiptIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	req := AppendRequest{Session: "s", Turn: 1, ReceiptKey: "cmd-1", Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.Message{ID: "u1", Role: proto.RoleUser, Text: "fix it"}},
	}}
	first := appendOK(t, s, req)
	second := appendOK(t, s, req)
	if first.Duplicate || !second.Duplicate || second.FirstSeq != first.FirstSeq || second.LastSeq != first.LastSeq {
		t.Fatalf("first %+v second %+v", first, second)
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 2 {
		t.Fatalf("duplicate append wrote events: %d", len(evs))
	}
}

func TestAppendIngestOffset(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	if off, err := s.IngestOffset(ctx, "s"); err != nil || off != 0 {
		t.Fatalf("initial offset %d, %v", off, err)
	}
	for _, want := range []int64{120, 480} {
		off := want
		appendOK(t, s, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.Thinking{Text: "x"}}}, IngestOffset: &off})
		if got, err := s.IngestOffset(ctx, "s"); err != nil || got != want {
			t.Fatalf("offset %d, %v; want %d", got, err, want)
		}
	}
}

func TestAppendFailsAtomically(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	_, err := s.Append(ctx, AppendRequest{Session: "s", Time: t0, Entries: []Entry{
		{Payload: proto.Thinking{Text: "kept only if all succeed"}},
		{Payload: proto.TurnStarted{}}, // turn 0 is invalid for turn.started
	}})
	if err == nil {
		t.Fatal("append with an invalid entry succeeded")
	}
	if evs, _ := s.Events(ctx, "s", 0, 100); len(evs) != 0 {
		t.Fatalf("partial append left %d events", len(evs))
	}
	if sess, _ := s.Session(ctx, "s"); sess.LastSeq != 0 {
		t.Fatalf("last_seq moved to %d", sess.LastSeq)
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "missing", Entries: []Entry{{Payload: proto.Thinking{}}}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session: %v", err)
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "s"}); err == nil {
		t.Fatal("empty append accepted")
	}
	if _, err := s.Append(ctx, AppendRequest{Session: "s", Entries: []Entry{{Payload: proto.ApprovalResolved{ID: "a", Outcome: "maybe"}}}}); !errors.Is(err, proto.ErrInvalidPayload) {
		t.Fatalf("invalid payload: %v", err)
	}
}

func TestDurableAppendRestoresSynchronous(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, ReceiptKey: "k", Entries: []Entry{{Payload: proto.TurnStarted{}}}})
	var sync int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if sync != 1 { // 1 = NORMAL
		t.Fatalf("synchronous = %d after a durable append, want 1 (NORMAL)", sync)
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/`
预期：编译失败，`undefined: AppendRequest`

- [ ] **步骤 3：实现** `internal/store/append.go`

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Entry is one payload to append, with the harness's raw event if any.
type Entry struct {
	Payload proto.Payload
	Raw     *proto.BlobRef
}

// AppendRequest appends entries to a session's log in one transaction.
type AppendRequest struct {
	Session proto.SessionID
	Turn    uint32
	Time    time.Time
	Entries []Entry
	// ReceiptKey, when set, makes the append idempotent: a second append
	// with the same key changes nothing and reports Duplicate.
	ReceiptKey string
	// IngestOffset, when set, records how far the shim spool has been read,
	// in the same transaction as the events it produced.
	IngestOffset *int64
}

// AppendResult reports the sequence numbers assigned.
type AppendResult struct {
	FirstSeq  uint64
	LastSeq   uint64
	Duplicate bool
}

// Append writes events, updates projections (session status, turns,
// approvals), records the receipt and ingest offset, all in one
// transaction. It is durable when it carries a receipt, a turn end or an
// approval decision.
func (s *Store) Append(ctx context.Context, req AppendRequest) (AppendResult, error) {
	if len(req.Entries) == 0 && req.IngestOffset == nil {
		return AppendResult{}, errors.New("store: append needs entries or an ingest offset")
	}
	if req.Time.IsZero() {
		req.Time = time.Now()
	}
	events := make([]proto.Event, 0, len(req.Entries))
	for _, e := range req.Entries {
		ev, err := proto.NewEvent(req.Session, 0, req.Turn, req.Time, e.Payload)
		if err != nil {
			return AppendResult{}, fmt.Errorf("store: %w", err)
		}
		ev.Raw = e.Raw
		events = append(events, ev)
	}
	var res AppendResult
	err := s.tx(ctx, durable(req), func(tx *sql.Tx) error {
		if req.ReceiptKey != "" {
			err := tx.QueryRowContext(ctx, `SELECT first_seq, last_seq FROM receipts WHERE key = ?`, req.ReceiptKey).
				Scan(&res.FirstSeq, &res.LastSeq)
			if err == nil {
				res.Duplicate = true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("store: read receipt: %w", err)
			}
		}
		var last uint64
		var status proto.SessionStatus
		err := tx.QueryRowContext(ctx, `SELECT last_seq, status FROM sessions WHERE id = ?`, req.Session).Scan(&last, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: session %s", ErrNotFound, req.Session)
		}
		if err != nil {
			return fmt.Errorf("store: read session: %w", err)
		}
		res.FirstSeq = last + 1
		for i := range events {
			last++
			events[i].Seq = last
			if err := insertEvent(ctx, tx, events[i], messageID(req.Entries[i].Payload)); err != nil {
				return err
			}
			if status, err = project(ctx, tx, events[i], req.Entries[i].Payload, status); err != nil {
				return err
			}
		}
		res.LastSeq = last
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET last_seq = ?, status = ?, updated_at = ? WHERE id = ?`,
			last, status, req.Time.UTC().UnixMilli(), req.Session)
		if err != nil {
			return fmt.Errorf("store: update session: %w", err)
		}
		if req.ReceiptKey != "" {
			_, err := tx.ExecContext(ctx, `INSERT INTO receipts (key, session_id, first_seq, last_seq, created_at) VALUES (?, ?, ?, ?, ?)`,
				req.ReceiptKey, req.Session, res.FirstSeq, res.LastSeq, req.Time.UTC().UnixMilli())
			if err != nil {
				return fmt.Errorf("store: write receipt: %w", err)
			}
		}
		if req.IngestOffset != nil {
			_, err := tx.ExecContext(ctx, `INSERT INTO ingest_offsets (session_id, offset) VALUES (?, ?)
				ON CONFLICT (session_id) DO UPDATE SET offset = excluded.offset`, req.Session, *req.IngestOffset)
			if err != nil {
				return fmt.Errorf("store: write ingest offset: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return AppendResult{}, err
	}
	return res, nil
}

func durable(req AppendRequest) bool {
	if req.ReceiptKey != "" {
		return true
	}
	for _, e := range req.Entries {
		switch e.Payload.(type) {
		case proto.TurnEnded, proto.ApprovalResolved:
			return true
		}
	}
	return false
}

// messageID returns the message a delta or message belongs to, so that
// compaction can find superseded deltas.
func messageID(p proto.Payload) sql.NullString {
	switch p := p.(type) {
	case proto.MessageDelta:
		return sql.NullString{String: p.MessageID, Valid: true}
	case proto.Message:
		return sql.NullString{String: p.ID, Valid: true}
	}
	return sql.NullString{}
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev proto.Event, msgID sql.NullString) error {
	var rawSHA sql.NullString
	var rawSize sql.NullInt64
	if ev.Raw != nil {
		rawSHA = sql.NullString{String: ev.Raw.SHA256, Valid: true}
		rawSize = sql.NullInt64{Int64: ev.Raw.Size, Valid: true}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO events (session_id, seq, ts, turn, type, v, payload, raw_sha256, raw_size, message_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.Session, ev.Seq, ev.Time.UnixMilli(), ev.Turn, ev.Type, ev.V, string(ev.Payload), rawSHA, rawSize, msgID)
	if err != nil {
		return fmt.Errorf("store: insert event %d: %w", ev.Seq, err)
	}
	return nil
}

// project applies one event to the projections and returns the session's
// new status.
func project(ctx context.Context, tx *sql.Tx, ev proto.Event, p proto.Payload, status proto.SessionStatus) (proto.SessionStatus, error) {
	exec := func(q string, args ...any) error {
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("store: project %s at seq %d: %w", ev.Type, ev.Seq, err)
		}
		return nil
	}
	switch p := p.(type) {
	case proto.TurnStarted:
		if ev.Turn == 0 {
			return status, fmt.Errorf("store: turn.started at seq %d has no turn number", ev.Seq)
		}
		return proto.StatusRunning, exec(`INSERT INTO turns (session_id, n, start_seq) VALUES (?, ?, ?)`, ev.Session, ev.Turn, ev.Seq)
	case proto.TurnEnded:
		next := proto.StatusIdle
		if p.Reason == proto.TurnFailed || p.Reason == proto.TurnAborted {
			next = proto.StatusErrored
		}
		return next, exec(`UPDATE turns SET end_seq = ?, reason = ? WHERE session_id = ? AND n = ?`, ev.Seq, p.Reason, ev.Session, ev.Turn)
	case proto.ApprovalRequested:
		return proto.StatusWaiting, exec(`INSERT INTO approvals (id, session_id, request_seq, kind, status) VALUES (?, ?, ?, ?, 'pending')`,
			p.ID, ev.Session, ev.Seq, p.Kind)
	case proto.ApprovalResolved:
		if err := exec(`UPDATE approvals SET status = ?, option_id = ?, decided_by = ?, resolved_seq = ? WHERE id = ? AND session_id = ?`,
			p.Outcome, p.OptionID, p.DecidedBy, ev.Seq, p.ID, ev.Session); err != nil {
			return status, err
		}
		var pending int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM approvals WHERE session_id = ? AND status = 'pending'`, ev.Session).Scan(&pending); err != nil {
			return status, fmt.Errorf("store: count pending approvals: %w", err)
		}
		if pending == 0 && status == proto.StatusWaiting {
			return proto.StatusRunning, nil
		}
		return status, nil
	}
	return status, nil
}
```

- [ ] **步骤 4：实现** `internal/store/events.go`

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// Events returns up to limit events of a session with seq > after, in
// order. Seq gaps are normal: compaction removes superseded deltas.
func (s *Store) Events(ctx context.Context, id proto.SessionID, after uint64, limit int) ([]proto.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, ts, turn, type, v, payload, raw_sha256, raw_size
		FROM events WHERE session_id = ? AND seq > ? ORDER BY seq LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, fmt.Errorf("store: read events: %w", err)
	}
	defer rows.Close()
	var out []proto.Event
	for rows.Next() {
		var (
			ev      = proto.Event{Session: id}
			ts      int64
			payload string
			rawSHA  sql.NullString
			rawSize sql.NullInt64
		)
		if err := rows.Scan(&ev.Seq, &ts, &ev.Turn, &ev.Type, &ev.V, &payload, &rawSHA, &rawSize); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		ev.Time = time.UnixMilli(ts).UTC()
		ev.Payload = json.RawMessage(payload)
		if rawSHA.Valid {
			ev.Raw = &proto.BlobRef{SHA256: rawSHA.String, Size: rawSize.Int64}
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read events: %w", err)
	}
	return out, nil
}

// PendingApprovals returns the session's unresolved approval requests in
// request order.
func (s *Store) PendingApprovals(ctx context.Context, id proto.SessionID) ([]proto.ApprovalRequested, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.type, e.v, e.payload FROM approvals a
		JOIN events e ON e.session_id = a.session_id AND e.seq = a.request_seq
		WHERE a.session_id = ? AND a.status = 'pending' ORDER BY a.request_seq`, id)
	if err != nil {
		return nil, fmt.Errorf("store: read pending approvals: %w", err)
	}
	defer rows.Close()
	var out []proto.ApprovalRequested
	for rows.Next() {
		var ev proto.Event
		var payload string
		if err := rows.Scan(&ev.Type, &ev.V, &payload); err != nil {
			return nil, fmt.Errorf("store: scan approval: %w", err)
		}
		ev.Payload = json.RawMessage(payload)
		p, err := ev.Decode()
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		req, ok := p.(proto.ApprovalRequested)
		if !ok {
			return nil, fmt.Errorf("store: approval row points at a %s event", ev.Type)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read pending approvals: %w", err)
	}
	return out, nil
}

// IngestOffset returns how far the session's shim spool has been ingested;
// 0 when nothing has been.
func (s *Store) IngestOffset(ctx context.Context, id proto.SessionID) (int64, error) {
	var off int64
	err := s.db.QueryRowContext(ctx, `SELECT offset FROM ingest_offsets WHERE session_id = ?`, id).Scan(&off)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read ingest offset: %w", err)
	}
	return off, nil
}
```

- [ ] **步骤 5：运行，确认通过**

运行：`go test -race ./internal/store/`
预期：`ok`

- [ ] **步骤 6：commit**

```bash
git add -A
git commit -s -m "feat(store): append events with projections, receipts and ingest offsets"
```

### 任务 5：压缩被取代的增量

**文件：** 创建 `internal/store/compact.go`、`internal/store/compact_test.go`

**接口：** `Compact`

- [ ] **步骤 1：写失败的测试**

`internal/store/compact_test.go`

```go
package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestCompactKeepsFinalMessages(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "hel"}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "lo"}},
		{Payload: proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "hello"}},
		{Payload: proto.MessageDelta{MessageID: "m2", Text: "still streaming"}},
	}})
	n, err := s.Compact(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("compacted %d events, want 2", n)
	}
	evs, err := s.Events(ctx, "s", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, ev := range evs {
		types = append(types, fmt.Sprintf("%d:%s", ev.Seq, ev.Type))
	}
	want := "1:turn.started 4:message 5:message.delta"
	if got := strings.Join(types, " "); got != want {
		t.Fatalf("after compaction: %s, want %s", got, want)
	}
	next := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.Thinking{}}}})
	if next.FirstSeq != 6 {
		t.Fatalf("seq reused after compaction: %d", next.FirstSeq)
	}
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/`
预期：编译失败，`s.Compact undefined`

- [ ] **步骤 3：实现** `internal/store/compact.go`

```go
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// Compact deletes a session's message deltas that a complete message with
// the same ID has superseded, and returns how many it deleted. Replaying
// the log afterwards yields the same final messages.
func (s *Store) Compact(ctx context.Context, id proto.SessionID) (int64, error) {
	var n int64
	err := s.tx(ctx, false, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM events WHERE session_id = ? AND type = ? AND message_id IN
			(SELECT message_id FROM events WHERE session_id = ? AND type = ?)`,
			id, proto.TypeMessageDelta, id, proto.TypeMessage)
		if err != nil {
			return fmt.Errorf("store: compact %s: %w", id, err)
		}
		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: compact %s: %w", id, err)
		}
		return nil
	})
	return n, err
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./internal/store/`
预期：`ok`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(store): compact deltas superseded by full messages"
```

### 任务 6：blob 存储

**文件：** 创建 `internal/store/blob.go`、`internal/store/blob_test.go`

**接口：** `PutBlob`、`OpenBlob`

- [ ] **步骤 1：写失败的测试**

`internal/store/blob_test.go`

```go
package store

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestBlobs(t *testing.T) {
	s := newStore(t)
	ref, err := s.PutBlob(strings.NewReader("tool output"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size != 11 || len(ref.SHA256) != 64 {
		t.Fatalf("ref %+v", ref)
	}
	again, err := s.PutBlob(strings.NewReader("tool output"))
	if err != nil || again != ref {
		t.Fatalf("same content: %+v, %v", again, err)
	}
	rc, err := s.OpenBlob(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if got := readAll(t, rc); got != "tool output" {
		t.Fatalf("content %q", got)
	}
	missing := proto.BlobRef{SHA256: strings.Repeat("0", 64)}
	if _, err := s.OpenBlob(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
	if _, err := s.OpenBlob(proto.BlobRef{SHA256: "../../etc/passwd"}); err == nil {
		t.Fatal("bad reference accepted")
	}
	tmps, _ := filepath.Glob(filepath.Join(s.dir, "blobs", ".tmp-*"))
	if len(tmps) != 0 {
		t.Fatalf("temp files left behind: %v", tmps)
	}
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
```

- [ ] **步骤 2：运行，确认失败**

运行：`go test -race ./internal/store/`
预期：编译失败，`s.PutBlob undefined`

- [ ] **步骤 3：实现** `internal/store/blob.go`

```go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/proto"
)

// PutBlob stores r's content under its SHA-256 and returns its reference.
// The file is synced before it becomes visible; storing the same content
// twice keeps one copy.
func (s *Store) PutBlob(r io.Reader) (proto.BlobRef, error) {
	dir := filepath.Join(s.dir, "blobs")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: create blob: %w", err)
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: write blob: %w", err)
	}
	ref := proto.BlobRef{SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
	dst := s.blobPath(ref)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: create blob dir: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return proto.BlobRef{}, fmt.Errorf("store: publish blob: %w", err)
	}
	return ref, nil
}

// OpenBlob opens a stored blob, or returns ErrNotFound.
func (s *Store) OpenBlob(ref proto.BlobRef) (io.ReadCloser, error) {
	if len(ref.SHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("store: bad blob reference %q", ref.SHA256)
	}
	if _, err := hex.DecodeString(ref.SHA256); err != nil {
		return nil, fmt.Errorf("store: bad blob reference %q: %w", ref.SHA256, err)
	}
	f, err := os.Open(s.blobPath(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: blob %s", ErrNotFound, ref.SHA256)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open blob: %w", err)
	}
	return f, nil
}

func (s *Store) blobPath(ref proto.BlobRef) string {
	return filepath.Join(s.dir, "blobs", ref.SHA256[:2], ref.SHA256[2:])
}
```

- [ ] **步骤 4：运行，确认通过**

运行：`go test -race ./internal/store/`
预期：`ok`

- [ ] **步骤 5：commit**

```bash
git add -A
git commit -s -m "feat(store): add content-addressed blob storage"
```

### 任务 7：追加基准、全量检查

**文件：** 创建 `internal/store/bench_test.go`

- [ ] **步骤 1：写基准** `internal/store/bench_test.go`

```go
package store

import (
	"context"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// BenchmarkAppendDeltas measures appending streamed deltas in batches of
// 100, the size the daemon's ~100ms coalescing produces under load.
func BenchmarkAppendDeltas(b *testing.B) {
	s := newStore(b)
	newSession(b, s, "s")
	entries := make([]Entry, 100)
	for i := range entries {
		entries[i] = Entry{Payload: proto.MessageDelta{MessageID: "m", Text: "token "}}
	}
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Append(ctx, AppendRequest{Session: "s", Turn: 1, Time: t0, Entries: entries}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N*len(entries))/b.Elapsed().Seconds(), "events/s")
}
```

- [ ] **步骤 2：运行基准并记录结果**

运行：`go test ./internal/store/ -run '^$' -bench AppendDeltas -benchtime=1000x`
预期：输出 `events/s`。参考值（Apple Silicon 上实测）约 8 万 events/s，即 10 万事件约 1.2 秒；把实际输出贴进 PR 描述。低于 1 万 events/s 时停下，按总计划改用 cgo 驱动前先讨论。

- [ ] **步骤 3：全量检查**

运行：`make check`
预期：`golangci-lint run` 输出 `0 issues.`；`cmd/hoplet`、`internal/store`、`proto` 三个包 `ok`；`gen-check` 无输出

- [ ] **步骤 4：commit**

```bash
git add internal/store/bench_test.go
git commit -s -m "test(store): benchmark batched delta appends"
```

- [ ] **步骤 5：推送并开 PR（需维护者同意）**

```bash
git push -u origin feat/store
gh pr create --title "feat(store): add SQLite store with event log and receipts" --fill
```

## 完成标准

- 事件、投影、回执、spool 偏移在同一事务内提交；中途出错不留下任何部分写入。
- 同一幂等键重复追加不写任何事件，返回相同 seq。
- 需确认的写入以 `synchronous=FULL` 提交，之后连接恢复为 `NORMAL`。
- 压缩后回放得到同样的完整消息，seq 不复用。
- 迁移前备份，保留最近 3 份；遇到比二进制更新的库版本时拒绝打开。
- 基准结果已记录；`make check` 本地和 CI 通过。
