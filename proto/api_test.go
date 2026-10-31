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
