package store

import (
	"context"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// BenchmarkAppendDeltas measures appending streamed deltas in batches of
// 100, the size the daemon's ~100ms coalescing produces under load.
func BenchmarkAppendDeltas(b *testing.B) {
	s := newStore(b)
	newSession(b, s, "s")
	entries := make([]Entry, 100)
	for i := range entries {
		entries[i] = Entry{Payload: proto.MessageDelta{MessageID: "m", Text: "token "}}
	}
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, err := s.Append(ctx, AppendRequest{Session: "s", Turn: 1, Time: t0, Entries: entries}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.N*len(entries))/b.Elapsed().Seconds(), "events/s")
}
