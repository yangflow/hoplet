package locate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yangflow/hoplet/agent/internal/provider"
	claudeprovider "github.com/yangflow/hoplet/agent/internal/provider/claude"
)

func finders(claudeRoot string, exclude []string) []provider.Finder {
	return []provider.Finder{
		claudeprovider.New(claudeRoot, exclude),
	}
}

func TestLocateClaudeUsesJSONLCwd(t *testing.T) {
	root := t.TempDir()
	enc := "-tmp-hoplet-locate"
	dir := filepath.Join(root, enc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/tmp/hoplet-locate","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sess-loc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Locate("sess-loc", finders(root, nil)...)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cwd != "/tmp/hoplet-locate" || got.JSONLPath == "" {
		t.Fatalf("got %+v", got)
	}
}

func TestLocateNotFound(t *testing.T) {
	_, err := Locate("missing", finders(t.TempDir(), nil)...)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
