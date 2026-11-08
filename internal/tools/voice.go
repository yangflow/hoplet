package tools

import (
	"context"
	"encoding/json"

	"github.com/yangflow/hoplet/proto"
)

// voiceSession is what a call's controller hears about a session.
type voiceSession struct {
	ID     proto.SessionID     `json:"id"`
	Title  string              `json:"title"`
	Mode   proto.Mode          `json:"mode"`
	Status proto.SessionStatus `json:"status"`
}

// voiceRead is one session's state as a call's controller reads it out.
type voiceRead struct {
	voiceSession
	// Waiting are the approvals waiting on the user, who answers them on a
	// screen.
	Waiting     []proto.ApprovalRequested `json:"waiting"`
	Queued      int                       `json:"queued"`
	LatestReply string                    `json:"latest_reply"`
}

// voiceTools are the only tools a call's controller has: read, send to an
// existing session, and hand work off to a new Code session.
func voiceTools(src Source) []tool {
	return []tool{
		{
			info: proto.ToolInfo{
				Name:        "sessions_list",
				Description: "List the Hoplet sessions that are not archived: their IDs, titles, modes and statuses.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			},
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				ss, err := src.Sessions(ctx)
				if err != nil {
					return nil, err
				}
				out := []voiceSession{}
				for _, s := range ss {
					if s.ArchivedAt == nil {
						out = append(out, heardSession(s))
					}
				}
				return out, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name:        "session_read",
				Description: "Read one Hoplet session: its status, the approvals waiting on the user, how many messages are queued and its latest reply.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"session":{"type":"string","description":"Session ID or a unique prefix of at least 4 characters"}` +
					`},"required":["session"],"additionalProperties":false}`),
			},
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Session string `json:"session"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				id, err := resolve(ctx, src, p.Session)
				if err != nil {
					return nil, err
				}
				st, err := src.State(ctx, id)
				if err != nil {
					return nil, err
				}
				reply, err := src.LatestReply(ctx, id)
				if err != nil {
					return nil, err
				}
				return voiceRead{voiceSession: heardSession(st.Session), Waiting: st.Pending, Queued: len(st.Queue), LatestReply: reply}, nil
			},
		},
		{
			info: proto.ToolInfo{
				Name: "session_send",
				Description: "Send a message to an existing Hoplet session, through its queue. " +
					"Only after reading the target and the message out and hearing the user agree.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"session":{"type":"string","description":"Session ID or a unique prefix of at least 4 characters"},` +
					`"text":{"type":"string","description":"The message"}` +
					`},"required":["session","text"],"additionalProperties":false}`),
			},
			call: func(ctx context.Context, caller proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Session string `json:"session"`
					Text    string `json:"text"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Text == "" {
					return nil, failf("text is required")
				}
				id, err := resolve(ctx, src, p.Session)
				if err != nil {
					return nil, err
				}
				key, err := newKey("send:" + string(caller.ID))
				if err != nil {
					return nil, err
				}
				return src.Send(ctx, proto.SendParams{Session: id, Text: p.Text, Key: key, Via: proto.ViaVoice})
			},
		},
		{
			info: proto.ToolInfo{
				Name: "handoff",
				Description: "Start a new Hoplet Code session on a task. It works where an existing session works, " +
					"with that session's harness and model, and asks the user to approve its actions on a screen. " +
					"Only after reading the reference session and the task out and hearing the user agree.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{` +
					`"reference":{"type":"string","description":"ID, or unique prefix of at least 4 characters, of the existing session to take the directory from"},` +
					`"task":{"type":"string","description":"What to do and everything the session needs to know: its first message"},` +
					`"title":{"type":"string","description":"A short title for the session"}` +
					`},"required":["reference","task"],"additionalProperties":false}`),
			},
			call: func(ctx context.Context, _ proto.Session, args json.RawMessage) (any, error) {
				var p struct {
					Reference string `json:"reference"`
					Task      string `json:"task"`
					Title     string `json:"title"`
				}
				if err := decode(args, &p); err != nil {
					return nil, err
				}
				if p.Task == "" {
					return nil, failf("task is required")
				}
				id, err := resolve(ctx, src, p.Reference)
				if err != nil {
					return nil, err
				}
				st, err := src.State(ctx, id)
				if err != nil {
					return nil, err
				}
				ref := st.Session
				if ref.Directory == "" || ref.ArchivedAt != nil {
					return nil, failf("session %s has no directory to work in; ask for another one", ref.ID)
				}
				// No parent: nothing reports back after the call ends.
				return create(ctx, src, Create{
					Mode: proto.ModeCode, Harness: ref.Harness.Kind, Directory: ref.Directory, Title: p.Title,
					Owner: proto.OwnerMe, Permission: proto.ModeConfirm, Model: ref.Model, ModelProvider: ref.ModelProvider,
					Prompt: p.Task, Via: proto.ViaVoice,
				})
			},
		},
	}
}

func heardSession(s proto.Session) voiceSession {
	return voiceSession{ID: s.ID, Title: s.Title, Mode: s.Mode, Status: s.Status}
}
