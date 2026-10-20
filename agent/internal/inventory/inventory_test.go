package inventory

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"git version 2.45.1\n":                        "2.45.1",
		"git version 2.39.3 (Apple Git-146)":          "2.39.3",
		"v22.4.0":                                     "22.4.0",
		"9.4.0":                                       "9.4.0",
		"Python 3.12.3":                               "3.12.3",
		"go version go1.22.3 darwin/arm64":            "1.22.3",
		"cargo 1.79.0 (ffa9cf99a 2024-06-03)":         "1.79.0",
		"rustc 1.79.0 (129f3b996 2024-06-10)":         "1.79.0",
		"Docker version 26.1.4, build 5650f9b":        "26.1.4",
		"gh version 2.49.0 (2024-05-13)\nhttps://x/y": "2.49.0",
		"no number here":                              "",
		"":                                            "",
	} {
		if got := parseVersion(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestParseJavaVersion(t *testing.T) {
	t.Parallel()
	modern := "Picked up JAVA_TOOL_OPTIONS: -Xmx2g\nopenjdk version \"21.0.2\" 2024-01-16\nOpenJDK Runtime Environment (build 21.0.2+13)\n"
	legacy := "java version \"1.8.0_292\"\nJava(TM) SE Runtime Environment (build 1.8.0_292-b10)\n"
	for in, want := range map[string]string{modern: "21.0.2", legacy: "1.8.0", "Picked up X: 1.2.3\n": "", "": ""} {
		if got := parseJavaVersion(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	t.Parallel()
	got := parseNvidiaSMI("NVIDIA GeForce RTX 4090, 24564\nNVIDIA A100-SXM4-80GB, 81920\n\nTesla T4, [N/A]\n, 5\n")
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "NVIDIA GeForce RTX 4090" || got[0].Memory == nil || *got[0].Memory != 24564<<20 {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Memory == nil || *got[1].Memory != 81920<<20 {
		t.Errorf("second: %+v", got[1])
	}
	if got[2].Name != "Tesla T4" || got[2].Memory != nil {
		t.Errorf("third: %+v", got[2])
	}
	if got := parseNvidiaSMI(strings.Repeat("GPU, 1\n", 12)); len(got) != MaxGPUs {
		t.Errorf("not capped: %d", len(got))
	}
}

const appleSilicon = `{"SPDisplaysDataType":[{"_name":"Apple M3 Pro","spdisplays_vendor":"sppci_vendor_Apple","sppci_model":"Apple M3 Pro","sppci_cores":"18"}]}`
const intelMac = `{"SPDisplaysDataType":[{"_name":"Intel Iris Plus","sppci_model":"Intel Iris Plus Graphics 640","spdisplays_vram":"1536 MB"},{"_name":"AMD Radeon Pro 560X","spdisplays_vram":"4 GB"}]}`

func TestParseSystemProfiler(t *testing.T) {
	t.Parallel()
	got := parseSystemProfiler(appleSilicon, 36<<30)
	if len(got) != 1 || got[0].Name != "Apple M3 Pro" || got[0].Memory == nil || *got[0].Memory != 36<<30 {
		t.Errorf("apple: %+v", got)
	}
	if got := parseSystemProfiler(appleSilicon, 0); len(got) != 1 || got[0].Memory != nil {
		t.Errorf("apple without memory: %+v", got)
	}
	got = parseSystemProfiler(intelMac, 16<<30)
	if len(got) != 2 || got[0].Name != "Intel Iris Plus Graphics 640" || *got[0].Memory != 1536<<20 ||
		got[1].Name != "AMD Radeon Pro 560X" || *got[1].Memory != 4<<30 {
		t.Errorf("intel: %+v", got)
	}
	if got := parseSystemProfiler("not json", 1); len(got) != 0 {
		t.Errorf("garbage: %+v", got)
	}
}

func TestClipCountsUTF16Units(t *testing.T) {
	t.Parallel()
	if got := clip("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := clip("a😀b", 2); got != "a" {
		t.Errorf("an emoji is two units: got %q", got)
	}
	if got := clip("a😀b", 4); got != "a😀b" {
		t.Errorf("got %q", got)
	}
}

func fakeEnv(outputs map[string][2]string) *Env {
	return &Env{
		LookPath: func(name string) (string, error) {
			if _, ok := outputs[name]; ok {
				return "/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, path string, _ ...string) (string, string, error) {
			o, ok := outputs[strings.TrimPrefix(path, "/bin/")]
			if !ok || o[0] == "FAIL" {
				return "", "", errors.New("exit 1")
			}
			return o[0], o[1], nil
		},
	}
}

func TestDetectToolchainsKeepsListOrderAndSkipsMissingAndFailing(t *testing.T) {
	t.Parallel()
	env := fakeEnv(map[string][2]string{
		"gh":     {"gh version 2.49.0 (2024-05-13)\n", ""},
		"git":    {"git version 2.45.1\n", ""},
		"java":   {"", "openjdk version \"21.0.2\" 2024-01-16\n"},
		"docker": {"FAIL", ""},
		"npm":    {"garbage\n", ""},
		"go":     {"go version go1.22.3 linux/amd64\n", ""},
	})
	got := detectToolchains(context.Background(), env)
	want := []Toolchain{{"git", "2.45.1"}, {"go", "1.22.3"}, {"java", "21.0.2"}, {"gh", "2.49.0"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := detectToolchains(context.Background(), fakeEnv(nil)); got == nil || len(got) != 0 {
		t.Errorf("none found: %+v", got)
	}
}

func TestCleanOriginDropsUserinfoQueryAndFragment(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"https://user:token@github.com/o/r.git": "https://github.com/o/r.git",
		"https://github.com/o/r.git?token=abc":  "https://github.com/o/r.git",
		"https://github.com/o/r.git#frag":       "https://github.com/o/r.git",
		"https://u:p@h.test/o/r?a=b#c":          "https://h.test/o/r",
		"ssh://git@host.test:22/o/r":            "ssh://host.test:22/o/r",
		"git@github.com:o/r.git":                "git@github.com:o/r.git",
		"/srv/git/r.git":                        "/srv/git/r.git",
		"":                                      "",
	} {
		if got := cleanOrigin(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestDetectRepositories(t *testing.T) {
	t.Parallel()
	origins := map[string]string{"/a": "https://u:p@h.test/o/a.git?x=1", "/b": ""}
	got := detectRepositories(context.Background(), []string{"/a", "/b", "/c"}, func(_ context.Context, dir string) string { return origins[dir] })
	if len(got) != 3 || got[0].Path != "/a" || got[0].Origin == nil || *got[0].Origin != "https://h.test/o/a.git" ||
		got[1].Path != "/b" || got[1].Origin != nil || got[2].Origin != nil {
		t.Errorf("got %+v", got)
	}
	many := make([]string, MaxRepositories+5)
	for i := range many {
		many[i] = "/p"
	}
	if got := detectRepositories(context.Background(), many, func(context.Context, string) string { return "" }); len(got) != MaxRepositories {
		t.Errorf("not capped: %d", len(got))
	}
	if got := detectRepositories(context.Background(), nil, nil); got == nil || len(got) != 0 {
		t.Errorf("none: %+v", got)
	}
}

func TestCollectorReusesMachineDetectionAndReadsProjectsEachTime(t *testing.T) {
	t.Parallel()
	var runs atomic.Int32
	env := &Env{
		LookPath: func(string) (string, error) { return "", errors.New("none") },
		Run: func(context.Context, string, ...string) (string, string, error) {
			runs.Add(1)
			return "", "", errors.New("none")
		},
	}
	now := time.Unix(1000, 0)
	projects := []string{"/a"}
	c := New(Config{
		Env: env, Now: func() time.Time { return now },
		Projects: func() []string { return projects },
		Origin:   func(context.Context, string) string { return "" },
	})
	first := c.Detect(context.Background())
	projects = []string{"/a", "/b"}
	now = now.Add(time.Minute)
	second := c.Detect(context.Background())
	if len(first.Repositories) != 1 || len(second.Repositories) != 2 || !second.At.After(first.At) {
		t.Errorf("first %+v second %+v", first, second)
	}
	if first.Toolchains == nil || first.GPUs == nil {
		t.Errorf("lists must not be nil: %+v", first)
	}
}

func TestRefreshCoalesces(t *testing.T) {
	t.Parallel()
	c := New(Config{Env: fakeEnv(nil), Send: func(Report) {}})
	c.Refresh()
	c.Refresh()
	c.Refresh()
	if len(c.trigger) != 1 {
		t.Errorf("pending = %d", len(c.trigger))
	}
}
