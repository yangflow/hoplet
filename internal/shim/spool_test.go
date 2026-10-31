package shim

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReadRecordsSkipsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	content := `{"k":"stdout","d":"a"}` + "\n" + `{"k":"stdout","d":"b"}` + "\n" + `{"k":"std`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	var ends []int64
	err := readRecords(path, 0, int64(len(content)), func(r Record, end int64) error {
		got = append(got, r.Data)
		ends = append(ends, end)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "b"}) || ends[1] != int64(strings.LastIndex(content, "\n")+1) {
		t.Fatalf("records %q ends %v", got, ends)
	}
}

func TestSpoolAppendNotifyAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	size, notify := sp.state()
	if size != 0 {
		t.Fatalf("new spool size %d", size)
	}
	first, err := sp.append(Record{Kind: KindStart, Pid: 7})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-notify:
	default:
		t.Fatal("append did not notify")
	}
	end, err := sp.append(Record{Kind: KindExit, Code: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if size, _ := reopened.state(); size != end {
		t.Fatalf("reopened size %d, want %d", size, end)
	}
	var kinds []RecordKind
	err = readRecords(path, first, end, func(r Record, _ int64) error {
		kinds = append(kinds, r.Kind)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kinds, []RecordKind{KindExit}) {
		t.Fatalf("records after first: %v", kinds)
	}
}

func TestOpenSpoolCutsTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	whole := `{"k":"stdout","t":1,"d":"a"}` + "\n"
	if err := os.WriteFile(path, []byte(whole+`{"k":"std`), 0o600); err != nil {
		t.Fatal(err)
	}
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.close()
	if size, _ := sp.state(); size != int64(len(whole)) {
		t.Fatalf("size %d, want %d", size, len(whole))
	}
	end, err := sp.append(Record{Kind: KindStdout, Data: "b"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = readRecords(path, 0, end, func(r Record, _ int64) error {
		got = append(got, r.Data)
		return nil
	})
	if err != nil || !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("records %q, %v", got, err)
	}
	if !sp.boundary(int64(len(whole))) || sp.boundary(3) || !sp.boundary(0) {
		t.Fatal("boundary misjudged")
	}
}

func TestOpenSpoolWithoutNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	if err := os.WriteFile(path, []byte(`{"k":"std`), 0o600); err != nil {
		t.Fatal(err)
	}
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.close()
	if size, _ := sp.state(); size != 0 {
		t.Fatalf("size %d, want 0", size)
	}
}

func TestFailedAppendLeavesSpoolUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.append(Record{Kind: KindStdout, Data: "a"}); err != nil {
		t.Fatal(err)
	}
	before, _ := sp.state()
	if err := sp.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sp.append(Record{Kind: KindStdout, Data: "b"}); err == nil {
		t.Fatal("append to a closed spool succeeded")
	}
	if after, _ := sp.state(); after != before {
		t.Fatalf("size moved from %d to %d on a failed append", before, after)
	}
}

// failingSync is a spoolFile whose Sync fails.
type failingSync struct{ spoolFile }

func (failingSync) Sync() error { return errDiskFull }

func TestExitRecordSurvivesSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), SpoolName)
	sp, err := openSpool(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.close()
	sp.w = failingSync{sp.w}
	_, notify := sp.state()
	off, err := sp.append(Record{Kind: KindExit, Code: 3})
	if !errors.Is(err, errDiskFull) {
		t.Fatalf("append: %v, want the sync error", err)
	}
	st, serr := os.Stat(path)
	if serr != nil {
		t.Fatal(serr)
	}
	if size, _ := sp.state(); size != st.Size() || off != st.Size() {
		t.Fatalf("size %d, returned offset %d, file length %d: want all equal", size, off, st.Size())
	}
	select {
	case <-notify:
	default:
		t.Fatal("readers were not notified of the written record")
	}
}
