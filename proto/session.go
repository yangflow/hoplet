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
