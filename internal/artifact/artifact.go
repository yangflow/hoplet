// Package artifact publishes and reads session artifacts: versioned files
// a session hands over for viewing. Each version is copied into the
// daemon's own directory, so it never changes after it is published.
package artifact

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/yangflow/hoplet/internal/store"
	"github.com/yangflow/hoplet/proto"
)

// Limits on one version.
const (
	MaxFiles   = 64
	MaxSize    = 16 << 20
	MaxNameLen = 128
)

// Errors a publish or read fails with besides the store's.
var (
	// ErrInvalid is a request that breaks a publishing rule: a bad name,
	// an unsupported kind, a file outside the entry's directory, a limit.
	ErrInvalid = errors.New("artifact: invalid")
	// ErrNotFound is a file a version does not have.
	ErrNotFound = errors.New("artifact: not found")
)

// Store is what the service keeps in the daemon's store; *store.Store
// implements it.
type Store interface {
	Session(ctx context.Context, id proto.SessionID) (proto.Session, error)
	PublishArtifact(ctx context.Context, v proto.Artifact) (proto.Artifact, error)
	ArtifactByName(ctx context.Context, session proto.SessionID, name string) (proto.Artifact, error)
	ArtifactVersion(ctx context.Context, id proto.ArtifactID, n int) (proto.Artifact, error)
	ArtifactVersions(ctx context.Context, session proto.SessionID) (map[proto.ArtifactID][]int, error)
}

// Service publishes and reads artifacts. Publishes are serialized.
type Service struct {
	Store Store
	// Root holds the files, as <session>/<artifact>/v<n>/.
	Root string
	Log  *slog.Logger

	mu sync.Mutex
}

// Request is one publish. Entry and Files are absolute paths on this
// machine; Files go with an HTML entry only and sit under its directory.
type Request struct {
	Session proto.SessionID
	Name    string
	Entry   string
	Files   []string
	By      proto.ArtifactSource
}

// kinds maps an entry's lowercase extension to its kind.
var kinds = map[string]proto.ArtifactKind{
	".html": proto.ArtifactHTML, ".htm": proto.ArtifactHTML,
	".md": proto.ArtifactMarkdown, ".markdown": proto.ArtifactMarkdown,
	".png": proto.ArtifactImage, ".jpg": proto.ArtifactImage, ".jpeg": proto.ArtifactImage,
	".gif": proto.ArtifactImage, ".webp": proto.ArtifactImage,
	".csv": proto.ArtifactCSV,
	".txt": proto.ArtifactText,
}

// KindOf returns the kind of an artifact whose entry is named name.
func KindOf(name string) (proto.ArtifactKind, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if k, ok := kinds[ext]; ok {
		return k, nil
	}
	return "", fmt.Errorf("%w: unsupported entry type %q: use .html, .md, .png, .jpg, .gif, .webp, .csv or .txt", ErrInvalid, ext)
}

// CleanName returns name without surrounding space, or an error when it is
// not a valid artifact name.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: an artifact needs a name", ErrInvalid)
	case len(name) > MaxNameLen:
		return "", fmt.Errorf("%w: artifact names are at most %d bytes", ErrInvalid, MaxNameLen)
	case !utf8.ValidString(name):
		return "", fmt.Errorf("%w: artifact names are UTF-8", ErrInvalid)
	case strings.ContainsRune(name, '/'):
		return "", fmt.Errorf("%w: artifact names may not contain /", ErrInvalid)
	case strings.ContainsFunc(name, unicode.IsControl):
		return "", fmt.Errorf("%w: artifact names may not contain control characters", ErrInvalid)
	}
	return name, nil
}

// source is one file of a version: where it is read from and the path it
// is stored at, relative to the version's directory.
type source struct {
	from, rel string
}

// Publish copies the request's files into a new version and records it.
// The whole publish fails, leaving no files, when any rule is broken.
func (s *Service) Publish(ctx context.Context, req Request) (proto.Artifact, error) {
	name, err := CleanName(req.Name)
	if err != nil {
		return proto.Artifact{}, err
	}
	srcs, kind, err := sources(req.Entry, req.Files)
	if err != nil {
		return proto.Artifact{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	v := proto.Artifact{Session: req.Session, Name: name, Kind: kind, Entry: srcs[0].rel, By: req.By, Version: 1}
	cur, err := s.Store.ArtifactByName(ctx, req.Session, name)
	switch {
	case err == nil:
		if cur.Kind != kind {
			return proto.Artifact{}, fmt.Errorf("%w: artifact %q is %s, not %s", ErrInvalid, name, cur.Kind, kind)
		}
		v.ID, v.Version = cur.ID, cur.Version+1
	case errors.Is(err, store.ErrNotFound):
		if v.ID, err = newID(); err != nil {
			return proto.Artifact{}, err
		}
	default:
		return proto.Artifact{}, err
	}
	if sess, err := s.Store.Session(ctx, req.Session); err != nil {
		return proto.Artifact{}, err
	} else if sess.ArchivedAt != nil {
		return proto.Artifact{}, fmt.Errorf("%w: session %s is archived", ErrInvalid, req.Session)
	}

	dir := filepath.Join(s.Root, string(req.Session), string(v.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return proto.Artifact{}, fmt.Errorf("artifact: %w", err)
	}
	tmp, err := os.MkdirTemp(dir, ".tmp-")
	if err != nil {
		return proto.Artifact{}, fmt.Errorf("artifact: %w", err)
	}
	if v.Size, err = copyAll(tmp, srcs); err != nil {
		return proto.Artifact{}, errors.Join(err, os.RemoveAll(tmp))
	}
	v.Files = len(srcs)
	final := filepath.Join(dir, "v"+strconv.Itoa(v.Version))
	// A directory left at final has no record: a crash came between its
	// rename and its record.
	if err := os.RemoveAll(final); err != nil {
		return proto.Artifact{}, errors.Join(fmt.Errorf("artifact: %w", err), os.RemoveAll(tmp))
	}
	if err := os.Rename(tmp, final); err != nil {
		return proto.Artifact{}, errors.Join(fmt.Errorf("artifact: %w", err), os.RemoveAll(tmp))
	}
	if err := syncDir(dir); err != nil {
		return proto.Artifact{}, errors.Join(err, os.RemoveAll(final))
	}
	out, err := s.Store.PublishArtifact(ctx, v)
	if err != nil {
		return proto.Artifact{}, errors.Join(err, os.RemoveAll(final))
	}
	return out, nil
}

// sources checks a request's paths and returns its files, the entry first,
// with the artifact's kind.
func sources(entry string, files []string) ([]source, proto.ArtifactKind, error) {
	if !filepath.IsAbs(entry) {
		return nil, "", fmt.Errorf("%w: entry %q is not an absolute path", ErrInvalid, entry)
	}
	entry = filepath.Clean(entry)
	kind, err := KindOf(entry)
	if err != nil {
		return nil, "", err
	}
	if len(files) > 0 && kind != proto.ArtifactHTML {
		return nil, "", fmt.Errorf("%w: only an HTML artifact can carry other files", ErrInvalid)
	}
	if 1+len(files) > MaxFiles {
		return nil, "", fmt.Errorf("%w: a version has at most %d files", ErrInvalid, MaxFiles)
	}
	base := filepath.Dir(entry)
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	srcs := []source{{from: entry, rel: filepath.Base(entry)}}
	seen := map[string]bool{srcs[0].rel: true}
	for _, f := range files {
		if !filepath.IsAbs(f) {
			return nil, "", fmt.Errorf("%w: file %q is not an absolute path", ErrInvalid, f)
		}
		f = filepath.Clean(f)
		rel, err := filepath.Rel(base, f)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, "", fmt.Errorf("%w: %s is not under the entry's directory %s", ErrInvalid, f, base)
		}
		real, err := filepath.EvalSymlinks(f)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if r, err := filepath.Rel(realBase, real); err != nil || !filepath.IsLocal(r) {
			return nil, "", fmt.Errorf("%w: %s leads outside the entry's directory %s", ErrInvalid, f, base)
		}
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return nil, "", fmt.Errorf("%w: %s is given twice", ErrInvalid, rel)
		}
		seen[rel] = true
		srcs = append(srcs, source{from: f, rel: rel})
	}
	return srcs, kind, nil
}

// copyAll copies srcs into dir and returns their total size. It stops as
// soon as the total passes MaxSize.
func copyAll(dir string, srcs []source) (int64, error) {
	var total int64
	for _, src := range srcs {
		n, err := copyFile(filepath.Join(dir, filepath.FromSlash(src.rel)), src.from, MaxSize-total)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, syncTree(dir)
}

// copyFile copies the regular file from to to, failing when it holds more
// than budget bytes.
func copyFile(to, from string, budget int64) (n int64, err error) {
	in, err := os.Open(filepath.Clean(from))
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return 0, fmt.Errorf("artifact: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, from)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return 0, fmt.Errorf("artifact: %w", err)
	}
	out, err := os.OpenFile(filepath.Clean(to), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("artifact: %w", err)
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	n, err = io.Copy(out, io.LimitReader(in, budget+1))
	if err != nil {
		return 0, fmt.Errorf("artifact: copy %s: %w", from, err)
	}
	if n > budget {
		return 0, fmt.Errorf("%w: a version holds at most %d MiB", ErrInvalid, MaxSize>>20)
	}
	if err := out.Sync(); err != nil {
		return 0, fmt.Errorf("artifact: %w", err)
	}
	return n, nil
}

// syncTree syncs every directory under and including dir.
func syncTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("artifact: %w", err)
		}
		if d.IsDir() {
			return syncDir(p)
		}
		return nil
	})
}

func syncDir(dir string) error {
	f, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("artifact: %w", err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("artifact: sync %s: %w", dir, err)
	}
	return nil
}

func newID() (proto.ArtifactID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("artifact: new id: %w", err)
	}
	return proto.ArtifactID(hex.EncodeToString(b[:])), nil
}

// Read returns up to proto.MaxArtifactChunk bytes of a version's file from
// p.Offset. Paths outside the version and files it lacks are not found.
func (s *Service) Read(ctx context.Context, p proto.ArtifactsReadParams) (proto.ArtifactChunk, error) {
	if p.Version < 0 || p.Offset < 0 {
		return proto.ArtifactChunk{}, fmt.Errorf("%w: negative version or offset", ErrInvalid)
	}
	a, err := s.Store.ArtifactVersion(ctx, p.Artifact, p.Version)
	if err != nil {
		return proto.ArtifactChunk{}, err
	}
	rel := p.Path
	if rel == "" {
		rel = a.Entry
	}
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return proto.ArtifactChunk{}, fmt.Errorf("%w: %s", ErrNotFound, p.Path)
	}
	vdir := filepath.Join(s.Root, string(a.Session), string(a.ID), "v"+strconv.Itoa(a.Version))
	f, err := os.OpenInRoot(vdir, filepath.FromSlash(rel))
	if errors.Is(err, os.ErrNotExist) {
		return proto.ArtifactChunk{}, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if err != nil {
		return proto.ArtifactChunk{}, fmt.Errorf("artifact: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return proto.ArtifactChunk{}, fmt.Errorf("artifact: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return proto.ArtifactChunk{}, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if p.Offset > fi.Size() {
		return proto.ArtifactChunk{}, fmt.Errorf("%w: offset %d is past the end of %s (%d bytes)", ErrInvalid, p.Offset, rel, fi.Size())
	}
	buf := make([]byte, min(fi.Size()-p.Offset, proto.MaxArtifactChunk))
	if _, err := f.ReadAt(buf, p.Offset); err != nil && !errors.Is(err, io.EOF) {
		return proto.ArtifactChunk{}, fmt.Errorf("artifact: read %s: %w", rel, err)
	}
	ct := mime.TypeByExtension(path.Ext(rel))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return proto.ArtifactChunk{Data: buf, Size: fi.Size(), ContentType: ct}, nil
}

// Clean removes what an interrupted publish left behind: temporary
// directories, and version directories with no record. The daemon calls it
// at start, before any publish.
func (s *Service) Clean(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("artifact: %w", err)
	}
	for _, sd := range sessions {
		if !sd.IsDir() {
			continue
		}
		recorded, err := s.Store.ArtifactVersions(ctx, proto.SessionID(sd.Name()))
		if err != nil {
			return err
		}
		arts, err := os.ReadDir(filepath.Join(s.Root, sd.Name()))
		if err != nil {
			return fmt.Errorf("artifact: %w", err)
		}
		for _, ad := range arts {
			if !ad.IsDir() {
				continue
			}
			adir := filepath.Join(s.Root, sd.Name(), ad.Name())
			entries, err := os.ReadDir(adir)
			if err != nil {
				return fmt.Errorf("artifact: %w", err)
			}
			for _, e := range entries {
				if !leftover(e.Name(), recorded[proto.ArtifactID(ad.Name())]) {
					continue
				}
				if err := os.RemoveAll(filepath.Join(adir, e.Name())); err != nil {
					return fmt.Errorf("artifact: %w", err)
				}
				if s.Log != nil {
					s.Log.Info("removed unfinished artifact files", "session", sd.Name(), "artifact", ad.Name(), "name", e.Name())
				}
			}
		}
	}
	return nil
}

// leftover reports whether name, an entry of an artifact's directory, is
// a temporary directory or a version directory with no record.
func leftover(name string, versions []int) bool {
	if strings.HasPrefix(name, ".tmp-") {
		return true
	}
	n, ok := strings.CutPrefix(name, "v")
	if !ok {
		return false
	}
	v, err := strconv.Atoi(n)
	return err == nil && !slices.Contains(versions, v)
}
