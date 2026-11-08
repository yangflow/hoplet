package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// TestResumeVectors builds testdata/vectors/resume.json from the real store
// and catch-up logic: with -update it writes the file (go generate),
// otherwise it fails if the file differs.
var updateVectors = flag.Bool("update", false, "rewrite testdata/vectors")

const resumeVectorFile = "../../testdata/vectors/resume.json"

var vectorTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

type resumeSession struct {
	Name     string   `json:"name"`
	Have     []uint64 `json:"have"`
	AfterSeq uint64   `json:"after_seq"`
	Items    []any    `json:"items"`
	Want     wantLog  `json:"want"`
}

type wantLog struct {
	Snapshot bool     `json:"snapshot"`
	Seqs     []uint64 `json:"seqs"`
	Cursor   uint64   `json:"cursor"`
	More     bool     `json:"more"`
}

type resumeCatalog struct {
	Name         string            `json:"name"`
	Have         []proto.SessionID `json:"have"`
	AfterVersion uint64            `json:"after_version"`
	Item         proto.CatalogItem `json:"item"`
	Want         []proto.SessionID `json:"want"`
	WantVersion  uint64            `json:"want_version"`
}

type resumeVectors struct {
	Description string          `json:"description"`
	Sessions    []resumeSession `json:"sessions"`
	Catalog     []resumeCatalog `json:"catalog"`
}

func vectorSession(t *testing.T, st *store.Store, id proto.SessionID) {
	t.Helper()
	_, err := st.CreateSession(context.Background(), proto.Session{
		ID: id, Mode: proto.ModeCode, Harness: proto.HarnessRef{Kind: "claude"},
		Directory: "/work", Isolation: proto.InPlace, Owner: proto.OwnerMe, Origin: proto.OriginMe,
		CreatedAt: vectorTime,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func vectorTurn(t *testing.T, st *store.Store, id proto.SessionID, n uint32) {
	t.Helper()
	msg := proto.Message{ID: fmt.Sprintf("m%d", n), Role: proto.RoleAssistant, Text: "x"}
	req := store.AppendRequest{Session: id, Turn: n, Time: vectorTime}
	for _, p := range []proto.Payload{proto.TurnStarted{}, msg, msg, msg, proto.TurnEnded{Reason: proto.TurnCompleted}} {
		req.Entries = append(req.Entries, store.Entry{Payload: p})
	}
	if _, err := st.Append(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func seqRange(from, to uint64) []uint64 {
	out := []uint64{}
	for s := from; s <= to; s++ {
		out = append(out, s)
	}
	return out
}

func TestResumeVectors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &conn{s: &server{store: st, lim: limits{gapEvents: 5, gapBytes: 1 << 20, snapshotTurns: 2}}}

	const id = proto.SessionID("s1")
	vectorSession(t, st, id)
	for n := uint32(1); n <= 4; n++ {
		vectorTurn(t, st, id, n) // seqs 1..20; the last two turns start at 11
	}
	cases := []struct {
		name     string
		have     []uint64
		after    uint64
		snapshot bool
		before   []uint64 // the seqs after the first item
	}{
		{"small_gap", seqRange(1, 17), 17, false, seqRange(1, 20)},
		{"up_to_date", seqRange(1, 20), 20, false, seqRange(1, 20)},
		{"large_gap", seqRange(1, 3), 3, true, seqRange(11, 20)},
		{"first_subscribe", []uint64{}, 0, true, seqRange(11, 20)},
		{"cursor_past_end", []uint64{98, 99}, 99, true, seqRange(11, 20)},
	}
	first := make([]proto.SessionItem, len(cases))
	for i, tc := range cases {
		item, cursor, err := c.catchUp(ctx, proto.SessionParams{Session: id, AfterSeq: tc.after})
		if err != nil {
			t.Fatal(err)
		}
		if (item.Snapshot != nil) != tc.snapshot || cursor != 20 {
			t.Fatalf("%s: snapshot %v, cursor %d", tc.name, item.Snapshot != nil, cursor)
		}
		item.Queue = &proto.Queue{Messages: []proto.QueuedMessage{}}
		first[i] = item
	}
	vectorTurn(t, st, id, 5)
	live, err := st.Events(ctx, id, 20, 500)
	if err != nil {
		t.Fatal(err)
	}
	v := resumeVectors{Description: "Subscriptions resumed from a cursor, against the real catch-up logic."}
	for i, tc := range cases {
		v.Sessions = append(v.Sessions, resumeSession{
			Name: tc.name, Have: tc.have, AfterSeq: tc.after,
			Items: []any{first[i], proto.SessionItem{Events: live}},
			Want:  wantLog{Snapshot: tc.snapshot, Seqs: append(cloneSeqs(tc.before), seqRange(21, 25)...), Cursor: 25, More: tc.snapshot},
		})
	}

	mid, err := st.CatalogVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorSession(t, st, "s2")
	all := []proto.SessionID{"s1", "s2"}
	for _, tc := range []struct {
		name  string
		have  []proto.SessionID
		after uint64
		want  []proto.SessionID
	}{
		{"first_subscribe", []proto.SessionID{}, 0, all},
		{"incremental", []proto.SessionID{"s1"}, mid, all},
		{"version_past_end", []proto.SessionID{"gone"}, 1 << 40, all},
	} {
		item, err := c.catalogStart(ctx, proto.CatalogParams{AfterVersion: tc.after})
		if err != nil {
			t.Fatal(err)
		}
		v.Catalog = append(v.Catalog, resumeCatalog{Name: tc.name, Have: tc.have, AfterVersion: tc.after, Item: item, Want: tc.want, WantVersion: item.Version})
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if *updateVectors {
		if err := os.MkdirAll(filepath.Dir(resumeVectorFile), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resumeVectorFile, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	have, err := os.ReadFile(resumeVectorFile)
	if err != nil {
		t.Fatalf("%v; run make gen", err)
	}
	if !bytes.Equal(have, data) {
		t.Fatal("resume.json differs from the implementation; run make gen")
	}
}

func cloneSeqs(s []uint64) []uint64 { return append([]uint64{}, s...) }
