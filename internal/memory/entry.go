// Package memory stores long-term memory entries as Markdown files with an
// audited, crash-recoverable commit path.
package memory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yangflow/hoplet/internal/store"
)

// MaxBody is the largest entry body, in bytes.
const MaxBody = 16 << 10

// titleRunes is the longest title, in runes.
const titleRunes = 80

// Category is the kind of knowledge an entry holds.
type Category string

// The entry categories. Each is also a directory under entries/.
const (
	CategoryFact       Category = "fact"
	CategoryPreference Category = "preference"
	CategoryConvention Category = "convention"
	CategoryExperience Category = "experience"
)

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	switch c {
	case CategoryFact, CategoryPreference, CategoryConvention, CategoryExperience:
		return true
	}
	return false
}

// Status is where an entry is in its lifecycle.
type Status string

// The entry statuses.
const (
	StatusCandidate  Status = "candidate"
	StatusActive     Status = "active"
	StatusStale      Status = "stale"
	StatusSuperseded Status = "superseded"
	StatusWithdrawn  Status = "withdrawn"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusCandidate, StatusActive, StatusStale, StatusSuperseded, StatusWithdrawn:
		return true
	}
	return false
}

// ScopeKind says what an entry applies to.
type ScopeKind string

// The scope kinds.
const (
	ScopeGlobal    ScopeKind = "global"
	ScopeCompanion ScopeKind = "companion"
	ScopeRepo      ScopeKind = "repo"
	ScopeHost      ScopeKind = "host"
)

// Valid reports whether k is a known scope kind.
func (k ScopeKind) Valid() bool {
	switch k {
	case ScopeGlobal, ScopeCompanion, ScopeRepo, ScopeHost:
		return true
	}
	return false
}

// Scope is what an entry applies to. A global scope has no value.
type Scope struct {
	Kind  ScopeKind
	Value string
}

// EvidenceKind says how strongly a piece of evidence supports an entry.
type EvidenceKind string

// The evidence kinds.
const (
	EvidenceUserStated EvidenceKind = "user_stated"
	EvidenceVerified   EvidenceKind = "verified"
	EvidenceObserved   EvidenceKind = "observed"
	EvidenceInferred   EvidenceKind = "inferred"
)

// Valid reports whether k is a known evidence kind.
func (k EvidenceKind) Valid() bool {
	switch k {
	case EvidenceUserStated, EvidenceVerified, EvidenceObserved, EvidenceInferred:
		return true
	}
	return false
}

// Evidence refers to the persisted record that supports an entry.
type Evidence struct {
	Kind EvidenceKind
	Ref  string
}

// Actor says who made a write.
type Actor string

// The actors.
const (
	ActorUser       Actor = "user"
	ActorCompanion  Actor = "companion"
	ActorExtraction Actor = "extraction"
	ActorFile       Actor = "file" // an edit found on disk by a scan
)

// Valid reports whether a is a known actor.
func (a Actor) Valid() bool {
	switch a {
	case ActorUser, ActorCompanion, ActorExtraction, ActorFile:
		return true
	}
	return false
}

// Entry is one memory entry.
type Entry struct {
	ID         string
	Version    int
	Category   Category
	Status     Status
	Scope      Scope
	Evidence   []Evidence
	ValidUntil *time.Time
	ReplacedBy string
	Links      []string // related entries; undirected, held by the entry that made the link
	Unlinked   []string // links the user removed, which extraction may not make again
	UpdatedAt  time.Time
	Body       string
}

var idPattern = regexp.MustCompile(`^mem_[0-9a-f]{32}$`)

// NewID returns a fresh entry ID.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("memory: new ID: %w", err)
	}
	return "mem_" + hex.EncodeToString(b[:]), nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: memory: %s", store.ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks every field of the entry.
func (e Entry) Validate() error {
	if !idPattern.MatchString(e.ID) {
		return invalid("bad entry ID %q", e.ID)
	}
	if e.Version < 1 {
		return invalid("version %d", e.Version)
	}
	if !e.Category.Valid() {
		return invalid("category %q", e.Category)
	}
	if !e.Status.Valid() {
		return invalid("status %q", e.Status)
	}
	if !e.Scope.Kind.Valid() {
		return invalid("scope kind %q", e.Scope.Kind)
	}
	if (e.Scope.Kind == ScopeGlobal) != (e.Scope.Value == "") {
		return invalid("scope %q with value %q", e.Scope.Kind, e.Scope.Value)
	}
	if !utf8.ValidString(e.Scope.Value) {
		return invalid("scope value is not UTF-8")
	}
	for _, ev := range e.Evidence {
		if !ev.Kind.Valid() {
			return invalid("evidence kind %q", ev.Kind)
		}
		if ev.Ref == "" || !utf8.ValidString(ev.Ref) {
			return invalid("evidence reference %q", ev.Ref)
		}
	}
	if e.ValidUntil != nil && e.ValidUntil.Location() != time.UTC {
		return invalid("valid_until is not UTC")
	}
	if (e.Status == StatusSuperseded) != (e.ReplacedBy != "") {
		return invalid("status %q with replaced_by %q", e.Status, e.ReplacedBy)
	}
	if e.ReplacedBy != "" && !idPattern.MatchString(e.ReplacedBy) {
		return invalid("bad replaced_by %q", e.ReplacedBy)
	}
	if err := e.checkLinks(); err != nil {
		return err
	}
	if e.UpdatedAt.IsZero() || e.UpdatedAt.Location() != time.UTC {
		return invalid("updated_at is missing or not UTC")
	}
	if e.Body == "" || e.Body != strings.TrimSpace(e.Body) {
		return invalid("body is empty or has surrounding space")
	}
	if len(e.Body) > MaxBody {
		return invalid("body is %d bytes, the limit is %d", len(e.Body), MaxBody)
	}
	if !utf8.ValidString(e.Body) {
		return invalid("body is not UTF-8")
	}
	return nil
}

// Title is the entry's first body line, cut to a listing's width.
func (e Entry) Title() string {
	line, _, _ := strings.Cut(e.Body, "\n")
	r := []rune(strings.TrimSpace(line))
	if len(r) > titleRunes {
		r = r[:titleRunes]
	}
	return string(r)
}

// path is the file of the entry with this category and ID under root.
func path(root string, c Category, id string) string {
	return filepath.Join(root, entriesDir, string(c), id+fileExt)
}
