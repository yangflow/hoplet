package session

import (
	"context"
	"errors"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// maxBatch bounds how many events one append commits.
const maxBatch = 256

// group is consecutive events that belong to the same turn.
type group struct {
	turn    uint32
	entries []store.Entry
	offset  int64 // highest spool offset in the group; 0 if none
}

// ingest commits the run's events until it ends. Raw native lines stay in
// the session's spool, which the shim keeps until the session is archived.
func (m *Manager) ingest(ctx context.Context, l *live, last uint32, open bool) {
	turn, inTurn := last, open
	committed, _ := m.cfg.Store.IngestOffset(ctx, l.id)
	var detached bool
	defer func() {
		m.mu.Lock()
		if m.live[l.id] == l {
			delete(m.live, l.id)
		}
		closing := m.closing
		m.mu.Unlock()
		close(l.done)
		m.wg.Done()
		if detached && !closing {
			go m.reattach(ctx, l.id)
		}
	}()

	flush := func(g *group) {
		if len(g.entries) == 0 && g.offset <= committed {
			return
		}
		req := store.AppendRequest{Session: l.id, Turn: g.turn, Entries: g.entries}
		if g.offset > committed {
			off := g.offset
			req.IngestOffset = &off
		}
		if err := m.appendTolerant(ctx, req); err != nil {
			m.log.Error("ingest append failed", "session", l.id, "err", err)
			return
		}
		if req.IngestOffset != nil {
			committed = *req.IngestOffset
		}
		m.notify(l.id)
	}

	events := l.run.Events()
	for {
		ev, ok := <-events
		if !ok {
			detached = errors.Is(l.run.Err(), harness.ErrDetached)
			return
		}
		g := &group{}
		for n := 0; ; n++ {
			t := uint32(0)
			switch ev.Payload.(type) {
			case proto.TurnStarted:
				turn++
				inTurn = true
				t = turn
			case proto.TurnEnded:
				t = turn
				inTurn = false
			default:
				if inTurn {
					t = turn
				}
			}
			if len(g.entries) > 0 && t != g.turn {
				flush(g)
				g = &group{}
			}
			if len(g.entries) == 0 {
				g.turn = t
			}
			if ev.Native != "" {
				native := ev.Native
				if _, err := m.cfg.Store.UpdateSession(ctx, l.id, store.SessionUpdate{NativeID: &native}); err != nil {
					m.log.Error("record native session", "session", l.id, "err", err)
				}
			}
			if ev.Payload != nil {
				g.entries = append(g.entries, store.Entry{Payload: ev.Payload})
			}
			if ev.Offset > g.offset {
				g.offset = ev.Offset
			}
			if ev.Exit != nil {
				flush(g)
				if err := m.abortOpenTurn(ctx, l.id, "the harness exited"); err != nil {
					m.log.Error("abort open turn", "session", l.id, "err", err)
				}
				if ev.Offset > 0 {
					if err := l.stdio.Ack(ev.Offset); err != nil {
						m.log.Error("ack exit record", "session", l.id, "err", err)
					}
				}
				g = &group{}
				break
			}
			if n+1 >= maxBatch {
				break
			}
			select {
			case next, more := <-events:
				if !more {
					flush(g)
					detached = errors.Is(l.run.Err(), harness.ErrDetached)
					return
				}
				ev = next
				continue
			default:
			}
			break
		}
		flush(g)
	}
}

// appendTolerant appends req; if the store rejects it as invalid, it
// retries entry by entry and replaces each invalid entry with a proto.Error,
// so one bad harness line never stalls ingestion.
func (m *Manager) appendTolerant(ctx context.Context, req store.AppendRequest) error {
	_, err := m.cfg.Store.Append(ctx, req)
	if !errors.Is(err, store.ErrInvalid) || len(req.Entries) == 0 {
		return err
	}
	for i, e := range req.Entries {
		one := store.AppendRequest{Session: req.Session, Turn: req.Turn, Entries: []store.Entry{e}}
		if i == len(req.Entries)-1 {
			one.IngestOffset = req.IngestOffset
		}
		if _, err := m.cfg.Store.Append(ctx, one); errors.Is(err, store.ErrInvalid) {
			one.Entries = []store.Entry{{Payload: proto.Error{Message: "invalid harness event: " + err.Error()}}}
			one.Turn = 0
			if _, err := m.cfg.Store.Append(ctx, one); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

// reattach reconnects to a session whose connection ended without an exit.
func (m *Manager) reattach(ctx context.Context, id proto.SessionID) {
	sess, err := m.cfg.Store.Session(ctx, id)
	if err != nil {
		return
	}
	if _, err := m.attach(ctx, sess, false); err != nil {
		if err := m.abortOpenTurn(ctx, id, "lost the connection to the harness"); err != nil {
			m.log.Error("abort open turn", "session", id, "err", err)
		}
	}
}
