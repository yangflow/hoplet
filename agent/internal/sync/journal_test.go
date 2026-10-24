package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func journalAt(t *testing.T, home string) *journal {
	t.Helper()
	j := openJournal(home)
	j.now = func() time.Time { return time.UnixMilli(1000) }
	return j
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

func state(id string) RuntimeState {
	return RuntimeState{V: ProtocolVersion, Type: TypeRuntimeState, SessionID: id, State: "busy"}
}

func TestJournalNumbersEventsAndReplaysWhatTheCoreHasNotConfirmed(t *testing.T) {
	home := t.TempDir()
	j := journalAt(t, home)
	for _, id := range []string{"a", "b", "c"} {
		raw, err := j.append(state(id))
		if err != nil {
			t.Fatal(err)
		}
		m := decode(t, raw)
		if m["at"] != float64(1000) || m["replayed"] != nil {
			t.Fatalf("live event: %s", raw)
		}
	}
	j.ack(1)

	// A restart keeps the numbering and the pending events.
	j = journalAt(t, home)
	j.welcome(1)
	pending := j.pending()
	if len(pending) != 2 {
		t.Fatalf("pending=%d want 2", len(pending))
	}
	for i, raw := range pending {
		m := decode(t, raw)
		if m["eventSeq"] != float64(i+2) || m["replayed"] != true || m["sessionId"] != []string{"b", "c"}[i] {
			t.Fatalf("replayed event %d: %s", i, raw)
		}
	}
	raw, _ := j.append(state("d"))
	if decode(t, raw)["eventSeq"] != float64(4) {
		t.Fatalf("next event: %s", raw)
	}
	j.welcome(4)
	if len(j.pending()) != 0 {
		t.Fatal("confirmed events are replayed")
	}
	info, err := os.Stat(filepath.Join(home, journalFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode %v", info.Mode().Perm())
	}
}

func TestJournalSkipsATornLine(t *testing.T) {
	home := t.TempDir()
	j := journalAt(t, home)
	_, _ = j.append(state("a"))
	f, err := os.OpenFile(filepath.Join(home, journalFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":2,"at":1,"msg":{"v":24,"ty`)
	f.Close()
	j = journalAt(t, home)
	if got := len(j.pending()); got != 1 {
		t.Fatalf("pending=%d want 1", got)
	}
}

func TestJournalRenumbersAfterTheCoreThatAppliedMore(t *testing.T) {
	j := journalAt(t, "")
	_, _ = j.append(state("a"))
	j.welcome(40) // the journal file was lost; the core has applied 40 events of this machine
	m := decode(t, j.pending()[0])
	if m["eventSeq"] != float64(41) {
		t.Fatalf("renumbered event: %v", m)
	}
}

func TestJournalCapDropsTheOldestButKeepsPermissionRequestsAndCountsAGap(t *testing.T) {
	j := journalAt(t, "")
	j.maxEvents = 10
	perm := RuntimePermission{V: ProtocolVersion, Type: TypeRuntimePermission, SessionID: "a", RequestID: "r1", Tool: "Bash", Input: json.RawMessage(`{}`)}
	if _, err := j.append(perm); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		_, _ = j.append(state("a"))
	}
	pending := j.pending()
	if len(pending) > 10 {
		t.Fatalf("pending=%d over the cap", len(pending))
	}
	first := decode(t, pending[0])
	if first["type"] != TypeRuntimePermission || first["requestId"] != "r1" {
		t.Fatalf("the permission request was dropped: %v", first)
	}
	gaps, dropped := 0, 0.0
	for _, raw := range pending {
		m := decode(t, raw)
		if m["type"] == TypeJournalGap {
			gaps++
			dropped = m["dropped"].(float64)
		}
	}
	states := len(pending) - 1 - gaps
	if gaps != 1 || int(dropped)+states != 30 {
		t.Fatalf("gaps=%d dropped=%v states=%d", gaps, dropped, states)
	}
}

func TestJournalBytesCap(t *testing.T) {
	j := journalAt(t, "")
	j.maxBytes = 2000
	for i := 0; i < 100; i++ {
		_, _ = j.append(state("a"))
	}
	if j.size > j.maxBytes {
		t.Fatalf("size=%d over %d", j.size, j.maxBytes)
	}
}
