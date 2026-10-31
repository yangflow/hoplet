package client

import (
	"context"

	"github.com/yangflow/hoplet/proto"
)

// Sessions lists every session, most recently updated first.
func (c *Client) Sessions(ctx context.Context) ([]proto.Session, error) {
	var out []proto.Session
	err := c.Call(ctx, proto.MethodSessionsList, struct{}{}, &out)
	return out, err
}

// Create creates a session.
func (c *Client) Create(ctx context.Context, p proto.CreateParams) (proto.Session, error) {
	var out proto.Session
	err := c.Call(ctx, proto.MethodSessionsCreate, p, &out)
	return out, err
}

// Send sends a message, or queues it behind the running turn.
func (c *Client) Send(ctx context.Context, p proto.SendParams) (proto.SendReply, error) {
	var out proto.SendReply
	err := c.Call(ctx, proto.MethodSessionsSend, p, &out)
	return out, err
}

// Respond answers an approval; the first answer wins and later ones get a
// conflict error.
func (c *Client) Respond(ctx context.Context, p proto.RespondParams) error {
	return c.Call(ctx, proto.MethodSessionsRespond, p, nil)
}

// Interrupt ends the running turn.
func (c *Client) Interrupt(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsInterrupt, proto.SessionRef{Session: id}, nil)
}

// Steer adds text to the running turn.
func (c *Client) Steer(ctx context.Context, p proto.SteerParams) error {
	return c.Call(ctx, proto.MethodSessionsSteer, p, nil)
}

// Answer answers a question.
func (c *Client) Answer(ctx context.Context, p proto.AnswerParams) error {
	return c.Call(ctx, proto.MethodSessionsAnswer, p, nil)
}

// SetPermission changes a session's permission mode.
func (c *Client) SetPermission(ctx context.Context, p proto.SetPermissionParams) error {
	return c.Call(ctx, proto.MethodSessionsSetPerm, p, nil)
}

// SetPlan turns a session's plan mode on or off.
func (c *Client) SetPlan(ctx context.Context, p proto.SetPlanParams) error {
	return c.Call(ctx, proto.MethodSessionsSetPlan, p, nil)
}

// SetFast turns a session's fast mode on or off.
func (c *Client) SetFast(ctx context.Context, p proto.SetFastParams) error {
	return c.Call(ctx, proto.MethodSessionsSetFast, p, nil)
}

// SetEffort changes how hard a session's harness thinks.
func (c *Client) SetEffort(ctx context.Context, p proto.SetEffortParams) error {
	return c.Call(ctx, proto.MethodSessionsSetEffort, p, nil)
}

// Stop stops a session's harness.
func (c *Client) Stop(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsStop, proto.SessionRef{Session: id}, nil)
}

// Archive archives a session.
func (c *Client) Archive(ctx context.Context, id proto.SessionID) error {
	return c.Call(ctx, proto.MethodSessionsArchive, proto.SessionRef{Session: id}, nil)
}

// EventsBefore returns a page of a session's events older than
// p.BeforeSeq, oldest first; More says older events remain.
func (c *Client) EventsBefore(ctx context.Context, p proto.EventsParams) (proto.EventsPage, error) {
	var out proto.EventsPage
	err := c.Call(ctx, proto.MethodSessionsEvents, p, &out)
	return out, err
}

// Queue returns a session's queued messages, next first.
func (c *Client) Queue(ctx context.Context, id proto.SessionID) ([]proto.QueuedMessage, error) {
	var out proto.Queue
	err := c.Call(ctx, proto.MethodQueueList, proto.SessionRef{Session: id}, &out)
	return out.Messages, err
}

// EditQueued changes a queued message's text.
func (c *Client) EditQueued(ctx context.Context, p proto.QueueEditParams) error {
	return c.Call(ctx, proto.MethodQueueEdit, p, nil)
}

// DeleteQueued removes a queued message.
func (c *Client) DeleteQueued(ctx context.Context, p proto.QueueKeyParams) error {
	return c.Call(ctx, proto.MethodQueueDelete, p, nil)
}

// MoveQueued moves a queued message; position 0 is next.
func (c *Client) MoveQueued(ctx context.Context, p proto.QueueMoveParams) error {
	return c.Call(ctx, proto.MethodQueueMove, p, nil)
}

// SubscribeCatalog subscribes to the session list; items are
// proto.CatalogItem.
func (c *Client) SubscribeCatalog(ctx context.Context, afterVersion uint64) (*Subscription, error) {
	return c.Subscribe(ctx, proto.MethodSubscribeCatalog, proto.CatalogParams{AfterVersion: afterVersion})
}

// SubscribeSession subscribes to a session's events; items are
// proto.SessionItem.
func (c *Client) SubscribeSession(ctx context.Context, p proto.SessionParams) (*Subscription, error) {
	return c.Subscribe(ctx, proto.MethodSubscribeSession, p)
}

// Session returns a session's record, pending approvals and queue.
func (c *Client) Session(ctx context.Context, id proto.SessionID) (proto.SessionState, error) {
	var out proto.SessionState
	err := c.Call(ctx, proto.MethodSessionsGet, proto.SessionRef{Session: id}, &out)
	return out, err
}

// RefreshEnv makes the daemon read the login shell's environment again for
// harnesses started from now on.
func (c *Client) RefreshEnv(ctx context.Context) (proto.EnvInfo, error) {
	var out proto.EnvInfo
	err := c.Call(ctx, proto.MethodEnvRefresh, struct{}{}, &out)
	return out, err
}

// Doctor returns the daemon's health checks.
func (c *Client) Doctor(ctx context.Context) (proto.DoctorReport, error) {
	var out proto.DoctorReport
	err := c.Call(ctx, proto.MethodSystemDoctor, struct{}{}, &out)
	return out, err
}
