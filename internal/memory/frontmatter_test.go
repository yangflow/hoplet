package memory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yangflow/hoplet/internal/store"
)

const testID = "mem_0123456789abcdef0123456789abcdef"
const otherID = "mem_fedcba9876543210fedcba9876543210"

var zeroTime time.Time

func testEntry() Entry {
	return Entry{
		ID:        testID,
		Version:   3,
		Category:  CategoryPreference,
		Status:    StatusActive,
		Scope:     Scope{ScopeRepo, "github.com/x/y"},
		Evidence:  []Evidence{{EvidenceUserStated, "event:ses_123/456"}},
		UpdatedAt: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		Body:      "以后优先直达\n\n第二段",
	}
}

func TestRoundTrip(t *testing.T) {
	until := time.Date(2027, 1, 2, 3, 4, 5, 6000000, time.UTC)
	tests := []struct {
		name string
		mod  func(*Entry)
	}{
		{"plain", func(*Entry) {}},
		{"global", func(e *Entry) { e.Scope = Scope{ScopeGlobal, ""} }},
		{"no evidence", func(e *Entry) { e.Evidence = nil }},
		{"chinese scope", func(e *Entry) { e.Scope.Value = "我的 仓库" }},
		{"awkward values", func(e *Entry) {
			e.Scope.Value = `a: "b", c }` + "\nd"
			e.Evidence = []Evidence{{EvidenceObserved, `x, ref: "y"`}, {EvidenceInferred, "第二\t条"}}
		}},
		{"valid until", func(e *Entry) { e.ValidUntil = &until }},
		{"superseded", func(e *Entry) { e.Status, e.ReplacedBy = StatusSuperseded, otherID }},
		{"fence in body", func(e *Entry) { e.Body = "a\n---\nb: c" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := testEntry()
			tt.mod(&e)
			b, err := Encode(e)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(b)
			if err != nil {
				t.Fatalf("%v\n%s", err, b)
			}
			if !reflect.DeepEqual(got, e) {
				t.Fatalf("got %+v, want %+v\n%s", got, e, b)
			}
		})
	}
}

func TestDecodeAcceptsAnyOrderAndStatusComment(t *testing.T) {
	in := `---
updated_at: 2026-11-08T10:00:00Z
replaced_by: null
valid_until: null
evidence: []
scope: { kind: global, value: "" }
status: active   # candidate | active
category: fact
version: 1
id: ` + testID + `
---
正文
`
	e, err := Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != StatusActive || e.Body != "正文" {
		t.Fatalf("%+v", e)
	}
}

func TestDecodeRejects(t *testing.T) {
	good, err := Encode(testEntry())
	if err != nil {
		t.Fatal(err)
	}
	s := string(good)
	replace := func(old, new string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("%q not in file", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	tests := []struct {
		name, in string
	}{
		{"empty", ""},
		{"no opening fence", strings.TrimPrefix(s, "---\n")},
		{"not closed", replace("\n---\n以后", "\n以后")},
		{"unknown field", replace("id:", "extra: x\nid:")},
		{"duplicate field", replace("version: 3", "version: 3\nversion: 3")},
		{"missing field", replace("category: preference\n", "")},
		{"indented field", replace("version: 3", " version: 3")},
		{"no space after colon", replace("version: 3", "version:3")},
		{"bad id", replace(testID, "mem_x")},
		{"traversal id", replace(testID, "../../x")},
		{"version zero", replace("version: 3", "version: 0")},
		{"version text", replace("version: 3", "version: three")},
		{"bad category", replace("category: preference", "category: skill")},
		{"bad status", replace("status: active", "status: gone")},
		{"bad scope kind", replace("kind: repo", "kind: scene")},
		{"global with value", replace("kind: repo", "kind: global")},
		{"scope not a mapping", replace("{ kind: repo, value: github.com/x/y }", "repo")},
		{"scope trailing", replace("github.com/x/y }", "github.com/x/y } x")},
		{"bad evidence kind", replace("user_stated", "guess")},
		{"empty evidence ref", replace("event:ses_123/456", `""`)},
		{"evidence without items", replace("  - { kind: user_stated, ref: event:ses_123/456 }\n", "")},
		{"evidence bad list", replace("evidence:\n", "evidence: x\n")},
		{"unterminated quote", replace("github.com/x/y", `"abc`)},
		{"local time", replace("10:00:00Z", "10:00:00+08:00")},
		{"not a time", replace("2026-11-08T10:00:00Z", "soon")},
		{"replaced_by without superseded", replace("replaced_by: null", "replaced_by: "+otherID)},
		{"superseded without replaced_by", replace("status: active", "status: superseded")},
		{"empty body", s[:strings.LastIndex(s, "---\n")+4]},
		{"crlf", strings.ReplaceAll(s, "\n", "\r\n")},
		{"oversized body", s + strings.Repeat("x", MaxBody)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode([]byte(tt.in)); !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	e := testEntry()
	e.Body = "  padded"
	if _, err := Encode(e); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestTitle(t *testing.T) {
	e := testEntry()
	if got := e.Title(); got != "以后优先直达" {
		t.Fatal(got)
	}
	e.Body = strings.Repeat("字", 100)
	if got := []rune(e.Title()); len(got) != titleRunes {
		t.Fatal(len(got))
	}
}

func FuzzDecode(f *testing.F) {
	b, err := Encode(testEntry())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte("---\n---\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := Decode(data)
		if err != nil {
			return
		}
		again, err := Encode(e)
		if err != nil {
			t.Fatalf("decoded entry does not encode: %v", err)
		}
		got, err := Decode(again)
		if err != nil || !reflect.DeepEqual(got, e) {
			t.Fatalf("round trip: %+v, %v, want %+v", got, err, e)
		}
	})
}
