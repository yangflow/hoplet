package proto

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// TestGoldenEventsDecode keeps every persisted event version decodable:
// testdata/events.jsonl only ever grows, one line per type and version.
func TestGoldenEventsDecode(t *testing.T) {
	f, err := os.Open("testdata/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	n := 0
	for s.Scan() {
		n++
		var e Event
		if err := json.Unmarshal(s.Bytes(), &e); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
		if _, err := e.Decode(); err != nil {
			t.Fatalf("line %d: %v", n, err)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no golden events")
	}
}
