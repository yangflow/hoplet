package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// noReply is the report of a handed-off turn that ended with no message.
const noReply = "The turn ended without a reply."

// handoffs reports the progress of handed-off sessions, Code sessions the
// user owns that a Personal session started, to the session that started
// them: each turn's end and each approval waiting on the user. Reports go
// under keys derived from what they report, so looking again, here or in
// the next daemon, never queues one twice. While no daemon runs, turns may
// end unreported; the next daemon reports only the latest.
type handoffs struct {
	store *store.Store
	log   *slog.Logger
	seen  map[proto.SessionID]uint64 // the last sequence number looked at
}

// run looks at the handed-off sessions after every store change until ctx
// ends.
func (h *handoffs) run(ctx context.Context) {
	for {
		changes := h.store.Changes()
		h.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-changes:
		}
	}
}

// check reports what changed in each handed-off session since it was last
// looked at.
func (h *handoffs) check(ctx context.Context) {
	ss, err := h.store.Sessions(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.log.Error("list sessions for handoff reports", "err", err)
		}
		return
	}
	for _, s := range ss {
		if s.Owner != proto.OwnerMe || s.Parent == "" || s.ArchivedAt != nil {
			continue
		}
		if seq, ok := h.seen[s.ID]; ok && seq == s.LastSeq {
			continue
		}
		if err := h.report(ctx, s); err != nil {
			if ctx.Err() == nil {
				h.log.Error("report handoff progress", "session", s.ID, "err", err)
			}
			continue
		}
		h.seen[s.ID] = s.LastSeq
	}
}

// report queues the reports of one handed-off session's last turn.
func (h *handoffs) report(ctx context.Context, s proto.Session) error {
	snap, err := h.store.Snapshot(ctx, s.ID, 1)
	if err != nil {
		return err
	}
	for _, p := range snap.Pending {
		text := "Waiting for the user to approve: " + p.Request.Title
		if p.Request.Detail != "" {
			text += "\n\n" + p.Request.Detail
		}
		if err := h.send(ctx, s, "approval:"+p.Request.ID, text); err != nil {
			return err
		}
	}
	var (
		ended bool
		turn  uint32
		reply string
	)
	for _, ev := range snap.Events {
		switch ev.Type {
		case proto.TypeTurnEnded:
			ended, turn = true, ev.Turn
		case proto.TypeMessage:
			p, err := ev.Decode()
			if err != nil {
				return err
			}
			if m := p.(proto.Message); m.Role == proto.RoleAssistant && strings.TrimSpace(m.Text) != "" {
				reply = m.Text
			}
		}
	}
	if !ended {
		return nil
	}
	if reply == "" {
		reply = noReply
	}
	return h.send(ctx, s, fmt.Sprintf("turn:%d", turn), reply)
}

// send reports text from s to its parent; a parent that is archived or
// gone gets nothing.
func (h *handoffs) send(ctx context.Context, s proto.Session, what, text string) error {
	_, _, err := h.store.Report(ctx, s.Parent, s.ID, "handoff:"+string(s.ID)+":"+what, text)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
		h.log.Debug("handoff report skipped", "session", s.ID, "parent", s.Parent, "err", err)
		return nil
	}
	return err
}
