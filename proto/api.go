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
	MethodSessionsStop      = "sessions.stop"
	MethodSessionsArchive   = "sessions.archive"
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
	Mode       Mode           `json:"mode"`
	Harness    string         `json:"harness"`
	Directory  string         `json:"directory,omitempty"`
	Isolation  Isolation      `json:"isolation,omitempty"`
	Permission PermissionMode `json:"permission,omitempty"`
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
