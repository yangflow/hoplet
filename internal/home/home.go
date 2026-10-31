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

// tokenName is the token file's name in the home directory.
const tokenName = "token"

// TokenFile holds the local token that clients on this machine present.
func TokenFile(home string) string { return filepath.Join(home, tokenName) }

// ReadToken reads the local token. It refuses a token file that other users
// can read (any group or other permission bit set).
func ReadToken(home string) (string, error) {
	dir := os.DirFS(home)
	fi, err := fs.Stat(dir, tokenName)
	if err != nil {
		return "", fmt.Errorf("home: read token: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("home: token file %s is accessible to other users (mode %v); run chmod 600 on it", TokenFile(home), fi.Mode().Perm())
	}
	b, err := fs.ReadFile(dir, tokenName)
	if err != nil {
		return "", fmt.Errorf("home: read token: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("home: token file is empty")
	}
	return tok, nil
}

// EnsureToken returns the local token, creating the token file (mode 0600)
// when it does not exist.
func EnsureToken(home string) (string, error) {
	tok, err := ReadToken(home)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return tok, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("home: make token: %w", err)
	}
	tok = hex.EncodeToString(b)
	tmp, err := os.CreateTemp(home, ".token-*")
	if err != nil {
		return "", fmt.Errorf("home: write token: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(tok + "\n")
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return "", fmt.Errorf("home: write token: %w", err)
	}
	// The link publishes the file whole and fails if another process created it first.
	if err := os.Link(tmp.Name(), TokenFile(home)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ReadToken(home)
		}
		return "", fmt.Errorf("home: write token: %w", err)
	}
	return tok, nil
}
