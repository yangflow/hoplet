package codex

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const threadB = "01a14adf-8185-7132-97c5-ad6f176a3266"

// stage writes data as the session file a handoff received.
func stage(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallKeepsTheDateAndNameOfTheOriginalAndWritesNoIndex(t *testing.T) {
	localZone(t, 0)
	root := filepath.Join(t.TempDir(), "home", "sessions")
	data := rollout(threadA, "/source/proj", 5)
	got, err := New(root, nil).Install("/target/proj", threadA, stage(t, data))
	if err != nil {
		t.Fatal(err)
	}
	if want := rolloutPath(root, threadA); got != want {
		t.Fatalf("installed at %s, want %s", got, want)
	}
	read, _ := os.ReadFile(got)
	if !bytes.Equal(read, data) {
		t.Fatal("the installed bytes differ")
	}
	entries, _ := os.ReadDir(filepath.Dir(root))
	if len(entries) != 1 || entries[0].Name() != "sessions" {
		t.Fatalf("something other than sessions/ was written: %v", entries)
	}
}

// localZone makes the machine's local time a fixed zone for the test.
func localZone(t *testing.T, hours int) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("test", hours*3600)
	t.Cleanup(func() { time.Local = saved })
}

func TestInstallNamesTheFileInLocalTime(t *testing.T) {
	localZone(t, 8)
	for name, tc := range map[string]struct{ stamp, dir, file string }{
		"same day":         {"2026-10-18T10:39:33.082Z", "2026/10/18", "2026-10-18T18-39-33"},
		"crosses midnight": {"2026-12-31T20:15:00.000Z", "2027/01/01", "2027-01-01T04-15-00"},
		"offset recorded":  {"2026-10-18T23:30:05.250-05:00", "2026/10/19", "2026-10-19T12-30-05"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data := bytes.Replace(rollout(threadA, "/w", 1), []byte(`"payload":{`), []byte(`"payload":{"timestamp":"`+tc.stamp+`",`), 1)
			got, err := New(root, nil).Install("/w", threadA, stage(t, data))
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(root, filepath.FromSlash(tc.dir), "rollout-"+tc.file+"-"+threadA+".jsonl")
			if got != want {
				t.Fatalf("installed at %s, want %s", got, want)
			}
		})
	}
}

func TestInstallNeverReplacesAnExistingFile(t *testing.T) {
	root := t.TempDir()
	data := rollout(threadA, "/w", 2)
	target := rolloutPath(root, threadA)
	write(t, target, []byte("keep me\n"))
	if _, err := New(root, nil).Install("/w", threadA, stage(t, data)); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if read, _ := os.ReadFile(target); string(read) != "keep me\n" {
		t.Fatal("the existing file was replaced")
	}
}

func TestInstallRefusesAThreadAlreadyPresentElsewhere(t *testing.T) {
	data := rollout(threadA, "/w", 2)
	for name, existing := range map[string]string{
		"another date": filepath.Join("2026", "09", "01", "rollout-2026-09-01T00-00-00-"+threadA+".jsonl"),
		"compressed":   filepath.Join("2026", "10", "18", "rollout-2026-10-18T01-18-44-"+threadA+".jsonl.zst"),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, existing), compress(t, data))
			if _, err := New(root, nil).Install("/w", threadA, stage(t, data)); !errors.Is(err, ErrExists) {
				t.Fatalf("err = %v", err)
			}
			if _, err := os.Stat(rolloutPath(root, threadA)); err == nil {
				t.Fatal("a second copy was installed")
			}
		})
	}
}

func TestInstallRejectsAFileThatIsNotTheSession(t *testing.T) {
	good := rollout(threadA, "/w", 1)
	for name, tc := range map[string]struct {
		id   string
		data []byte
	}{
		"other thread":  {threadB, good},
		"no meta":       {threadA, []byte("{\"type\":\"event_msg\"}\n")},
		"incomplete":    {threadA, []byte("{\"type\":\"session_meta\"")},
		"invalid id":    {"../x", good},
		"bad timestamp": {threadA, bytes.Replace(good, []byte("2026-10-18T01:18:44.000Z"), []byte("yesterday"), 1)},
		"no timestamp":  {threadA, bytes.Replace(good, []byte(`"timestamp":"2026-10-18T01:18:44.000Z",`), nil, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if _, err := New(root, nil).Install("/w", tc.id, stage(t, tc.data)); err == nil {
				t.Fatal("installed")
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("left %v", entries)
			}
		})
	}
}

func TestRemoveTakesARolloutAndItsEmptyDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	p := New(root, nil)
	a := rolloutPath(root, threadA)
	b := filepath.Join(filepath.Dir(a), "rollout-2026-10-18T02-00-00-"+threadB+".jsonl.zst")
	write(t, a, rollout(threadA, "/w", 1))
	write(t, b, compress(t, rollout(threadB, "/w", 1)))
	other := filepath.Join(root, "notes.txt")
	write(t, other, []byte("x"))
	outside := filepath.Join(t.TempDir(), "rollout-2026-10-18T01-18-44-"+threadA+".jsonl")
	write(t, outside, []byte("x"))

	p.Remove(other)
	p.Remove(outside)
	p.Remove(filepath.Join(root, "..", filepath.Base(outside)))
	p.Remove(root)
	for _, path := range []string{other, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was removed", path)
		}
	}
	p.Remove(a)
	if _, err := os.Stat(a); err == nil {
		t.Fatal("rollout kept")
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatal("the date directory was removed while it held another rollout")
	}
	p.Remove(b)
	if _, err := os.Stat(filepath.Join(root, "2026")); err == nil {
		t.Fatal("empty date directories kept")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("sessions/ itself was removed")
	}
}

func TestSessionFileIsUnknownBeforeTheSessionExists(t *testing.T) {
	if got := New(t.TempDir(), nil).SessionFile("/w", threadA); got != "" {
		t.Fatalf("got %q", got)
	}
}

// A session handed from one agent to another keeps its bytes, whichever encoding the source kept.
func TestHandoffBetweenTwoAgentsKeepsTheBytes(t *testing.T) {
	localZone(t, 0)
	for _, compressed := range []bool{false, true} {
		name := "plain"
		if compressed {
			name = "zst"
		}
		t.Run(name, func(t *testing.T) {
			data := rollout(threadA, "/source/proj", 40)
			srcRoot, dstRoot := t.TempDir(), t.TempDir()
			if compressed {
				write(t, rolloutPath(srcRoot, threadA)+".zst", compress(t, data))
			} else {
				write(t, rolloutPath(srcRoot, threadA), data)
			}
			src := scanOne(t, New(srcRoot, nil))
			// The core stores the stream and sends it to the target as the session part.
			stream, _ := drain(t, src, 0, 1<<20)
			if !bytes.Equal(stream, data) {
				t.Fatal("the source stream differs from the rollout")
			}
			dst := New(dstRoot, nil)
			path, err := dst.Install("/target/proj-worktree", src.ID(), stage(t, stream))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(path, ".jsonl") {
				t.Fatalf("installed %s compressed", path)
			}
			got := scanOne(t, dst)
			if got.ID() != threadA || got.Cwd() != "/source/proj" || got.Dir() != "2026/10/18" {
				t.Fatalf("target sees %s %s %s", got.ID(), got.Cwd(), got.Dir())
			}
			// The target continues from the cursor: it reads exactly the prefix it was given.
			read, _ := drain(t, got, 0, 1<<20)
			if !bytes.Equal(read, data) {
				t.Fatal("the target's bytes differ")
			}
			if found, ok := dst.Find(threadA); !ok || found.NativePath != path {
				t.Fatalf("find: %+v %v", found, ok)
			}
			if _, err := dst.Install("/target/proj-worktree", threadA, stage(t, stream)); !errors.Is(err, ErrExists) {
				t.Fatalf("second install: %v", err)
			}
			dst.Remove(path)
			if _, ok := dst.Find(threadA); ok {
				t.Fatal("still found after Remove")
			}
		})
	}
}
