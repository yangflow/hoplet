package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Memory is the part of the memory service a session start needs.
type Memory interface {
	Digest(ctx context.Context, scopes []memory.Scope) (memory.Digest, error)
}

// ProfileScopes is what a session's memory applies to: everything, this
// host, the repository it works in and, for a Personal session, the
// companion it belongs to.
func (m *Manager) ProfileScopes(ctx context.Context, sess proto.Session) ([]memory.Scope, error) {
	scopes := []memory.Scope{{Kind: memory.ScopeGlobal}}
	if m.cfg.Host != "" {
		scopes = append(scopes, memory.Scope{Kind: memory.ScopeHost, Value: m.cfg.Host})
	}
	if sess.Directory != "" && m.cfg.RepoIdentity != nil {
		if id, ok := m.cfg.RepoIdentity(ctx, sess.Directory); ok {
			scopes = append(scopes, memory.Scope{Kind: memory.ScopeRepo, Value: id})
		}
	}
	if sess.Mode != proto.ModePersonal {
		return scopes, nil
	}
	root := sess
	for root.Parent != "" {
		parent, err := m.cfg.Store.Session(ctx, root.Parent)
		if err != nil {
			return nil, fmt.Errorf("session: profile scopes: %w", err)
		}
		root = parent
	}
	c, err := m.cfg.Store.CompanionByMain(ctx, root.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return scopes, nil
	case err != nil:
		return nil, fmt.Errorf("session: profile scopes: %w", err)
	}
	return append(scopes, memory.Scope{Kind: memory.ScopeCompanion, Value: string(c.ID)}), nil
}

// takesCodeDigest reports whether h has an entry for a Code session's digest.
func takesCodeDigest(h harness.Harness) bool {
	d, ok := h.(harness.CodeDigester)
	return ok && d.TakesCodeDigest()
}

// addDigest puts the session's memory digest on spec and returns what to
// record once the harness has started, or nil for nothing to record. A
// failure leaves the session without a digest; it never stops the start. A
// resumed session keeps the context it started with, so it records nothing.
func (m *Manager) addDigest(ctx context.Context, h harness.Harness, sess proto.Session, spec *harness.Spec) *store.MemoryInjection {
	if m.cfg.Memory == nil || spec.Ephemeral {
		return nil
	}
	if sess.Mode == proto.ModePersonal && !spec.Companion {
		return nil // a delegated child uses the read tools
	}
	record := func(in store.MemoryInjection) *store.MemoryInjection {
		if spec.Resume != "" {
			return nil
		}
		in.Session = sess.ID
		return &in
	}
	if sess.Mode != proto.ModePersonal && !takesCodeDigest(h) {
		return record(store.MemoryInjection{Status: store.MemoryInjectionUnsupported, Detail: "no entry for a Code session's digest"})
	}
	failed := func(err error) *store.MemoryInjection {
		m.log.WarnContext(ctx, "memory digest failed", "session", sess.ID, "err", err)
		return record(store.MemoryInjection{Status: store.MemoryInjectionFailed, Detail: err.Error()})
	}
	scopes, err := m.ProfileScopes(ctx, sess)
	if err != nil {
		return failed(err)
	}
	d, err := m.cfg.Memory.Digest(ctx, scopes)
	if err != nil {
		return failed(err)
	}
	if d.Text == "" {
		return nil
	}
	spec.Profile = d.Text
	entries := make([]store.MemoryInjectionEntry, 0, len(d.Entries)+len(d.Indexed))
	for _, e := range d.Entries {
		entries = append(entries, store.MemoryInjectionEntry{ID: e.ID, Version: e.Version})
	}
	for _, e := range d.Indexed {
		entries = append(entries, store.MemoryInjectionEntry{ID: e.ID, Version: e.Version, Indexed: true})
	}
	return record(store.MemoryInjection{Status: store.MemoryInjectionDelivered, Entries: entries})
}

// recordInjection saves in, if any. A failure only logs: the session is
// already running.
func (m *Manager) recordInjection(ctx context.Context, in *store.MemoryInjection) {
	if in == nil {
		return
	}
	if err := m.cfg.Store.AddMemoryInjection(ctx, *in); err != nil {
		m.log.LogAttrs(ctx, slog.LevelWarn, "record memory injection", slog.String("session", string(in.Session)), slog.Any("err", err))
	}
}
