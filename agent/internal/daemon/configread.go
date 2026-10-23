package daemon

import (
	"context"
	"errors"
	"os"

	"github.com/yangflow/hoplet/agent/internal/config"
	"github.com/yangflow/hoplet/agent/internal/configfiles"
	"github.com/yangflow/hoplet/agent/internal/locate"
	"github.com/yangflow/hoplet/agent/internal/sync"
)

// configRead answers a read of the session's own configuration files (ADR 0036). The project scope is the
// session's working directory, the user scope the default provider directory of the home directory.
func configRead(cfg *config.Config) sync.ConfigReadFunc {
	home, _ := os.UserHomeDir()
	reader := configfiles.Reader{Home: home}
	return func(_ context.Context, req sync.SessionConfigRead) (sync.SessionConfig, error) {
		sess, err := locate.Locate(req.SessionID, finders(cfg)...)
		if err != nil {
			return sync.SessionConfig{}, &sync.ConfigError{Code: configfiles.CodeIO, Reason: err.Error()}
		}
		page, err := reader.Read(sess.Cwd, configfiles.Request{
			Provider: req.Provider,
			Kind:     req.Kind,
			Scope:    req.Scope,
			Name:     req.Name,
			Offset:   req.Offset,
		})
		if err != nil {
			var refusal *configfiles.Error
			if errors.As(err, &refusal) {
				return sync.SessionConfig{}, &sync.ConfigError{Code: refusal.Code, Reason: refusal.Reason}
			}
			return sync.SessionConfig{}, &sync.ConfigError{Code: configfiles.CodeIO, Reason: err.Error()}
		}
		entries := make([]sync.ConfigFileEntry, len(page.Entries))
		for i, e := range page.Entries {
			entries[i] = sync.ConfigFileEntry{Kind: e.Kind, Name: e.Name, Description: e.Description}
		}
		return sync.SessionConfig{
			Exists:     page.Exists,
			Size:       page.Size,
			Hash:       page.Hash,
			Offset:     page.Offset,
			Text:       page.Text,
			NextOffset: page.NextOffset,
			Entries:    entries,
		}, nil
	}
}
