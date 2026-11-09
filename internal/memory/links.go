package memory

import (
	"context"
	"fmt"
	"slices"

	"github.com/yangflow/hoplet/internal/store"
)

func (e Entry) checkLinks() error {
	for _, ids := range [][]string{e.Links, e.Unlinked} {
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if !idPattern.MatchString(id) || id == e.ID || seen[id] {
				return invalid("bad link %q on entry %s", id, e.ID)
			}
			seen[id] = true
		}
	}
	return nil
}

// addLinks adds ids to the entry's links, except those it has or the user
// removed.
func (e *Entry) addLinks(ids []string) {
	for _, id := range ids {
		if id != e.ID && !slices.Contains(e.Links, id) && !slices.Contains(e.Unlinked, id) {
			e.Links = append(e.Links, id)
		}
	}
}

// Unlink removes the link between two entries as the user. Extraction will
// not make it again from either side. Repeating it changes nothing; opID
// makes an interrupted one finish without writing twice.
func (s *Service) Unlink(ctx context.Context, id, other, opID string) error {
	if opID == "" || id == other {
		return invalid("an unlink needs an operation ID and two entries")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, pair := range [][2]string{{id, other}, {other, id}} {
		holder, peer := pair[0], pair[1]
		e, err := s.Get(ctx, holder)
		if err != nil {
			return err
		}
		if !slices.Contains(e.Links, peer) {
			continue
		}
		_, err = s.step(ctx, fmt.Sprintf("%s-%d", opID, i+1), func() (Op, error) {
			e.Links = slices.DeleteFunc(e.Links, func(l string) bool { return l == peer })
			if len(e.Links) == 0 {
				e.Links = nil
			}
			e.Unlinked = append(e.Unlinked, peer)
			return Op{Entry: e, ExpectedVersion: e.Version, Reason: "unlinked " + peer, LinksOnly: true}, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Links returns each usable entry's related entries, from both directions.
// A link to an entry that is not in use is not shown.
func (s *Service) Links(ctx context.Context) (map[string][]string, error) {
	entries, err := s.Search(ctx, store.MemoryQuery{})
	if err != nil {
		return nil, err
	}
	usable := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if e.Status == StatusActive || e.Status == StatusStale {
			usable[e.ID] = e
		}
	}
	out := make(map[string][]string, len(usable))
	add := func(a, b string) {
		if !slices.Contains(out[a], b) {
			out[a] = append(out[a], b)
		}
	}
	for _, e := range usable {
		for _, peer := range e.Links {
			if p, ok := usable[peer]; ok && !slices.Contains(p.Unlinked, e.ID) {
				add(e.ID, peer)
				add(peer, e.ID)
			}
		}
	}
	for id := range out {
		slices.Sort(out[id])
	}
	return out, nil
}
