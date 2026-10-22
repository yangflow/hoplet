package hooks

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// record is what the journal keeps of a running script: enough to find its process group again, never content.
type record struct {
	RunID  string `json:"runId"`
	HookID string `json:"hookId"`
	PID    int    `json:"pid"`
	Start  string `json:"start"`
}

func journalDir(home string) string { return filepath.Join(home, "hooks") }

func newRunID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeRecord(home string, r record) error {
	dir := journalDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, r.RunID+".json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, r.RunID+".json"))
}

func removeRecord(home, runID string) {
	_ = os.Remove(filepath.Join(journalDir(home), runID+".json"))
}

// Sweep kills the process groups that records of an earlier run name and removes the records. A record counts
// only while its process still has the recorded start time, so a recycled PID is left alone. It returns how
// many groups it killed.
func Sweep(home string) int {
	dir := journalDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	killed := 0
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") {
			_ = os.Remove(path) // a half-written record
			continue
		}
		var r record
		if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &r) == nil && r.PID > 1 {
			if start, ok := startTime(r.PID); ok && start == r.Start {
				_ = syscall.Kill(-r.PID, syscall.SIGKILL)
				_ = syscall.Kill(r.PID, syscall.SIGKILL)
				killed++
			}
		}
		_ = os.Remove(path)
	}
	return killed
}
