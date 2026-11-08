// Package home locates Hoplet's data directory and the files the daemon and
// its clients share there.
package home

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Env overrides the default home directory.
const Env = "HOPLET_HOME"

// Dir returns Hoplet's home: $HOPLET_HOME when set, else ~/.hoplet. The
// path is absolute; it is not created.
func Dir() (string, error) {
	if d := os.Getenv(Env); d != "" {
		return filepath.Abs(d)
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home: %w", err)
	}
	return filepath.Join(h, ".hoplet"), nil
}

// Socket is the daemon's Unix socket in home.
func Socket(home string) string { return filepath.Join(home, "daemon.sock") }

// Lock is the file the running daemon holds an exclusive lock on.
func Lock(home string) string { return filepath.Join(home, "daemon.lock") }

// Sessions is the parent of the per-session directories.
func Sessions(home string) string { return filepath.Join(home, "sessions") }

// Companions is the parent of the per-companion directories.
func Companions(home string) string { return filepath.Join(home, "companions") }

// Artifacts holds the files of every artifact version.
func Artifacts(home string) string { return filepath.Join(home, "artifacts") }

// Profile returns the root of the user's memory profile.
func Profile(home string) string { return filepath.Join(home, "profile") }

// tokenName is the token file's name in the home directory.
const tokenName = "token"

// identityName is the identity key file's path relative to the home directory.
const identityName = "keys/identity"

// TokenFile holds the local token that clients on this machine present.
func TokenFile(home string) string { return filepath.Join(home, tokenName) }

// IdentityFile holds this home's long-term private key.
func IdentityFile(home string) string { return filepath.Join(home, filepath.FromSlash(identityName)) }

// ReadToken reads the local token. It refuses a token file that other users
// can read (any group or other permission bit set).
func ReadToken(home string) (string, error) {
	return ReadPrivate(home, tokenName)
}

// EnsureToken returns the local token, creating the token file (mode 0600)
// when it does not exist.
func EnsureToken(home string) (string, error) {
	return EnsurePrivate(home, tokenName, func() (string, error) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("home: make token: %w", err)
		}
		return hex.EncodeToString(b), nil
	})
}

// ReadIdentity reads the identity key file's content; see ReadPrivate.
func ReadIdentity(home string) (string, error) {
	return ReadPrivate(home, identityName)
}

// EnsureIdentity returns the identity key file's content, writing the
// value fresh returns when the file does not exist; see EnsurePrivate.
func EnsureIdentity(home string, fresh func() (string, error)) (string, error) {
	return EnsurePrivate(home, identityName, fresh)
}

// ReadPrivate reads the private file name (slash-separated, relative to
// home) and returns its content without surrounding space. It refuses a
// file that other users can access (any group or other permission bit set)
// and an empty file.
func ReadPrivate(home, name string) (string, error) {
	dir := os.DirFS(home)
	path := filepath.Join(home, filepath.FromSlash(name))
	fi, err := fs.Stat(dir, name)
	if err != nil {
		return "", fmt.Errorf("home: read %s: %w", name, err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("home: %s is accessible to other users (mode %v); run chmod 600 on it", path, fi.Mode().Perm())
	}
	b, err := fs.ReadFile(dir, name)
	if err != nil {
		return "", fmt.Errorf("home: read %s: %w", name, err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("home: %s is empty", path)
	}
	return v, nil
}

// EnsurePrivate returns the content of the private file name, creating it
// with the value fresh returns when it does not exist. The file is mode
// 0600 and its directory, created when missing, 0700. Concurrent callers
// all get the content of the one file that was published.
func EnsurePrivate(home, name string, fresh func() (string, error)) (string, error) {
	v, err := ReadPrivate(home, name)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return v, err
	}
	if v, err = fresh(); err != nil {
		return "", err
	}
	path := filepath.Join(home, filepath.FromSlash(name))
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("home: write %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return "", fmt.Errorf("home: write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(v + "\n")
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return "", fmt.Errorf("home: write %s: %w", name, err)
	}
	// The link publishes the file whole and fails if another process created it first.
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ReadPrivate(home, name)
		}
		return "", fmt.Errorf("home: write %s: %w", name, err)
	}
	return strings.TrimSpace(v), nil
}
