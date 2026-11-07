package relay

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/yangflow/hoplet/internal/noise"
)

// allowName is the allowlist's file name in the relay's directory.
const allowName = "allow"

// idLen is the length of a host ID.
const idLen = 26

// ValidID reports whether id has the form of a host ID: 26 characters of
// lowercase base32.
func ValidID(id string) bool {
	if len(id) != idLen {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// List returns the host IDs in dir's allowlist, in file order. A missing
// file is an empty list; a file other users can access, or a line that is
// not a host ID, is an error.
func List(dir string) ([]noise.HostID, error) {
	path := filepath.Join(dir, allowName)
	fsys := os.DirFS(dir)
	fi, err := fs.Stat(fsys, allowName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("relay: allowlist: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("relay: %s is accessible to other users (mode %v); run chmod 600 on it", path, fi.Mode().Perm())
	}
	b, err := fs.ReadFile(fsys, allowName)
	if err != nil {
		return nil, fmt.Errorf("relay: allowlist: %w", err)
	}
	var ids []noise.HostID
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if !ValidID(line) {
			return nil, fmt.Errorf("relay: %s line %d: not a host ID", path, n)
		}
		ids = append(ids, noise.HostID(line))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("relay: allowlist: %w", err)
	}
	return ids, nil
}

// Allow adds id to dir's allowlist, creating the directory and the file
// when missing. Adding an ID already there changes nothing.
func Allow(dir string, id noise.HostID) error {
	if !ValidID(string(id)) {
		return fmt.Errorf("relay: %q is not a host ID", id)
	}
	ids, err := List(dir)
	if err != nil {
		return err
	}
	if slices.Contains(ids, id) {
		return nil
	}
	return write(dir, append(ids, id))
}

// Deny removes id from dir's allowlist. Removing an ID that is not there
// is an error.
func Deny(dir string, id noise.HostID) error {
	ids, err := List(dir)
	if err != nil {
		return err
	}
	i := slices.Index(ids, id)
	if i < 0 {
		return fmt.Errorf("relay: %s is not in the allowlist", id)
	}
	return write(dir, slices.Delete(ids, i, i+1))
}

// allowed reports whether id is in dir's allowlist.
func allowed(dir string, id noise.HostID) (bool, error) {
	ids, err := List(dir)
	if err != nil {
		return false, err
	}
	return slices.Contains(ids, id), nil
}

// write replaces dir's allowlist with ids, mode 0600, in one rename. Run
// as root, it gives the file to dir's owner: a relay that runs as its own
// user reads the allowlist root wrote for it.
func write(dir string, ids []noise.HostID) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("relay: allowlist: %w", err)
	}
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(string(id) + "\n")
	}
	tmp, err := os.CreateTemp(dir, "."+allowName+"-*")
	if err != nil {
		return fmt.Errorf("relay: allowlist: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(b.String())
	if werr == nil && os.Geteuid() == 0 {
		werr = matchOwner(tmp, dir)
	}
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("relay: allowlist: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, allowName)); err != nil {
		return fmt.Errorf("relay: allowlist: %w", err)
	}
	return nil
}

// matchOwner gives f the owner and group of dir.
func matchOwner(f *os.File, dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("relay: no owner for %s", dir)
	}
	return f.Chown(int(st.Uid), int(st.Gid))
}
