package daemon

import (
	"context"
	"slices"

	"github.com/yangflow/hoplet/proto"
)

// catalog streams the session list: first the sessions changed after the
// client's version (all of them, as a snapshot, for version 0 or a version
// the daemon has never reached), then every later change.
func (c *conn) catalog(ctx context.Context, sub uint64, p proto.CatalogParams) error {
	st := c.s.store
	changes := st.Changes()
	cur, err := st.CatalogVersion(ctx)
	if err != nil {
		return err
	}
	var item proto.CatalogItem
	if p.AfterVersion == 0 || p.AfterVersion > cur {
		item.Snapshot = true
		item.Sessions, err = st.Sessions(ctx)
	} else {
		item.Sessions, err = st.SessionsSince(ctx, p.AfterVersion)
		item.Version = p.AfterVersion
	}
	if err != nil {
		return err
	}
	if item.Sessions == nil {
		item.Sessions = []proto.Session{}
	}
	item.Version = maxVersion(item.Sessions, item.Version)
	if err := c.item(sub, item); err != nil {
		return err
	}
	v := item.Version
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changes:
		}
		changes = st.Changes()
		changed, err := st.SessionsSince(ctx, v)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			continue
		}
		v = maxVersion(changed, v)
		if err := c.item(sub, proto.CatalogItem{Sessions: changed, Version: v}); err != nil {
			return err
		}
	}
}

func maxVersion(ss []proto.Session, v uint64) uint64 {
	for _, s := range ss {
		v = max(v, s.Version)
	}
	return v
}

// session streams one session: first what the client missed after its
// cursor, replayed when the gap is small and as a snapshot otherwise, with
// the session's queue; then new events and queue changes as they commit.
func (c *conn) session(ctx context.Context, sub uint64, p proto.SessionParams) error {
	st := c.s.store
	changes := st.Changes()
	item, cursor, err := c.catchUp(ctx, p)
	if err != nil {
		return err
	}
	queue, err := st.Queue(ctx, p.Session)
	if err != nil {
		return err
	}
	item.Queue = &proto.Queue{Messages: nonNil(queue)}
	if err := c.item(sub, item); err != nil {
		return err
	}
	// more is set while a full page suggests further committed events.
	more := len(item.Events) == c.s.lim.gapEvents
	for {
		if !more {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changes:
			}
		}
		changes = st.Changes()
		evs, err := st.Events(ctx, p.Session, cursor, c.s.lim.page)
		if err != nil {
			return err
		}
		q, err := st.Queue(ctx, p.Session)
		if err != nil {
			return err
		}
		more = len(evs) == c.s.lim.page
		var it proto.SessionItem
		if len(evs) > 0 {
			it.Events = evs
			cursor = evs[len(evs)-1].Seq
		}
		if !slices.EqualFunc(q, queue, sameQueued) {
			it.Queue = &proto.Queue{Messages: nonNil(q)}
			queue = q
		}
		if it.Events == nil && it.Queue == nil {
			continue
		}
		if err := c.item(sub, it); err != nil {
			return err
		}
	}
}

// catchUp returns the first item of a session subscription and the cursor
// after it.
func (c *conn) catchUp(ctx context.Context, p proto.SessionParams) (proto.SessionItem, uint64, error) {
	st, lim := c.s.store, c.s.lim
	sess, err := st.Session(ctx, p.Session)
	if err != nil {
		return proto.SessionItem{}, 0, err
	}
	// A cursor past the log's end is from another store (a restored
	// backup, say); the client's view cannot be trusted, so it is replaced.
	if p.AfterSeq <= sess.LastSeq {
		n, size, err := st.GapSize(ctx, p.Session, p.AfterSeq)
		if err != nil {
			return proto.SessionItem{}, 0, err
		}
		if n <= int64(lim.gapEvents) && size <= lim.gapBytes {
			evs, err := st.Events(ctx, p.Session, p.AfterSeq, lim.gapEvents)
			if err != nil {
				return proto.SessionItem{}, 0, err
			}
			cursor := p.AfterSeq
			if len(evs) > 0 {
				cursor = evs[len(evs)-1].Seq
			}
			return proto.SessionItem{Events: evs}, cursor, nil
		}
	}
	snap, err := st.Snapshot(ctx, p.Session, lim.snapshotTurns)
	if err != nil {
		return proto.SessionItem{}, 0, err
	}
	out := &proto.SessionSnapshot{Session: snap.Session, Events: nonNil(snap.Events)}
	for _, pa := range snap.Pending {
		out.Pending = append(out.Pending, pa.Request)
	}
	if len(snap.Events) > 0 {
		older, err := st.EventsBefore(ctx, p.Session, snap.Events[0].Seq, 1)
		if err != nil {
			return proto.SessionItem{}, 0, err
		}
		out.More = len(older) > 0
	}
	return proto.SessionItem{Snapshot: out}, snap.Session.LastSeq, nil
}

func sameQueued(a, b proto.QueuedMessage) bool {
	return a.Key == b.Key && a.Text == b.Text && a.Position == b.Position && a.Error == b.Error && a.CreatedAt.Equal(b.CreatedAt)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
