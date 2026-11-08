package proto

import "time"

// ProfileScope is what a memory entry applies to.
type ProfileScope struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

// ProfileEvidence refers to the record that supports a memory entry.
type ProfileEvidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// ProfileEntry is one memory entry.
type ProfileEntry struct {
	ID         string            `json:"id"`
	Version    int               `json:"version"`
	Category   string            `json:"category"`
	Status     string            `json:"status"`
	Scope      ProfileScope      `json:"scope"`
	Evidence   []ProfileEvidence `json:"evidence"`
	ValidUntil *time.Time        `json:"valid_until,omitempty"`
	ReplacedBy string            `json:"replaced_by,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Title      string            `json:"title"`
	Body       string            `json:"body"`
}

// ProfileVersion is one committed version of a memory entry.
type ProfileVersion struct {
	Version int       `json:"version"`
	Actor   string    `json:"actor"`
	Reason  string    `json:"reason"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// ProfileListParams selects memory entries. An empty Status means active;
// All lists every status.
type ProfileListParams struct {
	Category string        `json:"category,omitempty"`
	Status   string        `json:"status,omitempty"`
	All      bool          `json:"all,omitempty"`
	Scope    *ProfileScope `json:"scope,omitempty"`
	Text     string        `json:"text,omitempty"`
	Limit    int           `json:"limit,omitempty"`
}

// ProfileList is the result of profile.list.
type ProfileList struct {
	Entries []ProfileEntry `json:"entries"`
}

// ProfileRef names a memory entry by its full ID.
type ProfileRef struct {
	ID string `json:"id"`
}

// ProfileDetail is an entry and its history, newest first.
type ProfileDetail struct {
	Entry   ProfileEntry     `json:"entry"`
	History []ProfileVersion `json:"history"`
}

// ProfileUpdateParams replaces an entry's body as the user. A nil Scope
// keeps the entry's scope.
type ProfileUpdateParams struct {
	ID              string        `json:"id"`
	ExpectedVersion int           `json:"expected_version"`
	Body            string        `json:"body"`
	Scope           *ProfileScope `json:"scope,omitempty"`
	OpID            string        `json:"op_id"`
}

// ProfileWithdrawParams withdraws an entry as the user.
type ProfileWithdrawParams struct {
	ID              string `json:"id"`
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"reason,omitempty"`
	OpID            string `json:"op_id"`
}

// ProfileWriteResult says what became of a write. Only outcome "applied"
// means it was saved; "rejected" comes with Code and Reason.
type ProfileWriteResult struct {
	Outcome string        `json:"outcome"`
	Entry   *ProfileEntry `json:"entry,omitempty"`
	Code    string        `json:"code,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}

// ProfileCandidate is a proposed change waiting for the user.
type ProfileCandidate struct {
	Entry           ProfileEntry `json:"entry"`
	Action          string       `json:"action"`
	Target          string       `json:"target,omitempty"`
	ExpectedVersion int          `json:"expected_version"`
	Actor           string       `json:"actor"`
	Reason          string       `json:"reason"`
	CreatedAt       time.Time    `json:"created_at"`
}

// ProfileCandidates is the result of profile.candidates.
type ProfileCandidates struct {
	Candidates []ProfileCandidate `json:"candidates"`
}

// ProfileAcceptParams accepts a candidate; a non-nil Scope replaces the
// proposed one.
type ProfileAcceptParams struct {
	ID    string        `json:"id"`
	Scope *ProfileScope `json:"scope,omitempty"`
	OpID  string        `json:"op_id"`
}

// ProfileRejectParams rejects a candidate.
type ProfileRejectParams struct {
	ID   string `json:"id"`
	OpID string `json:"op_id"`
}
