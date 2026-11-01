package catalog

import (
	"reflect"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func sess(id string, mode proto.Mode, dir string, minutes int, mut ...func(*proto.Session)) proto.Session {
	s := proto.Session{ID: proto.SessionID(id), Mode: mode, Directory: dir, Status: proto.StatusIdle, Title: id,
		CreatedAt: t0.Add(-time.Duration(minutes) * time.Minute), UpdatedAt: t0.Add(time.Duration(minutes) * time.Minute)}
	for _, m := range mut {
		m(&s)
	}
	return s
}

func ids(ss []proto.Session) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, string(s.ID))
	}
	return out
}

func TestApply(t *testing.T) {
	var c Catalog
	c.Apply(proto.CatalogItem{Snapshot: true, Version: 3, Sessions: []proto.Session{sess("a", proto.ModeCode, "/w", 1)}})
	c.Apply(proto.CatalogItem{Version: 4, Sessions: []proto.Session{sess("b", proto.ModeCode, "/w", 2)}})
	if _, ok := c.Get("a"); !ok || c.Version() != 4 {
		t.Fatalf("after a change: version %d", c.Version())
	}
	c.Apply(proto.CatalogItem{Snapshot: true, Version: 9, Sessions: []proto.Session{sess("c", proto.ModeCode, "/w", 3)}})
	if _, ok := c.Get("a"); ok || c.Version() != 9 {
		t.Fatal("a snapshot kept a session it does not list")
	}
}

func TestList(t *testing.T) {
	archived := t0
	var c Catalog
	c.Apply(proto.CatalogItem{Snapshot: true, Sessions: []proto.Session{
		sess("a1", proto.ModeCode, "/w/api", 5),
		sess("a2", proto.ModeCode, "/w/api", 1, func(s *proto.Session) { s.Status = proto.StatusWaiting }),
		sess("b1", proto.ModeCode, "/w/web", 3, func(s *proto.Session) { s.Status = proto.StatusRunning }),
		sess("pin", proto.ModeCode, "/w/web", 0, func(s *proto.Session) { s.Pinned = true }),
		sess("old", proto.ModeCode, "/w/api", 9, func(s *proto.Session) { s.ArchivedAt = &archived }),
		sess("p1", proto.ModePersonal, "", 2),
		sess("p2", proto.ModePersonal, "", 4),
	}})
	for name, tc := range map[string]struct {
		v      View
		pinned []string
		groups [][]string
		dirs   []string
	}{
		"code by updated": {View{Mode: proto.ModeCode}, []string{"pin"}, [][]string{{"a1", "a2"}, {"b1"}}, []string{"/w/api", "/w/web"}},
		"code by created": {View{Mode: proto.ModeCode, Sort: ByCreated}, []string{"pin"}, [][]string{{"a2", "a1"}, {"b1"}}, []string{"/w/api", "/w/web"}},
		"code by name":    {View{Mode: proto.ModeCode, Sort: ByName}, []string{"pin"}, [][]string{{"a1", "a2"}, {"b1"}}, []string{"/w/api", "/w/web"}},
		"needs you":       {View{Mode: proto.ModeCode, Show: ShowNeedsYou}, []string{}, [][]string{{"a2"}}, []string{"/w/api"}},
		"running":         {View{Mode: proto.ModeCode, Show: ShowRunning}, []string{}, [][]string{{"b1"}}, []string{"/w/web"}},
		"archived":        {View{Mode: proto.ModeCode, Archived: true}, []string{}, [][]string{{"old"}}, []string{""}},
		"personal":        {View{Mode: proto.ModePersonal}, []string{}, [][]string{{"p2", "p1"}}, []string{""}},
	} {
		l := c.List(tc.v)
		var groups [][]string
		var dirs []string
		for _, g := range l.Groups {
			groups = append(groups, ids(g.Sessions))
			dirs = append(dirs, g.Dir)
		}
		if !reflect.DeepEqual(ids(l.Pinned), tc.pinned) || !reflect.DeepEqual(groups, tc.groups) || !reflect.DeepEqual(dirs, tc.dirs) {
			t.Errorf("%s: pinned %v groups %v dirs %v; want %v %v %v", name, ids(l.Pinned), groups, dirs, tc.pinned, tc.groups, tc.dirs)
		}
	}
	if l := c.List(View{Mode: proto.ModeCode}); l.Groups[0].Name != "api" {
		t.Errorf("group name %q", l.Groups[0].Name)
	}
	if n := c.Counts(); n != (Counts{NeedsYou: 1, Running: 1}) {
		t.Errorf("counts %+v", n)
	}
}
