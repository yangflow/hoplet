package proto

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestEventRoundTrip(t *testing.T) {
	ts := time.Date(2026, 10, 31, 8, 0, 0, 5_000_000, time.UTC)
	payloads := []Payload{
		TurnStarted{},
		TurnEnded{Reason: TurnCompleted},
		TurnEnded{Reason: TurnFailed, Message: "crash"},
		MessageDelta{MessageID: "m1", Text: "hel"},
		Message{ID: "m1", Role: RoleAssistant, Text: "hello"},
		Thinking{Text: "plan"},
		ToolCall{ID: "t1", Kind: ToolExec, Name: "Bash", Summary: "go test ./...", Status: ToolDone, Output: &BlobRef{SHA256: "ab", Size: 3}},
		FileChange{Path: "api/main.go", Op: FileModified},
		ApprovalRequested{ID: "a1", Kind: ApprovalCommand, Title: "Run command", Detail: "go test", Options: []ApprovalOption{{ID: "once", Label: "Allow once", Scope: ScopeOnce}, {ID: "deny", Label: "Deny", Scope: ScopeDeny}}},
		ApprovalResolved{ID: "a1", Outcome: OutcomeAllowed, OptionID: "once", DecidedBy: "device:iphone"},
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

func TestNewEventNilPayload(t *testing.T) {
	if _, err := NewEvent("s1", 1, 0, time.Now(), nil); !errors.Is(err, ErrNilPayload) {
		t.Fatalf("got %v, want ErrNilPayload", err)
	}
}

func TestNewEventTruncatesToMillis(t *testing.T) {
	ts := time.Date(2026, 10, 31, 8, 0, 0, 123_456_789, time.UTC)
	e, err := NewEvent("s1", 1, 0, ts, TurnStarted{})
	if err != nil {
		t.Fatal(err)
	}
	if want := ts.Truncate(time.Millisecond); !e.Time.Equal(want) {
		t.Fatalf("time %v, want %v", e.Time, want)
	}
}

func TestRegisterLegacyUpgrades(t *testing.T) {
	const legacy EventType = "test.legacy"
	r := registry{}
	r.add(decoderKey{legacy, 1}, func(b json.RawMessage) (Payload, error) {
		var old struct {
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(b, &old); err != nil {
			return nil, err
		}
		return Error{Message: old.Msg}, nil
	})

	got, err := r.decode(Event{Type: legacy, V: 1, Payload: json.RawMessage(`{"msg":"old"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Error{Message: "old"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestApprovalResolvedValidation(t *testing.T) {
	cases := []struct {
		name  string
		p     ApprovalResolved
		valid bool
	}{
		{"allowed", ApprovalResolved{ID: "a1", Outcome: OutcomeAllowed, OptionID: "once"}, true},
		{"denied", ApprovalResolved{ID: "a1", Outcome: OutcomeDenied, OptionID: "deny"}, true},
		{"expired", ApprovalResolved{ID: "a1", Outcome: OutcomeExpired}, true},
		{"expired with option", ApprovalResolved{ID: "a1", Outcome: OutcomeExpired, OptionID: "once"}, false},
		{"unknown outcome", ApprovalResolved{ID: "a1", Outcome: "maybe"}, false},
		{"empty outcome", ApprovalResolved{ID: "a1"}, false},
		{"no id", ApprovalResolved{Outcome: OutcomeAllowed}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewEvent("s1", 1, 1, time.Now(), c.p)
			if c.valid && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.valid && !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("got %v, want ErrInvalidPayload", err)
			}
		})
	}
}
