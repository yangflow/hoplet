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
		err = c.hooks.Runtime.Prompt(sessionID, req.Text, req.Attachments)
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

func (c *Client) handleRuntimeSteer(cs *connState, raw json.RawMessage) {
	var req RuntimeSteer
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	var err error
	if c.hooks.Runtime.Steer == nil {
		err = errors.New("unsupported")
	} else {
		err = c.hooks.Runtime.Steer(req.SessionID, req.Text, req.Attachments)
	}
	if err != nil {
		log.Printf("[agent] steer refused for %s: %v", shortID(req.SessionID), err)
		_ = c.writeMsg(cs, RuntimeRejected{
			V: ProtocolVersion, Type: TypeRuntimeRejected, SessionID: req.SessionID, PromptID: req.SteerID, Reason: err.Error(),
		})
		return
	}
	_ = c.writeMsg(cs, RuntimeSteered{
		V: ProtocolVersion, Type: TypeRuntimeSteered, SessionID: req.SessionID, SteerID: req.SteerID,
	})
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
	started, err := c.hooks.Runtime.Start(context.Background(), req)
	if err != nil {
		reject(err.Error())
		return
	}
	sessionID := started.SessionID
	if err := c.writeMsg(cs, RuntimeStarted{
		V: ProtocolVersion, Type: TypeRuntimeStarted, RequestID: req.RequestID, SessionID: sessionID,
		Worktree: started.Worktree,
	}); err != nil {
		return
	}
	log.Printf("[agent] started session %s", shortID(sessionID))
	if c.hooks.Runtime.Started != nil {
		c.hooks.Runtime.Started(sessionID)
	}
}

func (c *Client) handleRuntimeAnswer(sessionID, requestID, decision, message, mode string) {
	if c.hooks.Runtime.Answer == nil {
		return
	}
	if err := c.hooks.Runtime.Answer(sessionID, requestID, decision == "allow", message, mode); err != nil {
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

func (c *Client) handleRuntimeConfig(raw json.RawMessage) {
	var req RuntimeConfig
	if err := json.Unmarshal(raw, &req); err != nil || c.hooks.Runtime.Config == nil {
		return
	}
	if err := c.hooks.Runtime.Config(req.SessionID, req.ConfigID, req.Value); err != nil {
		log.Printf("[agent] config %s for %s not applied: %v", req.ConfigID, shortID(req.SessionID), err)
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
			if err == nil {
				// An import is the user asking for the session back, also after a deletion.
				c.mu.Lock()
				for _, id := range head.SessionIDs {
					delete(c.suppressed, id)
				}
				c.mu.Unlock()
			}
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

// handleSessionDeletion answers the messages of a session deletion (ADR 0023). Deleting and forgetting stop
// syncing the session first, so no round writes a delta of a file that is about to go.
func (c *Client) handleSessionDeletion(cs *connState, kind string, raw json.RawMessage) {
	var req struct {
		RequestID     string `json:"requestId"`
		SessionID     string `json:"sessionId"`
		VendorFile    bool   `json:"vendorFile"`
		Worktree      bool   `json:"worktree"`
		WorktreeDirty bool   `json:"worktreeDirty"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return
	}
	ctx := context.Background()
	reject := func(reason string) {
		log.Printf("[agent] session %s %s rejected: %s", shortID(req.SessionID), kind, reason)
		_ = c.writeMsg(cs, DeviceSessionRejected{
			V: ProtocolVersion, Type: TypeDeviceSessionRejected, RequestID: req.RequestID, Reason: reason,
		})
	}
	switch kind {
	case TypeDeviceSessionInspect:
		if c.hooks.SessionInspect == nil {
			reject("not-implemented")
			return
		}
		found, err := c.hooks.SessionInspect(ctx, req.SessionID)
		if err != nil {
			reject(err.Error())
			return
		}
		_ = c.writeMsg(cs, DeviceSessionInspected{
			V: ProtocolVersion, Type: TypeDeviceSessionInspected, RequestID: req.RequestID,
			VendorFile: found.VendorFile, Worktree: found.Worktree,
		})
	case TypeDeviceSessionDelete:
		if c.hooks.SessionDelete == nil {
			reject("not-implemented")
			return
		}
		c.suppress(req.SessionID)
		steps := c.hooks.SessionDelete(ctx, req.SessionID, req.VendorFile, req.Worktree, req.WorktreeDirty)
		if steps == nil {
			steps = []DeletionStep{}
		}
		_ = c.writeMsg(cs, DeviceSessionDeleted{
			V: ProtocolVersion, Type: TypeDeviceSessionDeleted, RequestID: req.RequestID, Steps: steps,
		})
	case TypeDeviceSessionForget:
		if c.hooks.SessionForget == nil {
			reject("not-implemented")
			return
		}
		c.suppress(req.SessionID)
		if err := c.hooks.SessionForget(ctx, req.SessionID); err != nil {
			reject(err.Error())
			return
		}
		_ = c.writeMsg(cs, DeviceSessionForgotten{
			V: ProtocolVersion, Type: TypeDeviceSessionForgotten, RequestID: req.RequestID, SessionID: req.SessionID,
		})
	}
}

// suppress stops syncing a session until the process restarts or the session is accepted again.
func (c *Client) suppress(sessionID string) {
	c.mu.Lock()
	c.suppressed[sessionID] = true
	c.mu.Unlock()
}

// handleCloneCheck answers device.clone.check; a machine that cannot clone says no-origin.
func (c *Client) handleCloneCheck(cs *connState, requestID, origin, cloneInto string) {
	res := CloneChecked{Reason: "no-origin", CloneInto: cloneInto}
	if c.hooks.CloneCheck != nil {
		res = c.hooks.CloneCheck(context.Background(), origin, cloneInto)
	}
	_ = c.writeMsg(cs, DeviceCloneChecked{
		V: ProtocolVersion, Type: TypeDeviceCloneChecked, RequestID: requestID,
		Reason: res.Reason, Detail: res.Detail, CloneInto: res.CloneInto,
	})
}

// beginClone makes the context of the clone requestID: it ends on a cancel and when the connection closes,
// because nobody is waiting for the clone any more then.
func (c *Client) beginClone(cs *connState, requestID string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	c.cloneMu.Lock()
	c.clones[requestID] = cancel
	c.cloneMu.Unlock()
	go func() {
		select {
		case <-cs.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}

// cancelClone ends the clone of requestID; the clone's own goroutine answers with device.clone.failed.
func (c *Client) cancelClone(requestID string) {
	c.cloneMu.Lock()
	cancel := c.clones[requestID]
	c.cloneMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// handleCloneStart runs one clone and ends it with device.clone.done or device.clone.failed.
func (c *Client) handleCloneStart(ctx context.Context, cs *connState, requestID, handoffID, origin, cloneInto string) {
	defer func() {
		c.cloneMu.Lock()
		cancel := c.clones[requestID]
		delete(c.clones, requestID)
		c.cloneMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}()
	fail := func(reason, detail string) {
		_ = c.writeMsg(cs, DeviceCloneFailed{
			V: ProtocolVersion, Type: TypeDeviceCloneFailed, RequestID: requestID, Reason: reason, Detail: detail,
		})
	}
	if c.hooks.CloneStart == nil {
		fail("git-failed", "not-implemented")
		return
	}
	path, err := c.hooks.CloneStart(ctx, handoffID, origin, cloneInto, func(received int64, total *int64) {
		_ = c.writeMsg(cs, DeviceCloneProgress{
			V: ProtocolVersion, Type: TypeDeviceCloneProgress, RequestID: requestID, Received: received, Total: total,
		})
	})
	if err != nil {
		var ce *CloneError
		if errors.As(err, &ce) {
			fail(ce.Reason, ce.Detail)
		} else {
			fail("git-failed", "")
		}
		return
	}
	_ = c.writeMsg(cs, DeviceCloneDone{V: ProtocolVersion, Type: TypeDeviceCloneDone, RequestID: requestID, Path: path})
}

// providerList is the configurations to announce, never nil-valued when there is a hook.
func (c *Client) providerList() []ProviderInfo {
	if c.hooks.Providers == nil {
		return nil
	}
	return c.hooks.Providers()
}

// parseProvidersUpdate decodes a device.providers.update. Only the name, the enabled flag and the defaults
// change from the core; the command and the environment are set on the machine, so any other key is refused.
func parseProvidersUpdate(raw []byte) (DeviceProvidersUpdate, error) {
	var req DeviceProvidersUpdate
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for k := range fields {
		switch k {
		case "v", "type", "requestId", "id", "name", "enabled", "defaults":
		default:
			return req, errors.New("only name, enabled and defaults can be changed")
		}
	}
	return req, nil
}

// handleRules applies a device.rules and answers device.rules.applied. The message is handed to the hook
// without its envelope; the hook refuses any field it does not know.
func (c *Client) handleRules(cs *connState, raw []byte) {
	var head struct {
		Revision int `json:"revision"`
	}
	_ = json.Unmarshal(raw, &head)
	rev, err := 0, error(nil)
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err == nil {
		delete(fields, "v")
		delete(fields, "type")
		var body []byte
		if body, err = json.Marshal(fields); err == nil {
			if c.hooks.Rules == nil {
				err = errors.New("not-implemented")
			} else {
				rev, err = c.hooks.Rules(body)
			}
		}
	}
	out := DeviceRulesApplied{V: ProtocolVersion, Type: TypeDeviceRulesApplied, Revision: rev}
	if err != nil {
		log.Printf("[agent] permission rules refused: %v", err)
		out.Revision = head.Revision
		out.Error = err.Error()
	}
	_ = c.writeMsg(cs, out)
}

func (c *Client) handleProvidersUpdate(cs *connState, raw []byte) {
	req, err := parseProvidersUpdate(raw)
	var list []ProviderInfo
	if err == nil {
		if c.hooks.ProvidersUpdate == nil {
			err = errors.New("not-implemented")
		} else {
			list, err = c.hooks.ProvidersUpdate(req)
		}
	}
	if err != nil {
		log.Printf("[agent] provider update rejected: %v", err)
		_ = c.writeMsg(cs, DeviceProvidersRejected{
			V: ProtocolVersion, Type: TypeDeviceProvidersRejected, RequestID: req.RequestID, Reason: err.Error(),
		})
		return
	}
	if list == nil {
		list = []ProviderInfo{}
	}
	_ = c.writeMsg(cs, DeviceProviders{V: ProtocolVersion, Type: TypeDeviceProviders, RequestID: req.RequestID, Providers: list})
}
