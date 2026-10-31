package proto

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestEventRoundTrip(t *testing.T) {
	ts := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	payloads := []Payload{
		TurnStarted{},
		TurnEnded{Reason: TurnCompleted},
		MessageDelta{MessageID: "m1", Text: "hel"},
		Message{ID: "m1", Role: RoleAssistant, Text: "hello"},
		Thinking{Text: "plan"},
		ToolCall{ID: "t1", Kind: ToolExec, Name: "Bash", Summary: "go test ./...", Status: ToolDone, Output: &BlobRef{SHA256: "ab", Size: 3}},
		FileChange{Path: "api/main.go", Op: FileModified},
		ApprovalRequested{ID: "a1", Kind: "command", Title: "Run command", Detail: "go test", Options: []ApprovalOption{{ID: "once", Label: "Allow once"}, {ID: "deny", Label: "Deny"}}},
		ApprovalResolved{ID: "a1", OptionID: "once", DecidedBy: "device:iphone"},
		Question{ID: "q1", Text: "Which repo?", Options: []string{"api", "web"}},
		Usage{InputTokens: 10, OutputTokens: 20},
		Error{Message: "boom"},
	}
	for _, p := range payloads {
		t.Run(string(p.eventType()), func(t *testing.T) {
			e, err := NewEvent("s1", 7, 2, ts, p)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			var back Event
			if err := json.Unmarshal(wire, &back); err != nil {
				t.Fatal(err)
			}
			got, err := back.Decode()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, p) {
				t.Fatalf("got %#v, want %#v", got, p)
			}
			if back.Seq != 7 || back.Turn != 2 || !back.Time.Equal(ts) || back.V != 1 {
				t.Fatalf("envelope changed: %+v", back)
			}
		})
	}
}

func TestDecodeUnknown(t *testing.T) {
	cases := []Event{
		{Type: "nope", V: 1, Payload: json.RawMessage(`{}`)},
		{Type: TypeMessage, V: 99, Payload: json.RawMessage(`{}`)},
	}
	for _, e := range cases {
		if _, err := e.Decode(); !errors.Is(err, ErrUnknownEvent) {
			t.Fatalf("%s v%d: got %v, want ErrUnknownEvent", e.Type, e.V, err)
		}
	}
}

func TestEveryEventTypeIsRegistered(t *testing.T) {
	for _, typ := range []EventType{
		TypeTurnStarted, TypeTurnEnded, TypeMessageDelta, TypeMessage, TypeThinking,
		TypeToolCall, TypeFileChange, TypeApprovalRequest, TypeApprovalResolved,
		TypeQuestion, TypeUsage, TypeError,
	} {
		if _, ok := decoders[decoderKey{typ, 1}]; !ok {
			t.Errorf("%s v1 has no decoder", typ)
		}
	}
}
