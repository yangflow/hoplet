// Package verified keeps, in a harness adapter's own directory, the
// harness versions that passed Hoplet's self-check on this machine and the
// outcome of the last automatic check of a new version, and decides when a
// version that failed may be checked again.
package verified

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The files kept in an adapter's directory.
const (
	versionsFile = "verified.json"
	outcomeFile  = "update.json"
)

// MaxAttempts is how many self-check failures in a row a version gets
// before Hoplet stops checking it: three daily tries, as one failure may
// be transient.
const MaxAttempts = 3

// RetryAfter is how long a version that just failed is left alone; it
// keeps a daemon that restarts often from checking the same version more
// than once a day.
const RetryAfter = 20 * time.Hour

// ErrSelfCheck marks a failure of the self-check itself, as opposed to a
// failure around it (copying a binary, say); only these count as attempts.
var ErrSelfCheck = errors.New("self-check failed")

// Has reports whether version v passed the self-check here.
func Has(dir, v string) bool {
	vs, _ := read(dir)
	return slices.Contains(vs, v)
}

func read(dir string) ([]string, error) {
	b, err := fs.ReadFile(os.DirFS(dir), versionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Versions []string `json:"versions"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("verified: %s: %w", filepath.Join(dir, versionsFile), err)
	}
	return f.Versions, nil
}

// Add records that version v passed the self-check here.
func Add(dir, v string) error {
	vs, err := read(dir)
	if err != nil {
		return err
	}
	if slices.Contains(vs, v) {
		return nil
	}
	b, err := json.MarshalIndent(struct {
		Versions []string `json:"versions"`
	}{append(vs, v)}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, versionsFile), b, 0o600)
}

// Outcome is the record of the last automatic check of a new version, kept
// for doctor.
type Outcome struct {
	At      time.Time `json:"at"`
	From    string    `json:"from,omitempty"`
	Version string    `json:"version,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Attempts counts the consecutive checks of Version that failed the
	// self-check; other failures leave it zero.
	Attempts int `json:"attempts,omitempty"`
}

// Last reads the outcome of the last automatic check, if any.
func Last(dir string) (Outcome, bool) {
	b, err := fs.ReadFile(os.DirFS(dir), outcomeFile)
	if err != nil {
		return Outcome{}, false
	}
	var o Outcome
	if json.Unmarshal(b, &o) != nil {
		return Outcome{}, false
	}
	return o, true
}

// Save records an outcome; losing it only costs a check.
func Save(dir string, o Outcome) error {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, outcomeFile), b, 0o600)
}

// Due reports whether version v may be checked at now, given the last
// outcome (ok false: none), and whether that outcome was a failure of v.
func Due(last Outcome, ok bool, v string, now time.Time) (due, failedBefore bool) {
	failedBefore = ok && last.Version == v && last.Error != ""
	switch {
	case failedBefore && last.Attempts >= MaxAttempts:
		return false, true // given up on this version
	case failedBefore && now.Sub(last.At) < RetryAfter && !last.At.After(now):
		return false, true // tried recently, perhaps by a previous daemon
	}
	return true, failedBefore
}

// Next is the record of a check of v (moving from version from) at now
// that ended with err (nil: passed). last is the previous record, and
// failedBefore says it was a failure of the same version. Only an error
// wrapping ErrSelfCheck counts as an attempt; any other records zero, so
// it is retried.
func Next(last Outcome, failedBefore bool, from, v string, err error, now time.Time) Outcome {
	o := Outcome{At: now.UTC().Truncate(time.Second), From: from, Version: v}
	if err == nil {
		return o
	}
	o.Error = err.Error()
	if errors.Is(err, ErrSelfCheck) {
		o.Attempts = 1
		if failedBefore {
			o.Attempts = last.Attempts + 1
		}
	}
	return o
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("verified: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("verified: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Chmod(mode), tmp.Sync(), tmp.Close()); err != nil {
		return fmt.Errorf("verified: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("verified: %w", err)
	}
	return nil
}
