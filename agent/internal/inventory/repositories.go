package inventory

import (
	"context"
	"strings"

	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// DefaultOrigin reads the origin remote of the repository that contains dir.
func DefaultOrigin(ctx context.Context, dir string) string {
	return workspace.Origin(ctx, dir)
}

// cleanOrigin leaves a remote URL as host and path: no user, password, query or fragment.
func cleanOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return clip(workspace.WithoutUserinfo(raw), originMax)
}

// detectRepositories reads the origin of each project, concurrently and bounded, in the order given; paths
// beyond the limit are left out. A path is reported even when it has no origin.
func detectRepositories(ctx context.Context, paths []string, origin func(ctx context.Context, dir string) string) []Repository {
	if origin == nil {
		origin = DefaultOrigin
	}
	if len(paths) > MaxRepositories {
		paths = paths[:MaxRepositories]
	}
	repos := make([]Repository, len(paths))
	forEach(len(paths), func(i int) {
		repos[i].Path = clip(paths[i], pathMax)
		cctx, cancel := context.WithTimeout(ctx, commandTimeout)
		defer cancel()
		if o := cleanOrigin(origin(cctx, paths[i])); o != "" {
			repos[i].Origin = &o
		}
	})
	return repos
}
