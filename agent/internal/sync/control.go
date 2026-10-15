package sync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
)

func (c *Client) handleRuntimePrompt(cs *connState, raw json.RawMessage) {
	var req RuntimePrompt
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	sessionID, promptID := req.SessionID, req.PromptID
	var err error
	if c.hooks.Runtime.Prompt == nil {
		err = errors.New("not-implemented")
	} else {
		err = c.hooks.Runtime.Prompt(sessionID, req.Text, req.Attachments, req.Model)
	}
	if err != nil {
		log.Printf("[agent] prompt refused for %s: %v", shortID(sessionID), err)
		_ = c.writeMsg(cs, RuntimeRejected{
			V:         ProtocolVersion,
			Type:      TypeRuntimeRejected,
			SessionID: sessionID,
			PromptID:  promptID,
			Reason:    err.Error(),
		})
	}
}

func (c *Client) handleRuntimeStart(cs *connState, raw json.RawMessage) {
	var req RuntimeStart
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	reject := func(reason string) {
		log.Printf("[agent] new session refused: %s", reason)
		_ = c.writeMsg(cs, RuntimeStartRejected{
			V: ProtocolVersion, Type: TypeRuntimeStartRejected, RequestID: req.RequestID, Reason: reason,
		})
	}
	if c.hooks.Runtime.Start == nil {
		reject("not-implemented")
		return
	}
	sessionID, err := c.hooks.Runtime.Start(context.Background(), req)
	if err != nil {
		reject(err.Error())
		return
	}
	if err := c.writeMsg(cs, RuntimeStarted{
		V: ProtocolVersion, Type: TypeRuntimeStarted, RequestID: req.RequestID, SessionID: sessionID,
	}); err != nil {
		return
	}
	log.Printf("[agent] started session %s", shortID(sessionID))
	if c.hooks.Runtime.Started != nil {
		c.hooks.Runtime.Started(sessionID)
	}
}

func (c *Client) handleRuntimeAnswer(sessionID, requestID, decision string) {
	if c.hooks.Runtime.Answer == nil {
		return
	}
	if err := c.hooks.Runtime.Answer(sessionID, requestID, decision == "allow"); err != nil {
		log.Printf("[agent] permission answer for %s not applied: %v", shortID(sessionID), err)
	}
}

func (c *Client) handleRuntimeInterrupt(sessionID string) {
	if c.hooks.Runtime.Interrupt == nil {
		return
	}
	if err := c.hooks.Runtime.Interrupt(sessionID); err != nil {
		log.Printf("[agent] interrupt for %s not applied: %v", shortID(sessionID), err)
	}
}

// ErrNotConnected is returned by Send when no connection to the core is up.
var ErrNotConnected = errors.New("not connected to the core")

// Send writes a message on the current connection, or returns ErrNotConnected. Runtime events use it:
// they are transient, and the state of every process is reported again after a reconnect.
func (c *Client) Send(msg any) error {
	cs := c.cs.Load()
	if cs == nil {
		return ErrNotConnected
	}
	return c.writeMsg(cs, msg)
}

func (c *Client) rejectWorkspace(cs *connState, sessionID, requestID, what string, err error) {
	log.Printf("[agent] workspace %s rejected for %s: %v", what, shortID(sessionID), err)
	_ = c.writeMsg(cs, SessionWorkspaceRejected{
		V:         ProtocolVersion,
		Type:      TypeSessionWorkspaceRejected,
		SessionID: sessionID,
		RequestID: requestID,
		Reason:    err.Error(),
	})
}

func (c *Client) handleWorkspace(cs *connState, sessionID, requestID string) {
	var listing WorkspaceListing
	var err error
	if c.hooks.Workspace == nil {
		err = errors.New("not-implemented")
	} else {
		listing, err = c.hooks.Workspace(context.Background(), sessionID)
	}
	if err != nil {
		c.rejectWorkspace(cs, sessionID, requestID, "listing", err)
		return
	}
	if listing.Changes == nil {
		listing.Changes = []WorkspaceChange{}
	}
	_ = c.writeMsg(cs, SessionWorkspace{
		V:         ProtocolVersion,
		Type:      TypeSessionWorkspace,
		SessionID: sessionID,
		RequestID: requestID,
		Git:       listing.Git,
		Commit:    listing.Commit,
		Origin:    listing.Origin,
		Paths:     listing.Paths,
		Truncated: listing.Truncated,
		Changes:   listing.Changes,
	})
}

// handleWorkspaceFile answers on the bulk lane: an image can be megabytes.
func (c *Client) handleWorkspaceFile(cs *connState, sessionID, requestID, rel, at string) {
	var file WorkspaceFileContent
	var err error
	if c.hooks.File == nil {
		err = errors.New("not-implemented")
	} else {
		file, err = c.hooks.File(context.Background(), sessionID, rel, at)
	}
	if err != nil {
		c.rejectWorkspace(cs, sessionID, requestID, "file", err)
		return
	}
	msg := SessionWorkspaceFile{
		V:         ProtocolVersion,
		Type:      TypeSessionWorkspaceFile,
		SessionID: sessionID,
		RequestID: requestID,
		Path:      rel,
		Kind:      file.Kind,
		Size:      file.Size,
		Text:      file.Text,
		Truncated: file.Truncated,
		MediaType: file.MediaType,
	}
	if len(file.Data) > 0 {
		msg.Data = base64.StdEncoding.EncodeToString(file.Data)
	}
	_ = c.writeMsg(cs, msg)
}

// handleWorkspaceDiff answers on the bulk lane, like a file read.
func (c *Client) handleWorkspaceDiff(cs *connState, sessionID, requestID, rel string) {
	var diff WorkspaceDiffContent
	var err error
	if c.hooks.Diff == nil {
		err = errors.New("not-implemented")
	} else {
		diff, err = c.hooks.Diff(context.Background(), sessionID, rel)
	}
	if err != nil {
		c.rejectWorkspace(cs, sessionID, requestID, "diff", err)
		return
	}
	_ = c.writeMsg(cs, SessionWorkspaceDiff{
		V:         ProtocolVersion,
		Type:      TypeSessionWorkspaceDiff,
		SessionID: sessionID,
		RequestID: requestID,
		Path:      rel,
		Status:    diff.Status,
		Kind:      diff.Kind,
		Diff:      diff.Diff,
		Truncated: diff.Truncated,
		Size:      diff.Size,
		OldSize:   diff.OldSize,
		NewSize:   diff.NewSize,
	})
}

func (c *Client) handleProjects(cs *connState, raw json.RawMessage) {
	var head struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return
	}
	ctx := context.Background()
	var cands []string
	var listing []DeviceProject
	var err error
	var label string
	switch head.Type {
	case TypeDeviceProjectsList:
		label = "projects list"
		if c.hooks.ProjectsList == nil {
			err = errors.New("not-implemented")
		} else {
			cands, listing, err = c.hooks.ProjectsList(ctx)
		}
	case TypeDeviceProjectsAdd:
		label = "projects add"
		if c.hooks.ProjectsAdd == nil {
			err = errors.New("not-implemented")
		} else {
			cands, listing, err = c.hooks.ProjectsAdd(ctx, head.Path)
		}
	case TypeDeviceProjectsRemove:
		label = "projects remove"
		if c.hooks.ProjectsRemove == nil {
			err = errors.New("not-implemented")
		} else {
			cands, listing, err = c.hooks.ProjectsRemove(ctx, head.Path)
		}
	default:
		return
	}
	c.replyDeviceControl(cs, head.RequestID, label, cands, listing, err)
}

func (c *Client) handleDirs(cs *connState, requestID, path string) {
	var listing DirListing
	var err error
	if c.hooks.DirsList == nil {
		err = errors.New("not-implemented")
	} else {
		listing, err = c.hooks.DirsList(context.Background(), path)
	}
	if err != nil {
		log.Printf("[agent] dirs list rejected: %v", err)
		_ = c.writeMsg(cs, DeviceDirsRejected{
			V: ProtocolVersion, Type: TypeDeviceDirsRejected, RequestID: requestID, Reason: err.Error(),
		})
		return
	}
	if listing.Entries == nil {
		listing.Entries = []DirEntry{}
	}
	_ = c.writeMsg(cs, DeviceDirs{
		V:         ProtocolVersion,
		Type:      TypeDeviceDirs,
		RequestID: requestID,
		Path:      listing.Path,
		Parent:    listing.Parent,
		Root:      listing.Root,
		Entries:   listing.Entries,
		Truncated: listing.Truncated,
	})
}

// handleAttachment takes a chunk of attachment.put and answers on the control lane once the image is stored,
// or when a chunk is refused. Chunks are handled in the order they arrive, in the read loop.
func (c *Client) handleAttachment(cs *connState, raw json.RawMessage) {
	var req AttachmentPut
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	done, err := false, errors.New("not-implemented")
	if c.hooks.AttachmentPut != nil {
		done, err = c.hooks.AttachmentPut(req)
	}
	if err != nil {
		log.Printf("[agent] attachment refused: %v", err)
		_ = c.writeMsg(cs, AttachmentRejected{
			V: ProtocolVersion, Type: TypeAttachmentRejected, RequestID: req.RequestID, Reason: err.Error(),
		})
		return
	}
	if !done {
		return
	}
	_ = c.writeMsg(cs, AttachmentStored{
		V: ProtocolVersion, Type: TypeAttachmentStored, RequestID: req.RequestID, AttachmentID: req.AttachmentID,
	})
}

// handleClone answers device.clone.find; a failure to look is answered as no clone.
func (c *Client) handleClone(cs *connState, requestID, origin string) {
	var path *string
	if c.hooks.CloneFind != nil {
		clone, err := c.hooks.CloneFind(context.Background(), origin)
		if err != nil {
			log.Printf("[agent] clone find: %v", err)
		} else if clone != "" {
			path = &clone
		}
	}
	_ = c.writeMsg(cs, DeviceClone{V: ProtocolVersion, Type: TypeDeviceClone, RequestID: requestID, Path: path})
}

func (c *Client) handleSessions(cs *connState, raw json.RawMessage) {
	var head struct {
		Type       string   `json:"type"`
		RequestID  string   `json:"requestId"`
		Path       string   `json:"path"`
		SessionIDs []string `json:"sessionIds"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return
	}
	ctx := context.Background()
	var cands []string
	var listing []DeviceProject
	var err error
	var label string
	switch head.Type {
	case TypeDeviceSessionsImport:
		label = "sessions import"
		if c.hooks.SessionsImport == nil {
			err = errors.New("not-implemented")
		} else {
			cands, listing, err = c.hooks.SessionsImport(ctx, head.Path, head.SessionIDs)
		}
	case TypeDeviceSessionsRemove:
		label = "sessions remove"
		if c.hooks.SessionsRemove == nil {
			err = errors.New("not-implemented")
		} else {
			cands, listing, err = c.hooks.SessionsRemove(ctx, head.Path, head.SessionIDs)
		}
	default:
		return
	}
	c.replyDeviceControl(cs, head.RequestID, label, cands, listing, err)
}

func (c *Client) replyDeviceControl(
	cs *connState,
	requestID, label string,
	cands []string,
	listing []DeviceProject,
	err error,
) {
	if err != nil {
		log.Printf("[agent] %s rejected: %v", label, err)
		_ = c.writeMsg(cs, DeviceProjectsRejected{
			V:         ProtocolVersion,
			Type:      TypeDeviceProjectsRejected,
			RequestID: requestID,
			Reason:    err.Error(),
		})
		return
	}
	if cands == nil {
		cands = []string{}
	}
	if listing == nil {
		listing = []DeviceProject{}
	}
	_ = c.writeMsg(cs, DeviceProjects{
		V:          ProtocolVersion,
		Type:       TypeDeviceProjects,
		RequestID:  requestID,
		Candidates: cands,
		Projects:   listing,
	})
}
