package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

// fakeBinary is a script standing in for Codex: it answers initialize, then
// model/list over two pages, and counts how often it was started.
const fakeBinary = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex-cli 0.167.0"; exit 0; fi
echo run >> "$COUNT"
while read -r line; do
  case "$line" in
  *'"initialize"'*) echo '{"id":"list","result":{}}' ;;
  *'"cursor":"p2"'*) echo '{"method":"noise"}'; echo '{"id":"list","result":{"data":[{"id":"b","model":"b","displayName":"B","supportedReasoningEfforts":[]},{"id":"h","model":"h","hidden":true}]}}' ;;
  *'"model/list"'*) echo '{"id":"list","result":{"data":[{"id":"a","model":"a","displayName":"A","description":"d","isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"future"},{"reasoningEffort":"ultra"}]}],"nextCursor":"p2"}}' ;;
  esac
done
`

func TestModels(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte(fakeBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	count := filepath.Join(dir, "count")
	h := &Harness{Binary: bin}
	h.UseEnv(func() []string { return []string{"COUNT=" + count, "PATH=/bin:/usr/bin"} })
	want := []proto.ModelInfo{
		{ID: "a", Model: "a", Name: "A", Description: "d", Default: true, Efforts: []proto.Effort{proto.EffortLow, proto.EffortUltra}},
		{ID: "b", Model: "b", Name: "B", NoEffort: true},
	}
	for range 2 {
		got, err := h.Models(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v", got)
		}
	}
	b, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "run"); n != 1 {
		t.Fatalf("started %d times, want the list kept", n)
	}
}

func TestModelsRefused(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nwhile read -r line; do echo '{\"id\":\"list\",\"error\":{\"code\":1,\"message\":\"signed out\"}}'; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Binary: bin}
	h.UseEnv(func() []string { return []string{"PATH=/bin:/usr/bin"} })
	if _, err := h.Models(context.Background()); err == nil || !strings.Contains(err.Error(), "signed out") {
		t.Fatalf("got %v", err)
	}
}
