package sync

import "encoding/json"

// Wire protocol structures, mirroring shared/src/wire/ (docs/architecture.md#wire-protocol).
// scripts/test/contract.test.ts compares the message types and field names of both sides: a struct is
// named after its message type constant, a field's JSON name is its tag, and `omitempty` marks the
// fields the schema declares optional.
// Every sessionId is an exact nonempty ASCII letters/digits/underscore/hyphen key
// (provider.ValidSessionID, ADR 0006). Invalid IDs receive error invalid-session-id
// before decoding or persistence; the wire envelope and cursor are unchanged.

const ProtocolVersion = 5

// Wire message types: Type<Struct> is the `type` of the struct of that name.
// shared/src/wire/ holds the same table; scripts/test/contract.test.ts compares both.
const (
	TypeDeviceHello              = "device.hello"
	TypeWelcome                  = "welcome"
	TypePingMsg                  = "ping"
	TypePongMsg                  = "pong"
	TypeErrorMsg                 = "error"
	TypeSessionAppend            = "session.append"
	TypeWorkspaceSnapshot        = "workspace.snapshot"
	TypeSessionAck               = "session.ack"
	TypeSessionResync            = "session.resync"
	TypeSessionRejected          = "session.rejected"
	TypeRuntimePrompt            = "runtime.prompt"
	TypeRuntimeStart             = "runtime.start"
	TypeRuntimeStarted           = "runtime.started"
	TypeRuntimeStartRejected     = "runtime.start.rejected"
	TypeRuntimePermissionAnswer  = "runtime.permission.answer"
	TypeRuntimeInterrupt         = "runtime.interrupt"
	TypeRuntimeStop              = "runtime.stop"
	TypeRuntimeState             = "runtime.state"
	TypeRuntimeOutput            = "runtime.output"
	TypeRuntimePermission        = "runtime.permission"
	TypeRuntimeRejected          = "runtime.rejected"
	TypeSessionWorkspaceList     = "session.workspace.list"
	TypeSessionWorkspace         = "session.workspace"
	TypeSessionWorkspaceRejected = "session.workspace.rejected"
	TypeSessionWorkspaceFileRead = "session.workspace.file.read"
	TypeSessionWorkspaceFile     = "session.workspace.file"
	TypeSessionWorkspaceDiffRead = "session.workspace.diff.read"
	TypeSessionWorkspaceDiff     = "session.workspace.diff"
	TypeHandoffPrepare           = "handoff.prepare"
	TypeHandoffPrepared          = "handoff.prepared"
	TypeHandoffInstall           = "handoff.install"
	TypeHandoffData              = "handoff.data"
	TypeHandoffInstalled         = "handoff.installed"
	TypeHandoffVerify            = "handoff.verify"
	TypeHandoffVerified          = "handoff.verified"
	TypeHandoffFailed            = "handoff.failed"
	TypeHandoffCommit            = "handoff.commit"
	TypeHandoffAbort             = "handoff.abort"
	TypeDeviceProjectsList       = "device.projects.list"
	TypeDeviceProjectsAdd        = "device.projects.add"
	TypeDeviceProjectsRemove     = "device.projects.remove"
	TypeDeviceSessionsImport     = "device.sessions.import"
	TypeDeviceSessionsRemove     = "device.sessions.remove"
	TypeDeviceProjects           = "device.projects"
	TypeDeviceProjectsRejected   = "device.projects.rejected"
	TypeDeviceDirsList           = "device.dirs.list"
	TypeDeviceDirs               = "device.dirs"
	TypeDeviceDirsRejected       = "device.dirs.rejected"
	TypeDeviceCloneFind          = "device.clone.find"
	TypeDeviceClone              = "device.clone"
)

// Error codes of an `error` message that the agent acts on (shared/src/errors.ts).
const CodeProtocolVersionMismatch = "protocol-version-mismatch"

type Encoding string

const (
	EncodingPlain      Encoding = "plain"
	EncodingGzipBase64 Encoding = "gzip+base64"
)

type SessionMeta struct {
	SessionID     string `json:"sessionId"`
	ProjectPath   string `json:"projectPath"`
	ProjectDir    string `json:"projectDir"`
	Title         string `json:"title"`
	Provider      string `json:"provider"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	LineCount     int64  `json:"lineCount"`
	ByteSize      int64  `json:"byteSize"`
	DeviceName    string `json:"deviceName"`
	OwnerDeviceID string `json:"ownerDeviceId,omitempty"`
	OwnerEpoch    int64  `json:"ownerEpoch"`
	GitBranch     string `json:"gitBranch,omitempty"`
}

// device → server

type DeviceHello struct {
	V        int    `json:"v"`
	Type     string `json:"type"`
	DeviceID string `json:"deviceId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	// Providers are the vendors this agent runs, with the capabilities of their runtime drivers.
	Providers []ProviderInfo `json:"providers,omitempty"`
}

// ProviderInfo is one vendor an agent runs and what its runtime driver declares.
type ProviderInfo struct {
	Provider     string              `json:"provider"`
	Capabilities RuntimeCapabilities `json:"capabilities"`
	// Version is the installed vendor version; Tested says the driver was verified against exactly it.
	// Both are absent when the version could not be read or is refused.
	Version string `json:"version,omitempty"`
	Tested  *bool  `json:"tested,omitempty"`
}

type SessionAppend struct {
	V          int      `json:"v"`
	Type       string   `json:"type"`
	SessionID  string   `json:"sessionId"`
	ProjectDir string   `json:"projectDir"`
	FilePath   string   `json:"filePath"`
	ByteStart  int64    `json:"byteStart"`
	Encoding   Encoding `json:"encoding"`
	Lines      string   `json:"lines"` // raw lines joined by \n; gzip+base64 when encoded
	// Title is PeekTitle from the live file; updates the store index only.
	Title string `json:"title,omitempty"`
}

// WorkspaceSnapshot is one chunk of a session's workspace snapshot (docs/architecture.md#workspace-snapshots):
// the chunks of one snapshot are sent in order from Seq 0, the last with Last set, and Data is the base64 of
// at most SnapshotChunkBytes bytes of the archive. The metadata repeats in every chunk.
type WorkspaceSnapshot struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	SessionID  string `json:"sessionId"`
	SnapshotID string `json:"snapshotId"`
	CreatedAt  int64  `json:"createdAt"`
	BaseCommit string `json:"baseCommit"`
	OriginURL  string `json:"originUrl"`
	TreeHash   string `json:"treeHash"`
	ByteSize   int64  `json:"byteSize"`
	SHA256     string `json:"sha256"`
	Seq        int    `json:"seq"`
	Last       bool   `json:"last"`
	Data       string `json:"data"`
}

// SnapshotChunkBytes is the most archive bytes one WorkspaceSnapshot carries.
const SnapshotChunkBytes = 1 << 20

type PingMsg struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	T    int64  `json:"t,omitempty"`
}

// server → device

type Welcome struct {
	V          int           `json:"v"`
	Type       string        `json:"type"`
	ServerTime int64         `json:"serverTime"`
	Sessions   []SessionMeta `json:"sessions"`
}

type SessionAck struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	ByteSize  int64  `json:"byteSize"`
	LineCount int64  `json:"lineCount"`
}

type SessionResync struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	ByteSize  int64  `json:"byteSize"`
}

type SessionRejected struct {
	V             int    `json:"v"`
	Type          string `json:"type"`
	SessionID     string `json:"sessionId"`
	Reason        string `json:"reason"`
	OwnerDeviceID string `json:"ownerDeviceId,omitempty"`
}

type ErrorMsg struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	SessionID string `json:"sessionId,omitempty"`
}

type PongMsg struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	T    int64  `json:"t,omitempty"`
}

// core → device

// Remote control (ADR 0008). core → device:

type RuntimePrompt struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	PromptID  string `json:"promptId"`
	Text      string `json:"text"`
}

type RuntimePermissionAnswer struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Decision  string `json:"decision"` // allow | deny
}

type RuntimeInterrupt struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
}

type RuntimeStop struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
}

// device → core:

// RuntimeCapabilities is what a runtime driver declares it can do. Models lists the model IDs a new session
// can choose from; it is empty (never null) when the driver cannot choose.
type RuntimeCapabilities struct {
	Permissions bool     `json:"permissions"`
	Interrupt   bool     `json:"interrupt"`
	Thinking    bool     `json:"thinking"`
	Fast        bool     `json:"fast"`
	Models      []string `json:"models"`
}

// RuntimeStartOptions are the options of a new session; each is set only when the driver declares it.
type RuntimeStartOptions struct {
	Model    string `json:"model,omitempty"`
	Thinking *bool  `json:"thinking,omitempty"`
	Fast     *bool  `json:"fast,omitempty"`
}

// RuntimeStart (core → device) starts a new session in ProjectPath, a watched project of the device. The
// agent answers with RuntimeStarted or RuntimeStartRejected for the same RequestID.
type RuntimeStart struct {
	V           int                 `json:"v"`
	Type        string              `json:"type"`
	RequestID   string              `json:"requestId"`
	ProjectPath string              `json:"projectPath"`
	Provider    string              `json:"provider"`
	Options     RuntimeStartOptions `json:"options"`
	Text        string              `json:"text"`
	PromptID    string              `json:"promptId"`
}

// RuntimeStarted (device → core): the session is created, imported and syncs from now on.
type RuntimeStarted struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
}

type RuntimeStartRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type RuntimeState struct {
	V            int                 `json:"v"`
	Type         string              `json:"type"`
	SessionID    string              `json:"sessionId"`
	State        string              `json:"state"` // external | idle | starting | ready | running | waiting-permission | failed
	Error        string              `json:"error,omitempty"`
	Capabilities RuntimeCapabilities `json:"capabilities"`
}

type RuntimeOutput struct {
	V         int             `json:"v"`
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"` // text | thinking | tool-call | tool-result
	Text      string          `json:"text,omitempty"`
	Tokens    int             `json:"tokens,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	ToolUseID string          `json:"toolUseId,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"isError,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

type RuntimePermission struct {
	V         int             `json:"v"`
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	RequestID string          `json:"requestId"`
	Tool      string          `json:"tool"`
	ToolUseID string          `json:"toolUseId,omitempty"`
	Input     json.RawMessage `json:"input"`
}

type RuntimeRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	PromptID  string `json:"promptId"`
	Reason    string `json:"reason"` // busy | starting | external | diagnostic text
}

// Workspace reads (docs/spec.md#workspace). core → device:

type SessionWorkspaceList struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
}

// SessionWorkspaceFileRead asks for one file of the working tree; At is "head" to read it as HEAD has it.
type SessionWorkspaceFileRead struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	At        string `json:"at,omitempty"`
}

type SessionWorkspaceDiffRead struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// device → core:

// WorkspaceChange is one changed path against HEAD: its status (modified, added, deleted, untracked) and
// its added and removed line counts, or Binary.
type WorkspaceChange struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   *int   `json:"added,omitempty"`
	Removed *int   `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}

// SessionWorkspace lists the working tree; Git is false for a directory without a commit, which then has
// no paths and no changes.
type SessionWorkspace struct {
	V         int               `json:"v"`
	Type      string            `json:"type"`
	SessionID string            `json:"sessionId"`
	RequestID string            `json:"requestId"`
	Git       bool              `json:"git"`
	Commit    string            `json:"commit"`
	Origin    string            `json:"origin"`
	Paths     []string          `json:"paths"`
	Truncated bool              `json:"truncated,omitempty"`
	Changes   []WorkspaceChange `json:"changes"`
}

type SessionWorkspaceRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// SessionWorkspaceFile is one file (bulk lane): Kind "text" with Text, "image" with MediaType and the base64
// Data of the whole file, or "binary" with only Size.
type SessionWorkspaceFile struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      string `json:"data,omitempty"`
}

// SessionWorkspaceDiff is one changed path's diff (bulk lane): Kind "text" with the hunks in Diff and the
// full Size, or "binary" with the sizes of the sides that exist.
type SessionWorkspaceDiff struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	Kind      string `json:"kind"`
	Diff      string `json:"diff,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Size      int64  `json:"size,omitempty"`
	OldSize   *int64 `json:"oldSize,omitempty"`
	NewSize   *int64 `json:"newSize,omitempty"`
}

// Handoff (docs/spec.md#handoff, ADR 0009). core → device:

type HandoffPrepare struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
}

// HandoffInstall starts an installation; the snapshot archive and the session stream follow as HandoffData
// chunks, and the sizes and checksums here are what the received bytes are held to.
type HandoffInstall struct {
	V              int    `json:"v"`
	Type           string `json:"type"`
	SessionID      string `json:"sessionId"`
	HandoffID      string `json:"handoffId"`
	TargetPath     string `json:"targetPath,omitempty"`
	SnapshotBytes  int64  `json:"snapshotBytes"`
	SnapshotSHA256 string `json:"snapshotSha256"`
	SessionBytes   int64  `json:"sessionBytes"`
	SessionSHA256  string `json:"sessionSha256"`
}

// HandoffData is one chunk of a transfer: at most SnapshotChunkBytes bytes, plain base64 or base64 of their
// gzip, of the part "snapshot" or "session", in order from Seq 0.
type HandoffData struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
	Part      string `json:"part"`
	Seq       int    `json:"seq"`
	Last      bool   `json:"last"`
	Encoding  string `json:"encoding"`
	Data      string `json:"data"`
}

type HandoffVerify struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
}

// HandoffCommit says ownership moved: OwnerDeviceID owns the session from now on, at epoch OwnerEpoch, with
// the cursor ByteSize.
type HandoffCommit struct {
	V             int    `json:"v"`
	Type          string `json:"type"`
	SessionID     string `json:"sessionId"`
	HandoffID     string `json:"handoffId"`
	OwnerDeviceID string `json:"ownerDeviceId"`
	OwnerEpoch    int64  `json:"ownerEpoch"`
	ByteSize      int64  `json:"byteSize"`
}

type HandoffAbort struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
	Reason    string `json:"reason"`
}

// device → core:

// HandoffPrepared: the owner stopped the session's process, took a snapshot and sent it; ByteSize ends the
// session file.
type HandoffPrepared struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	SessionID  string `json:"sessionId"`
	HandoffID  string `json:"handoffId"`
	SnapshotID string `json:"snapshotId"`
	ByteSize   int64  `json:"byteSize"`
}

type HandoffInstalled struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
	Worktree  string `json:"worktree"`
	Branch    string `json:"branch"`
}

type HandoffVerified struct {
	V             int    `json:"v"`
	Type          string `json:"type"`
	SessionID     string `json:"sessionId"`
	HandoffID     string `json:"handoffId"`
	SessionBytes  int64  `json:"sessionBytes"`
	SessionSHA256 string `json:"sessionSha256"`
	TreeHash      string `json:"treeHash"`
}

// HandoffFailed reports a step the device could not do ("prepare", "install" or "verify"); whatever it had
// installed is already removed.
type HandoffFailed struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
	Step      string `json:"step"`
	Reason    string `json:"reason"`
}

type DeviceProjectSession struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Imported  bool   `json:"imported"`
	Title     string `json:"title,omitempty"`
}

// DeviceProject is a watched project: its path, the git branch checked out there (empty outside a
// repository) and the sessions found in it.
type DeviceProject struct {
	Path     string                 `json:"path"`
	Branch   string                 `json:"branch,omitempty"`
	Sessions []DeviceProjectSession `json:"sessions"`
}

type DeviceProjectsList struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
}

type DeviceProjectsAdd struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

type DeviceProjectsRemove struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

type DeviceSessionsImport struct {
	V          int      `json:"v"`
	Type       string   `json:"type"`
	RequestID  string   `json:"requestId"`
	Path       string   `json:"path"`
	SessionIDs []string `json:"sessionIds"`
}

type DeviceSessionsRemove struct {
	V          int      `json:"v"`
	Type       string   `json:"type"`
	RequestID  string   `json:"requestId"`
	Path       string   `json:"path"`
	SessionIDs []string `json:"sessionIds"`
}

type DeviceProjects struct {
	V          int             `json:"v"`
	Type       string          `json:"type"`
	RequestID  string          `json:"requestId"`
	Candidates []string        `json:"candidates"`
	Projects   []DeviceProject `json:"projects"`
}

type DeviceProjectsRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// DirEntry is one directory under the browse root; Git is set when it holds a .git entry.
type DirEntry struct {
	Name string `json:"name"`
	Git  bool   `json:"git"`
}

type DeviceDirsList struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// DeviceDirs answers device.dirs.list; Parent is nil (JSON null) at the browse root.
type DeviceDirs struct {
	V         int        `json:"v"`
	Type      string     `json:"type"`
	RequestID string     `json:"requestId"`
	Path      string     `json:"path"`
	Parent    *string    `json:"parent"`
	Root      string     `json:"root"`
	Entries   []DirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

type DeviceDirsRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type DeviceCloneFind struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Origin    string `json:"origin"`
}

// DeviceClone answers device.clone.find; Path is nil (JSON null) when no watched project has that origin.
type DeviceClone struct {
	V         int     `json:"v"`
	Type      string  `json:"type"`
	RequestID string  `json:"requestId"`
	Path      *string `json:"path"`
}
