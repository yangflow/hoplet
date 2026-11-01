package proto

import (
	"encoding/json"
	"time"
)

// ProtocolMajor is the client protocol's major version. A client and a
// daemon whose major versions differ refuse to talk; the client is told to
// upgrade.
const ProtocolMajor = 1

// APIPath is the URL path the daemon serves the client protocol on.
const APIPath = "/v1"

// ClientFrame is one frame a client sends: a request (ID and Method set) or
// the cancellation of a subscription (Sub and Cancel set).
type ClientFrame struct {
	ID     uint64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Sub    uint64          `json:"sub,omitempty"`
	Cancel bool            `json:"cancel,omitempty"`
}

// ServerFrame is one frame the daemon sends: a response to request ID
// (Result or Error), or an item of subscription Sub (Item, or End when the
// subscription is over, with Error if it failed). A subscription's ID is
// the ID of the request that opened it.
type ServerFrame struct {
	ID     uint64          `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *APIError       `json:"error,omitempty"`
	Sub    uint64          `json:"sub,omitempty"`
	Item   json.RawMessage `json:"item,omitempty"`
	End    bool            `json:"end,omitempty"`
}

// ErrorCode classifies an API error.
type ErrorCode string

// API error codes.
const (
	CodeBadRequest   ErrorCode = "bad_request"
	CodeUnauthorized ErrorCode = "unauthorized"
	CodeVersion      ErrorCode = "version"
	CodeNotFound     ErrorCode = "not_found"
	CodeBusy         ErrorCode = "busy"
	CodeConflict     ErrorCode = "conflict"
	CodeInvalid      ErrorCode = "invalid"
	CodeUnsupported  ErrorCode = "unsupported"
	CodeNotRunning   ErrorCode = "not_running"
	CodeInternal     ErrorCode = "internal"
)

// APIError is an error the daemon returns.
type APIError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// Error implements error.
func (e *APIError) Error() string { return string(e.Code) + ": " + e.Message }

// Methods.
const (
	MethodHello             = "hello"
	MethodSessionsList      = "sessions.list"
	MethodSessionsCreate    = "sessions.create"
	MethodSessionsSend      = "sessions.send"
	MethodSessionsRespond   = "sessions.respond"
	MethodSessionsInterrupt = "sessions.interrupt"
	MethodSessionsSteer     = "sessions.steer"
	MethodSessionsAnswer    = "sessions.answer"
	MethodSessionsSetPerm   = "sessions.set_permission"
	MethodSessionsSetPlan   = "sessions.set_plan"
	MethodSessionsSetEffort = "sessions.set_effort"
	MethodSessionsSetFast   = "sessions.set_fast"
	MethodSessionsUpdate    = "sessions.update"
	MethodSessionsStop      = "sessions.stop"
	MethodSessionsArchive   = "sessions.archive"
	MethodSessionsEvents    = "sessions.events"
	MethodSessionsGet       = "sessions.get"
	MethodSessionsDiff      = "sessions.diff"
	MethodEnvRefresh        = "env.refresh"
	MethodSystemDoctor      = "system.doctor"
	MethodQueueList         = "queue.list"
	MethodQueueEdit         = "queue.edit"
	MethodQueueDelete       = "queue.delete"
	MethodQueueMove         = "queue.move"
	MethodSubscribeCatalog  = "subscribe.catalog"
	MethodSubscribeSession  = "subscribe.session"
)

// HelloParams opens a connection; it must be the first request.
type HelloParams struct {
	ProtocolMajor int    `json:"protocol_major"`
	DeviceID      string `json:"device_id"`
	Token         string `json:"token"`
}

// HelloResult describes the daemon.
type HelloResult struct {
	Host          string `json:"host"`
	Version       string `json:"version"`
	ProtocolMajor int    `json:"protocol_major"`
}

// SessionRef names a session.
type SessionRef struct {
	Session SessionID `json:"session"`
}

// CreateParams creates a session.
type CreateParams struct {
	Mode      Mode      `json:"mode"`
	Harness   string    `json:"harness"`
	Directory string    `json:"directory,omitempty"`
	Isolation Isolation `json:"isolation,omitempty"`
	// Permission defaults to auto_review for code sessions and confirm for
	// personal ones; the harness runs the nearest mode it has (see
	// Capabilities.Fallback), which the created session reports.
	Permission PermissionMode `json:"permission,omitempty"`
	Plan       bool           `json:"plan,omitempty"`
	Effort     Effort         `json:"effort,omitempty"`
	Fast       bool           `json:"fast,omitempty"`
	Title      string         `json:"title,omitempty"`
}

// SendParams sends a message; Key makes the request idempotent.
type SendParams struct {
	Session SessionID `json:"session"`
	Text    string    `json:"text"`
	Key     string    `json:"key"`
}

// SendReply says where the message went: recorded as event Seq in Turn, or
// queued behind the running turn.
type SendReply struct {
	Seq    uint64 `json:"seq,omitempty"`
	Turn   uint32 `json:"turn,omitempty"`
	Queued bool   `json:"queued,omitempty"`
}

// RespondParams answers an approval with one of its options.
type RespondParams struct {
	Session  SessionID `json:"session"`
	Approval string    `json:"approval"`
	Option   string    `json:"option"`
	Key      string    `json:"key"`
}

// SteerParams adds text to the running turn.
type SteerParams struct {
	Session SessionID `json:"session"`
	Text    string    `json:"text"`
}

// AnswerParams answers a question.
type AnswerParams struct {
	Session  SessionID `json:"session"`
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
}

// SetPermissionParams changes a session's permission mode.
type SetPermissionParams struct {
	Session    SessionID      `json:"session"`
	Permission PermissionMode `json:"permission"`
}

// SetPlanParams turns a session's plan mode on or off.
type SetPlanParams struct {
	Session SessionID `json:"session"`
	Plan    bool      `json:"plan"`
}

// SetFastParams turns a session's fast mode on or off.
type SetFastParams struct {
	Session SessionID `json:"session"`
	Fast    bool      `json:"fast"`
}

// SetEffortParams changes how hard a session's harness thinks.
type SetEffortParams struct {
	Session SessionID `json:"session"`
	Effort  Effort    `json:"effort"`
}

// UpdateParams changes a session's list properties; nil fields stay as
// they are. An empty title is allowed: the session shows untitled.
type UpdateParams struct {
	Session SessionID `json:"session"`
	Title   *string   `json:"title,omitempty"`
	Pinned  *bool     `json:"pinned,omitempty"`
	Unread  *bool     `json:"unread,omitempty"`
}

// QueuedMessage is a message waiting for the running turn to end. Error,
// when set, says why the daemon could not send it; the queue then waits
// until the message is edited, moved or deleted.
type QueuedMessage struct {
	Key       string    `json:"key"`
	Text      string    `json:"text"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	Error     string    `json:"error,omitempty"`
}

// QueueEditParams changes a queued message's text.
type QueueEditParams struct {
	Session SessionID `json:"session"`
	Key     string    `json:"key"`
	Text    string    `json:"text"`
}

// QueueKeyParams names a queued message.
type QueueKeyParams struct {
	Session SessionID `json:"session"`
	Key     string    `json:"key"`
}

// QueueMoveParams moves a queued message to Position (0 is next).
type QueueMoveParams struct {
	Session  SessionID `json:"session"`
	Key      string    `json:"key"`
	Position int       `json:"position"`
}

// CatalogParams subscribes to the session list after a catalog version.
type CatalogParams struct {
	AfterVersion uint64 `json:"after_version"`
}

// CatalogItem carries sessions that changed since the last item, or all
// sessions when Snapshot is set (a first item or a cursor the daemon
// doesn't recognize). Version is the catalog version this item brings the
// client up to.
type CatalogItem struct {
	Sessions []Session `json:"sessions"`
	Version  uint64    `json:"version"`
	Snapshot bool      `json:"snapshot,omitempty"`
}

// SessionParams subscribes to one session's events after a seq.
type SessionParams struct {
	Session  SessionID `json:"session"`
	AfterSeq uint64    `json:"after_seq"`
}

// EventsParams asks for a page of a session's events older than BeforeSeq,
// as a client pages back through history from a snapshot. Limit 0, or one
// above the daemon's page size, asks for a full page. BeforeSeq 0 has
// nothing before it, so it returns an empty page.
type EventsParams struct {
	Session   SessionID `json:"session"`
	BeforeSeq uint64    `json:"before_seq"`
	Limit     int       `json:"limit,omitempty"`
}

// EventsPage is a page of a session's events, oldest first, with More set
// when older events remain.
type EventsPage struct {
	Events []Event `json:"events"`
	More   bool    `json:"more,omitempty"`
}

// SessionItem carries a session's new events in order, or a Snapshot that
// replaces the client's view, and the session's whole queue when it changed
// (always in the first item).
type SessionItem struct {
	Events   []Event          `json:"events,omitempty"`
	Snapshot *SessionSnapshot `json:"snapshot,omitempty"`
	Queue    *Queue           `json:"queue,omitempty"`
}

// Queue is a session's queued messages, next first.
type Queue struct {
	Messages []QueuedMessage `json:"messages"`
}

// SessionSnapshot is the current state of one session: the record, its
// pending approvals and the events of its last turns, with More set when
// older events exist.
type SessionSnapshot struct {
	Session Session             `json:"session"`
	Pending []ApprovalRequested `json:"pending,omitempty"`
	Events  []Event             `json:"events"`
	More    bool                `json:"more,omitempty"`
}

// SessionState is a session's record with what waits on the user: its
// pending approvals and its queue.
type SessionState struct {
	Session Session             `json:"session"`
	Pending []ApprovalRequested `json:"pending"`
	Queue   []QueuedMessage     `json:"queue"`
}

// EnvInfo describes the environment harnesses start with. It never carries
// variable values.
type EnvInfo struct {
	Shell string `json:"shell"`
	// Vars is how many variables are passed on.
	Vars int `json:"vars"`
	// Withheld are the names of credential variables not passed on.
	Withheld []string `json:"withheld"`
	// Proxy is the proxy URL harnesses use, password redacted; empty for
	// none. ProxySource is config, shell, system or none.
	Proxy       string    `json:"proxy,omitempty"`
	ProxySource string    `json:"proxy_source"`
	ResolvedAt  time.Time `json:"resolved_at"`
	// Note, when set, says why no proxy is in use when that may surprise
	// (the system proxy is a PAC file).
	Note string `json:"note,omitempty"`
	// Warning, when set, says the shell could not be read again and the
	// cached environment, without the secrets only the shell has, is in use.
	Warning string `json:"warning,omitempty"`
	// Error, when set, says why the login shell could not be read; harnesses
	// then start with a minimal environment.
	Error string `json:"error,omitempty"`
}

// CheckStatus is the outcome of a doctor check.
type CheckStatus string

// Check outcomes.
const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// Check is one doctor check.
type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

// DoctorReport is the daemon's view of its health.
type DoctorReport struct {
	Checks []Check `json:"checks"`
}

// DiffParams asks for a session's changes since its baseline. Repo limits
// the answer to one repository (by its path under the working directory);
// Patch asks for the patch text as well as the file list.
type DiffParams struct {
	Session SessionID `json:"session"`
	Repo    string    `json:"repo,omitempty"`
	Patch   bool      `json:"patch,omitempty"`
}

// SessionDiff is everything a session changed in the git repositories under
// its working directory, per repository. Clients ask again after a turn
// ends or files change, and skip redrawing a repository whose SHA is
// unchanged. BaselineAt is when the baseline was taken, absent when there is
// none; BaselineError says why it is missing or incomplete.
type SessionDiff struct {
	Session       SessionID  `json:"session"`
	BaselineAt    *time.Time `json:"baseline_at,omitempty"`
	BaselineError string     `json:"baseline_error,omitempty"`
	Repos         []RepoDiff `json:"repos"`
}

// RepoState says how a repository's diff was made.
type RepoState string

// Repository states.
const (
	// RepoOK is a repository present at the baseline and now.
	RepoOK RepoState = "ok"
	// RepoNew is a repository that appeared after the baseline; its diff
	// is against the empty tree.
	RepoNew RepoState = "new"
	// RepoMissing is a repository the baseline had that is gone.
	RepoMissing RepoState = "missing"
	// RepoError is a repository whose diff could not be made; Error says why.
	RepoError RepoState = "error"
)

// RepoDiff is one repository's change since the baseline. Path is relative
// to the working directory ("." for the directory itself); Parent is the
// path of the repository that contains this one (a submodule or nested
// repository), empty otherwise. Patch is set only when asked for and is cut
// at a line boundary, with Truncated set, when it is too large (a response
// is limited to a few MiB as encoded, so later repositories may get no patch,
// or no file list). SHA changes whenever anything shown does: the content,
// branch, state and skipped files, the parent, and how much of the patch and
// the lists the response carries.
type RepoDiff struct {
	Path   string     `json:"path"`
	Parent string     `json:"parent,omitempty"`
	Branch string     `json:"branch,omitempty"`
	State  RepoState  `json:"state"`
	Error  string     `json:"error,omitempty"`
	Files  []FileDiff `json:"files"`
	// FilesTruncated says Files holds only the first files by path.
	FilesTruncated bool          `json:"files_truncated,omitempty"`
	Patch          string        `json:"patch,omitempty"`
	Truncated      bool          `json:"truncated,omitempty"`
	Skipped        []SkippedFile `json:"skipped,omitempty"`
	// SkippedTruncated says Skipped holds only the first files by path.
	SkippedTruncated bool   `json:"skipped_truncated,omitempty"`
	SHA              string `json:"sha,omitempty"`
}

// FileStatus is how a file changed.
type FileStatus string

// File statuses.
const (
	FileStatusAdded       FileStatus = "added"
	FileStatusModified    FileStatus = "modified"
	FileStatusDeleted     FileStatus = "deleted"
	FileStatusRenamed     FileStatus = "renamed"
	FileStatusTypeChanged FileStatus = "type_changed"
)

// FileDiff is one changed file. OldPath is set for a rename. Binary files
// have no line counts. Filter names the git clean/smudge filter the file's
// attributes select; such a file is listed without patch text.
type FileDiff struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"old_path,omitempty"`
	Status    FileStatus `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Binary    bool       `json:"binary,omitempty"`
	Filter    string     `json:"filter,omitempty"`
}

// SkippedFile is a file left out of a repository's snapshots; the version
// recorded before, if any, is compared instead. Reason is "too large" or
// "unreadable".
type SkippedFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	Reason string `json:"reason,omitempty"`
}
