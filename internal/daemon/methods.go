package daemon

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// call runs a command and returns its result.
func (c *conn) call(ctx context.Context, f proto.ClientFrame) (any, error) {
	s := c.s
	done := struct{}{}
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
			Permission: p.Permission, Title: p.Title, Origin: proto.OriginMe,
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
			proto.MethodSessionsStop:      s.mgr.Stop,
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
	default:
		return nil, &proto.APIError{Code: proto.CodeBadRequest, Message: "unknown method " + f.Method}
	}
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
	case errors.Is(err, store.ErrNotFound):
		code = proto.CodeNotFound
	case errors.Is(err, store.ErrInvalid), errors.Is(err, session.ErrInvalidOption), errors.Is(err, session.ErrUnknownHarness):
		code = proto.CodeInvalid
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrExists), errors.Is(err, session.ErrArchived):
		code = proto.CodeConflict
	case errors.Is(err, session.ErrBusy):
		code = proto.CodeBusy
	case errors.Is(err, session.ErrNotRunning):
		code = proto.CodeNotRunning
	case errors.Is(err, session.ErrUnsupportedVersion), errors.Is(err, harness.ErrUnsupported):
		code = proto.CodeUnsupported
	}
	return &proto.APIError{Code: code, Message: err.Error()}
}
