package sync

import "context"

// RuntimeHooks handle the remote control messages of the core (docs/spec.md#remote-control). The vendor
// process, not Hoplet, writes the session file.
type RuntimeHooks struct {
	// Prompt accepts a prompt and the uploaded images it names or refuses it; the error text is the reason
	// sent as runtime.rejected.
	Prompt    func(sessionID, text string, attachments []string, model string) error
	Answer    func(sessionID, requestID string, allow bool) error
	Interrupt func(sessionID string) error
	Stop      func(sessionID string)
	// Start creates a session as RuntimeStart asks and returns its ID once the session file exists and is
	// imported, or the reason it could not. The error text is sent as runtime.start.rejected.
	Start func(ctx context.Context, req RuntimeStart) (sessionID string, err error)
	// Started is called after the answer to a successful Start was written; the agent reports the state of
	// the new session then, which the core accepts only once it has the answer.
	Started func(sessionID string)
	// Connected is called in its own goroutine after every connection to the core; the agent reports the
	// state of the processes it still runs through Client.Send.
	Connected func()
}

// WorkspaceListing is the content of session.workspace.
type WorkspaceListing struct {
	Git       bool
	Commit    string
	Origin    string
	Paths     []string
	Truncated bool
	Changes   []WorkspaceChange
}

// WorkspaceFileContent is the content of session.workspace.file; Data is sent as base64.
type WorkspaceFileContent struct {
	Kind      string
	Size      int64
	Text      string
	Truncated bool
	MediaType string
	Data      []byte
}

// WorkspaceDiffContent is the content of session.workspace.diff.
type WorkspaceDiffContent struct {
	Status    string
	Kind      string
	Diff      string
	Truncated bool
	Size      int64
	OldSize   *int64
	NewSize   *int64
}

// WorkspaceFunc lists the working tree of a session on this device.
type WorkspaceFunc func(ctx context.Context, sessionID string) (WorkspaceListing, error)

// WorkspaceFileFunc reads one file of a session's working tree; at is "worktree", "head" or empty.
type WorkspaceFileFunc func(ctx context.Context, sessionID, path, at string) (WorkspaceFileContent, error)

// WorkspaceDiffFunc reads the diff of one changed path of a session's working tree against HEAD.
type WorkspaceDiffFunc func(ctx context.Context, sessionID, path string) (WorkspaceDiffContent, error)

// DirListing is the content of device.dirs.
type DirListing struct {
	Path      string
	Parent    *string
	Root      string
	Entries   []DirEntry
	Truncated bool
}

// Hooks are the device-side handlers for control messages from core.
type Hooks struct {
	// Providers are announced in device.hello with the capabilities of their runtime drivers.
	Providers []ProviderInfo

	Runtime   RuntimeHooks
	Workspace WorkspaceFunc
	File      WorkspaceFileFunc
	Diff      WorkspaceDiffFunc
	Handoff   HandoffHooks

	// ProjectsList / ProjectsAdd / ProjectsRemove match hoplet-agent projects list|add|remove.
	ProjectsList   func(ctx context.Context) (candidates []string, projects []DeviceProject, err error)
	ProjectsAdd    func(ctx context.Context, path string) (candidates []string, projects []DeviceProject, err error)
	ProjectsRemove func(ctx context.Context, path string) (candidates []string, projects []DeviceProject, err error)

	// DirsList lists the directories of path under the browse root; an error becomes device.dirs.rejected
	// with its text as the reason.
	DirsList func(ctx context.Context, path string) (DirListing, error)

	// AttachmentPut takes one chunk of an image for a coming prompt and reports whether the image is complete
	// and stored; an error becomes attachment.rejected with its text.
	AttachmentPut func(put AttachmentPut) (done bool, err error)

	// CloneFind names the clone a handoff of a repository with this origin would use, "" when there is none.
	CloneFind func(ctx context.Context, origin string) (string, error)

	// SessionsImport / SessionsRemove match hoplet-agent sessions import|remove.
	SessionsImport func(ctx context.Context, path string, sessionIDs []string) (candidates []string, projects []DeviceProject, err error)
	SessionsRemove func(ctx context.Context, path string, sessionIDs []string) (candidates []string, projects []DeviceProject, err error)
}
