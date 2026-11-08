package daemon

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/yangflow/hoplet/internal/artifact"
	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/noise"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/internal/tools"
	"github.com/yangflow/hoplet/internal/workspace"
	"github.com/yangflow/hoplet/proto"
)

// call runs a command and returns its result.
func (c *conn) call(ctx context.Context, f proto.ClientFrame) (any, error) {
	s := c.s
	done := struct{}{}
	if c.remote && localOnly(f.Method) {
		return nil, &proto.APIError{Code: proto.CodeUnauthorized, Message: f.Method + " is only for this host's own clients"}
	}
	switch f.Method {
	case proto.MethodHello:
		return nil, &proto.APIError{Code: proto.CodeBadRequest, Message: "hello was already said"}
	case proto.MethodSessionsList:
		ss, err := s.store.Sessions(ctx)
		return nonNil(ss), err
	case proto.MethodSessionsCreate:
		var p proto.CreateParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		if p.Mode == proto.ModeCode && p.Directory == "" {
			return nil, invalid("a code session needs a directory")
		}
		if p.Directory != "" && !filepath.IsAbs(p.Directory) {
			return nil, invalid("the directory must be an absolute path")
		}
		return s.mgr.Create(ctx, session.CreateRequest{
			Mode: p.Mode, Harness: p.Harness, Directory: p.Directory, Isolation: p.Isolation,
			Permission: p.Permission, Plan: p.Plan, Fast: p.Fast, Ultracode: p.Ultracode, Effort: p.Effort, Title: p.Title, Origin: proto.OriginMe,
			Model: p.Model, ModelProvider: p.ModelProvider,
		})
	case proto.MethodSessionsSend:
		var p proto.SendParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.queue.send(ctx, p)
	case proto.MethodSessionsRespond:
		var p proto.RespondParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		if p.Key == "" {
			return nil, invalid("respond needs an idempotency key")
		}
		return done, s.mgr.Respond(ctx, p.Session, p.Approval, p.Option, c.device, p.Key)
	case proto.MethodSessionsInterrupt, proto.MethodSessionsStop, proto.MethodSessionsArchive:
		var p proto.SessionRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		op := map[string]func(context.Context, proto.SessionID) error{
			proto.MethodSessionsInterrupt: s.mgr.Interrupt,
			proto.MethodSessionsStop:      s.queue.stop,
			proto.MethodSessionsArchive:   s.mgr.Archive,
		}[f.Method]
		return done, op(ctx, p.Session)
	case proto.MethodSessionsSteer:
		var p proto.SteerParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.Steer(ctx, p.Session, p.Text)
	case proto.MethodSessionsAnswer:
		var p proto.AnswerParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.Answer(ctx, p.Session, p.Question, p.Answer)
	case proto.MethodSessionsSetPerm:
		var p proto.SetPermissionParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetPermission(ctx, p.Session, p.Permission)
	case proto.MethodSessionsSetPlan:
		var p proto.SetPlanParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetPlan(ctx, p.Session, p.Plan)
	case proto.MethodSessionsSetEffort:
		var p proto.SetEffortParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetEffort(ctx, p.Session, p.Effort)
	case proto.MethodSessionsSetFast:
		var p proto.SetFastParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetFast(ctx, p.Session, p.Fast)
	case proto.MethodSessionsSetUltra:
		var p proto.SetUltracodeParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetUltracode(ctx, p.Session, p.Ultracode)
	case proto.MethodSessionsSetModel:
		var p proto.SetModelParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.mgr.SetModel(ctx, p.Session, p.Model, p.Provider)
	case proto.MethodSessionsOutput:
		var p proto.OutputParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.mgr.Output(ctx, p.Session, p.ToolCall)
	case proto.MethodHarnessesList:
		return s.mgr.Harnesses(ctx), nil
	case proto.MethodSessionsUpdate:
		var p proto.UpdateParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.store.UpdateSession(ctx, p.Session, store.SessionUpdate{Title: p.Title, Pinned: p.Pinned, Unread: p.Unread})
	case proto.MethodSessionsEvents:
		var p proto.EventsParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return c.eventsBefore(ctx, p)
	case proto.MethodSessionsGet:
		var p proto.SessionRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return sessionState(ctx, s.store, p.Session)
	case proto.MethodCompanionsList:
		var p proto.CompanionsListParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		cs, err := s.mgr.Companions(ctx, p.All)
		for i := range cs {
			cs[i].Host = string(s.identity.ID())
		}
		return proto.CompanionsList{Companions: cs}, err
	case proto.MethodCompanionsGet:
		var p proto.CompanionRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		comp, err := s.mgr.Companion(ctx, string(p.ID))
		comp.Host = string(s.identity.ID())
		return comp, err
	case proto.MethodCompanionsCreate:
		var p proto.CompanionCreateParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		sess, comp, err := s.mgr.CreateCompanion(ctx, session.CreateRequest{
			Harness: p.Harness, Permission: p.Permission, Plan: p.Plan, Fast: p.Fast, Ultracode: p.Ultracode, Effort: p.Effort,
			Title: p.Title, Origin: proto.OriginMe, Model: p.Model, ModelProvider: p.ModelProvider,
		})
		comp.Host = string(s.identity.ID())
		return proto.CompanionCreated{Companion: comp, Session: sess}, err
	case proto.MethodCompanionsPersona:
		var p proto.CompanionRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.mgr.Persona(ctx, string(p.ID))
	case proto.MethodCompanionsSetPers:
		var p proto.Persona
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		applies, err := s.mgr.SetPersona(ctx, string(p.ID), p.Text)
		return proto.SetPersonaResult{Applies: applies}, err
	case proto.MethodToolsList, proto.MethodToolsCall:
		caller, err := c.toolCaller(ctx)
		if err != nil {
			return nil, err
		}
		if f.Method == proto.MethodToolsList {
			return proto.ToolsListResult{Tools: s.tools.List(caller)}, nil
		}
		var p proto.ToolsCallParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.tools.Call(ctx, caller, p.Name, p.Arguments)
	case proto.MethodSessionsDiff:
		var p proto.DiffParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		dir, err := s.mgr.WorkDir(ctx, p.Session)
		if err != nil {
			return nil, err
		}
		// A diff only reads, so unlike the commands above it follows the
		// connection: a client that goes away stops waiting for it.
		return s.ws.Diff(c.ctx, p.Session, dir, p)
	case proto.MethodSessionsCommits:
		var p proto.CommitsParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		dir, err := s.mgr.WorkDir(ctx, p.Session)
		if err != nil {
			return nil, err
		}
		return s.ws.Commits(c.ctx, p.Session, dir, p)
	case proto.MethodFilesList:
		var p proto.FilesListParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		dir, err := s.mgr.WorkDir(ctx, p.Session)
		if err != nil {
			return nil, err
		}
		// Reading files follows the connection too.
		return s.ws.List(c.ctx, dir, p)
	case proto.MethodFilesRead:
		var p proto.FilesReadParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		dir, err := s.mgr.WorkDir(ctx, p.Session)
		if err != nil {
			return nil, err
		}
		return s.ws.Read(c.ctx, p.Session, dir, p)
	case proto.MethodArtifactsPublish:
		var p proto.ArtifactsPublishParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.arts.Publish(ctx, artifact.Request{Session: p.Session, Name: p.Name, Entry: p.Entry, Files: p.Files, By: proto.ArtifactByCLI})
	case proto.MethodArtifactsList:
		var p proto.ArtifactsListParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		if _, err := s.store.Session(ctx, p.Session); err != nil {
			return nil, err
		}
		list, err := s.store.Artifacts(ctx, p.Session)
		return proto.ArtifactsList{Artifacts: nonNil(list)}, err
	case proto.MethodArtifactsVersions:
		var p proto.ArtifactsVersionsParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		list, err := s.store.ArtifactHistory(ctx, p.Artifact)
		return proto.ArtifactsVersions{Versions: list}, err
	case proto.MethodArtifactsRead:
		var p proto.ArtifactsReadParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.arts.Read(ctx, p)
	case proto.MethodEnvRefresh:
		return s.sys.refresh(ctx)
	case proto.MethodSystemDoctor:
		return s.sys.doctor(ctx), nil
	case proto.MethodQueueList:
		var p proto.SessionRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		q, err := s.store.Queue(ctx, p.Session)
		return proto.Queue{Messages: nonNil(q)}, err
	case proto.MethodQueueEdit:
		var p proto.QueueEditParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.queue.edit(ctx, p)
	case proto.MethodQueueDelete:
		var p proto.QueueKeyParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.queue.remove(ctx, p)
	case proto.MethodQueueMove:
		var p proto.QueueMoveParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.queue.move(ctx, p)
	case proto.MethodPairingStart:
		return s.startPairing()
	case proto.MethodDevicesList:
		ds, err := s.store.Devices(ctx, "")
		out := make([]proto.Device, len(ds))
		for i, d := range ds {
			out[i] = proto.Device{ID: d.ID, Name: d.Name, PairedAt: d.PairedAt, RevokedAt: d.RevokedAt}
		}
		return out, err
	case proto.MethodDevicesRevoke:
		var p proto.DeviceRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.revoke(ctx, p.ID)
	case proto.MethodHostsAdd:
		var p proto.Host
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		if id, err := noise.IDOf(p.Key); err != nil || string(id) != p.ID {
			return nil, invalid("the host ID does not match its key")
		}
		if p.Name == "" || len(p.Routes) == 0 {
			return nil, invalid("a host needs a name and routes")
		}
		return done, s.store.AddHost(ctx, store.Host{ID: p.ID, PublicKey: p.Key, Name: p.Name, Routes: p.Routes, AddedAt: s.now()})
	case proto.MethodHostsList:
		hs, err := s.store.Hosts(ctx)
		out := make([]proto.Host, len(hs))
		for i, h := range hs {
			out[i] = proto.Host{ID: h.ID, Key: h.PublicKey, Name: h.Name, Routes: h.Routes, AddedAt: h.AddedAt}
		}
		return out, err
	case proto.MethodHostsRemove:
		var p proto.HostRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return done, s.store.RemoveHost(ctx, p.Host)
	case proto.MethodProfileList:
		var p proto.ProfileListParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.profileList(ctx, p)
	case proto.MethodProfileGet:
		var p proto.ProfileRef
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.profileGet(ctx, p.ID)
	case proto.MethodProfileUpdate:
		var p proto.ProfileUpdateParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.profileUpdate(ctx, p)
	case proto.MethodProfileWithdraw:
		var p proto.ProfileWithdrawParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		return s.profileWithdraw(ctx, p)
	case proto.MethodProfileCandidates:
		return s.profileCandidates(ctx)
	case proto.MethodProfileAccept:
		var p proto.ProfileAcceptParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		e, err := s.memory.Accept(ctx, p.ID, p.OpID, profileScope(p.Scope))
		return profileEntry(e), err
	case proto.MethodProfileReject:
		var p proto.ProfileRejectParams
		if err := decode(f, &p); err != nil {
			return nil, err
		}
		e, err := s.memory.Reject(ctx, p.ID, p.OpID)
		return profileEntry(e), err
	default:
		return nil, &proto.APIError{Code: proto.CodeBadRequest, Message: "unknown method " + f.Method}
	}
}

// localOnly reports whether a device connected over Noise may not call
// method: pairing hands out access to this host, its hosts are its own,
// publishing reads files by their paths on this machine, and the profile
// stays with this machine's own clients for now.
func localOnly(method string) bool {
	switch method {
	case proto.MethodProfileList, proto.MethodProfileGet, proto.MethodProfileUpdate, proto.MethodProfileWithdraw,
		proto.MethodProfileCandidates, proto.MethodProfileAccept, proto.MethodProfileReject:
		return true
	case proto.MethodPairingStart, proto.MethodHostsAdd, proto.MethodHostsList, proto.MethodHostsRemove, proto.MethodArtifactsPublish:
		return true
	}
	return false
}

// eventsBefore returns a page of the session's events before p.BeforeSeq,
// oldest first. One event more than the page is read to tell whether older
// ones remain.
func (c *conn) eventsBefore(ctx context.Context, p proto.EventsParams) (proto.EventsPage, error) {
	limit := p.Limit
	if limit <= 0 || limit > c.s.lim.page {
		limit = c.s.lim.page
	}
	evs, err := c.s.store.EventsBefore(ctx, p.Session, p.Agent, p.BeforeSeq, limit+1)
	if err != nil {
		return proto.EventsPage{}, err
	}
	page := proto.EventsPage{Events: evs}
	if len(evs) > limit {
		page.Events, page.More = evs[1:], true // the oldest is the extra one
	}
	page.Events = nonNil(page.Events)
	return page, nil
}

func invalid(msg string) error { return &proto.APIError{Code: proto.CodeInvalid, Message: msg} }

// apiError classifies err for the client.
func apiError(err error) *proto.APIError {
	var ae *proto.APIError
	if errors.As(err, &ae) {
		return ae
	}
	code := proto.CodeInternal
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, workspace.ErrNoBaseline), errors.Is(err, fs.ErrNotExist),
		errors.Is(err, artifact.ErrNotFound):
		code = proto.CodeNotFound
	case errors.Is(err, workspace.ErrInvalidPath), errors.Is(err, artifact.ErrInvalid):
		code = proto.CodeInvalid
	case errors.Is(err, store.ErrInvalid), errors.Is(err, session.ErrInvalidOption), errors.Is(err, session.ErrUnknownHarness):
		code = proto.CodeInvalid
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrExists), errors.Is(err, session.ErrArchived):
		code = proto.CodeConflict
	case errors.Is(err, session.ErrBusy):
		code = proto.CodeBusy
	case errors.Is(err, session.ErrNotRunning):
		code = proto.CodeNotRunning
	case errors.Is(err, session.ErrUnsupportedVersion), errors.Is(err, harness.ErrUnsupported), errors.Is(err, workspace.ErrNoGit),
		errors.Is(err, workspace.ErrUnsupported):
		code = proto.CodeUnsupported
	}
	return &proto.APIError{Code: code, Message: err.Error()}
}

// sessionState reads a session with its pending approvals and queue. The
// three reads are not one transaction: a concurrent approval or dequeue can
// give a mixed view (for example status waiting with nothing pending). That
// is acceptable for a status view, which the next call corrects, and
// nothing acts on it.
func sessionState(ctx context.Context, st *store.Store, id proto.SessionID) (proto.SessionState, error) {
	sess, err := st.Session(ctx, id)
	if err != nil {
		return proto.SessionState{}, err
	}
	pending, err := st.PendingApprovals(ctx, id)
	if err != nil {
		return proto.SessionState{}, err
	}
	q, err := st.Queue(ctx, id)
	if err != nil {
		return proto.SessionState{}, err
	}
	out := proto.SessionState{Session: sess, Pending: []proto.ApprovalRequested{}, Queue: nonNil(q)}
	for _, p := range pending {
		out.Pending = append(out.Pending, p.Request)
	}
	return out, nil
}

// toolCaller is the session a tool connection acts for, read afresh so
// that a session archived since hello no longer calls tools.
func (c *conn) toolCaller(ctx context.Context) (proto.Session, error) {
	if c.caller == "" {
		return proto.Session{}, &proto.APIError{Code: proto.CodeUnauthorized, Message: "tools are only for a session's tool connection"}
	}
	sess, err := c.s.store.Session(ctx, c.caller)
	if err != nil {
		return proto.Session{}, err
	}
	if sess.ArchivedAt != nil {
		return proto.Session{}, &proto.APIError{Code: proto.CodeUnauthorized, Message: "the session is archived"}
	}
	return sess, nil
}

// toolSource gives the tools the same services the client API uses. An
// error a client would get as anything but an internal error is the
// model's to read, so it comes back as a tools.Failure.
type toolSource struct {
	mem   *memory.Service
	store *store.Store
	mgr   *session.Manager
	queue *queuer
	arts  *artifact.Service
	sim   tools.Simulator
}

// forModel turns an error the model can act on into a tools.Failure.
func forModel(err error) error {
	if err == nil {
		return nil
	}
	if ae := apiError(err); ae.Code != proto.CodeInternal {
		return &tools.Failure{Message: ae.Message}
	}
	return err
}

func (t toolSource) Create(ctx context.Context, c tools.Create) (proto.Session, error) {
	sess, err := t.mgr.Create(ctx, session.CreateRequest{
		Mode: c.Mode, Harness: c.Harness, Directory: c.Directory, Permission: c.Permission, Effort: c.Effort,
		Title: c.Title, Origin: proto.OriginMe, Model: c.Model, ModelProvider: c.ModelProvider, Parent: c.Parent, Owner: c.Owner,
	})
	if err != nil {
		return proto.Session{}, forModel(err)
	}
	if _, err := t.queue.send(ctx, proto.SendParams{Session: sess.ID, Text: c.Prompt, Key: "create:" + string(sess.ID)}); err != nil {
		return sess, forModel(err)
	}
	return t.store.Session(ctx, sess.ID)
}

func (t toolSource) Send(ctx context.Context, p proto.SendParams) (proto.SendReply, error) {
	r, err := t.queue.send(ctx, p)
	return r, forModel(err)
}

func (t toolSource) Report(ctx context.Context, parent, from proto.SessionID, key, text string) error {
	_, _, err := t.store.Report(ctx, parent, from, proto.ReportDone, key, text)
	return forModel(err)
}

func (t toolSource) WorkDir(ctx context.Context, id proto.SessionID) (string, error) {
	dir, err := t.mgr.WorkDir(ctx, id)
	return dir, forModel(err)
}

func (t toolSource) Publish(ctx context.Context, r artifact.Request) (proto.Artifact, error) {
	a, err := t.arts.Publish(ctx, r)
	return a, forModel(err)
}

func (t toolSource) Simulator() tools.Simulator { return t.sim }

func (t toolSource) Profile() tools.ProfileSource { return profileSource{mem: t.mem, mgr: t.mgr} }

// profileSource gives the profile tools the memory service. Get keeps
// store.ErrNotFound, which the tool answers in its own words.
type profileSource struct {
	mem *memory.Service
	mgr *session.Manager
}

func (p profileSource) Scopes(ctx context.Context, sess proto.Session) ([]memory.Scope, error) {
	return p.mgr.ProfileScopes(ctx, sess)
}

func (p profileSource) List(ctx context.Context, scopes []memory.Scope, q store.MemoryQuery) ([]memory.Entry, error) {
	entries, err := p.mem.InScope(ctx, scopes, q)
	return entries, forModel(err)
}

func (p profileSource) Get(ctx context.Context, scopes []memory.Scope, id string) (memory.Entry, error) {
	return p.mem.GetInScope(ctx, scopes, id)
}

func (p profileSource) Apply(ctx context.Context, w memory.Write) (memory.Result, error) {
	r, err := p.mem.Apply(ctx, w)
	return r, forModel(err)
}

func (t toolSource) Sessions(ctx context.Context) ([]proto.Session, error) {
	return t.store.Sessions(ctx)
}

func (t toolSource) State(ctx context.Context, id proto.SessionID) (proto.SessionState, error) {
	return sessionState(ctx, t.store, id)
}
