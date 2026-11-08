package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yangflow/hoplet/proto"
)

func TestMemoryExtractionLifecycle(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	run := MemoryExtraction{Session: "a", Harness: "claude", Model: "m", From: 1, To: 9, Trigger: "idle"}
	if _, err := st.CreateMemoryExtraction(ctx, MemoryExtraction{Session: "a", Harness: "claude", Model: "", From: 1, To: 9, Trigger: "idle"}, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no model: %v", err)
	}
	run, err = st.CreateMemoryExtraction(ctx, run, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMemoryExtraction(ctx, run, now); !errors.Is(err, ErrExists) {
		t.Fatalf("second open run: %v", err)
	}
	if err := st.StartMemoryExtraction(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	usage := proto.Usage{InputTokens: 3, OutputTokens: 4}
	if err := st.SetMemoryExtractionParsed(ctx, run.ID, `{"ops":[]}`, usage, now); err != nil {
		t.Fatal(err)
	}
	open, err := st.OpenMemoryExtractions(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("open = %v, %v", open, err)
	}
	if got := open[0]; got.State != ExtractionParsed || got.Attempts != 1 || got.Output != `{"ops":[]}` || got.Usage != usage {
		t.Fatalf("run = %+v", got)
	}
	if err := st.FinishMemoryExtraction(ctx, open[0], now); err != nil {
		t.Fatal(err)
	}
	if seq, err := st.MemoryExtractionProgress(ctx, "a"); err != nil || seq != 9 {
		t.Fatalf("progress = %d, %v", seq, err)
	}
	if err := st.AdvanceMemoryExtraction(ctx, "a", 4); err != nil {
		t.Fatal(err)
	}
	if seq, _ := st.MemoryExtractionProgress(ctx, "a"); seq != 9 {
		t.Fatalf("progress moved back to %d", seq)
	}
	next := MemoryExtraction{Session: "a", Harness: "claude", Model: "m", From: 10, To: 12, Trigger: "archive"}
	next, err = st.CreateMemoryExtraction(ctx, next, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FailMemoryExtraction(ctx, next.ID, "boom", now); err != nil {
		t.Fatal(err)
	}
	last, err := st.LastMemoryExtraction(ctx, "a")
	if err != nil || last.State != ExtractionFailed || last.Error != "boom" {
		t.Fatalf("last = %+v, %v", last, err)
	}
	day := now.Add(-time.Hour)
	if n, err := st.CountMemoryExtractions(ctx, "claude", day, day.Add(24*time.Hour)); err != nil || n != 2 {
		t.Fatalf("count = %d, %v", n, err)
	}
	if n, _ := st.CountMemoryExtractions(ctx, "hermes", day, day.Add(24*time.Hour)); n != 0 {
		t.Fatalf("hermes count = %d", n)
	}
	if n, _ := st.CountFailedMemoryExtractions(ctx, day, day.Add(24*time.Hour)); n != 1 {
		t.Fatalf("failed = %d", n)
	}
}

func TestMemoryExtractionSwitch(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if on, err := st.MemoryExtractionEnabled(ctx); err != nil || !on {
		t.Fatalf("default = %v, %v", on, err)
	}
	if err := st.SetMemoryExtractionEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := st.MemoryExtractionEnabled(ctx); on {
		t.Fatal("still on")
	}
}
