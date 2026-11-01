package verified

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestVersions(t *testing.T) {
	dir := t.TempDir() + "/nested" // created on first write
	if Has(dir, "1.0") {
		t.Fatal("an empty directory has versions")
	}
	for range 2 {
		if err := Add(dir, "1.0"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Add(dir, "1.1"); err != nil {
		t.Fatal(err)
	}
	if vs, err := read(dir); err != nil || fmt.Sprint(vs) != "[1.0 1.1]" {
		t.Fatalf("versions %v %v", vs, err)
	}
}

func TestOutcomes(t *testing.T) {
	dir := t.TempDir()
	if _, ok := Last(dir); ok {
		t.Fatal("an outcome before any check")
	}
	o := Outcome{At: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Version: "2", Error: "x", Attempts: 1}
	if err := Save(dir, o); err != nil {
		t.Fatal(err)
	}
	if got, ok := Last(dir); !ok || got != o {
		t.Fatalf("last %+v %v", got, ok)
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	failed := func(attempts int, ago time.Duration) Outcome {
		return Outcome{At: now.Add(-ago), Version: "2", Error: "x", Attempts: attempts}
	}
	for _, tt := range []struct {
		name        string
		last        Outcome
		ok          bool
		v           string
		due, before bool
	}{
		{"no record", Outcome{}, false, "2", true, false},
		{"another version failed", failed(1, time.Minute), true, "3", true, false},
		{"failed a minute ago", failed(1, time.Minute), true, "2", false, true},
		{"failed a day ago", failed(1, 21*time.Hour), true, "2", true, true},
		{"failed in the future (clock set back)", failed(1, -48*time.Hour), true, "2", true, true},
		{"given up", failed(MaxAttempts, 72*time.Hour), true, "2", false, true},
		{"passed", Outcome{At: now, Version: "2"}, true, "2", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			due, before := Due(tt.last, tt.ok, tt.v, now)
			if due != tt.due || before != tt.before {
				t.Fatalf("due %v before %v", due, before)
			}
		})
	}
}

func TestNextCountsOnlySelfCheckFailures(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	check := errors.Join(ErrSelfCheck, errors.New("no approval"))
	other := errors.New("disk full")
	prior := Outcome{Version: "2", Error: "x", Attempts: 2}
	for _, tt := range []struct {
		name         string
		last         Outcome
		failedBefore bool
		err          error
		wantAttempts int
	}{
		{"success", prior, true, nil, 0},
		{"first check failure", Outcome{}, false, check, 1},
		{"repeated check failure", prior, true, check, 3},
		{"other failure is not counted", Outcome{}, false, other, 0},
		{"other failure resets the count", prior, true, other, 0},
		{"check failure after an uncounted one", Outcome{Version: "2", Error: "x"}, true, check, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := Next(tt.last, tt.failedBefore, "1", "2", tt.err, now)
			if o.Attempts != tt.wantAttempts || (o.Error != "") != (tt.err != nil) || o.From != "1" || o.Version != "2" || !o.At.Equal(now) {
				t.Fatalf("%+v", o)
			}
		})
	}
}
