package proto

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestSessionJSONRoundTrip(t *testing.T) {
	archived := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	in := Session{
		ID: "s1", Mode: ModeCode, Harness: HarnessRef{Kind: "claude", Version: "2.1.312", NativeID: "n1"}, Permission: ModeAutoEdit,
		Directory: "/work", Isolation: InPlace, Owner: OwnerMe, Origin: OriginMe, Status: StatusIdle,
		Title: "sync", CreatedAt: archived.Add(-time.Hour), UpdatedAt: archived, ArchivedAt: &archived, LastSeq: 7, Version: 9,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Session
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}
