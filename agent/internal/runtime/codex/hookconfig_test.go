package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yangflow/hoplet/agent/internal/rules"
	"github.com/yangflow/hoplet/agent/internal/runtime"
)

func hookHarness(t *testing.T) (*harness, *HookServer) {
	t.Helper()
	s := startServer(t)
	exe, _ := os.Executable()
	h := &harness{t: t, log: filepath.Join(t.TempDir(), "log"), img: filepath.Join(t.TempDir(), "images")}
	t.Setenv("HOPLET_FAKE_CODEX", "1")
	t.Setenv("FAKE_CODEX_LOG", h.log)
	h.d = New(Options{
		Bin: exe, ImageDir: h.img, Hook: s, HookCommand: HookCommand("/opt/hoplet/hoplet-agent"),
		Rules: func(runtime.StartSpec) (*rules.Engine, error) { return rules.New(rules.Env{}, nil, nil) },
	})
	return h, s
}

func checkHookInjected(t *testing.T, params map[string]any) {
	t.Helper()
	cfg, _ := params["config"].(map[string]any)
	if cfg == nil || cfg["bypass_hook_trust"] != true {
		t.Fatalf("config = %v", params["config"])
	}
	pre := cfg["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	cmd := pre["hooks"].([]any)[0].(map[string]any)["command"]
	if cmd != "'/opt/hoplet/hoplet-agent' hook codex" {
		t.Errorf("command = %v", cmd)
	}
}

func TestHookIsInjectedIntoNewAndResumedThreads(t *testing.T) {
	h, s := hookHarness(t)
	h.startNew(nil)
	checkHookInjected(t, h.received("thread/start")[0])
	if got := h.d.Capabilities(); len(got) != 5 {
		t.Errorf("capabilities = %v", got)
	}

	p, err := h.d.Start(ctx(t), runtime.StartSpec{SessionID: "abc", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	expect[runtime.Ready](t, p)
	checkHookInjected(t, h.received("thread/resume")[0])

	s.mu.Lock()
	n := len(s.targets)
	s.mu.Unlock()
	if n != 2 {
		t.Fatalf("registered processes = %d", n)
	}
	_ = p.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n = len(s.targets)
		s.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a killed process stays registered (%d)", n)
}

func TestNoHookWithoutASocket(t *testing.T) {
	h := newHarness(t)
	h.startNew(nil)
	if _, ok := h.received("thread/start")[0]["config"]; ok {
		t.Error("the hook was injected without a socket")
	}
}
