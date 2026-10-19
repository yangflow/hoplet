package codex

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yangflow/hoplet/agent/internal/provider"
)

var _ provider.Store = (*Provider)(nil)

// ErrExists reports that the target already has a rollout of the thread: an installation never replaces one,
// whatever its date directory or encoding.
var ErrExists = errors.New("session-exists")

// SessionFile is empty: Codex names a rollout after the time its thread starts and assigns the thread ID
// itself, so the path is not known before the session is created. A new session is found by locating it.
func (p *Provider) SessionFile(_, _ string) string { return "" }

// Install writes the staged rollout into sessions/ byte for byte, uncompressed, and returns its path. The
// handoff protocol carries the session ID and the bytes only, so the date directory and the name come from
// the first line: `sessions/YYYY/MM/DD/rollout-YYYY-MM-DDThh-mm-ss-<threadId>.jsonl`, with the time of the
// line's timestamp (UTC) in this machine's local time, which is how Codex names its rollouts. The working
// directory cwd is not recorded anywhere: Codex is given it when the thread is resumed. Nothing is written
// to Codex's index, and a rollout of the thread that is already anywhere under sessions/ (another date, or
// `.jsonl.zst`) or at the target path is an error, never an overwrite.
func (p *Provider) Install(_, sessionID, staged string) (string, error) {
	if !provider.ValidSessionID(sessionID) {
		return "", errors.New("invalid-session-id")
	}
	fl, ok := readFirstLine(staged)
	if !ok || fl.Payload.ID != sessionID {
		return "", errors.New("the session file does not start with the session's session_meta")
	}
	stamp := fl.Payload.Timestamp
	if stamp == "" {
		stamp = fl.Timestamp
	}
	ts, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return "", fmt.Errorf("the session_meta timestamp: %w", err)
	}
	ts = ts.Local()
	rel := filepath.Join(ts.Format("2006"), ts.Format("01"), ts.Format("02"),
		"rollout-"+ts.Format("2006-01-02T15-04-05")+"-"+sessionID+plainExt)
	for _, f := range scanFiles(p.root) {
		if strings.EqualFold(f.key, sessionID) {
			return "", fmt.Errorf("%w: %s", ErrExists, f.current())
		}
	}
	if err := os.MkdirAll(p.root, 0o700); err != nil {
		return "", err
	}
	store, err := os.OpenRoot(p.root)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := store.MkdirAll(filepath.Dir(rel), 0o700); err != nil {
		return "", err
	}
	src, err := os.Open(staged)
	if err != nil {
		return "", err
	}
	defer src.Close()
	out, err := store.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(out, src)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = store.Remove(rel)
		return "", errors.Join(copyErr, closeErr)
	}
	return filepath.Join(p.root, rel), nil
}

// Remove removes a rollout under sessions/ (`.jsonl` or `.jsonl.zst`) and the date directories it leaves
// empty. Any other path is left alone.
func (p *Provider) Remove(path string) {
	rel, err := filepath.Rel(p.root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	name := parts[len(parts)-1]
	base := strings.TrimSuffix(strings.TrimSuffix(name, ".zst"), plainExt)
	if len(parts) != 4 || !strings.HasPrefix(name, "rollout-") ||
		!(strings.HasSuffix(name, plainExt) || strings.HasSuffix(name, zstExt)) || !rolloutName.MatchString(base) {
		return
	}
	store, err := os.OpenRoot(p.root)
	if err != nil {
		return
	}
	defer store.Close()
	if store.Remove(rel) != nil {
		return
	}
	for dir := filepath.Dir(rel); dir != "."; dir = filepath.Dir(dir) {
		if store.Remove(dir) != nil { // fails while it holds anything else
			return
		}
	}
}
