package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	entriesDir = "entries"
	fileExt    = ".md"
	tempPrefix = ".tmp-"
)

// Config configures a Service.
type Config struct {
	Root  string // the profile directory
	Store *store.Store
	// Sessions answers what mode a session is in; Store when nil.
	Sessions Sessions
	Log      *slog.Logger
}

// Sessions looks up the sessions that write to memory.
type Sessions interface {
	Session(ctx context.Context, id proto.SessionID) (proto.Session, error)
}

// Op is one requested write. Commit sets Entry's Version and UpdatedAt.
type Op struct {
	ID              string
	Entry           Entry
	ExpectedVersion int // 0 creates the entry
	Actor           Actor
	Reason          string
}

// Version is one committed version of an entry.
type Version struct {
	Version int
	Actor   Actor
	Reason  string
	Content string
	Hash    string
	At      time.Time
}

// hook lets tests stop a commit at a named point, as a crash would.
type hook func(point string) error

// Service owns the entry files, their audit log and their index.
type Service struct {
	root     string
	store    *store.Store
	sessions Sessions
	log      *slog.Logger
	hook     hook

	mu sync.Mutex // serializes writes to the files
}

// Open prepares the profile directory, finishes any interrupted commit and
// scans the entry files.
func Open(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Root == "" || cfg.Store == nil {
		return nil, fmt.Errorf("%w: memory: a root and a store are required", store.ErrInvalid)
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Join(cfg.Root, entriesDir), 0o700); err != nil {
		return nil, fmt.Errorf("memory: create profile: %w", err)
	}
	sessions := cfg.Sessions
	if sessions == nil {
		sessions = cfg.Store
	}
	s := &Service{root: cfg.Root, store: cfg.Store, sessions: sessions, log: log}
	if err := s.recoverOps(ctx); err != nil {
		return nil, err
	}
	if err := s.Scan(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) at(point string) error {
	if s.hook == nil {
		return nil
	}
	return s.hook(point)
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func rowOf(e Entry, hash string) store.MemoryRow {
	return store.MemoryRow{
		ID:         e.ID,
		Version:    e.Version,
		Category:   string(e.Category),
		Status:     string(e.Status),
		ScopeKind:  string(e.Scope.Kind),
		ScopeValue: e.Scope.Value,
		Title:      e.Title(),
		Body:       e.Body,
		Hash:       hash,
		UpdatedAt:  e.UpdatedAt,
	}
}

// Commit writes one new version of an entry. It fails with store.ErrConflict
// when the entry is not at op.ExpectedVersion or its file has an edit no scan
// has picked up. Repeating a committed op returns its result and writes
// nothing.
func (s *Service) Commit(ctx context.Context, op Op) (Entry, error) {
	if op.ID == "" || !op.Actor.Valid() || op.ExpectedVersion < 0 {
		return Entry{}, invalid("op %q by %q at version %d", op.ID, op.Actor, op.ExpectedVersion)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(ctx, op)
}

// commit is Commit for a caller that holds s.mu.
func (s *Service) commit(ctx context.Context, op Op) (Entry, error) {
	if e, done, err := s.repeated(ctx, op); done {
		return e, err
	}
	if err := s.checkHead(ctx, op); err != nil {
		return Entry{}, err
	}
	e, _, err := s.write(ctx, op)
	if err != nil {
		return Entry{}, err
	}
	if err := s.writeProfile(ctx); err != nil {
		s.log.WarnContext(ctx, "memory: write profile", "err", err)
	}
	return e, nil
}

// repeated handles an op whose ID is already known.
func (s *Service) repeated(ctx context.Context, op Op) (Entry, bool, error) {
	prev, err := s.store.MemoryOpByID(ctx, op.ID)
	if errors.Is(err, store.ErrNotFound) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, true, err
	}
	e, err := Decode([]byte(prev.Content))
	if err != nil {
		return Entry{}, true, fmt.Errorf("memory: op %s: %w", op.ID, err)
	}
	asked := op.Entry
	asked.Version, asked.UpdatedAt = e.Version, e.UpdatedAt
	if !reflect.DeepEqual(asked, e) || prev.ExpectedVersion != op.ExpectedVersion {
		return Entry{}, true, fmt.Errorf("%w: memory op %s has other content", store.ErrConflict, op.ID)
	}
	if prev.State != store.MemoryOpCommitted {
		return Entry{}, true, fmt.Errorf("%w: memory op %s is %s", store.ErrConflict, op.ID, prev.State)
	}
	return e, true, nil
}

// checkHead compares the request with the entry's current state.
func (s *Service) checkHead(ctx context.Context, op Op) error {
	row, err := s.store.MemoryIndexEntry(ctx, op.Entry.ID)
	if errors.Is(err, store.ErrNotFound) {
		ops, err := s.store.MemoryOps(ctx, op.Entry.ID)
		if err != nil {
			return err
		}
		head := 0
		if len(ops) > 0 {
			head = ops[0].NewVersion
		}
		if head != op.ExpectedVersion {
			return fmt.Errorf("%w: memory entry %s is at version %d", store.ErrConflict, op.Entry.ID, head)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if row.Version != op.ExpectedVersion {
		return fmt.Errorf("%w: memory entry %s is at version %d", store.ErrConflict, op.Entry.ID, row.Version)
	}
	if row.Category != string(op.Entry.Category) {
		return invalid("entry %s cannot move from category %s", op.Entry.ID, row.Category)
	}
	data, err := os.ReadFile(path(s.root, Category(row.Category), row.ID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("memory: read entry %s: %w", row.ID, err)
	}
	if err != nil || hashOf(data) != row.Hash {
		return fmt.Errorf("%w: memory entry %s changed on disk", store.ErrConflict, row.ID)
	}
	return nil
}

// write runs the commit protocol; the caller holds s.mu.
func (s *Service) write(ctx context.Context, op Op) (Entry, store.MemoryRow, error) {
	e := op.Entry
	e.Version = op.ExpectedVersion + 1
	e.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	data, err := Encode(e)
	if err != nil {
		return Entry{}, store.MemoryRow{}, err
	}
	hash := hashOf(data)
	_, err = s.store.BeginMemoryOp(ctx, store.MemoryOp{
		ID:              op.ID,
		EntryID:         e.ID,
		ExpectedVersion: op.ExpectedVersion,
		NewVersion:      e.Version,
		Actor:           string(op.Actor),
		Reason:          op.Reason,
		Content:         string(data),
		Hash:            hash,
	})
	if err != nil {
		return Entry{}, store.MemoryRow{}, err
	}
	if err := s.at("after-begin"); err != nil {
		return Entry{}, store.MemoryRow{}, err
	}
	crashed, err := s.place(path(s.root, e.Category, e.ID), data)
	if crashed {
		return Entry{}, store.MemoryRow{}, err
	}
	if err != nil {
		return Entry{}, store.MemoryRow{}, errors.Join(err, s.store.FailMemoryOp(context.WithoutCancel(ctx), op.ID))
	}
	row := rowOf(e, hash)
	if err := s.store.FinishMemoryOp(ctx, op.ID, row); err != nil {
		return Entry{}, store.MemoryRow{}, err
	}
	if err := s.at("after-finish"); err != nil {
		return Entry{}, store.MemoryRow{}, err
	}
	return e, row, nil
}

// place durably replaces the file at target. crashed reports that a test
// hook stopped it, leaving everything where it was.
func (s *Service) place(target string, data []byte) (crashed bool, err error) {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("memory: create directory: %w", err)
	}
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return false, fmt.Errorf("memory: create temp file: %w", err)
	}
	temp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, errors.Join(fmt.Errorf("memory: write temp file: %w", err), os.Remove(temp))
	}
	if err := s.at("after-temp"); err != nil {
		return true, err
	}
	if err := os.Rename(temp, target); err != nil {
		return false, errors.Join(fmt.Errorf("memory: replace entry: %w", err), os.Remove(temp))
	}
	if err := syncDir(dir); err != nil {
		return false, err
	}
	if err := s.at("after-rename"); err != nil {
		return true, err
	}
	return false, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the entry directory this service owns
	if err != nil {
		return fmt.Errorf("memory: open directory: %w", err)
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("memory: sync directory: %w", err)
	}
	return nil
}

func (s *Service) read(row store.MemoryRow) (Entry, error) {
	data, err := os.ReadFile(path(s.root, Category(row.Category), row.ID))
	if err != nil {
		return Entry{}, fmt.Errorf("memory: read entry %s: %w", row.ID, err)
	}
	return Decode(data)
}

// Get returns one indexed entry.
func (s *Service) Get(ctx context.Context, id string) (Entry, error) {
	row, err := s.store.MemoryIndexEntry(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	return s.read(row)
}

// Search returns the matching indexed entries, newest first. An entry whose
// file has become unreadable since the last scan is left out.
func (s *Service) Search(ctx context.Context, q store.MemoryQuery) ([]Entry, error) {
	rows, err := s.store.SearchMemoryIndex(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e, err := s.read(row)
		if err != nil {
			s.log.WarnContext(ctx, "memory: skip entry", "id", row.ID, "err", err)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// History returns an entry's committed versions, newest first.
func (s *Service) History(ctx context.Context, id string) ([]Version, error) {
	ops, err := s.store.MemoryOps(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(ops))
	for _, op := range ops {
		v := Version{Version: op.NewVersion, Actor: Actor(op.Actor), Reason: op.Reason, Content: op.Content, Hash: op.Hash, At: op.CreatedAt}
		out = append(out, v)
	}
	return out, nil
}

// Problems returns the entry files the last scan could not use.
func (s *Service) Problems(ctx context.Context) ([]store.MemoryProblem, error) {
	return s.store.MemoryProblems(ctx)
}
