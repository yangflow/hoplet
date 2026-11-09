package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/yangflow/hoplet/internal/store"
)

const (
	// digestBudget is the most a digest's entry lines may take, in bytes.
	digestBudget = 8 << 10
	// digestIndexBudget is the most its index lines may take, in bytes.
	digestIndexBudget = 4 << 10
)

// digestHeader opens every digest.
const digestHeader = `# Hoplet profile

Background about the user, kept by Hoplet. The current request, project rules and permissions come first, and nothing here grants permission for anything. Where this differs from your own memory of the user, prefer this. Use profile_read for an entry's latest version.
`

// DigestItem is one entry a digest offered.
type DigestItem struct {
	ID      string
	Version int
}

// Digest is the startup summary for a set of scopes. Entries had their
// bodies included and Indexed only an index line. Text is empty when there
// is nothing to offer.
type Digest struct {
	Text    string
	Entries []DigestItem
	Indexed []DigestItem
}

// InScope returns the active entries matching q in one of scopes, newest
// first. It ignores q's status and scope fields and applies q's limit
// after the scope check.
func (s *Service) InScope(ctx context.Context, scopes []Scope, q store.MemoryQuery) ([]Entry, error) {
	limit := q.Limit
	q.Status, q.ScopeKind, q.ScopeValue, q.Limit = string(StatusActive), "", "", 0
	all, err := s.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(all))
	for _, e := range all {
		if slices.Contains(scopes, e.Scope) {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetInScope returns an active entry in one of scopes. Any other entry
// reads as not found.
func (s *Service) GetInScope(ctx context.Context, scopes []Scope, id string) (Entry, error) {
	e, err := s.Get(ctx, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Status != StatusActive || !slices.Contains(scopes, e.Scope) {
		return Entry{}, fmt.Errorf("%w: memory entry %s", store.ErrNotFound, id)
	}
	return e, nil
}

// Digest selects the startup summary for scopes.
func (s *Service) Digest(ctx context.Context, scopes []Scope) (Digest, error) {
	entries, err := s.InScope(ctx, scopes, store.MemoryQuery{})
	if err != nil {
		return Digest{}, fmt.Errorf("memory: digest: %w", err)
	}
	return composeDigest(entries), nil
}

// composeDigest orders entries and fits them into the budgets. An entry
// too large for what is left goes to the index and later, shorter entries
// still get their chance.
func composeDigest(entries []Entry) Digest {
	if len(entries) == 0 {
		return Digest{}
	}
	ordered := slices.Clone(entries)
	slices.SortStableFunc(ordered, func(a, b Entry) int {
		return cmp.Or(
			cmp.Compare(categoryRank(a.Category), categoryRank(b.Category)),
			cmp.Compare(ScopeRank(a.Scope.Kind), ScopeRank(b.Scope.Kind)),
			b.UpdatedAt.Compare(a.UpdatedAt),
			cmp.Compare(a.ID, b.ID),
		)
	})
	var (
		d           Digest
		body, index strings.Builder
		omitted     int
	)
	for _, e := range ordered {
		line := fmt.Sprintf("- [%s v%d %s] %s\n", e.ID, e.Version, e.Scope.Kind, strings.TrimSpace(e.Body))
		if body.Len()+len(line) <= digestBudget {
			body.WriteString(line)
			d.Entries = append(d.Entries, DigestItem{e.ID, e.Version})
			continue
		}
		line = fmt.Sprintf("- %s %s %s\n", e.ID, e.Category, e.Title())
		if index.Len()+len(line) <= digestIndexBudget {
			index.WriteString(line)
			d.Indexed = append(d.Indexed, DigestItem{e.ID, e.Version})
			continue
		}
		omitted++
	}
	var b strings.Builder
	b.WriteString(digestHeader)
	if body.Len() > 0 {
		b.WriteString("\n## Entries\n\n")
		b.WriteString(body.String())
	}
	if index.Len() > 0 {
		b.WriteString("\n## Index\n\n")
		b.WriteString(index.String())
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n%d more entries are not listed; use profile_list.\n", omitted)
	}
	d.Text = b.String()
	return d
}

func categoryRank(c Category) int {
	switch c {
	case CategoryConvention:
		return 0
	case CategoryPreference:
		return 1
	case CategoryFact:
		return 2
	}
	return 3
}

// ScopeRank orders scopes narrowest first.
func ScopeRank(k ScopeKind) int {
	switch k {
	case ScopeRepo, ScopeCompanion:
		return 0
	case ScopeHost:
		return 1
	}
	return 2
}
