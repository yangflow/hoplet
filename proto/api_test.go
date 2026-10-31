package proto

import (
	"encoding/json"
	"testing"
)

// TestFrameWireFormat pins the JSON shape of protocol frames: every client
// depends on it.
func TestFrameWireFormat(t *testing.T) {
	for name, tc := range map[string]struct {
		v    any
		want string
	}{
		"request":     {ClientFrame{ID: 1, Method: MethodHello, Params: json.RawMessage(`{"token":"t"}`)}, `{"id":1,"method":"hello","params":{"token":"t"}}`},
		"cancel":      {ClientFrame{Sub: 4, Cancel: true}, `{"sub":4,"cancel":true}`},
		"result":      {ServerFrame{ID: 1, Result: json.RawMessage(`{}`)}, `{"id":1,"result":{}}`},
		"error":       {ServerFrame{ID: 2, Error: &APIError{Code: CodeBusy, Message: "m"}}, `{"id":2,"error":{"code":"busy","message":"m"}}`},
		"item":        {ServerFrame{Sub: 3, Item: json.RawMessage(`{"events":[]}`)}, `{"sub":3,"item":{"events":[]}}`},
		"end":         {ServerFrame{Sub: 3, End: true}, `{"sub":3,"end":true}`},
		"queued":      {SendReply{Queued: true}, `{"queued":true}`},
		"recorded":    {SendReply{Seq: 7, Turn: 2}, `{"seq":7,"turn":2}`},
		"empty queue": {SessionItem{Queue: &Queue{Messages: []QueuedMessage{}}}, `{"queue":{"messages":[]}}`},
		"events page": {EventsPage{Events: []Event{}, More: true}, `{"events":[],"more":true}`},
		"events args": {EventsParams{Session: "s", BeforeSeq: 11, Limit: 5}, `{"session":"s","before_seq":11,"limit":5}`},
	} {
		b, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("%s: %s, want %s", name, b, tc.want)
		}
	}
	if got := (&APIError{Code: CodeNotFound, Message: "no session"}).Error(); got != "not_found: no session" {
		t.Errorf("error text %q", got)
	}
}

// TestFrameRoundTrip checks that frames survive a decode and re-encode with
// identical bytes, and that a request's params decode into their type.
func TestFrameRoundTrip(t *testing.T) {
	req := ClientFrame{ID: 1, Method: MethodSessionsSend, Params: json.RawMessage(`{"session":"s","text":"t","key":"k"}`)}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var back ClientFrame
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(b) {
		t.Fatalf("client frame re-encoded as %s, want %s", again, b)
	}
	var sp SendParams
	if err := json.Unmarshal(back.Params, &sp); err != nil {
		t.Fatal(err)
	}
	if sp.Session != "s" || sp.Text != "t" || sp.Key != "k" {
		t.Fatalf("send params %+v", sp)
	}

	for name, v := range map[string]any{
		"cancel": &ClientFrame{Sub: 2, Cancel: true},
		"error":  &ServerFrame{ID: 3, Error: &APIError{Code: CodeConflict, Message: "m"}},
		"item":   &ServerFrame{Sub: 4, Item: json.RawMessage(`{"events":[]}`), End: false},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		switch v.(type) {
		case *ClientFrame:
			out = new(ClientFrame)
		default:
			out = new(ServerFrame)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(b) {
			t.Errorf("%s re-encoded as %s, want %s", name, again, b)
		}
	}
}
