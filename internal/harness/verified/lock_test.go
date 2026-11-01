package verified

import (
	"testing"
	"time"
)

func TestLockExcludesAnotherHolder(t *testing.T) {
	dir := t.TempDir() + "/nested" // created by Lock
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan func())
	go func() {
		u, err := Lock(dir)
		if err != nil {
			t.Error(err)
			u = func() {}
		}
		got <- u
	}()
	select {
	case <-got:
		t.Fatal("a second Lock got in while the first was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case u := <-got:
		u()
	case <-time.After(10 * time.Second):
		t.Fatal("the second Lock never got in after the release")
	}
}
