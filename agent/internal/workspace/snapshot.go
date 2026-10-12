package workspace

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Snapshot is a workspace snapshot archive on disk, ready to be uploaded.
type Snapshot struct {
	ID     string // unique per snapshot; a valid key of the core's snapshot files
	Path   string
	Size   int64 // compressed size of the archive
	SHA256 string
	Meta   SnapshotMeta
}

// limitWriter fails with ErrTooLarge instead of writing past `left` bytes.
type limitWriter struct {
	w    io.Writer
	left int64
	over bool
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		l.over = true
		return 0, ErrTooLarge
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}

// repository returns the repository root of cwd and cwd relative to it, both as real paths.
func repository(ctx context.Context, cwd string) (top, subdir string, err error) {
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", "", err
	}
	out, err := gitLine(ctx, real, "rev-parse", "--show-toplevel")
	if err != nil || out == "" {
		return "", "", ErrNotGitRepo
	}
	top, err = filepath.EvalSymlinks(out)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(top, real)
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return "", "", ErrNotGitRepo
	}
	if rel == "." {
		rel = ""
	}
	return top, filepath.ToSlash(rel), nil
}

// originURL is the URL of the remote named origin without the user and password a URL can carry, empty when
// there is none. Clones are matched by host and path, so nothing needs the credentials.
func originURL(ctx context.Context, top string) string {
	out, err := gitLine(ctx, top, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return withoutUserinfo(out)
}

var urlUserinfo = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^@/]*@`)

// withoutUserinfo drops `user:password@` from a URL such as https://user:token@host/o/r.git; an scp-like
// `git@host:o/r.git` names only a user and stays as it is.
func withoutUserinfo(raw string) string {
	return urlUserinfo.ReplaceAllString(raw, "$1")
}

func newSnapshotID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CreateSnapshot captures the working tree of the repository that contains cwd into an archive in outDir:
// the base commit (HEAD), the URL of `origin`, the binary diff of tracked changes against the base commit,
// the untracked files that are not ignored with their modes, and a bundle of the commits that `origin`
// (as last fetched) does not contain. The repository is only read. It fails with ErrTooLarge when the
// archive would pass the size limits, and with ErrWorkspaceMoved when the tree changed while it was read.
func CreateSnapshot(ctx context.Context, cwd, outDir string) (*Snapshot, error) {
	top, subdir, err := repository(ctx, cwd)
	if err != nil {
		return nil, err
	}
	base, err := gitLine(ctx, top, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, ErrNoCommit
	}
	tree, err := TreeHash(ctx, top)
	if err != nil {
		return nil, err
	}
	id, err := newSnapshotID()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(outDir, "stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	// The prefixes are explicit so the user's diff.noprefix or diff.mnemonicPrefix cannot change what
	// `git apply` and the core's reader expect.
	diffPath := filepath.Join(stage, diffName)
	if err := writeGit(ctx, top, diffPath, "diff", "--binary", "--full-index", "--no-renames", "--no-color",
		"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "HEAD"); err != nil {
		return nil, err
	}
	bundlePath, err := bundleUnpushed(ctx, top, filepath.Join(stage, bundleName))
	if err != nil {
		return nil, err
	}
	untracked, err := untrackedFiles(ctx, top)
	if err != nil {
		return nil, err
	}

	meta := SnapshotMeta{
		Version:    snapshotFormat,
		BaseCommit: base,
		OriginURL:  originURL(ctx, top),
		CreatedAt:  time.Now().UnixMilli(),
		Subdir:     subdir,
		TreeHash:   tree,
	}
	final := filepath.Join(outDir, "snapshot-"+id+".tar.gz")
	partial := final + ".part"
	sum, size, err := writeArchive(top, partial, meta, diffPath, bundlePath, untracked)
	if err != nil {
		os.Remove(partial)
		return nil, err
	}
	// The tree must still be the one that was described; a change in between would make the archive
	// disagree with its own hash.
	again, err := TreeHash(ctx, top)
	if err != nil || again != tree {
		os.Remove(partial)
		if err != nil {
			return nil, err
		}
		return nil, ErrWorkspaceMoved
	}
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return nil, err
	}
	return &Snapshot{ID: id, Path: final, Size: size, SHA256: sum, Meta: meta}, nil
}

// writeGit runs a git command and stores its standard output in dest, refusing more than the uncompressed
// limit.
func writeGit(ctx context.Context, dir, dest string, args ...string) error {
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	lw := &limitWriter{w: out, left: MaxSnapshotUncompressed}
	cmd := gitCommand(ctx, dir, args...)
	cmd.Stdout = lw
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if lw.over {
			return ErrTooLarge
		}
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// bundleUnpushed writes a bundle of HEAD's commits that no remote-tracking branch of origin contains and
// returns its path, or "" when there are none or origin was never fetched.
func bundleUnpushed(ctx context.Context, top, dest string) (string, error) {
	refs, err := gitLine(ctx, top, "for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes/origin")
	if err != nil || refs == "" {
		return "", nil
	}
	count, err := gitLine(ctx, top, "rev-list", "--count", "HEAD", "--not", "--remotes=origin")
	if err != nil || count == "0" {
		return "", err
	}
	if _, err := git(ctx, top, "bundle", "create", dest, "HEAD", "--not", "--remotes=origin"); err != nil {
		return "", err
	}
	return dest, nil
}

// writeArchive writes the archive to dest and returns its SHA-256 and size. The compressed size and the
// uncompressed size are bounded as they are written.
func writeArchive(top, dest string, meta SnapshotMeta, diffPath, bundlePath string, files []untrackedFile) (string, int64, error) {
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	hash := sha256.New()
	compressed := &limitWriter{w: io.MultiWriter(out, hash), left: MaxSnapshotCompressed}
	gz, err := gzip.NewWriterLevel(compressed, gzip.DefaultCompression)
	if err != nil {
		return "", 0, err
	}
	tw := tar.NewWriter(&limitWriter{w: gz, left: MaxSnapshotUncompressed})

	raw, err := json.Marshal(meta)
	if err != nil {
		return "", 0, err
	}
	if err := writeTarBytes(tw, metaName, raw); err != nil {
		return "", 0, err
	}
	if err := writeTarFile(tw, diffName, diffPath, 0o600); err != nil {
		return "", 0, err
	}
	if bundlePath != "" {
		if err := writeTarFile(tw, bundleName, bundlePath, 0o600); err != nil {
			return "", 0, err
		}
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	for _, f := range files {
		if f.Link != "" {
			h := &tar.Header{Typeflag: tar.TypeSymlink, Name: filePrefix + f.Path, Linkname: f.Link, Mode: 0o777, ModTime: time.Unix(0, 0)}
			if err := tw.WriteHeader(h); err != nil {
				return "", 0, err
			}
			continue
		}
		src, err := root.Open(f.Path)
		if err != nil {
			return "", 0, ErrWorkspaceMoved
		}
		info, err := src.Stat()
		if err != nil || !info.Mode().IsRegular() {
			src.Close()
			return "", 0, ErrWorkspaceMoved
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: filePrefix + f.Path, Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: time.Unix(0, 0)}
		err = tw.WriteHeader(h)
		if err == nil {
			_, err = io.CopyN(tw, src, info.Size())
		}
		src.Close()
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return "", 0, ErrTooLarge
			}
			return "", 0, fmt.Errorf("%w: %v", ErrWorkspaceMoved, err)
		}
	}
	if err := tw.Close(); err != nil {
		return "", 0, err
	}
	if err := gz.Close(); err != nil {
		return "", 0, err
	}
	if err := out.Sync(); err != nil {
		return "", 0, err
	}
	info, err := out.Stat()
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte) error {
	h := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0)}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, name, src string, mode int64) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	h := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: info.Size(), ModTime: time.Unix(0, 0)}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
