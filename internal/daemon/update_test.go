package daemon

import (
	"testing"

	"github.com/yangflow/hoplet/proto"
)

func TestSessionUpdate(t *testing.T) {
	h, script := newHome(t, echoScript)
	start(t, testConfig(h, script))
	c := dial(t, h, "device:a")
	sess := create(t, c, h)
	cat, err := c.SubscribeCatalog(timeout(t), sess.Version)
	if err != nil {
		t.Fatal(err)
	}

	title, on := "Fix sync", true
	got, err := c.Update(timeout(t), proto.UpdateParams{Session: sess.ID, Title: &title, Pinned: &on})
	if err != nil || got.Title != "Fix sync" || !got.Pinned || got.Unread || !got.UpdatedAt.Equal(sess.UpdatedAt) {
		t.Fatalf("update %+v %v", got, err)
	}
	// The first item brings the client to its cursor; the change follows.
	for {
		var ci proto.CatalogItem
		if err := cat.Next(timeout(t), &ci); err != nil {
			t.Fatal(err)
		}
		if len(ci.Sessions) == 0 {
			continue
		}
		if len(ci.Sessions) != 1 || !ci.Sessions[0].Pinned || ci.Sessions[0].Title != "Fix sync" {
			t.Fatalf("catalog item after update %+v", ci)
		}
		break
	}

	if _, err := c.Update(timeout(t), proto.UpdateParams{Session: "missing", Pinned: &on}); apiCode(err) != proto.CodeNotFound {
		t.Fatalf("unknown session: %v", err)
	}
}
