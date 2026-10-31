// Package home locates Hoplet's data directory and the files the daemon and
// its clients share there.
package home

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// TokenFile holds the local token that clients on this machine present.
func TokenFile(home string) string { return filepath.Join(home, "token") }

// ReadToken reads the local token.
func ReadToken(home string) (string, error) {
	b, err := os.ReadFile(TokenFile(home))
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
	_, werr := tmp.WriteString(tok + "\n")
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("home: write token: %w", err)
	}
	// CreateTemp makes the file 0600; the rename publishes it whole.
	if err := os.Rename(tmp.Name(), TokenFile(home)); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("home: write token: %w", err)
	}
	return tok, nil
}
