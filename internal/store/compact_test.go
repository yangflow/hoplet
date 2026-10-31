package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestCompactKeepsFinalMessages(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	newSession(t, s, "s")
	appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{
		{Payload: proto.TurnStarted{}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "hel"}},
		{Payload: proto.MessageDelta{MessageID: "m1", Text: "lo"}},
		{Payload: proto.Message{ID: "m1", Role: proto.RoleAssistant, Text: "hello"}},
		{Payload: proto.MessageDelta{MessageID: "m2", Text: "still streaming"}},
	}})
	n, err := s.Compact(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("compacted %d events, want 2", n)
	}
	evs, err := s.Events(ctx, "s", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, ev := range evs {
		types = append(types, fmt.Sprintf("%d:%s", ev.Seq, ev.Type))
	}
	want := "1:turn.started 4:message 5:message.delta"
	if got := strings.Join(types, " "); got != want {
		t.Fatalf("after compaction: %s, want %s", got, want)
	}
	next := appendOK(t, s, AppendRequest{Session: "s", Turn: 1, Entries: []Entry{{Payload: proto.Thinking{}}}})
	if next.FirstSeq != 6 {
		t.Fatalf("seq reused after compaction: %d", next.FirstSeq)
	}
}
