package daemon

import (
	"context"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/sync"
	"github.com/yangflow/hoplet/agent/internal/workspace"
)

func workspaceList(cfg *config.Config) sync.WorkspaceFunc {
	return func(wctx context.Context, sessionID string) (sync.WorkspaceListing, error) {
		listing, err := workspace.List(wctx, finders(cfg), sessionID)
		if err != nil {
			return sync.WorkspaceListing{}, err
		}
		changes := make([]sync.WorkspaceChange, len(listing.Changes))
		for i, c := range listing.Changes {
			changes[i] = sync.WorkspaceChange{Path: c.Path, Status: c.Status, Added: c.Added, Removed: c.Removed, Binary: c.Binary}
		}
		return sync.WorkspaceListing{
			Git:       listing.Git,
			Commit:    listing.Commit,
			Origin:    listing.Origin,
			Paths:     listing.Paths,
			Truncated: listing.Truncated,
			Changes:   changes,
		}, nil
	}
}

func workspaceFile(cfg *config.Config) sync.WorkspaceFileFunc {
	return func(fctx context.Context, sessionID, rel, at string) (sync.WorkspaceFileContent, error) {
		file, err := workspace.Read(fctx, finders(cfg), sessionID, rel, at)
		if err != nil {
			return sync.WorkspaceFileContent{}, err
		}
		return sync.WorkspaceFileContent{
			Kind:      file.Kind,
			Size:      file.Size,
			Text:      file.Text,
			Truncated: file.Truncated,
			MediaType: file.MediaType,
			Data:      file.Data,
		}, nil
	}
}

func workspaceDiff(cfg *config.Config) sync.WorkspaceDiffFunc {
	return func(dctx context.Context, sessionID, rel string) (sync.WorkspaceDiffContent, error) {
		diff, err := workspace.ReadDiff(dctx, finders(cfg), sessionID, rel)
		if err != nil {
			return sync.WorkspaceDiffContent{}, err
		}
		return sync.WorkspaceDiffContent{
			Status:    diff.Status,
			Kind:      diff.Kind,
			Diff:      diff.Text,
			Truncated: diff.Truncated,
			Size:      diff.Size,
			OldSize:   diff.OldSize,
			NewSize:   diff.NewSize,
		}, nil
	}
}
