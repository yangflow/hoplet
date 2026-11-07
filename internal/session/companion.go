package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

const (
	// personaFile is the companion's persona, in its directory.
	personaFile = "persona.md"
	// MaxPersona is the largest persona, in bytes.
	MaxPersona = 64 << 10
)

func (m *Manager) companionDir(id proto.CompanionID) string {
	return filepath.Join(m.cfg.Companions, string(id))
}

// newCompanionDir creates a companion's directory with an empty persona.
func newCompanionDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("session: create companion dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Clean(filepath.Join(dir, personaFile)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("session: create persona: %w", err)
	}
	return f.Close()
}

// companion is the record clients see; the daemon adds the host.
func (m *Manager) companion(c store.Companion) *proto.Companion {
	pc := c.Companion
	pc.Dir = m.companionDir(c.ID)
	return &pc
}

// CreateCompanion stores a new companion and its main session; req's mode
// is personal and it has no directory.
func (m *Manager) CreateCompanion(ctx context.Context, req CreateRequest) (proto.Session, proto.Companion, error) {
	if req.Directory != "" || req.Isolation != "" {
		return proto.Session{}, proto.Companion{}, fmt.Errorf("%w: a companion has no project directory", store.ErrInvalid)
	}
	req.Mode = proto.ModePersonal
	sess, c, err := m.create(ctx, req)
	if err != nil {
		return proto.Session{}, proto.Companion{}, err
	}
	return sess, *c, nil
}

// Companions lists companions, oldest first; all includes those whose main
// session is archived.
func (m *Manager) Companions(ctx context.Context, all bool) ([]proto.Companion, error) {
	cs, err := m.cfg.Store.Companions(ctx)
	if err != nil {
		return nil, err
	}
	out := []proto.Companion{}
	for _, c := range cs {
		if all || c.ArchivedAt == nil {
			out = append(out, *m.companion(c))
		}
	}
	return out, nil
}

// Companion returns the companion named by id, its ID or a unique prefix
// of at least 4 characters.
func (m *Manager) Companion(ctx context.Context, id string) (proto.Companion, error) {
	c, err := m.cfg.Store.Companion(ctx, id)
	if err != nil {
		return proto.Companion{}, err
	}
	return *m.companion(c), nil
}

// Persona returns a companion's persona; a companion without persona.md
// has an empty one.
func (m *Manager) Persona(ctx context.Context, id string) (proto.Persona, error) {
	c, err := m.cfg.Store.Companion(ctx, id)
	if err != nil {
		return proto.Persona{}, err
	}
	text, err := readPersona(m.companionDir(c.ID))
	if err != nil {
		return proto.Persona{}, err
	}
	return proto.Persona{ID: c.ID, Text: text}, nil
}

// SetPersona replaces a companion's persona and says when the change takes
// effect: at the main session's next start, or, for a harness that keeps
// the persona its native session began with, only in a new main session.
func (m *Manager) SetPersona(ctx context.Context, id, text string) (proto.PersonaApplies, error) {
	if err := checkPersona([]byte(text)); err != nil {
		return "", err
	}
	c, err := m.cfg.Store.Companion(ctx, id)
	if err != nil {
		return "", err
	}
	dir := m.companionDir(c.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session: create companion dir: %w", err)
	}
	if err := writeAtomic(dir, personaFile, []byte(text)); err != nil {
		return "", err
	}
	if k, ok := m.cfg.Harnesses[c.Harness.Kind].(harness.PersonaKeeper); ok && k.KeepsPersona() && c.Harness.NativeID != "" {
		return proto.AppliesNewMainSession, nil
	}
	return proto.AppliesNextLaunch, nil
}

// launchPersona is the persona a companion's main session starts with. A
// companion that came from an existing session gets its directory here.
func (m *Manager) launchPersona(ctx context.Context, id proto.SessionID) (string, error) {
	c, err := m.cfg.Store.CompanionByMain(ctx, id)
	if err != nil {
		return "", err
	}
	dir := m.companionDir(c.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session: create companion dir: %w", err)
	}
	return readPersona(dir)
}

func readPersona(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, personaFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("session: read persona: %w", err)
	}
	if err := checkPersona(b); err != nil {
		return "", err
	}
	return string(b), nil
}

func checkPersona(b []byte) error {
	if len(b) > MaxPersona {
		return fmt.Errorf("%w: %s is %d bytes, over the limit of %d", store.ErrInvalid, personaFile, len(b), MaxPersona)
	}
	if !utf8.Valid(b) {
		return fmt.Errorf("%w: %s is not UTF-8 text", store.ErrInvalid, personaFile)
	}
	return nil
}

// writeWorkFiles writes a launch's work files, mode 0600, into dir, each
// replacing its file whole.
func writeWorkFiles(dir string, files map[string][]byte) error {
	for name, b := range files {
		if name == "" || filepath.Base(name) != name {
			return fmt.Errorf("session: work file %q is not a plain name", name)
		}
		if err := writeAtomic(dir, name, b); err != nil {
			return err
		}
	}
	return nil
}

// writeAtomic replaces dir/name with b, mode 0600, through a temporary
// file, so a reader sees the old content or the new, never a part.
func writeAtomic(dir, name string, b []byte) error {
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("session: write %s: %w", name, err)
	}
	tmp := f.Name()
	_, err = f.Write(b)
	err = errors.Join(err, f.Chmod(0o600), f.Close())
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		return errors.Join(fmt.Errorf("session: write %s: %w", name, err), os.Remove(tmp))
	}
	return nil
}
