package hermes

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/yangflow/hoplet/internal/harness"
	"github.com/yangflow/hoplet/internal/harness/scrub"
)

// Entry is one line of a fixture: "in" (written to the bridge), "out"
// (the bridge's stdout) or "exit" (its exit code).
type Entry struct {
	Dir  string `json:"dir"`
	Line string `json:"line"`
}

// spoolRecord is the part of a shim spool record a fixture keeps.
type spoolRecord struct {
	Kind string `json:"k"`
	Data string `json:"d"`
	More bool   `json:"more"`
	Code int    `json:"code"`
}

// Sanitize turns the spool of a real Hermes session into a contract-test
// fixture: it keeps the bridge's input and output, drops events that only
// dress a user interface, cuts what a snapshot, a receipt or session info
// carries about the user's own setup (tools, MCP servers, history) and
// replaces dir and home with /work and /Users/me.
func Sanitize(in io.Reader, out io.Writer, dir, home string) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, harness.MaxLine+1<<20)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	clean := scrub.New(dir, home)
	var pending strings.Builder // a line continued over several records
	for sc.Scan() {
		var r spoolRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return fmt.Errorf("hermes: spool record: %w", err)
		}
		var e Entry
		switch r.Kind {
		case "stdout", "stdin":
			pending.WriteString(r.Data)
			if r.More {
				continue
			}
			line := pending.String()
			pending.Reset()
			reduced, keep, err := reduce(line)
			if err != nil {
				return err
			}
			if !keep {
				continue
			}
			e = Entry{Dir: map[string]string{"stdout": "out", "stdin": "in"}[r.Kind], Line: clean(reduced)}
		case "exit":
			e = Entry{Dir: "exit", Line: strconv.Itoa(r.Code)}
		default:
			continue
		}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

// dropped are events Hoplet ignores that only describe a user interface.
var dropped = map[string]bool{
	"gateway.ready": true, "sessions.changed": true, "platforms.changed": true, "status.update": true,
}

// kept are the session info fields the adapter reads.
var kept = []string{
	"session_id", "stored_session_id", "running", "revision", "execution_generation", "authority_epoch",
	"last_sequence", "replay_epoch", "pending", "prompts",
}

// reduce trims one line; keep is false for a line to drop.
func reduce(line string) (string, bool, error) {
	var v map[string]any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return "", false, fmt.Errorf("hermes: recorded line: %w", err)
	}
	if p, ok := v["params"].(map[string]any); ok && v["method"] == "event" {
		typ, _ := p["type"].(string)
		if dropped[typ] {
			return "", false, nil
		}
		if typ == "session.info" {
			p["payload"] = only(p["payload"], kept)
		}
	}
	if res, ok := v["result"].(map[string]any); ok {
		switch {
		case res["stored_session_id"] != nil: // a snapshot
			v["result"] = only(res, kept)
		case res["tools"] != nil: // session.info
			v["result"] = map[string]any{}
		case res["result"] != nil: // a receipt
			res["result"] = only(res["result"], []string{
				"final_response", "completed", "interrupted", "failed", "failure_reason", "error",
			})
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// only keeps the named fields of an object.
func only(v any, keys []string) map[string]any {
	m, _ := v.(map[string]any)
	out := map[string]any{}
	for _, k := range keys {
		if x, ok := m[k]; ok {
			out[k] = x
		}
	}
	return out
}
