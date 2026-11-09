package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/yangflow/hoplet/internal/memory"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	profileListDefault = 50
	profileListMax     = 200
)

// ProfileSource is the memory the profile tools work on.
type ProfileSource interface {
	// Scopes is what memory applies to the session; the session decides,
	// never the caller's arguments.
	Scopes(ctx context.Context, sess proto.Session) ([]memory.Scope, error)
	List(ctx context.Context, scopes []memory.Scope, q store.MemoryQuery) ([]memory.Entry, error)
	// Get returns store.ErrNotFound for anything not active in scopes.
	Get(ctx context.Context, scopes []memory.Scope, id string) (memory.Entry, error)
	Apply(ctx context.Context, w memory.Write) (memory.Result, error)
	// Usable reports whether sessions of mode use memory now.
	Usable(ctx context.Context, mode proto.Mode) bool
	// Read records that the session read e.
	Read(ctx context.Context, session proto.SessionID, e memory.Entry) error
}

type profileItem struct {
	ID       string       `json:"id"`
	Version  int          `json:"version"`
	Category string       `json:"category"`
	Scope    profileScope `json:"scope"`
	Title    string       `json:"title"`
}

type profileScope struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type profileEntry struct {
	ID       string       `json:"id"`
	Version  int          `json:"version"`
	Category string       `json:"category"`
	Scope    profileScope `json:"scope"`
	Body     string       `json:"body"`
}

type profileEvidence struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

type profileWriteArgs struct {
	Action          string            `json:"action"`
	ID              string            `json:"id,omitempty"`
	ExpectedVersion int               `json:"expected_version,omitempty"`
	Category        string            `json:"category,omitempty"`
	Scope           string            `json:"scope,omitempty"`
	Body            string            `json:"body,omitempty"`
	Evidence        []profileEvidence `json:"evidence,omitempty"`
	Reason          string            `json:"reason,omitempty"`
}

type profileWriteResult struct {
	Outcome string `json:"outcome"`
	ID      string `json:"id,omitempty"`
	Version int    `json:"version,omitempty"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func noEntry(id string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return failf("no entry %s", id)
	}
	return err
}

func scopeOf(kind string, scopes []memory.Scope) (memory.Scope, bool) {
	i := slices.IndexFunc(scopes, func(s memory.Scope) bool { return string(s.Kind) == kind })
	if i < 0 {
		return memory.Scope{}, false
	}
	return scopes[i], true
}

// profileOpID makes retrying one call one write.
func profileOpID(session proto.SessionID, p profileWriteArgs) (string, error) {
	args, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("tools: encode profile write: %w", err)
	}
	sum := sha256.Sum256(append([]byte(string(session)+"\x00"), args...))
	return "tool_" + hex.EncodeToString(sum[:16]), nil
}

func profileTools(src Source) []tool {
	usable := func(caller proto.Session) bool {
		return src.Profile().Usable(context.Background(), caller.Mode)
	}
	const scopeKinds = `"enum":["global","companion","repo","host"]`
	return []tool{
		{
			info: proto.ToolInfo{
				Name:        "profile_list",
				Description: "List the active entries of the user's Hoplet profile that apply to this session: ID, version, category, scope and title.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"category":{"type":"string","enum":["fact","preference","convention","experience"]},"scope":{"type":"string",` + scopeKinds + `},"text":{"type":"string","description":"Only entries containing this text"},"limit":{"type":"integer","description":"At most this many, 50 by default and 200 at most"}},"additionalProperties":false}`),
			},
			visible: usable,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Category string `json:"category"`
					Scope    string `json:"scope"`
					Text     string `json:"text"`
					Limit    int    `json:"limit"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				scopes, err := src.Profile().Scopes(ctx, caller)
				if err != nil {
					return nil, err
				}
				if p.Scope != "" {
					scopes = slices.DeleteFunc(scopes, func(s memory.Scope) bool { return string(s.Kind) != p.Scope })
				}
				if p.Limit <= 0 {
					p.Limit = profileListDefault
				}
				entries, err := src.Profile().List(ctx, scopes, store.MemoryQuery{Category: p.Category, Text: p.Text, Limit: min(p.Limit, profileListMax)})
				if err != nil {
					return nil, err
				}
				out := make([]profileItem, 0, len(entries))
				for _, e := range entries {
					out = append(out, profileItem{e.ID, e.Version, string(e.Category), profileScope{string(e.Scope.Kind), e.Scope.Value}, e.Title()})
				}
				return out, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "profile_read",
				Description: "Read the latest active version of one entry of the user's Hoplet profile.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
			},
			visible: usable,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					ID string `json:"id"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				scopes, err := src.Profile().Scopes(ctx, caller)
				if err != nil {
					return nil, err
				}
				e, err := src.Profile().Get(ctx, scopes, p.ID)
				if err != nil {
					return nil, noEntry(p.ID, err)
				}
				if err := src.Profile().Read(ctx, caller.ID, e); err != nil {
					slog.WarnContext(ctx, "record memory read", "session", caller.ID, "entry", e.ID, "err", err)
				}
				return profileEntry{e.ID, e.Version, string(e.Category), profileScope{string(e.Scope.Kind), e.Scope.Value}, e.Body}, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "profile_write",
				Description: "Create, update or withdraw an entry of the user's Hoplet profile. Only an outcome of applied may be told to the user as saved; candidate is waiting for the user's review and rejected was not saved.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","update","withdraw"]},"id":{"type":"string","description":"The entry, for update and withdraw"},"expected_version":{"type":"integer","description":"The version last read, for update and withdraw"},"category":{"type":"string","enum":["fact","preference","convention","experience"]},"scope":{"type":"string",` + scopeKinds + `,"description":"The kind only; its value comes from this session"},"body":{"type":"string"},"evidence":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["user_stated","verified","observed","inferred"]},"ref":{"type":"string"}},"required":["kind"],"additionalProperties":false}},"reason":{"type":"string"}},"required":["action","reason"],"additionalProperties":false}`),
			},
			visible: usable,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				if caller.Mode != proto.ModePersonal {
					return nil, failf("profile_write is only available in Personal sessions")
				}
				var p profileWriteArgs
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				return profileWrite(ctx, src.Profile(), caller, p)
			},
		},
	}
}

func profileWrite(ctx context.Context, src ProfileSource, caller proto.Session, p profileWriteArgs) (any, error) {
	scopes, err := src.Scopes(ctx, caller)
	if err != nil {
		return nil, err
	}
	opID, err := profileOpID(caller.ID, p)
	if err != nil {
		return nil, err
	}
	w := memory.Write{
		OpID: opID, Actor: memory.ActorCompanion, Action: memory.Action(p.Action), Target: p.ID,
		ExpectedVersion: p.ExpectedVersion, Category: memory.Category(p.Category), Body: p.Body,
		Reason: p.Reason, Session: caller.ID,
	}
	for _, ev := range p.Evidence {
		w.Evidence = append(w.Evidence, memory.Evidence{Kind: memory.EvidenceKind(ev.Kind), Ref: ev.Ref})
	}
	if p.Scope != "" {
		var ok bool
		if w.Scope, ok = scopeOf(p.Scope, scopes); !ok {
			return nil, failf("this session has no %s scope", p.Scope)
		}
	}
	if w.Action != memory.ActionCreate {
		// An entry outside this session's scopes does not exist for it.
		if _, err := src.Get(ctx, scopes, p.ID); err != nil {
			return nil, noEntry(p.ID, err)
		}
	}
	r, err := src.Apply(ctx, w)
	if err != nil {
		return nil, err
	}
	out := profileWriteResult{Outcome: string(r.Outcome), Code: r.Code, Reason: r.Reason}
	if r.Outcome != memory.OutcomeRejected {
		out.ID, out.Version = r.Entry.ID, r.Entry.Version
	}
	return out, nil
}
