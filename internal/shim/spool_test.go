package shim

import (
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
