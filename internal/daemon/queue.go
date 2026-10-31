package daemon

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"

	"github.com/yangflow/hoplet/internal/session"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// queuer sends messages, queueing those that arrive while a turn runs, and
// sends queued messages in order as turns end. Every queue change for a
// session happens under that session's lock, so a message being sent from
// the queue cannot be edited, moved or deleted under the sender.
//
// An edited message is sent under a new key (see store.QueueHead). A client
// that retries its original send after the edited message has left the
// queue therefore sends the message again; clients retry within seconds of
// a lost reply, long before a user edits a queued message.
type queuer struct {
	store *store.Store
	mgr   *session.Manager
	log   *slog.Logger

	mu      sync.Mutex
	locks   map[proto.SessionID]*sync.Mutex
	waiting map[proto.SessionID]bool // sessions with a drain waiting for the lock
}

func (q *queuer) sessionLock(id proto.SessionID) *sync.Mutex {
	q.mu.Lock()
	defer q.mu.Unlock()
	l, ok := q.locks[id]
	if !ok {
		l = &sync.Mutex{}
		q.locks[id] = l
	}
	return l
}

func (q *queuer) lock(id proto.SessionID) func() {
	l := q.sessionLock(id)
	l.Lock()
	return l.Unlock
}

// send sends a message now when the session is idle and nothing is queued
// ahead of it, and queues it otherwise.
func (q *queuer) send(ctx context.Context, p proto.SendParams) (proto.SendReply, error) {
	if p.Key == "" {
		return proto.SendReply{}, invalid("send needs an idempotency key")
	}
	if p.Text == "" {
		return proto.SendReply{}, invalid("send needs text")
	}
	defer q.lock(p.Session)()
	msgs, err := q.store.Queue(ctx, p.Session)
	if err != nil {
		return proto.SendReply{}, err
	}
	if slices.ContainsFunc(msgs, func(m proto.QueuedMessage) bool { return m.Key == p.Key }) {
		return proto.SendReply{Queued: true}, nil // a retry of a queued message
	}
	if len(msgs) == 0 {
		res, err := q.mgr.Send(ctx, p.Session, p.Text, p.Key)
		if err == nil {
			return proto.SendReply{Seq: res.Seq, Turn: res.Turn}, nil
		}
		if !errors.Is(err, session.ErrBusy) {
			return proto.SendReply{}, err
		}
	}
	if _, _, err := q.store.Enqueue(ctx, p.Session, p.Key, p.Text); err != nil {
		return proto.SendReply{}, err
	}
	return proto.SendReply{Queued: true}, nil
}

func (q *queuer) edit(ctx context.Context, p proto.QueueEditParams) error {
	defer q.lock(p.Session)()
	return q.store.EditQueued(ctx, p.Session, p.Key, p.Text)
}

func (q *queuer) remove(ctx context.Context, p proto.QueueKeyParams) error {
	defer q.lock(p.Session)()
	return q.store.DeleteQueued(ctx, p.Session, p.Key)
}

func (q *queuer) move(ctx context.Context, p proto.QueueMoveParams) error {
	defer q.lock(p.Session)()
	return q.store.MoveQueued(ctx, p.Session, p.Key, p.Position)
}

// run sends queued messages until ctx ends, looking at the queues after
// every store change: a turn ending is a change, and so is any queue edit.
func (q *queuer) run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		changes := q.store.Changes()
		ids, err := q.store.QueuedSessions(ctx)
		if err != nil && ctx.Err() == nil {
			q.log.Error("list queued sessions", "err", err)
		}
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				q.drain(ctx, id)
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
	}
}

// drain sends the head of the session's queue if the session takes it. A
// message whose send failed stays at the head with its error, holding the
// queue, until the user edits, moves or deletes it. A session another drain
// is working on is skipped, since that drain ends in a store write that
// starts another pass; a session a command holds is waited for, because the
// command's writes may already have been seen by a pass that skipped it.
func (q *queuer) drain(ctx context.Context, id proto.SessionID) {
	l := q.sessionLock(id)
	if !l.TryLock() {
		q.mu.Lock()
		if q.waiting[id] {
			q.mu.Unlock()
			return
		}
		q.waiting[id] = true
		q.mu.Unlock()
		l.Lock()
		q.mu.Lock()
		delete(q.waiting, id)
		q.mu.Unlock()
	}
	defer l.Unlock()
	head, sendKey, ok, err := q.store.QueueHead(ctx, id)
	if err != nil || !ok || head.Error != "" {
		return
	}
	// The key makes this safe to repeat: if the daemon dies after the send
	// and before the delete, the next daemon's send finds the receipt. An
	// edited message goes out under a new key, as a new message.
	_, err = q.mgr.Send(ctx, id, head.Text, sendKey)
	switch {
	case ctx.Err() != nil:
	case errors.Is(err, session.ErrBusy):
	case err != nil:
		if ferr := q.store.FailQueued(ctx, id, head.Key, err.Error()); ferr != nil {
			q.log.Error("record queued send failure", "session", id, "err", ferr)
		}
	default:
		if err := q.store.DeleteQueued(ctx, id, head.Key); err != nil {
			q.log.Error("dequeue sent message", "session", id, "err", err)
		}
	}
}
