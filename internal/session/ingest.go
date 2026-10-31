package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	// maxBatch bounds how many events one append commits.
	maxBatch = 256
	// batchWindow is how long ingestion keeps collecting events after the
	// first one of a batch before committing them together.
	batchWindow = 50 * time.Millisecond
)

// group is consecutive events that belong to the same turn.
type group struct {
	turn    uint32
	entries []store.Entry
	offset  int64 // highest spool offset in the group; 0 if none
}

// ingester turns a run's events into store appends.
type ingester struct {
	m         *Manager
	l         *live
	turn      uint32 // the highest turn number
	inTurn    bool
	committed int64 // the ingest offset in the store
	g         group
	flushNow  bool // the batch should be committed before waiting for more
}

// ingest commits the run's events until it ends. Raw native lines stay in
// the session's spool, which the shim keeps until the session is archived.
//
// Any failure to append is fatal for the run: nothing is acknowledged, the
// run is closed and the session reattaches from the committed offset, so
// every event is stored exactly once.
func (m *Manager) ingest(l *live, last uint32, open bool) {
	var detached, failed bool
	var failNote string
	var failDelay time.Duration
	defer func() {
		m.mu.Lock()
		if m.live[l.id] == l {
			delete(m.live, l.id)
		}
		m.mu.Unlock()
		if err := l.run.Close(); err != nil {
			m.log.Warn("close run", "session", l.id, "err", err)
		}
		close(l.done)
		switch {
		case failed:
			m.startReattach(l.id, failDelay, failNote)
		case detached:
			m.startReattach(l.id, 0, "")
		}
		m.wg.Done()
	}()
	fail := func(err error) {
		m.log.Error("ingest failed; reattaching from the committed offset", "session", l.id, "err", err)
		failed = true
		prior := m.failStreak(l.id)
		failDelay = ingestBackoff(prior)
		// A note not yet recorded travels on; a new one is made only for the
		// first failure of a streak.
		failNote = l.note
		if failNote == "" && prior == 0 {
			failNote = "ingest failed: " + err.Error() + "; resumed from the committed offset"
		}
	}

	committed, err := m.cfg.Store.IngestOffset(m.ctx, l.id)
	if err != nil {
		fail(err)
		return
	}
	l.committed.Store(committed)
	in := &ingester{m: m, l: l, turn: last, inTurn: open, committed: committed}
	events := l.run.Events()
	for {
		ev, ok := <-events
		if !ok {
			detached = in.ended(fail)
			return
		}
		timer := time.NewTimer(batchWindow)
		for n := 1; ; n++ {
			wasInTurn := in.inTurn
			if err := in.event(ev); err != nil {
				timer.Stop()
				fail(err)
				return
			}
			if ev.Exit != nil {
				timer.Stop()
				if err := in.exit(ev, wasInTurn); err != nil {
					fail(err)
				}
				return
			}
			if in.flushNow {
				if err := in.flush(); err != nil {
					timer.Stop()
					fail(err)
					return
				}
			}
			if n >= maxBatch {
				break
			}
			var more bool
			select {
			case ev, more = <-events:
				if !more {
					timer.Stop()
					detached = in.ended(fail)
					return
				}
				continue
			case <-timer.C:
			}
			break
		}
		timer.Stop()
		if err := in.flush(); err != nil {
			fail(err)
			return
		}
	}
}

// ended handles the end of the event stream: it commits what is collected
// and reports whether the transport was lost (so the session reattaches).
func (in *ingester) ended(fail func(error)) (detached bool) {
	if err := in.flush(); err != nil {
		fail(err)
		return false
	}
	err := in.l.run.Err()
	if err != nil && !errors.Is(err, harness.ErrDetached) {
		in.m.log.Error("harness adapter failed", "session", in.l.id, "err", err)
	}
	if detached = errors.Is(err, harness.ErrDetached) && !in.m.isClosing(); detached || in.m.isClosing() || !in.inTurn {
		return detached
	}
	// The stream ended without an exit and without the chance of a reattach:
	// nothing will end the open turn.
	in.inTurn = false
	if err := in.add(in.turn, &store.Entry{Payload: proto.TurnEnded{Reason: proto.TurnAborted, Message: "the harness event stream ended"}}, 0); err != nil {
		fail(err)
		return false
	}
	if err := in.flush(); err != nil {
		fail(err)
	}
	return false
}

// event adds one event to the current batch, tracking turns.
func (in *ingester) event(ev harness.Event) error {
	l := in.l
	if ev.StdinError {
		l.pending.Store(false)
	}
	if ev.Native != "" && ev.Native != l.native {
		native := ev.Native
		if _, err := in.m.cfg.Store.UpdateSession(in.m.ctx, l.id, store.SessionUpdate{NativeID: &native}); err != nil {
			in.m.log.Error("record native session", "session", l.id, "err", err)
		} else {
			l.native = native
		}
	}
	var turn uint32
	payload := ev.Payload
	switch payload.(type) {
	case proto.TurnStarted:
		if in.inTurn {
			err := in.add(in.turn, &store.Entry{Payload: proto.TurnEnded{
				Reason: proto.TurnFailed, Message: "the harness started a new turn without ending the previous one",
			}}, 0)
			if err != nil {
				return err
			}
		}
		in.turn++
		in.inTurn = true
		turn = in.turn
	case proto.TurnEnded:
		if !in.inTurn {
			payload = nil // nothing to end; the line's offset still counts
			break
		}
		turn = in.turn
		in.inTurn = false
		in.flushNow = true
	default:
		switch payload.(type) {
		case proto.ApprovalRequested, proto.Question:
			in.flushNow = true // someone is waiting to see it
		}
		if in.inTurn {
			turn = in.turn
		}
	}
	var entry *store.Entry
	if payload != nil {
		entry = &store.Entry{Payload: payload}
	}
	return in.add(turn, entry, ev.Offset)
}

// add puts an entry (nil for an offset only) of the given turn into the
// batch, committing the batch first when the turn changes.
func (in *ingester) add(turn uint32, e *store.Entry, off int64) error {
	if e != nil {
		if len(in.g.entries) > 0 && turn != in.g.turn {
			if err := in.flush(); err != nil {
				return err
			}
		}
		if len(in.g.entries) == 0 {
			in.g.turn = turn
		}
		in.g.entries = append(in.g.entries, *e)
	}
	if off > in.g.offset {
		in.g.offset = off
	}
	return nil
}

// flush commits the batch: its entries and the highest spool offset in one
// append. A pending note from an earlier failure rides along once.
func (in *ingester) flush() error {
	in.flushNow = false
	g, l := &in.g, in.l
	if len(g.entries) == 0 && g.offset <= in.committed {
		*g = group{}
		return nil
	}
	entries := g.entries
	if l.note != "" {
		entries = append([]store.Entry{{Payload: proto.Error{Message: l.note}}}, entries...)
	}
	req := store.AppendRequest{Session: l.id, Turn: g.turn, Entries: prepareEntries(g.turn, entries)}
	if g.offset > in.committed {
		off := g.offset
		req.IngestOffset = &off
	}
	if _, err := in.m.appendFn(in.m.ctx, req); err != nil {
		return err
	}
	if req.IngestOffset != nil {
		in.committed = *req.IngestOffset
		l.committed.Store(in.committed)
	}
	l.note = ""
	in.m.resetStreak(l.id)
	for _, e := range g.entries {
		if _, ok := e.Payload.(proto.TurnStarted); ok {
			l.pending.Store(false) // the message's turn is recorded as started
		}
	}
	*g = group{}
	return nil
}

// exit handles the harness's exit: it commits what is collected, closes the
// turn if one is still open, and only then acknowledges the exit record so
// the shim may leave.
func (in *ingester) exit(ev harness.Event, wasInTurn bool) error {
	l := in.l
	if !wasInTurn && !l.stopping.Load() && (ev.Exit.Code != 0 || ev.Exit.Signal != "") {
		msg := fmt.Sprintf("the harness exited between turns: code %d", ev.Exit.Code)
		if ev.Exit.Signal != "" {
			msg = "the harness exited between turns: killed by SIG" + strings.TrimPrefix(ev.Exit.Signal, "SIG")
		}
		// In the group being collected, if any, so it commits with the exit's
		// offset in one append.
		turn := uint32(0)
		if len(in.g.entries) > 0 {
			turn = in.g.turn
		}
		if err := in.add(turn, &store.Entry{Payload: proto.Error{Message: msg}}, 0); err != nil {
			return err
		}
	}
	if in.inTurn { // the adapter normally ends the turn itself
		in.inTurn = false
		if err := in.add(in.turn, &store.Entry{Payload: proto.TurnEnded{Reason: proto.TurnAborted, Message: "the harness exited"}}, 0); err != nil {
			return err
		}
	}
	if err := in.flush(); err != nil {
		return err
	}
	l.pending.Store(false)
	// Nobody may be handed a run whose harness has exited.
	in.m.mu.Lock()
	if in.m.live[l.id] == l {
		delete(in.m.live, l.id)
	}
	in.m.mu.Unlock()
	if l.acker != nil && ev.Offset > 0 {
		if err := l.acker.Ack(ev.Offset); err != nil {
			in.m.log.Error("ack exit record", "session", l.id, "err", err)
		}
	}
	l.exited.Store(true)
	return nil
}

// prepareEntries replaces entries the store would reject with a proto.Error
// event, so one bad harness line never stalls ingestion.
func prepareEntries(turn uint32, entries []store.Entry) []store.Entry {
	out := make([]store.Entry, len(entries))
	for i, e := range entries {
		if err := store.ValidateEntry(turn, e); err != nil {
			e = store.Entry{Payload: proto.Error{Message: "invalid harness event: " + err.Error()}}
		}
		out[i] = e
	}
	return out
}
