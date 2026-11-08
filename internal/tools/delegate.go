package tools

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yangflow/hoplet/proto"
)

// Create is a session a tool creates: a Personal child the caller
// delegates to, or a Code session it hands work off to. Prompt is the
// session's first message, sent at once under the key "create:<ID>".
type Create struct {
	Mode          proto.Mode
	Harness       string
	Directory     string // empty: the session's own directory
	Title         string
	Owner         proto.Owner
	Parent        proto.SessionID
	Permission    proto.PermissionMode
	Model         string
	ModelProvider string
	Effort        proto.Effort
	Prompt        string
}

func personal(caller proto.Session) bool { return caller.Mode == proto.ModePersonal }

func hasParent(caller proto.Session) bool { return caller.Parent != "" }

func delegationTools(src Source) []tool {
	return []tool{
		{
			info: proto.ToolInfo{
				Name: "session_create",
				Description: "Start a Hoplet session that works on a task for you and reports back with its report tool. " +
					"It is a Personal session with your permission mode; it uses your harness unless you name another, and then also your model and effort. " +
					"It works in directory, an existing absolute path, or in a directory of its own. Returns the new session.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"prompt":{"type":"string","description":"The task: the session's first message"},` +
					`"title":{"type":"string","description":"A short title for the session"},` +
					`"harness":{"type":"string","description":"Harness kind, such as claude, codex or hermes; default: yours"},` +
					`"directory":{"type":"string","description":"Absolute path of an existing directory to work in"}` +
					`},"required":["prompt"],"additionalProperties":false}`),
			},
			visible: personal,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Prompt    string `json:"prompt"`
					Title     string `json:"title"`
					Harness   string `json:"harness"`
					Directory string `json:"directory"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Prompt == "" {
					return nil, failf("prompt is required")
				}
				if p.Directory != "" {
					if err := checkDir(p.Directory); err != nil {
						return nil, err
					}
				}
				c := Create{
					Mode: proto.ModePersonal, Harness: p.Harness, Directory: p.Directory, Title: p.Title,
					Owner: proto.OwnerDelegated, Parent: caller.ID, Permission: caller.Permission, Prompt: p.Prompt,
				}
				if c.Harness == "" || c.Harness == caller.Harness.Kind {
					c.Harness, c.Model, c.ModelProvider, c.Effort = caller.Harness.Kind, caller.Model, caller.ModelProvider, caller.Effort
				}
				return create(ctx, src, c)
			},
		},
		{
			info: proto.ToolInfo{
				Name: "session_send",
				Description: "Send a message to a session you started with session_create. It is sent now when the session is idle and queued while it works. " +
					"The ID may be a unique prefix of at least 4 characters.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"id":{"type":"string","description":"Session ID or a unique prefix of at least 4 characters"},` +
					`"text":{"type":"string","description":"The message"}` +
					`},"required":["id","text"],"additionalProperties":false}`),
			},
			visible: personal,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Text == "" {
					return nil, failf("text is required")
				}
				id, err := resolve(ctx, src, p.ID)
				if err != nil {
					return nil, err
				}
				st, err := src.State(ctx, id)
				if err != nil {
					return nil, err
				}
				if t := st.Session; t.Parent != caller.ID || t.Owner != proto.OwnerDelegated {
					return nil, failf("session %s was not started by you with session_create; you can only send to those", id)
				}
				key, err := newKey("send:" + string(caller.ID))
				if err != nil {
					return nil, err
				}
				return src.Send(ctx, proto.SendParams{Session: id, Text: p.Text, Key: key})
			},
		},
		{
			info: proto.ToolInfo{
				Name: "handoff",
				Description: "Hand work that changes code off to a new Hoplet Code session the user owns. It starts at once with your summary as its first message, " +
					"asks the user to approve its actions, and reports its progress back to you. " +
					"directory is the absolute path of an existing directory, usually a repository. Returns the new session.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"directory":{"type":"string","description":"Absolute path of the directory to work in"},` +
					`"summary":{"type":"string","description":"What to do and everything the session needs to know: its first message"},` +
					`"title":{"type":"string","description":"A short title for the session"},` +
					`"harness":{"type":"string","description":"Harness kind, such as claude or codex; default: yours"}` +
					`},"required":["directory","summary"],"additionalProperties":false}`),
			},
			visible: personal,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Directory string `json:"directory"`
					Summary   string `json:"summary"`
					Title     string `json:"title"`
					Harness   string `json:"harness"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Summary == "" {
					return nil, failf("summary is required")
				}
				if p.Directory == "" {
					return nil, failf("directory is required")
				}
				if err := checkDir(p.Directory); err != nil {
					return nil, err
				}
				return create(ctx, src, Create{
					Mode: proto.ModeCode, Harness: cmp.Or(p.Harness, caller.Harness.Kind), Directory: p.Directory, Title: p.Title,
					Owner: proto.OwnerMe, Parent: caller.ID, Permission: proto.ModeConfirm, Prompt: p.Summary,
				})
			},
		},
		{
			info: proto.ToolInfo{
				Name: "report",
				Description: "Report to the session that started you: your result, a question, or progress. " +
					"The report reaches it now when it is idle, or after its current turn.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"text":{"type":"string","description":"The report"}` +
					`},"required":["text"],"additionalProperties":false}`),
			},
			visible: hasParent,
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Text string `json:"text"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Text == "" {
					return nil, failf("text is required")
				}
				key, err := newKey("report:" + string(caller.ID))
				if err != nil {
					return nil, err
				}
				if err := src.Report(ctx, caller.Parent, caller.ID, key, p.Text); err != nil {
					return nil, err
				}
				return proto.SendReply{Queued: true}, nil
			},
		},
	}
}

func create(ctx context.Context, src Source, c Create) (any, error) {
	sess, err := src.Create(ctx, c)
	if err != nil && sess.ID != "" {
		return nil, failf("session %s was created, but its first message could not be sent: %v", sess.ID, err)
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func checkDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return failf("directory must be an absolute path")
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return failf("directory %s: %v", dir, err)
	}
	if !fi.IsDir() {
		return failf("%s is not a directory", dir)
	}
	return nil
}

// newKey returns prefix with a random suffix: the key of one message.
func newKey(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tools: new key: %w", err)
	}
	return prefix + ":" + hex.EncodeToString(b[:]), nil
}
