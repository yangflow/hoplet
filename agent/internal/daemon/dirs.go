package daemon

import (
	"context"

	"github.com/yangflow/hoplet/agent/internal/browse"
	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

// dirsList handles device.dirs.list: the directories under the browse root, names only.
func dirsList(cfg *config.Config) func(context.Context, string) (sync.DirListing, error) {
	return func(_ context.Context, path string) (sync.DirListing, error) {
		l, err := browse.List(cfg.BrowseRoot, path)
		if err != nil {
			return sync.DirListing{}, err
		}
		entries := make([]sync.DirEntry, len(l.Entries))
		for i, e := range l.Entries {
			entries[i] = sync.DirEntry{Name: e.Name, Git: e.Git}
		}
		return sync.DirListing{
			Path: l.Path, Parent: l.Parent, Root: l.Root, Entries: entries, Truncated: l.Truncated,
		}, nil
	}
}

// cloneFind handles device.clone.find: the clone among the watched projects that a handoff would use.
func cloneFind(st *store) func(context.Context, string) (string, error) {
	return func(ctx context.Context, origin string) (string, error) {
		return workspace.FindClone(ctx, st.watchedPaths(), origin), nil
	}
}
