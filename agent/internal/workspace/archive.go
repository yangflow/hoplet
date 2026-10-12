package workspace

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The snapshot archive (docs/architecture.md#workspace-snapshots) is a gzip-compressed tar:
//
//	snapshot.json    SnapshotMeta
//	tracked.diff     binary diff of the tracked changes against the base commit
//	commits.bundle   commits the remote does not contain; present only when there are some
//	untracked/<path> every untracked file that is not ignored, with its mode; symbolic links as links
//
// Snapshots are at most 64 MiB compressed and 256 MiB uncompressed. Both bounds are enforced while the
// bytes are read and decompressed, never from a size an archive declares about itself.
const (
	MaxSnapshotCompressed   int64 = 64 << 20
	MaxSnapshotUncompressed int64 = 256 << 20

	metaName   = "snapshot.json"
	diffName   = "tracked.diff"
	bundleName = "commits.bundle"
	filePrefix = "untracked/"

	maxMetaBytes   = 64 << 10
	maxArchiveRows = 200_000
	snapshotFormat = 1
)

// SnapshotMeta is snapshot.json.
type SnapshotMeta struct {
	Version    int    `json:"version"`
	BaseCommit string `json:"baseCommit"`
	OriginURL  string `json:"originUrl"`
	// CreatedAt is the time of the snapshot in milliseconds since the epoch.
	CreatedAt int64 `json:"createdAt"`
	// Subdir is the session's working directory relative to the repository root; empty at the root.
	Subdir string `json:"subdir"`
	// TreeHash is the git tree id of the working tree the snapshot describes (TreeHash).
	TreeHash string `json:"treeHash"`
}

func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (m SnapshotMeta) validate() error {
	switch {
	case m.Version != snapshotFormat:
		return fmt.Errorf("%w: unsupported format", ErrCorrupt)
	case !isObjectID(m.BaseCommit) || !isObjectID(m.TreeHash):
		return fmt.Errorf("%w: object id", ErrCorrupt)
	case len(m.OriginURL) > 2048 || strings.ContainsAny(m.OriginURL, "\x00\r\n"):
		return fmt.Errorf("%w: origin", ErrCorrupt)
	case m.CreatedAt <= 0:
		return fmt.Errorf("%w: time", ErrCorrupt)
	case m.Subdir != "" && !localPath(m.Subdir):
		return fmt.Errorf("%w: subdirectory", ErrCorrupt)
	}
	return nil
}

// ArchiveFile is one untracked entry of an archive.
type ArchiveFile struct {
	Name string // slash separated, relative to the worktree
	Link string // target of a symbolic link, empty for a regular file
	Perm fs.FileMode
	Size int64
}

// Archive is a snapshot archive that passed inspection. Its tracked diff and bundle are staged as files
// beside it; the untracked files are still inside the archive.
type Archive struct {
	Path       string
	Meta       SnapshotMeta
	Files      []ArchiveFile
	DiffPath   string // staged tracked.diff, empty when the diff is empty
	BundlePath string // staged commits.bundle, empty when there is none
}

type entryKind int

const (
	kindMeta entryKind = iota
	kindDiff
	kindBundle
	kindFile
	kindLink
)

// budgetReader reads at most `left` bytes of r; the byte after them is an error rather than data.
type budgetReader struct {
	r    io.Reader
	left int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var one [1]byte
		n, err := b.r.Read(one[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.r.Read(p)
	b.left -= int64(n)
	return n, err
}

// corrupt marks an error as a damaged archive unless it already names a size violation.
func corrupt(err error) error {
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrCorrupt) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrCorrupt, err)
}

// scanArchive reads the whole archive once, validating every entry before calling visit. The reader passed
// to visit yields the entry's content; whatever visit leaves unread is skipped. Memory use does not depend
// on anything the archive declares.
func scanArchive(archivePath string, visit func(kind entryKind, name string, h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: not a file", ErrCorrupt)
	}
	if info.Size() > MaxSnapshotCompressed {
		return ErrTooLarge
	}
	// The size above can change while reading: the budget is what bounds the read.
	br := bufio.NewReader(&budgetReader{r: f, left: MaxSnapshotCompressed})
	gz, err := gzip.NewReader(br)
	if err != nil {
		return corrupt(err)
	}
	gz.Multistream(false)
	raw := &budgetReader{r: gz, left: MaxSnapshotUncompressed}
	tr := tar.NewReader(raw)

	seen := map[string]bool{}
	dirs := map[string]bool{}
	links := map[string]bool{}
	rows := 0
	for index := 0; ; index++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return corrupt(err)
		}
		rows++
		if rows > maxArchiveRows {
			return ErrTooLarge
		}
		kind, name, err := classify(h)
		if err != nil {
			return err
		}
		if (kind == kindMeta) != (index == 0) {
			return fmt.Errorf("%w: snapshot.json must be the first entry", ErrCorrupt)
		}
		key := h.Name
		if seen[key] {
			return fmt.Errorf("%w: duplicate entry", ErrCorrupt)
		}
		seen[key] = true
		if kind == kindFile || kind == kindLink {
			if err := checkPlacement(name, dirs, links, seen); err != nil {
				return err
			}
			if kind == kindLink {
				links[name] = true
			}
		}
		if err := visit(kind, name, h, tr); err != nil {
			return err
		}
	}
	// What follows the tar end marker must be nothing but zero padding, and the gzip stream must end there.
	if _, err := io.Copy(io.Discard, zeroOnly{raw}); err != nil {
		return corrupt(err)
	}
	if _, err := br.Peek(1); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: data after the archive", ErrCorrupt)
		}
		return corrupt(err)
	}
	return nil
}

// zeroOnly reads r and fails on any byte that is not zero.
type zeroOnly struct{ r io.Reader }

func (z zeroOnly) Read(p []byte) (int, error) {
	n, err := z.r.Read(p)
	for _, c := range p[:n] {
		if c != 0 {
			return 0, fmt.Errorf("%w: data after the end marker", ErrCorrupt)
		}
	}
	return n, err
}

func classify(h *tar.Header) (entryKind, string, error) {
	switch h.Name {
	case metaName:
		if h.Typeflag != tar.TypeReg || h.Size > maxMetaBytes {
			return 0, "", fmt.Errorf("%w: snapshot.json", ErrCorrupt)
		}
		return kindMeta, h.Name, nil
	case diffName:
		if h.Typeflag != tar.TypeReg {
			return 0, "", fmt.Errorf("%w: tracked.diff", ErrCorrupt)
		}
		return kindDiff, h.Name, nil
	case bundleName:
		if h.Typeflag != tar.TypeReg {
			return 0, "", fmt.Errorf("%w: commits.bundle", ErrCorrupt)
		}
		return kindBundle, h.Name, nil
	}
	rel, ok := strings.CutPrefix(h.Name, filePrefix)
	if !ok || !localPath(rel) {
		return 0, "", fmt.Errorf("%w: unexpected entry name", ErrCorrupt)
	}
	switch h.Typeflag {
	case tar.TypeReg:
		return kindFile, rel, nil
	case tar.TypeSymlink:
		if !safeLink(rel, h.Linkname) {
			return 0, "", fmt.Errorf("%w: symbolic link leaves the worktree", ErrCorrupt)
		}
		return kindLink, rel, nil
	}
	return 0, "", fmt.Errorf("%w: unsupported entry type", ErrCorrupt)
}

// checkPlacement refuses an entry that would be written through a link or over another entry's path.
func checkPlacement(name string, dirs, links, seen map[string]bool) error {
	for p := path.Dir(name); p != "."; p = path.Dir(p) {
		if links[p] || seen[filePrefix+p] {
			return fmt.Errorf("%w: entry below a link or a file", ErrCorrupt)
		}
		dirs[p] = true
	}
	if dirs[name] {
		return fmt.Errorf("%w: entry replaces a directory", ErrCorrupt)
	}
	return nil
}

// OpenArchive inspects the archive at archivePath completely, before anything is written to a worktree:
// sizes, names, link targets, tar structure and the gzip checksum. The tracked diff and the bundle are
// written into stage (a directory the caller owns). Any damaged or oversized archive is refused with
// ErrCorrupt or ErrTooLarge.
func OpenArchive(archivePath, stage string) (*Archive, error) {
	a := &Archive{Path: archivePath}
	err := scanArchive(archivePath, func(kind entryKind, name string, h *tar.Header, r io.Reader) error {
		switch kind {
		case kindMeta:
			dec := json.NewDecoder(io.LimitReader(r, maxMetaBytes+1))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&a.Meta); err != nil {
				return corrupt(err)
			}
			if err := a.Meta.validate(); err != nil {
				return err
			}
		case kindDiff, kindBundle:
			if h.Size == 0 {
				return nil
			}
			dest := filepath.Join(stage, name)
			out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, r)
			closeErr := out.Close()
			if copyErr != nil {
				return corrupt(copyErr)
			}
			if closeErr != nil {
				return closeErr
			}
			if kind == kindDiff {
				a.DiffPath = dest
			} else {
				a.BundlePath = dest
			}
		case kindFile:
			a.Files = append(a.Files, ArchiveFile{Name: name, Perm: fs.FileMode(h.Mode).Perm(), Size: h.Size})
		case kindLink:
			a.Files = append(a.Files, ArchiveFile{Name: name, Link: h.Linkname})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if a.Meta.Version == 0 {
		return nil, fmt.Errorf("%w: no snapshot.json", ErrCorrupt)
	}
	return a, nil
}

// ExtractUntracked writes the archive's untracked files and links into root. Every path is created through
// the root, so a name or a link that would leave the worktree fails instead of escaping; files are created
// exclusively, so nothing that is already there is overwritten. Call it only on an Archive that
// OpenArchive accepted.
func (a *Archive) ExtractUntracked(root *os.Root) error {
	return scanArchive(a.Path, func(kind entryKind, name string, h *tar.Header, r io.Reader) error {
		switch kind {
		case kindFile:
			if dir := path.Dir(name); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, r)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			return root.Chmod(name, fs.FileMode(h.Mode).Perm())
		case kindLink:
			if dir := path.Dir(name); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			return root.Symlink(h.Linkname, name)
		}
		return nil
	})
}
