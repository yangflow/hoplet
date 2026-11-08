// Package catalog keeps the desktop's copy of the daemon's session list
// and arranges it for the sidebar.
package catalog

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yangflow/hoplet/proto"
)

// Catalog is the session list as of a catalog version. The zero value is
// empty and ready to use.
type Catalog struct {
	sessions map[proto.SessionID]proto.Session
	version  uint64
}

// Apply brings the catalog up to an item of subscribe.catalog.
func (c *Catalog) Apply(it proto.CatalogItem) {
	if c.sessions == nil || it.Snapshot {
		c.sessions = make(map[proto.SessionID]proto.Session, len(it.Sessions))
	}
	for _, s := range it.Sessions {
		c.sessions[s.ID] = s
	}
	c.version = it.Version
}

// Version is the catalog version to resume a subscription from.
func (c *Catalog) Version() uint64 { return c.version }

// Get returns a session.
func (c *Catalog) Get(id proto.SessionID) (proto.Session, bool) {
	s, ok := c.sessions[id]
	return s, ok
}

// Children returns the sessions a session delegated or handed off, by ID.
func (c *Catalog) Children(id proto.SessionID) []proto.Session {
	var out []proto.Session
	for _, s := range c.sessions {
		if s.Parent == id {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b proto.Session) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Counts are what the menu bar icon and the Dock show.
type Counts struct {
	NeedsYou int // live sessions waiting on me
	Running  int // live sessions in a turn
}

// Counts counts the sessions that are not archived.
func (c *Catalog) Counts() Counts {
	var n Counts
	for _, s := range c.sessions {
		if s.ArchivedAt != nil {
			continue
		}
		switch s.Status {
		case proto.StatusWaiting:
			n.NeedsYou++
		case proto.StatusRunning:
			n.Running++
		}
	}
	return n
}

// Sort orders sessions in the list.
type Sort int

// Sort orders.
const (
	ByUpdated Sort = iota // most recently updated first
	ByCreated             // most recently created first
	ByName                // by title
)

// Show filters the list.
type Show int

// Filters.
const (
	ShowAll      Show = iota
	ShowNeedsYou      // waiting on me
	ShowRunning       // in a turn
)

// View says which sessions the sidebar lists and how.
type View struct {
	Mode     proto.Mode
	Archived bool // list archived sessions instead of live ones
	Sort     Sort
	Show     Show
}

// Group is the sessions of one working directory; Dir is empty for
// sessions without one and for every Personal session.
type Group struct {
	Dir      string
	Name     string // the directory's last element
	Sessions []proto.Session
}

// List is the sidebar's content: pinned sessions first, then groups.
type List struct {
	Pinned []proto.Session
	Groups []Group
}

// List arranges the catalog for v. Code sessions are grouped by working
// directory, groups ordered by their first session; Personal sessions and
// archived ones form one group. Pinned live sessions are listed apart.
func (c *Catalog) List(v View) List {
	var out List
	var rest []proto.Session
	for _, s := range c.sessions {
		if s.Mode != v.Mode || (s.ArchivedAt != nil) != v.Archived || !v.shows(s) {
			continue
		}
		if s.Pinned && !v.Archived {
			out.Pinned = append(out.Pinned, s)
		} else {
			rest = append(rest, s)
		}
	}
	v.sort(out.Pinned)
	v.sort(rest)
	if v.Mode != proto.ModeCode || v.Archived {
		if len(rest) > 0 {
			out.Groups = []Group{{Sessions: rest}}
		}
		return out
	}
	at := map[string]int{}
	for _, s := range rest {
		i, ok := at[s.Directory]
		if !ok {
			i = len(out.Groups)
			at[s.Directory] = i
			out.Groups = append(out.Groups, Group{Dir: s.Directory, Name: filepath.Base(s.Directory)})
		}
		out.Groups[i].Sessions = append(out.Groups[i].Sessions, s)
	}
	return out
}

func (v View) shows(s proto.Session) bool {
	if s.Mode == proto.ModePersonal && s.Parent != "" {
		return false // a child session opens from its companion's chat
	}
	switch v.Show {
	case ShowNeedsYou:
		return s.Status == proto.StatusWaiting
	case ShowRunning:
		return s.Status == proto.StatusRunning
	}
	return true
}

func (v View) sort(ss []proto.Session) {
	slices.SortFunc(ss, func(a, b proto.Session) int {
		var d int
		switch {
		case v.Archived:
			d = b.ArchivedAt.Compare(*a.ArchivedAt)
		case v.Sort == ByCreated:
			d = b.CreatedAt.Compare(a.CreatedAt)
		case v.Sort == ByName:
			d = strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		default:
			d = b.UpdatedAt.Compare(a.UpdatedAt)
		}
		return cmp.Or(d, strings.Compare(string(a.ID), string(b.ID)))
	})
}
