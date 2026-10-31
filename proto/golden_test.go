package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"
)

// TestGoldenEventsDecode keeps every persisted event version decodable:
// testdata/events.jsonl only ever grows, one line per type and version.
func TestGoldenEventsDecode(t *testing.T) {
	data, err := os.ReadFile("testdata/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	seen := map[decoderKey]bool{}
	n := 0
	for {
		var e Event
		if err := dec.Decode(&e); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("event %d: %v", n+1, err)
		}
		n++
		p, err := e.Decode()
		if err != nil {
			t.Fatalf("event %d: %v", n, err)
		}
		seen[decoderKey{e.Type, e.V}] = true

		// Lines at the payload's current version must round-trip exactly.
		// A line for a legacy version would instead be compared against the
		// expected upgraded payload; none exist yet, so every line must be
		// current.
		if e.V != p.eventVersion() {
			t.Fatalf("event %d: %s v%d is not the current version v%d", n, e.Type, e.V, p.eventVersion())
		}
		re, err := NewEvent(e.Session, e.Seq, e.Turn, e.Time, p)
		if err != nil {
			t.Fatalf("event %d: %v", n, err)
		}
		var want, got any
		if err := json.Unmarshal(e.Payload, &want); err != nil {
			t.Fatalf("event %d: %v", n, err)
		}
		if err := json.Unmarshal(re.Payload, &got); err != nil {
			t.Fatalf("event %d: %v", n, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event %d: re-encoded payload %s, want %s", n, re.Payload, e.Payload)
		}
	}
	if n == 0 {
		t.Fatal("no golden events")
	}
	for k := range decoders {
		if !seen[k] {
			t.Errorf("golden events have no line for %s v%d", k.t, k.v)
		}
	}
}
