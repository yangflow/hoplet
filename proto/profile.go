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
	Links      []string          `json:"links,omitempty"` // both directions
	Uses       int               `json:"uses,omitempty"`  // distinct sessions
}

// ProfileVersion is one committed version of a memory entry.
type ProfileVersion struct {
	Version int       `json:"version"`
	Actor   string    `json:"actor"`
	Reason  string    `json:"reason"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
	// Body and Evidence are what this version said.
	Body     string            `json:"body"`
	Evidence []ProfileEvidence `json:"evidence"`
}

// ProfileUnlinkParams removes the link between two memory entries.
type ProfileUnlinkParams struct {
	ID    string `json:"id"`
	Other string `json:"other"`
	OpID  string `json:"op_id"`
}

// ProfileUsageParams asks what one session used.
type ProfileUsageParams struct {
	Session SessionID `json:"session"`
}

// ProfileUsage is what one session used. Hidden means no block is shown;
// Failed means its digest could not be delivered.
type ProfileUsage struct {
	Hidden  bool           `json:"hidden,omitempty"`
	Failed  bool           `json:"failed,omitempty"`
	Count   int            `json:"count"`
	Entries []ProfileEntry `json:"entries,omitempty"` // at most 3, narrowest scope first
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

// ProfileDetail is an entry and its history, newest first. Scopes are the
// scopes the entry can be moved to.
type ProfileDetail struct {
	Entry   ProfileEntry     `json:"entry"`
	History []ProfileVersion `json:"history"`
	Scopes  []ProfileScope   `json:"scopes,omitempty"`
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

// ProfileCandidate is a proposed change waiting for the user. Scopes are the
// scopes it can be accepted into.
type ProfileCandidate struct {
	Entry           ProfileEntry   `json:"entry"`
	Action          string         `json:"action"`
	Target          string         `json:"target,omitempty"`
	ExpectedVersion int            `json:"expected_version"`
	Actor           string         `json:"actor"`
	Reason          string         `json:"reason"`
	CreatedAt       time.Time      `json:"created_at"`
	Scopes          []ProfileScope `json:"scopes,omitempty"`
}

// ProfileExtractionHarness is one harness in ProfileExtractionStatus. Used is
// today's runs; Unsupported says why this harness does not extract, empty
// when it does.
type ProfileExtractionHarness struct {
	Kind        string `json:"kind"`
	Used        int    `json:"used"`
	Unsupported string `json:"unsupported,omitempty"`
}

// ProfileExtractionStatus is the result of profile.extraction. DailyLimit
// applies to each harness; Failed counts today's runs that gave up.
type ProfileExtractionStatus struct {
	Enabled    bool                       `json:"enabled"`
	DailyLimit int                        `json:"daily_limit"`
	Failed     int                        `json:"failed"`
	Harnesses  []ProfileExtractionHarness `json:"harnesses"`
}

// ProfileExtractionSetParams turns background extraction on or off.
type ProfileExtractionSetParams struct {
	Enabled bool `json:"enabled"`
}

// ProfileSettings are the memory settings. An empty ExtractionHarness follows
// each session's own harness.
type ProfileSettings struct {
	UsePersonal       bool   `json:"use_personal"`
	UseCode           bool   `json:"use_code"`
	Extraction        bool   `json:"extraction"`
	ExtractionHarness string `json:"extraction_harness,omitempty"`
	IncludeCode       bool   `json:"include_code"`
	AutoSave          bool   `json:"auto_save"`
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
