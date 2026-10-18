package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// record is what the journal keeps of an open terminal: enough to find its shell again, never content.
type record struct {
	TerminalID string `json:"terminalId"`
	PID        int    `json:"pid"`
	Start      string `json:"start"`
	Cwd        string `json:"cwd"`
}

func journalDir(home string) string { return filepath.Join(home, "terminal") }

func writeRecord(home string, r record) error {
	dir := journalDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, r.TerminalID+".json.tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, r.TerminalID+".json"))
}

func removeRecord(home, id string) {
	_ = os.Remove(filepath.Join(journalDir(home), id+".json"))
}

// Sweep kills the shells that records of an earlier run name and removes the records. A record counts only
// while its process still has the recorded start time, so a recycled PID is left alone. It returns how many
// shells it killed.
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
				below := descendants(r.PID)
				killGroup(r.PID, syscall.SIGKILL)
				_ = syscall.Kill(r.PID, syscall.SIGKILL)
				killIdentities(below)
				killed++
			}
		}
		_ = os.Remove(path)
	}
	return killed
}
