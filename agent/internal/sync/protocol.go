package sync

import (
	"encoding/json"

	"github.com/yangflow/hoplet/agent/internal/rules"
)

// Wire protocol structures, mirroring shared/src/wire/ (docs/architecture.md#wire-protocol).
// scripts/test/contract.test.ts compares the message types and field names of both sides: a struct is
// named after its message type constant, a field's JSON name is its tag, and `omitempty` marks the
// fields the schema declares optional.
// Every sessionId is an exact nonempty ASCII letters/digits/underscore/hyphen key
// (provider.ValidSessionID, ADR 0006). Invalid IDs receive error invalid-session-id
// before decoding or persistence; the wire envelope and cursor are unchanged.

const ProtocolVersion = 21

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
	TypeRuntimeConfig            = "runtime.config"
	TypeRuntimeSteer             = "runtime.steer"
	TypeRuntimeSteered           = "runtime.steered"
	TypeRuntimeStart             = "runtime.start"
	TypeRuntimeStarted           = "runtime.started"
	TypeRuntimeStartRejected     = "runtime.start.rejected"
	TypeRuntimeMcpTokenRequest   = "runtime.mcp.token.request"
	TypeRuntimeMcpToken          = "runtime.mcp.token"
	TypeRuntimePermissionAnswer  = "runtime.permission.answer"
	TypeRuntimeInterrupt         = "runtime.interrupt"
	TypeRuntimeStop              = "runtime.stop"
	TypeRuntimeState             = "runtime.state"
	TypeRuntimeOutput            = "runtime.output"
	TypeRuntimePermission        = "runtime.permission"
	TypeRuntimePermissionAuto    = "runtime.permission.auto"
	TypeRuntimeRulesUnenforced   = "runtime.rules.unenforced"
	TypeRuntimeRejected          = "runtime.rejected"
	TypeRuntimeCommands          = "runtime.commands"
	TypeSessionWorkspaceList     = "session.workspace.list"
	TypeSessionWorkspace         = "session.workspace"
	TypeSessionWorkspaceRejected = "session.workspace.rejected"
	TypeSessionWorkspaceFileRead = "session.workspace.file.read"
	TypeSessionWorkspaceFile     = "session.workspace.file"
	TypeSessionWorkspaceDiffRead = "session.workspace.diff.read"
	TypeSessionWorkspaceDiff     = "session.workspace.diff"
	TypeSessionConfigRead        = "session.config.read"
	TypeSessionConfig            = "session.config"
	TypeSessionConfigRejected    = "session.config.rejected"
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
	TypeDeviceSessionInspect     = "device.session.inspect"
	TypeDeviceSessionInspected   = "device.session.inspected"
	TypeDeviceSessionRejected    = "device.session.rejected"
	TypeDeviceSessionDelete      = "device.session.delete"
	TypeDeviceSessionDeleted     = "device.session.deleted"
	TypeDeviceSessionForget      = "device.session.forget"
	TypeDeviceSessionForgotten   = "device.session.forgotten"
	TypeDeviceProjects           = "device.projects"
	TypeDeviceProjectsRejected   = "device.projects.rejected"
	TypeDeviceDirsList           = "device.dirs.list"
	TypeDeviceDirs               = "device.dirs"
	TypeDeviceDirsRejected       = "device.dirs.rejected"
	TypeDeviceProviders          = "device.providers"
	TypeDeviceProvidersUpdate    = "device.providers.update"
	TypeDeviceProvidersRejected  = "device.providers.rejected"
	TypeDeviceRules              = "device.rules"
	TypeDeviceRulesApplied       = "device.rules.applied"
	TypeHookEvent                = "hook.event"
	TypeHookResult               = "hook.result"
	TypeAttachmentPut            = "attachment.put"
	TypeAttachmentStored         = "attachment.stored"
	TypeAttachmentRejected       = "attachment.rejected"
	TypeDeviceCloneFind          = "device.clone.find"
	TypeDeviceClone              = "device.clone"
	TypeDeviceCloneCheck         = "device.clone.check"
	TypeDeviceCloneChecked       = "device.clone.checked"
	TypeDeviceCloneStart         = "device.clone.start"
	TypeDeviceCloneProgress      = "device.clone.progress"
	TypeDeviceCloneDone          = "device.clone.done"
	TypeDeviceCloneFailed        = "device.clone.failed"
	TypeDeviceCloneCancel        = "device.clone.cancel"
	TypeTerminalOpen             = "terminal.open"
	TypeTerminalInput            = "terminal.input"
	TypeTerminalResize           = "terminal.resize"
	TypeTerminalAck              = "terminal.ack"
	TypeTerminalClose            = "terminal.close"
	TypeTerminalOpened           = "terminal.opened"
	TypeTerminalOutput           = "terminal.output"
	TypeTerminalClosed           = "terminal.closed"
	TypeTerminalRejected         = "terminal.rejected"
	TypePortsWatch               = "ports.watch"
	TypeSessionPorts             = "session.ports"
	TypeDeviceResources          = "device.resources"
	TypeDeviceInventory          = "device.inventory"
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
	// System is the machine the agent runs on; nil means not announced.
	System *DeviceSystem `json:"system,omitempty"`
	// Providers are the vendors this agent runs, with the capabilities of their runtime drivers.
	Providers []ProviderInfo `json:"providers,omitempty"`
	// Capabilities lists what the agent offers beyond sessions; nil means nothing.
	Capabilities *DeviceCapabilities `json:"capabilities,omitempty"`
	// PreviewHost is the host the inspector links dev servers with: preview_host, else the machine's host name.
	PreviewHost string `json:"previewHost,omitempty"`
	// PermissionDeny are the operator's own deny rules (permission_deny, ADR 0029) as the engine holds them:
	// scope agent, enabled. The core shows them read-only.
	PermissionDeny []rules.Rule `json:"permissionDeny,omitempty"`
	// Hooks are the script hooks of the operator's config.json (ADR 0031) with their counters.
	Hooks []HookAnnounce `json:"hooks,omitempty"`
}

// HookAnnounce is a script hook as device.hello announces it; the names in Env are passed through, never
// their values.
type HookAnnounce struct {
	ID        string   `json:"id"`
	Command   string   `json:"command"`
	Events    []string `json:"events"`
	Projects  []string `json:"projects"`
	TimeoutMs int      `json:"timeoutMs"`
	Env       []string `json:"env"`
	Runs      int64    `json:"runs"`
	Failed    int64    `json:"failed"`
	Dropped   int64    `json:"dropped"`
}

// DeviceSystem is the system the agent runs on: runtime.GOOS and runtime.GOARCH verbatim.
type DeviceSystem struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// DeviceCapabilities is what an agent offers beyond its providers.
type DeviceCapabilities struct {
	// Terminal is set when the owner enabled the restricted terminal (ADR 0026).
	Terminal bool `json:"terminal,omitempty"`
	// SessionMcp is set when the owner enabled session_mcp: the agent serves the session MCP tools (ADR 0034).
	SessionMcp bool `json:"sessionMcp,omitempty"`
}

// ProviderInfo is one provider configuration of an agent (ADR 0028) and what its runtime driver declares:
// Provider is the vendor it extends.
type ProviderInfo struct {
	Provider      string                `json:"provider"`
	Configuration ProviderConfiguration `json:"configuration"`
	Capabilities  RuntimeCapabilities   `json:"capabilities"`
	// Version is the installed vendor version; Tested says the driver was verified against exactly it.
	// Both are absent when the version could not be read or is refused.
	Version string `json:"version,omitempty"`
	Tested  *bool  `json:"tested,omitempty"`
}

// ProviderConfiguration is what the machine's providers.json says of one configuration, without any value of
// its environment: Env lists the variables by name and whether each is set.
type ProviderConfiguration struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Extends  string           `json:"extends"`
	Enabled  bool             `json:"enabled"`
	Command  string           `json:"command,omitempty"`
	Env      []ProviderEnvVar `json:"env"`
	Defaults map[string]any   `json:"defaults"`
}

// ProviderEnvVar names one environment variable of a configuration; the value never leaves the machine.
type ProviderEnvVar struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

// DeviceProviders (device → core) is the full list of the agent's configurations. It answers a
// DeviceProvidersUpdate and is sent again when providers.json changes.
type DeviceProviders struct {
	V         int            `json:"v"`
	Type      string         `json:"type"`
	RequestID string         `json:"requestId,omitempty"`
	Providers []ProviderInfo `json:"providers"`
}

// DeviceProvidersUpdate (core → device) changes the name, the enabled flag or the defaults of one
// configuration; nothing else of a configuration can be changed from the core.
type DeviceProvidersUpdate struct {
	V         int            `json:"v"`
	Type      string         `json:"type"`
	RequestID string         `json:"requestId"`
	ID        string         `json:"id"`
	Name      *string        `json:"name,omitempty"`
	Enabled   *bool          `json:"enabled,omitempty"`
	Defaults  map[string]any `json:"defaults,omitempty"`
}

// DeviceProvidersRejected (device → core) refuses a DeviceProvidersUpdate.
type DeviceProvidersRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// DeviceRules (core → device, control lane) is every permission rule that can apply to a session on this
// device (ADR 0029): the global rules with the built-ins and their enabled state, the rules of projects of the
// device by project path, and the rules of the sessions the device owns by session ID. Each rule body is the
// agent's rules.Rule; the core-only fields are not sent, and the agent refuses a push with a field it does not
// know. Revision is the device's rule revision; the core sends the set after every connection and after every
// change.
type DeviceRules struct {
	V        int                 `json:"v"`
	Type     string              `json:"type"`
	Revision int                 `json:"revision"`
	Global   []rules.Rule        `json:"global"`
	Projects []rules.ProjectRule `json:"projects"`
	Sessions []rules.SessionRule `json:"sessions"`
	// Inherited holds the deny and ask rules a child session obeys besides its own, by the child's session ID.
	Inherited []rules.SessionRule `json:"inherited,omitempty"`
}

// DeviceRulesApplied (device → core, control lane) answers a DeviceRules. Without Error the agent holds the
// set and wrote it to its state directory; with Error it refused the push, keeps the previous set, and Revision
// is the one it was sent when it could read it.
type DeviceRulesApplied struct {
	V        int    `json:"v"`
	Type     string `json:"type"`
	Revision int    `json:"revision"`
	Error    string `json:"error,omitempty"`
}

// HookEvent is one lifecycle event for one script hook of this machine (ADR 0031); Event is the envelope, which
// the script receives as it is.
type HookEvent struct {
	V      int             `json:"v"`
	Type   string          `json:"type"`
	HookID string          `json:"hookId"`
	Event  json.RawMessage `json:"event"`
}

// HookResult is the outcome of one HookEvent: Error is timeout, spawn-failed or dropped when there is no exit code.
type HookResult struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	HookID     string `json:"hookId"`
	EventID    string `json:"eventId"`
	OK         bool   `json:"ok"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
	Stderr     string `json:"stderr"`
}

type SessionAppend struct {
	V          int      `json:"v"`
	Type       string   `json:"type"`
	SessionID  string   `json:"sessionId"`
	Provider   string   `json:"provider"` // the source's provider Name(): the vendor the session belongs to
	ProjectDir string   `json:"projectDir"`
	FilePath   string   `json:"filePath"`
	ByteStart  int64    `json:"byteStart"`
	Encoding   Encoding `json:"encoding"`
	Lines      string   `json:"lines"` // raw lines joined by \n; gzip+base64 when encoded
	// Title is PeekTitle from the live file; updates the store index only.
	Title string `json:"title,omitempty"`
	// Configuration is the ID of the provider configuration the session belongs to (ADR 0028); absent means the
	// vendor's own. The core keeps what the latest append says.
	Configuration string `json:"configuration,omitempty"`
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

// RuntimePrompt is a turn for a session; Attachments are the IDs of images uploaded for it (ADR 0017).
type RuntimePrompt struct {
	V           int      `json:"v"`
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	PromptID    string   `json:"promptId"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
	// Source is set when another session wrote the prompt (ADR 0034).
	Source *PromptSource `json:"source,omitempty"`
}

// PromptSource names the session behind a prompt: the core sets it from the sender's token, and the agent adds a
// first line that says so to the text it hands the vendor.
type PromptSource struct {
	Kind      string   `json:"kind"`
	SessionID string   `json:"sessionId"`
	Title     string   `json:"title"`
	Chain     []string `json:"chain"`
}

// RuntimeSteer (core → device, control lane) writes a message into the running turn (ADR 0020). The agent answers
// with RuntimeSteered, or RuntimeRejected carrying SteerID as PromptID.
type RuntimeSteer struct {
	V           int      `json:"v"`
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	SteerID     string   `json:"steerId"`
	Text        string   `json:"text"`
	Attachments []string `json:"attachments,omitempty"`
}

// RuntimeSteered (device → core, control lane): the message was written into the running turn.
type RuntimeSteered struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	SteerID   string `json:"steerId"`
}

// RuntimeConfig (core → device, control lane) sets one setting of a session (ADR 0018). Value is a string for
// a select option and a bool for a boolean one. The agent reports the session's settings in its next
// RuntimeState.
type RuntimeConfig struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
}

// AttachmentPut (core → device, bulk) carries one chunk of an image for a coming prompt, as base64, at most
// SnapshotChunkBytes; Seq counts from 0 and Last marks the final chunk, after which the device answers with
// AttachmentStored. Any chunk that fails is answered with AttachmentRejected.
type AttachmentPut struct {
	V            int    `json:"v"`
	Type         string `json:"type"`
	RequestID    string `json:"requestId"`
	AttachmentID string `json:"attachmentId"`
	MediaType    string `json:"mediaType"`
	Seq          int64  `json:"seq"`
	Last         bool   `json:"last"`
	Data         string `json:"data"`
}

type AttachmentStored struct {
	V            int    `json:"v"`
	Type         string `json:"type"`
	RequestID    string `json:"requestId"`
	AttachmentID string `json:"attachmentId"`
}

type AttachmentRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type RuntimePermissionAnswer struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Decision  string `json:"decision"`          // allow | deny
	Message   string `json:"message,omitempty"` // a refusal's reason, told to the model
	Mode      string `json:"mode,omitempty"`    // the mode an approved plan switches the session to
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

// RulesPromptsOnly is the value of RuntimeCapabilities.Rules of a driver whose permission rules act only on the
// approval requests the vendor sends (ADR 0029, ADR 0032).
const RulesPromptsOnly = "prompts-only"

// RuntimeCapabilities is what a runtime driver declares it can do. Config lists the settings of a session
// (ADR 0018); it is empty (never null) when the driver has none. Rules is absent when the permission rules check
// every tool call, and RulesPromptsOnly when they act only on the vendor's approval requests.
type RuntimeCapabilities struct {
	Permissions bool           `json:"permissions"`
	Interrupt   bool           `json:"interrupt"`
	Images      bool           `json:"images,omitempty"`
	Steer       bool           `json:"steer,omitempty"`
	Rules       string         `json:"rules,omitempty"`
	Config      []ConfigOption `json:"config"`
}

// ConfigOption is one setting of a session, shaped as an ACP session config option. Type is "select" (Options
// and a string CurrentValue) or "boolean" (a bool CurrentValue). In a provider's capabilities CurrentValue is
// the default of a new session; in a session's RuntimeState it is that session's value.
type ConfigOption struct {
	Type         string        `json:"type"`
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Category     string        `json:"category,omitempty"`
	Options      []ConfigValue `json:"options,omitempty"`
	CurrentValue any           `json:"currentValue"`
}

// ConfigValue is one value of a select option; Risk is "high" for a value clients should mark.
type ConfigValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk,omitempty"`
	Icon        string `json:"icon,omitempty"` // a name of the shared icon vocabulary (shared/src/runtime.ts)
}

// RuntimeStart (core → device) starts a new session in ProjectPath, a watched project of the device. The
// agent answers with RuntimeStarted or RuntimeStartRejected for the same RequestID.
type RuntimeStart struct {
	V           int    `json:"v"`
	Type        string `json:"type"`
	RequestID   string `json:"requestId"`
	ProjectPath string `json:"projectPath"`
	Provider    string `json:"provider"`
	// Configuration is the ID of the provider configuration to start with; absent means the vendor's own.
	Configuration string `json:"configuration,omitempty"`
	// Config holds the settings of the new session by option ID; an option left out keeps its default.
	Config      map[string]any `json:"config,omitempty"`
	Text        string         `json:"text"`
	Attachments []string       `json:"attachments,omitempty"`
	PromptID    string         `json:"promptId"`
	// Worktree asks for the session to run in a new git worktree of ProjectPath's repository, made before the
	// provider starts (ADR 0023 for its deletion). Absent: the session runs in ProjectPath.
	Worktree *NewWorktree `json:"worktree,omitempty"`
	// Source is set when another session started this one (ADR 0034).
	Source *PromptSource `json:"source,omitempty"`
	// InheritedRules are the deny and ask rules of the sessions that started this one, which it obeys from its
	// first tool call (ADR 0029). Never an allow rule.
	InheritedRules []rules.Rule `json:"inheritedRules,omitempty"`
}

// NewWorktree is the worktree a new session asks for. Branch names the new branch; empty means the agent
// generates one.
type NewWorktree struct {
	Branch string `json:"branch,omitempty"`
}

// StartedWorktree is the worktree the agent made for a new session: Clone is the repository's main working
// tree, Path the worktree directory and Branch its branch.
type StartedWorktree struct {
	Clone  string `json:"clone"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// RuntimeMcpTokenRequest (device → core) asks for the token of a session's MCP server (ADR 0034).
type RuntimeMcpTokenRequest struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
}

// RuntimeMcpToken (core → device) answers it with a session token and its expiry (Unix ms), or with Reason.
type RuntimeMcpToken struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	Token     string `json:"token,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// RuntimeStarted (device → core): the session is created, imported and syncs from now on. Worktree is the
// worktree made for it, when one was asked for.
type RuntimeStarted struct {
	V         int              `json:"v"`
	Type      string           `json:"type"`
	RequestID string           `json:"requestId"`
	SessionID string           `json:"sessionId"`
	Worktree  *StartedWorktree `json:"worktree,omitempty"`
}

type RuntimeStartRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type RuntimeState struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	State     string `json:"state"` // external | idle | starting | ready | running | waiting-permission | failed
	Error     string `json:"error,omitempty"`
	// Capabilities.Config carries this session's values.
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
	PlanExit  *PlanExit       `json:"planExit,omitempty"`
}

// RuntimePermissionAuto reports a tool call a permission rule decided without asking a person (ADR 0029). The
// core records it in the audit log.
type RuntimePermissionAuto struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	ToolUseID string `json:"toolUseId,omitempty"`
	Decision  string `json:"decision"` // allow | deny | ask
	RuleID    string `json:"ruleId"`
	Scope     string `json:"scope"` // global | project | session | agent
	Revision  int    `json:"revision"`
}

// RuntimeRulesUnenforced reports that a tool call ran without the permission rules being checked: the Codex hook did
// not run for it (ADR 0032). The agent sends it once per process.
type RuntimeRulesUnenforced struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	ToolUseID string `json:"toolUseId,omitempty"`
}

// PlanExit marks a permission request that asks to leave a plan (ADR 0019).
type PlanExit struct {
	Plan     string   `json:"plan"`
	PlanFile string   `json:"planFile,omitempty"`
	Option   string   `json:"option"`
	Modes    []string `json:"modes"`
	Default  string   `json:"default"`
	// FreshContext is set when the provider can start a new session from the approved plan (ADR 0025).
	FreshContext bool `json:"freshContext,omitempty"`
}

type RuntimeRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	PromptID  string `json:"promptId"`
	Reason    string `json:"reason"` // busy | starting | external | not-running | permission | unsupported | configuration-unavailable | diagnostic text
}

// Command is one slash command or skill a session offers (shared/src/commands.ts).
type Command struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint,omitempty"`
	Kind         string `json:"kind"`   // command | skill
	Source       string `json:"source"` // builtin | project | user | plugin
	Plugin       string `json:"plugin,omitempty"`
}

// RuntimeCommands is the complete list of commands and skills a running session offers, with the directory it
// runs in; it is sent whenever the list changes and again after a reconnect.
type RuntimeCommands struct {
	V         int       `json:"v"`
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	Cwd       string    `json:"cwd"`
	Commands  []Command `json:"commands"`
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

// SessionWorkspaceDiffRead asks for one page of a changed path's diff. Base compares with the commit the
// session's worktree started from instead of HEAD; Cursor is the byte offset in the hunks where the page starts
// (0, or the Next of the page before) and Limit the most bytes of hunks in it (the agent's own cap when absent).
type SessionWorkspaceDiffRead struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	Base      bool   `json:"base,omitempty"`
	Cursor    int64  `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// The changes of a session against the commit it started from (ADR 0035). core → device, then the answer on
// the control lane: it is at most MaxChangeFiles entries and no content.
const (
	TypeSessionWorkspaceChangesRead = "session.workspace.changes.read"
	TypeSessionWorkspaceChanges     = "session.workspace.changes"
)

type SessionWorkspaceChangesRead struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
}

// SessionWorkspaceChanges lists what a session changed. Base is the commit the changes are counted against and
// Branch the branch it works on ("" for a detached HEAD); Shared says the session has no worktree of its own, so
// the changes may be someone else's too. Files are at most MaxChangeFiles of Total, by path.
type SessionWorkspaceChanges struct {
	V         int               `json:"v"`
	Type      string            `json:"type"`
	SessionID string            `json:"sessionId"`
	RequestID string            `json:"requestId"`
	Branch    string            `json:"branch"`
	Base      string            `json:"base"`
	Shared    bool              `json:"shared,omitempty"`
	Total     int               `json:"total"`
	Files     []WorkspaceChange `json:"files"`
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

// SessionWorkspaceDiff is one page of a changed path's diff (bulk lane): Kind "text" with the hunks of the page
// in Diff, the full Size of all of them and, when more follow, Truncated and the Next cursor (absent on the last
// page), or "binary" with the sizes of the sides that exist.
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
	Next      *int64 `json:"next,omitempty"`
	Size      int64  `json:"size,omitempty"`
	OldSize   *int64 `json:"oldSize,omitempty"`
	NewSize   *int64 `json:"newSize,omitempty"`
}

// SessionConfigRead asks for a page of a session's own configuration file (ADR 0036). Provider is the stored
// session's vendor, which fixes the paths of each Kind; Name is absent for "memory" and "list".
type SessionConfigRead struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Provider  string `json:"provider"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Name      string `json:"name,omitempty"`
	Offset    int64  `json:"offset,omitempty"`
}

// ConfigFileEntry is one skill or command of a list answer.
type ConfigFileEntry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SessionConfig is a page of a configuration file (bulk lane): Exists false for a missing file, Size and Hash
// (SHA-256, hex) of the whole file, Text from byte Offset, NextOffset while more follows; Entries answer a list.
type SessionConfig struct {
	V          int               `json:"v"`
	Type       string            `json:"type"`
	SessionID  string            `json:"sessionId"`
	RequestID  string            `json:"requestId"`
	Kind       string            `json:"kind"`
	Scope      string            `json:"scope"`
	Name       string            `json:"name,omitempty"`
	Exists     bool              `json:"exists"`
	Size       int64             `json:"size"`
	Hash       string            `json:"hash"`
	Offset     int64             `json:"offset"`
	Text       string            `json:"text"`
	NextOffset *int64            `json:"nextOffset,omitempty"`
	Entries    []ConfigFileEntry `json:"entries"`
}

// SessionConfigRejected answers a SessionConfigRead the agent cannot answer; Code is "unsupported", "invalid"
// or "io".
type SessionConfigRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Code      string `json:"code"`
	Reason    string `json:"reason"`
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
	V         int    `json:"v"`
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	HandoffID string `json:"handoffId"`
	Provider  string `json:"provider"` // the session's vendor, whose store the target installs it in
	// Configuration is the target's provider configuration the session is installed for; absent means the vendor's own.
	Configuration  string `json:"configuration,omitempty"`
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
	Clone     string `json:"clone"`
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

// Deleting a session (ADR 0023). The core names the session; the agent finds the vendor file and the
// worktree itself and never takes a path from the wire.

// ReasonDeleted is the reason of a session.rejected for a session that was deleted.
const ReasonDeleted = "deleted"

// DeletionFile is the vendor's session file on this machine.
type DeletionFile struct {
	Path string `json:"path"`
}

// DeletionDirty counts uncommitted work in a worktree: changed tracked files and untracked files.
type DeletionDirty struct {
	Modified  int `json:"modified"`
	Untracked int `json:"untracked"`
}

// DeletionWorktree is the worktree and branch a handoff made; Unpushed counts commits no remote has.
type DeletionWorktree struct {
	Path     string        `json:"path"`
	Branch   string        `json:"branch"`
	Dirty    DeletionDirty `json:"dirty"`
	Unpushed int           `json:"unpushed"`
}

// DeletionStep is one step of a deletion: Step is stop-syncing, worktree, branch or vendor-file, Status is
// done, failed or skipped, and Reason says why a step failed.
type DeletionStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Step names and statuses of a DeletionStep.
const (
	StepStopSyncing = "stop-syncing"
	StepWorktree    = "worktree"
	StepBranch      = "branch"
	StepVendorFile  = "vendor-file"

	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// core → device:

type DeviceSessionInspect struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
}

type DeviceSessionDelete struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	RequestID  string `json:"requestId"`
	SessionID  string `json:"sessionId"`
	VendorFile bool   `json:"vendorFile"`
	Worktree   bool   `json:"worktree"`
	// WorktreeDirty: the user ticked a worktree the preview showed dirty.
	WorktreeDirty bool `json:"worktreeDirty"`
}

// DeviceSessionForget is the first step of a deletion alone: stop syncing the session and forget it.
type DeviceSessionForget struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
}

// device → core:

type DeviceSessionInspected struct {
	V          int               `json:"v"`
	Type       string            `json:"type"`
	RequestID  string            `json:"requestId"`
	VendorFile *DeletionFile     `json:"vendorFile,omitempty"`
	Worktree   *DeletionWorktree `json:"worktree,omitempty"`
}

type DeviceSessionRejected struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

type DeviceSessionDeleted struct {
	V         int            `json:"v"`
	Type      string         `json:"type"`
	RequestID string         `json:"requestId"`
	Steps     []DeletionStep `json:"steps"`
}

type DeviceSessionForgotten struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
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

// DeviceCloneCheck asks whether Origin can be cloned into CloneInto (empty: suggest a destination).
type DeviceCloneCheck struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Origin    string `json:"origin"`
	CloneInto string `json:"cloneInto,omitempty"`
}

// DeviceCloneChecked answers device.clone.check; CloneInto is the destination that was checked.
type DeviceCloneChecked struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
	CloneInto string `json:"cloneInto"`
}

// DeviceCloneStart clones Origin into CloneInto for a handoff.
type DeviceCloneStart struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	HandoffID string `json:"handoffId"`
	Origin    string `json:"origin"`
	CloneInto string `json:"cloneInto"`
}

// DeviceCloneProgress reports the bytes received so far, at most four times a second; Total is absent
// until git says it.
type DeviceCloneProgress struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Received  int64  `json:"received"`
	Total     *int64 `json:"total,omitempty"`
}

// DeviceCloneDone ends a clone that completed and is watched; Path is its main working tree.
type DeviceCloneDone struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// DeviceCloneFailed ends a clone that did not complete; the directory it created is gone.
type DeviceCloneFailed struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail,omitempty"`
}

// DeviceCloneCancel stops the clone of RequestID; it is answered by DeviceCloneFailed with reason cancelled.
type DeviceCloneCancel struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
}

// Terminal reasons (ADR 0026): why a terminal was refused or ended.
const (
	TerminalReasonDisabled       = "disabled"
	TerminalReasonBusy           = "terminal-busy"
	TerminalReasonTurnRunning    = "turn-running"
	TerminalReasonExternal       = "external"
	TerminalReasonHandoffRunning = "handoff-running"
	TerminalReasonOwnerOffline   = "owner-offline"
	TerminalReasonNotADirectory  = "not-a-directory"
	TerminalReasonIdle           = "idle"
	TerminalReasonMaxAge         = "max-age"
	TerminalReasonDetached       = "detached"
	TerminalReasonTurnStarted    = "turn-started"
	TerminalReasonHandoff        = "handoff"
	TerminalReasonDeleted        = "deleted"
	TerminalReasonClosed         = "closed"
	TerminalReasonInputFlood     = "input-flood"
	TerminalReasonExit           = "exit"
)

// TerminalOpen asks for a shell in the working directory of SessionID.
type TerminalOpen struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	SessionID  string `json:"sessionId"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

// TerminalInput is keyboard input, base64 of at most 4 KiB.
type TerminalInput struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Data       string `json:"data"`
}

// TerminalResize changes the size of the terminal (columns 1-500, rows 1-200).
type TerminalResize struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

// TerminalAck returns Bytes of output credit to the agent.
type TerminalAck struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Bytes      int64  `json:"bytes"`
}

// TerminalClose ends the terminal.
type TerminalClose struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Reason     string `json:"reason"`
}

// TerminalOpened answers TerminalOpen: the shell runs.
type TerminalOpened struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
}

// TerminalOutput is shell output, base64 of at most 4 KiB, numbered from 0.
type TerminalOutput struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Seq        int64  `json:"seq"`
	Data       string `json:"data"`
}

// TerminalClosed says the terminal ended and why; ExitCode is set when the shell exited.
type TerminalClosed struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Reason     string `json:"reason"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// TerminalRejected answers TerminalOpen when no shell was started.
type TerminalRejected struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	TerminalID string `json:"terminalId"`
	Reason     string `json:"reason"`
}

// PortsWatch replaces the set of sessions whose dev server ports the agent scans for; empty stops the scan
// (ADR 0026). At most 64 sessions.
type PortsWatch struct {
	V          int      `json:"v"`
	Type       string   `json:"type"`
	SessionIDs []string `json:"sessionIds"`
}

// SessionPort is one listening TCP port of a process started from the session's directory. Address is
// "loopback", "any" or "other"; Process is the executable's base name, at most 64 characters.
type SessionPort struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	Process string `json:"process"`
}

// SessionPorts reports the ports of one watched session, at most 16, sorted by port.
type SessionPorts struct {
	V         int           `json:"v"`
	Type      string        `json:"type"`
	SessionID string        `json:"sessionId"`
	Ports     []SessionPort `json:"ports"`
}

// SessionResources is the load of the processes Hoplet started for one session, summed over the process tree.
// CPU is the percent of the whole machine's CPU, null while there is no earlier reading to compare with; RSS is
// resident memory in bytes; Children counts the descendant processes.
type SessionResources struct {
	SessionID string   `json:"sessionId"`
	CPU       *float64 `json:"cpu"`
	RSS       uint64   `json:"rss"`
	Children  int      `json:"children"`
}

// DeviceResources reports the machine's load every 15 seconds on the control lane, at most 64 sessions. At is
// epoch milliseconds; CPU is the whole machine's percent, null while there is no earlier reading; Disk* describe
// the volume that holds the browse root. A report sent while the core is away is dropped.
type DeviceResources struct {
	V         int                `json:"v"`
	Type      string             `json:"type"`
	At        int64              `json:"at"`
	CPU       *float64           `json:"cpu"`
	MemUsed   uint64             `json:"memUsed"`
	MemTotal  uint64             `json:"memTotal"`
	DiskFree  uint64             `json:"diskFree"`
	DiskTotal uint64             `json:"diskTotal"`
	Sessions  []SessionResources `json:"sessions"`
}

// InventoryToolchain is one developer tool found on the machine with its version.
type InventoryToolchain struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InventoryGpu is one GPU; Memory is its memory in bytes, left out when unknown.
type InventoryGpu struct {
	Name   string  `json:"name"`
	Memory *uint64 `json:"memory,omitempty"`
}

// InventoryRepository is one watched project with its `origin` remote, nil when it has none or is no git
// repository. The URL carries no user, password, query or fragment.
type InventoryRepository struct {
	Path   string  `json:"path"`
	Origin *string `json:"origin"`
}

// DeviceInventory reports what the machine offers on the control lane: at most 32 toolchains, 8 GPUs and 200
// repositories (INVENTORY_*_MAX in shared/src/wire). It is sent after each connection and then hourly, and
// again soon after the watched projects change. At is epoch milliseconds. A report sent while the core is away
// is dropped.
type DeviceInventory struct {
	V            int                   `json:"v"`
	Type         string                `json:"type"`
	At           int64                 `json:"at"`
	Toolchains   []InventoryToolchain  `json:"toolchains"`
	Gpus         []InventoryGpu        `json:"gpus"`
	Repositories []InventoryRepository `json:"repositories"`
}
