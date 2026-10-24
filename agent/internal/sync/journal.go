package sync

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The journal holds the runtime events the core has not confirmed (ADR 0039): state changes, permission requests
// and their closures, rule decisions and its own gaps. Each gets an eventSeq, monotonic on the machine across
// restarts; the core names the highest it applied in the welcome and confirms the rest with journal.ack. The file
// is <hoplet_home>/journal.ndjson at mode 0600; with no home the journal lives in memory only.
//
// Lines of the file: a header {"next":N,"acked":A} written at each rewrite, an entry {"seq","at","pin","msg"} per
// event (msg is the wire message without eventSeq and at, added when it is sent) and {"acked":N} per confirmation.
// A torn last line is skipped on load.

const (
	journalFile = "journal.ndjson"
	// JournalMaxEvents and JournalMaxBytes are the cap (shared/src/wire/ JOURNAL_MAX_EVENTS and JOURNAL_MAX_BYTES).
	JournalMaxEvents = 10_000
	JournalMaxBytes  = 4 << 20
	// compactAfter is how many confirmations are appended before the file is rewritten without them.
	compactAfter = 512
)

type journalEntry struct {
	Seq int64           `json:"seq"`
	At  int64           `json:"at"`
	Pin bool            `json:"pin,omitempty"` // a permission request: never dropped at the cap
	Msg json.RawMessage `json:"msg"`
}

type journalLine struct {
	Next  *int64          `json:"next,omitempty"`
	Acked *int64          `json:"acked,omitempty"`
	Seq   int64           `json:"seq,omitempty"`
	At    int64           `json:"at,omitempty"`
	Pin   bool            `json:"pin,omitempty"`
	Msg   json.RawMessage `json:"msg,omitempty"`
}

type journal struct {
	mu        sync.Mutex
	path      string // empty: memory only
	next      int64  // the eventSeq of the next event
	acked     int64  // the highest eventSeq the core confirmed
	entries   []journalEntry
	size      int // bytes of the entries as lines
	sinceDump int // confirmations appended since the last rewrite
	maxEvents int
	maxBytes  int
	now       func() time.Time
}

// openJournal loads the journal of home; an empty home keeps it in memory.
func openJournal(home string) *journal {
	j := &journal{next: 1, maxEvents: JournalMaxEvents, maxBytes: JournalMaxBytes, now: time.Now}
	if home == "" {
		return j
	}
	j.path = filepath.Join(home, journalFile)
	if err := j.load(); err != nil {
		log.Printf("[agent] journal %s not read: %v; starting an empty one", j.path, err)
		j.entries, j.size = nil, 0
	}
	return j
}

func (j *journal) load() error {
	f, err := os.Open(j.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 2*JournalMaxBytes)
	var entries []journalEntry
	for sc.Scan() {
		var l journalLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue // a line torn by a crash
		}
		if l.Next != nil && *l.Next > j.next {
			j.next = *l.Next
		}
		if l.Acked != nil && *l.Acked > j.acked {
			j.acked = *l.Acked
		}
		if l.Seq > 0 && len(l.Msg) > 0 {
			entries = append(entries, journalEntry{Seq: l.Seq, At: l.At, Pin: l.Pin, Msg: l.Msg})
			if l.Seq >= j.next {
				j.next = l.Seq + 1
			}
		}
	}
	if j.acked >= j.next {
		j.next = j.acked + 1
	}
	for _, e := range entries {
		if e.Seq > j.acked {
			j.entries = append(j.entries, e)
			j.size += lineSize(e)
		}
	}
	return sc.Err()
}

func lineSize(e journalEntry) int {
	raw, _ := json.Marshal(e)
	return len(raw) + 1
}

// append journals msg and returns it as it goes on the wire, with eventSeq and at.
func (j *journal) append(msg any) ([]byte, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	_, pin := msg.(RuntimePermission)
	j.mu.Lock()
	defer j.mu.Unlock()
	e := j.add(raw, pin, j.now().UnixMilli())
	if j.over() {
		j.trim()
		j.rewrite()
	} else {
		j.write(e)
	}
	return wireOf(e, false), nil
}

func (j *journal) add(raw []byte, pin bool, at int64) journalEntry {
	e := journalEntry{Seq: j.next, At: at, Pin: pin, Msg: raw}
	j.next++
	j.entries = append(j.entries, e)
	j.size += lineSize(e)
	return e
}

func (j *journal) over() bool {
	return len(j.entries) > j.maxEvents || j.size > j.maxBytes
}

// trim drops the oldest events that are not permission requests until the journal is at 90% of its cap, and
// journals one gap that counts them (merged with a gap the core has not confirmed yet).
func (j *journal) trim() {
	keepEvents, keepBytes := j.maxEvents*9/10, j.maxBytes*9/10
	dropped := 0
	var from, to int64
	widen := func(f, t int64) {
		if from == 0 || f < from {
			from = f
		}
		if t > to {
			to = t
		}
	}
	kept := make([]journalEntry, 0, len(j.entries))
	for i, e := range j.entries {
		var g JournalGap
		if json.Unmarshal(e.Msg, &g) == nil && g.Type == TypeJournalGap {
			// An earlier gap the core has not confirmed folds into the new one.
			dropped += g.Dropped
			widen(g.From, g.To)
			j.size -= lineSize(e)
			continue
		}
		remaining := len(kept) + len(j.entries) - i
		if e.Pin || (remaining <= keepEvents && j.size <= keepBytes) {
			kept = append(kept, e)
			continue
		}
		dropped++
		widen(e.At, e.At)
		j.size -= lineSize(e)
	}
	j.entries = kept
	if dropped == 0 {
		return
	}
	log.Printf("[agent] the journal reached its cap; %d events the core has not received were dropped", dropped)
	raw, _ := json.Marshal(JournalGap{V: ProtocolVersion, Type: TypeJournalGap, Dropped: dropped, From: from, To: to})
	j.add(raw, false, j.now().UnixMilli())
}

// ack drops the events up to seq: the core applied them.
func (j *journal) ack(seq int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq <= j.acked {
		return
	}
	j.acked = seq
	if seq >= j.next {
		j.next = seq + 1
	}
	n := 0
	for n < len(j.entries) && j.entries[n].Seq <= seq {
		j.size -= lineSize(j.entries[n])
		n++
	}
	j.entries = append([]journalEntry(nil), j.entries[n:]...)
	j.sinceDump++
	if len(j.entries) == 0 || j.sinceDump > compactAfter {
		j.rewrite()
		return
	}
	j.writeLine([]byte(`{"acked":` + strconv.FormatInt(seq, 10) + `}`))
}

// welcome takes the core's high-water mark: what is at or before it is applied. A core that has applied more than
// this journal ever numbered (the file was lost) gets the pending events numbered after its mark.
func (j *journal) welcome(applied int64) {
	j.mu.Lock()
	if applied >= j.next {
		j.next = applied + 1
		for i := range j.entries {
			j.entries[i].Seq = j.next
			j.next++
		}
		j.acked = applied
		j.rewrite()
		j.mu.Unlock()
		return
	}
	j.mu.Unlock()
	j.ack(applied)
}

// pending is the events the core has not confirmed, in order, as they are replayed.
func (j *journal) pending() [][]byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([][]byte, 0, len(j.entries))
	for _, e := range j.entries {
		out = append(out, wireOf(e, true))
	}
	return out
}

// wireOf is the message of e with eventSeq and at, and replayed when it is sent again.
func wireOf(e journalEntry, replayed bool) []byte {
	msg := bytes.TrimRight(e.Msg, " \n")
	if len(msg) < 2 || msg[len(msg)-1] != '}' {
		return msg
	}
	var b bytes.Buffer
	b.Write(msg[:len(msg)-1])
	if len(msg) > 2 {
		b.WriteByte(',')
	}
	fmt.Fprintf(&b, `"eventSeq":%d,"at":%d`, e.Seq, e.At)
	if replayed {
		b.WriteString(`,"replayed":true`)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func (j *journal) write(e journalEntry) {
	raw, _ := json.Marshal(e)
	j.writeLine(raw)
}

func (j *journal) writeLine(line []byte) {
	if j.path == "" {
		return
	}
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		log.Printf("[agent] journal not written: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("[agent] journal not written: %v", err)
	}
}

// rewrite replaces the file with a header and the pending entries.
func (j *journal) rewrite() {
	j.sinceDump = 0
	if j.path == "" {
		return
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `{"next":%d,"acked":%d}`+"\n", j.next, j.acked)
	for _, e := range j.entries {
		raw, _ := json.Marshal(e)
		b.Write(raw)
		b.WriteByte('\n')
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		log.Printf("[agent] journal not written: %v", err)
		return
	}
	if err := os.Rename(tmp, j.path); err != nil {
		log.Printf("[agent] journal not written: %v", err)
	}
}
