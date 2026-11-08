// Package tools is the table of tools a harness session may call through
// its tool bridge. The daemon serves it to connections that said hello
// with a session token; each tool checks its caller and its arguments here,
// never in a prompt.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yangflow/hoplet/internal/artifact"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// TokenEnv is the variable that carries a session's token to its tool
// bridge, only in that MCP server's own environment.
const TokenEnv = "HOPLET_SESSION_TOKEN" //nolint:gosec // a variable name, not a credential

// Source is what the tools read and change, the same services the client
// API uses.
type Source interface {
	Sessions(ctx context.Context) ([]proto.Session, error)
	State(ctx context.Context, id proto.SessionID) (proto.SessionState, error)
	// Create stores the session and sends its prompt. When the session was
	// stored but the prompt could not be sent, it returns the session with
	// the error.
	Create(ctx context.Context, c Create) (proto.Session, error)
	// Send sends a message, queueing it while a turn runs.
	Send(ctx context.Context, p proto.SendParams) (proto.SendReply, error)
	// Report queues text for parent as a report from session from.
	Report(ctx context.Context, parent, from proto.SessionID, key, text string) error
	// WorkDir is the directory a session's harness works in.
	WorkDir(ctx context.Context, id proto.SessionID) (string, error)
	// Publish publishes a version of an artifact.
	Publish(ctx context.Context, r artifact.Request) (proto.Artifact, error)
}

// tool is one tool: what the model sees and what a call does. A call's
// *Failure is shown to the model; any other error fails the request.
type tool struct {
	info    proto.ToolInfo
	visible func(caller proto.Session) bool
	call    func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error)
}

// Failure is a call that failed in a way the model should read and can
// act on, such as a bad argument or a session that does not exist.
type Failure struct{ Message string }

func (f *Failure) Error() string { return f.Message }

func failf(format string, a ...any) error { return &Failure{fmt.Sprintf(format, a...)} }

// Set is the tool table.
type Set struct {
	tools []tool
}

// New returns the tool table over src.
func New(src Source) *Set {
	everyone := func(proto.Session) bool { return true }
	return &Set{tools: append([]tool{
		{
			info: proto.ToolInfo{
				Name:        "sessions_list",
				Description: "List the Hoplet sessions on this host that are not archived: their IDs, titles, modes, harnesses, statuses and directories.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
			visible: everyone,
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				ss, err := src.Sessions(ctx)
				if err != nil {
					return nil, err
				}
				out := []proto.Session{}
				for _, s := range ss {
					if s.ArchivedAt == nil {
						out = append(out, s)
					}
				}
				return out, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "session_get",
				Description: "Get one Hoplet session: its record, the approvals waiting on the user and its queued messages. The ID may be a unique prefix of at least 4 characters.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","description":"Session ID or a unique prefix of at least 4 characters"}},"required":["id"],"additionalProperties":false}`),
			},
			visible: everyone,
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					ID string `json:"id"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				id, err := resolve(ctx, src, p.ID)
				if err != nil {
					return nil, err
				}
				return src.State(ctx, id)
			},
		},
	}, append(delegationTools(src), artifactTools(src)...)...)}
}

// List returns the tools caller may call.
func (s *Set) List(caller proto.Session) []proto.ToolInfo {
	out := []proto.ToolInfo{}
	for _, t := range s.tools {
		if t.visible(caller) {
			out = append(out, t.info)
		}
	}
	return out
}

// Call runs a tool for caller. A failure the model should read comes back
// as a result with IsError set; the error is for everything else.
func (s *Set) Call(ctx context.Context, caller proto.Session, name string, args json.RawMessage) (proto.ToolsCallResult, error) {
	for _, t := range s.tools {
		if t.info.Name != name || !t.visible(caller) {
			continue
		}
		v, err := t.call(ctx, caller, args)
		var f *Failure
		if errors.As(err, &f) {
			return proto.ToolsCallResult{Text: f.Message, IsError: true}, nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return proto.ToolsCallResult{Text: err.Error(), IsError: true}, nil
		}
		if err != nil {
			return proto.ToolsCallResult{}, err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return proto.ToolsCallResult{}, fmt.Errorf("tools: encode %s result: %w", name, err)
		}
		return proto.ToolsCallResult{Text: string(b)}, nil
	}
	return proto.ToolsCallResult{Text: "no tool named " + name, IsError: true}, nil
}

// decode reads a call's arguments, which must be a JSON object with no
// fields v does not have; none at all reads as an empty object.
func decode(args json.RawMessage, v any) error {
	if len(bytes.TrimSpace(args)) == 0 || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		args = json.RawMessage(`{}`)
	}
	d := json.NewDecoder(bytes.NewReader(args))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return failf("bad arguments: %v", err)
	}
	return nil
}

// resolve finds the session whose ID is id or starts with it.
func resolve(ctx context.Context, src Source, id string) (proto.SessionID, error) {
	if len(id) < 4 {
		return "", failf("a session ID needs at least 4 characters")
	}
	ss, err := src.Sessions(ctx)
	if err != nil {
		return "", err
	}
	var found []proto.SessionID
	for _, s := range ss {
		if string(s.ID) == id {
			return s.ID, nil
		}
		if strings.HasPrefix(string(s.ID), id) {
			found = append(found, s.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", failf("no session %s", id)
	case 1:
		return found[0], nil
	}
	return "", failf("%s matches %d sessions; give more of the ID", id, len(found))
}
