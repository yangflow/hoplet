package sync

import (
	"encoding/json"
	"testing"
)

func TestHandleHookEventPassesTheEnvelopeAsItIs(t *testing.T) {
	var gotID string
	var gotEvent json.RawMessage
	c := &Client{hooks: Hooks{HookEvent: func(id string, ev json.RawMessage) { gotID, gotEvent = id, ev }}}
	c.handleHookEvent([]byte(`{"v":20,"type":"hook.event","hookId":"notify","event":{"id":"evt_1","type":"session.ended"}}`))
	if gotID != "notify" || string(gotEvent) != `{"id":"evt_1","type":"session.ended"}` {
		t.Fatalf("got %q %s", gotID, gotEvent)
	}
	(&Client{}).handleHookEvent([]byte(`{"hookId":"x","event":{}}`))
	c.handleHookEvent([]byte(`not json`))
}
