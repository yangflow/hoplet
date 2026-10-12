package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompleteLength(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		content string
		want    int64
	}{
		"empty":                   {"", 0},
		"one line":                {"abc\n", 4},
		"unfinished tail":         {"abc\ndef", 4},
		"only an unfinished line": {"abc", -1},
		"blank lines":             {"\n\n", 2},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"))
		os.WriteFile(path, []byte(tc.content), 0o600)
		got, err := CompleteLength(path)
		if tc.want < 0 {
			if err == nil {
				t.Errorf("%s: %d, nil", name, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %d, %v; want %d", name, got, err, tc.want)
		}
	}
	long := filepath.Join(dir, "long")
	os.WriteFile(long, append(bytes.Repeat([]byte("x"), 200<<10), '\n'), 0o600)
	if got, err := CompleteLength(long); err != nil || got != 200<<10+1 {
		t.Errorf("a line longer than a block: %d, %v", got, err)
	}
}
