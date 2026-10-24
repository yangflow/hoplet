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

// configWriter previews and applies the approved changes of a session to its own configuration files (ADR
// 0036). tested reports whether the vendor version that runs the session is one its driver was tested with:
// skills and commands are written only then, because their format is the vendor's and may change.
type configWriter struct {
	cfg    *config.Config
	writer configfiles.Writer
	tested func(sessionID, provider string) bool
}

func newConfigWriter(cfg *config.Config, tested func(sessionID, provider string) bool) configWriter {
	home, _ := os.UserHomeDir()
	return configWriter{cfg: cfg, writer: configfiles.Writer{Home: home}, tested: tested}
}

// configError is a writer refusal as the wire carries it.
func configError(err error) error {
	var refusal *configfiles.Error
	if errors.As(err, &refusal) {
		return &sync.ConfigError{Code: refusal.Code, Reason: refusal.Reason}
	}
	return &sync.ConfigError{Code: configfiles.CodeIO, Reason: err.Error()}
}

func (w configWriter) cwd(sessionID string) (string, error) {
	sess, err := locate.Locate(sessionID, finders(w.cfg)...)
	if err != nil {
		return "", &sync.ConfigError{Code: configfiles.CodeIO, Reason: err.Error()}
	}
	return sess.Cwd, nil
}

func (w configWriter) preview(_ context.Context, req sync.RuntimeConfigPreview) ([]sync.ConfigPreviewedObject, error) {
	cwd, err := w.cwd(req.SessionID)
	if err != nil {
		return nil, err
	}
	objects := make([]configfiles.Object, len(req.Objects))
	for i, o := range req.Objects {
		objects[i] = configfiles.Object{Kind: o.Kind, Name: o.Name, Content: o.Content}
	}
	got, err := w.writer.Preview(cwd, configfiles.Change{
		Provider: req.Provider,
		Tested:   w.tested(req.SessionID, req.Provider),
		Objects:  objects,
	})
	if err != nil {
		return nil, configError(err)
	}
	out := make([]sync.ConfigPreviewedObject, len(got))
	for i, p := range got {
		out[i] = sync.ConfigPreviewedObject{
			Kind:           p.Kind,
			Name:           p.Name,
			CurrentHash:    p.CurrentHash,
			Previous:       p.Previous,
			Diff:           p.Diff,
			NewDirectories: p.NewDirectories,
		}
	}
	return out, nil
}

func (w configWriter) apply(_ context.Context, req sync.RuntimeConfigApply) ([]sync.ConfigAppliedObject, error) {
	cwd, err := w.cwd(req.SessionID)
	if err != nil {
		return nil, err
	}
	objects := make([]configfiles.Object, len(req.Objects))
	for i, o := range req.Objects {
		objects[i] = configfiles.Object{Kind: o.Kind, Name: o.Name, Content: o.Content, BaseHash: o.BaseHash, NewHash: o.NewHash}
	}
	c := configfiles.Change{Provider: req.Provider, Restore: req.Restore, Objects: objects}
	if !req.Restore {
		c.Tested = w.tested(req.SessionID, req.Provider)
	}
	got, err := w.writer.Apply(cwd, c)
	if err != nil {
		return nil, configError(err)
	}
	out := make([]sync.ConfigAppliedObject, len(got))
	for i, a := range got {
		out[i] = sync.ConfigAppliedObject{Kind: a.Kind, Name: a.Name, Hash: a.Hash}
	}
	return out, nil
}

// versionTested reports whether the configuration that runs the session (the vendor's built-in when there is
// none) reported a vendor version its driver was tested with. An unknown version is not a tested one.
func (b *bridge) versionTested(sessionID, provider string) bool {
	id := b.sessionConfiguration(sessionID)
	if id == "" {
		id = provider
	}
	e := b.entry(id)
	if e == nil {
		return false
	}
	conf := e.config()
	if conf.Extends != provider {
		return false
	}
	e.check(conf.Enabled)
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tested != nil && *e.tested
}
