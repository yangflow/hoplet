package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/proto"
)

func TestExtractSupport(t *testing.T) {
	h := &Harness{}
	if why := h.ExtractSupport(harness.Info{Version: "2.1.319", Supported: true}); why != "" {
		t.Fatalf("2.1.319: %q", why)
	}
	for _, v := range []string{"2.1.312", "2.1.320"} {
		if why := h.ExtractSupport(harness.Info{Version: v, Supported: true}); !strings.Contains(why, v) {
			t.Fatalf("%s: %q", v, why)
		}
	}
}

// fakeExtract is a claude that records how it was run and answers reply.
func fakeExtract(t *testing.T, body string) (*Harness, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + dir + "/args\nenv > " + dir + "/env\npwd > " + dir + "/pwd\ncat > " + dir + "/stdin\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Harness{Binary: bin}, dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExtractRunsIsolated(t *testing.T) {
	h, dir := fakeExtract(t, `echo '{"is_error":false,"result":"{\"ops\":[]}","usage":{"input_tokens":7,"output_tokens":2}}'`)
	work := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/h", "CLAUDECODE=1", "CLAUDE_CODE_X=1"}
	res, err := h.Extract(context.Background(), harness.ExtractRequest{Env: env, Dir: work, Model: "m1", System: "SYS", Input: "MATERIAL"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != `{"ops":[]}` || res.Usage != (proto.Usage{InputTokens: 7, OutputTokens: 2}) {
		t.Fatalf("result = %+v", res)
	}
	want := []string{
		"-p", "--output-format", "json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--no-session-persistence", "--setting-sources", "user", "--settings", `{"disableAllHooks":true}`,
		"--disable-slash-commands", "--system-prompt", "SYS", "--model", "m1",
	}
	got := strings.Split(strings.TrimSuffix(read(t, dir+"/args"), "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
	if s := read(t, dir+"/stdin"); s != "MATERIAL" {
		t.Fatalf("stdin = %q", s)
	}
	gotEnv := strings.Split(read(t, dir+"/env"), "\n")
	for _, kv := range []string{"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1", "DISABLE_AUTOUPDATER=1", "HOME=/h"} {
		if !slices.Contains(gotEnv, kv) {
			t.Errorf("env lacks %s", kv)
		}
	}
	for _, kv := range []string{"CLAUDECODE=1", "CLAUDE_CODE_X=1"} {
		if slices.Contains(gotEnv, kv) {
			t.Errorf("env keeps %s", kv)
		}
	}
	real, err := filepath.EvalSymlinks(work)
	if err != nil {
		t.Fatal(err)
	}
	if pwd := strings.TrimSpace(read(t, dir+"/pwd")); pwd != real {
		t.Fatalf("dir = %q, want %q", pwd, real)
	}
}

func TestExtractErrors(t *testing.T) {
	cases := map[string]string{
		"exit":     `echo nope >&2; exit 3`,
		"is_error": `echo '{"is_error":true,"result":"limit reached"}'`,
		"not json": `echo hello`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := fakeExtract(t, body)
			_, err := h.Extract(context.Background(), harness.ExtractRequest{Dir: t.TempDir(), Model: "m", Env: []string{"PATH=" + os.Getenv("PATH")}})
			if err == nil {
				t.Fatal("no error")
			}
		})
	}
}
