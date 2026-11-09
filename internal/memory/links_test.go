package memory

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/store"
)

func TestLinksFrontmatter(t *testing.T) {
	plain, err := Encode(testEntry())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "links") {
		t.Fatalf("an entry without links wrote them:\n%s", plain)
	}
	e := testEntry()
	e.Links, e.Unlinked = []string{otherID}, []string{"mem_00000000000000000000000000000000"}
	data, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("%+v, %v", got, err)
	}
	for name, line := range map[string]string{
		"not a list": "links: " + otherID,
		"bad ID":     "links: [nope]",
		"self":       "links: [" + testID + "]",
		"repeated":   "links: [" + otherID + ", " + otherID + "]",
	} {
		t.Run(name, func(t *testing.T) {
			bad := strings.Replace(string(plain), "updated_at:", line+"\nupdated_at:", 1)
			if _, err := Decode([]byte(bad)); !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// linked commits testID holding a link to otherID, and otherID.
func (f *fixture) linked(alsoBack bool) {
	f.t.Helper()
	ctx := context.Background()
	a := newEntry("A")
	a.Links = []string{otherID}
	if _, err := f.svc.Commit(ctx, Op{ID: "a", Entry: a, Actor: ActorUser, Reason: "test"}); err != nil {
		f.t.Fatal(err)
	}
	b := newEntry("B")
	b.ID = otherID
	if alsoBack {
		b.Links = []string{testID}
	}
	if _, err := f.svc.Commit(ctx, Op{ID: "b", Entry: b, Actor: ActorUser, Reason: "test"}); err != nil {
		f.t.Fatal(err)
	}
}

func TestLinksBothDirections(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.linked(false)
	got, err := f.svc.Links(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{testID: {otherID}, otherID: {testID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestUnlink(t *testing.T) {
	for name, back := range map[string]bool{"one holder": false, "both hold": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			f.linked(back)
			before, err := f.svc.Get(ctx, testID)
			if err != nil {
				t.Fatal(err)
			}
			// From the side that may not hold the link.
			for range 2 {
				if err := f.svc.Unlink(ctx, otherID, testID, "u1"); err != nil {
					t.Fatal(err)
				}
			}
			after, err := f.svc.Get(ctx, testID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Version != before.Version || !after.UpdatedAt.Equal(before.UpdatedAt) || len(after.Links) != 0 || !slices.Equal(after.Unlinked, []string{otherID}) {
				t.Fatalf("%+v", after)
			}
			links, err := f.svc.Links(ctx)
			if err != nil || len(links) != 0 {
				t.Fatalf("%v, %v", links, err)
			}
			hist, err := f.svc.History(ctx, testID)
			if err != nil || len(hist) != 1 {
				t.Fatalf("%+v, %v", hist, err)
			}
			ops, err := f.st.MemoryOps(ctx, testID)
			if err != nil || len(ops) != 2 || ops[0].Kind == ops[1].Kind {
				t.Fatalf("audit %+v, %v", ops, err)
			}
			f.reopen() // the scan takes the file as it is
			if got, err := f.svc.Get(ctx, testID); err != nil || got.Version != before.Version {
				t.Fatalf("%+v, %v", got, err)
			}
		})
	}
}

func TestUnlinkCrash(t *testing.T) {
	tests := []struct {
		point  string
		linked bool // the link is still there after the restart
	}{
		{"after-begin", true},
		{"after-temp", true},
		{"after-rename", false},
		{"after-finish", false},
	}
	for _, tt := range tests {
		t.Run(tt.point, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			f.linked(false)
			f.svc.hook = func(p string) error {
				if p == tt.point {
					return errCrash
				}
				return nil
			}
			if err := f.svc.Unlink(ctx, testID, otherID, "u1"); !errors.Is(err, errCrash) {
				t.Fatalf("got %v", err)
			}
			f.reopen()
			e, err := f.svc.Get(ctx, testID)
			if err != nil || (len(e.Links) == 1) != tt.linked || e.Version != 1 {
				t.Fatalf("%+v, %v", e, err)
			}
			if err := f.svc.Unlink(ctx, testID, otherID, "u1"); err != nil {
				t.Fatal(err)
			}
			e, err = f.svc.Get(ctx, testID)
			if err != nil || len(e.Links) != 0 || !slices.Equal(e.Unlinked, []string{otherID}) {
				t.Fatalf("%+v, %v", e, err)
			}
			ops, err := f.st.MemoryOps(ctx, testID)
			if err != nil || len(ops) != 2 {
				t.Fatalf("audit %+v, %v", ops, err)
			}
		})
	}
}
