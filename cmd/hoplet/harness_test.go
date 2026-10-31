package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yangflow/hoplet/internal/config"
	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/internal/harness/claude"
)

// harnessApp is a command line whose self-check succeeds unless fail is
// set, counting the binaries it checked.
func harnessApp(out, errb *bytes.Buffer, checked *[]string, fail error) app {
	a := testApp(out, errb)
	a.env = env.Static{"PATH=/usr/bin:/bin"}
	a.selfCheck = func(_ context.Context, bin string, vars []string) error {
		if !slices.Contains(vars, "PATH=/usr/bin:/bin") {
			return errors.New("the self-check did not get the harness environment")
		}
		*checked = append(*checked, bin)
		return fail
	}
	return a
}

func fakeClaudeBin(t *testing.T, version string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+version+" (Claude Code)'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestHarnessInstall(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOPLET_HOME", h)
	var checked []string
	hoplet := func(fail error, args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := harnessApp(&out, &errb, &checked, fail).run(args)
		return code, out.String(), errb.String()
	}
	bin := fakeClaudeBin(t, "2.1.312")
	if code, out, errs := hoplet(nil, "harness", "install", "claude", "--from", bin); code != 0 || out != "installed claude 2.1.312 at "+claude.BinaryPath(h)+"\n" || len(checked) != 0 {
		t.Fatalf("install: exit %d %q %q, checked %v", code, out, errs, checked)
	}
	var got map[string]any
	_, out, _ := hoplet(nil, "harness", "upgrade", "claude", "--from", bin, "--json")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["version"] != "2.1.312" || got["checked"] != false {
		t.Fatalf("upgrade --json %v %v", got, err)
	}

	// A version without recordings needs the self-check.
	bad := fakeClaudeBin(t, "2.1.400")
	if code, _, errs := hoplet(errors.New("no approval came"), "harness", "upgrade", "claude", "--from", bad); code != 1 || !strings.Contains(errs, "failed the self-check") || len(checked) != 1 {
		t.Fatalf("failing upgrade: exit %d %q, checked %v", code, errs, checked)
	}
	if code, out, errs := hoplet(nil, "harness", "upgrade", "claude", "--from", bad); code != 0 || out != "claude 2.1.400 passed the self-check\ninstalled claude 2.1.400 at "+claude.BinaryPath(h)+"\n" || !strings.Contains(errs, "self-check") {
		t.Fatalf("passing upgrade: exit %d %q %q", code, out, errs)
	}

	for _, args := range [][]string{{"harness"}, {"harness", "install"}, {"harness", "install", "codex"}, {"harness", "remove", "claude"}} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, out, _ := cli(t, "harness", "help"); code != 0 || !strings.Contains(out, "self-check") {
		t.Errorf("harness help: exit %d %q", code, out)
	}
}

func TestDaemonFollowsTheUsersClaude(t *testing.T) {
	h := t.TempDir()
	var checked []string
	var out, errb bytes.Buffer
	a := harnessApp(&out, &errb, &checked, nil)
	if _, err := claude.Install(context.Background(), fakeClaudeBin(t, "2.1.312"), h, nil); err != nil {
		t.Fatal(err)
	}
	newer := fakeClaudeBin(t, "2.1.400")
	vars := []string{"PATH=" + filepath.Dir(newer) + ":/usr/bin:/bin"}
	logs := &bytes.Buffer{}
	work := a.maintenance(h, slog.New(slog.NewTextHandler(logs, nil)))

	// Turned off in the configuration: nothing happens.
	if err := os.WriteFile(config.Path(h), []byte("[harness.claude]\nfollow = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work(context.Background(), vars)
	if len(checked) != 0 {
		t.Fatalf("followed although turned off: %v", checked)
	}

	if err := os.Remove(config.Path(h)); err != nil {
		t.Fatal(err)
	}
	a.selfCheck = func(_ context.Context, bin string, _ []string) error {
		checked = append(checked, bin)
		return nil
	}
	work = a.maintenance(h, slog.New(slog.NewTextHandler(logs, nil)))
	work(context.Background(), vars)
	if len(checked) != 1 || !strings.Contains(logs.String(), "claude followed") {
		t.Fatalf("checked %v, log %s", checked, logs)
	}
	if target, err := os.Readlink(claude.BinaryPath(h)); err != nil || target != "claude-2.1.400" {
		t.Fatalf("link %q %v", target, err)
	}
}

func TestMaintenanceIsOffWithoutASelfCheck(t *testing.T) {
	var out, errb bytes.Buffer
	if testApp(&out, &errb).maintenance(t.TempDir(), slog.Default()) != nil {
		t.Fatal("maintenance without a self-check")
	}
}

func TestDevSanitize(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "rec.jsonl")
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(claude.Entry{Dir: "in", Line: `{"type":"user","message":{"role":"user","content":"look at ` + dir + `/a and ` + userHome + `/b"}}`})
	if err := os.WriteFile(in, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture.jsonl")
	mustCLI(t, "dev", "sanitize", "--in", in, "--out", out, "--dir", dir)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(dir)) || bytes.Contains(b, []byte(userHome)) || !bytes.Contains(b, []byte("/work/a")) || !bytes.Contains(b, []byte("/Users/me/b")) {
		t.Fatalf("fixture %s", b)
	}
	for _, args := range [][]string{{"dev"}, {"dev", "record"}, {"dev", "sanitize", "--in", in}} {
		if code, _, _ := cli(t, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
