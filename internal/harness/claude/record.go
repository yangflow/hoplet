package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yangflow/hoplet/internal/harness"
)

// Step is one step of a recording script.
//
//	{"do":"user","text":"..."}                        send a user message
//	{"do":"control","request":{"subtype":"interrupt"}} send a control request
//	{"do":"wait","for":"result"}                      wait for a line: result,
//	    control_response, or can_use_tool (answered with "answer": allow,
//	    always or deny); "timeout" is in seconds, default 120
type Step struct {
	Do      string          `json:"do"`
	Text    string          `json:"text,omitempty"`
	Request json.RawMessage `json:"request,omitempty"`
	For     string          `json:"for,omitempty"`
	Answer  string          `json:"answer,omitempty"`
	Timeout int             `json:"timeout,omitempty"`
}

// Entry is one recorded line: "in" (written to claude), "out" (claude's
// stdout), "err" (its stderr) or "exit" (its exit code).
type Entry struct {
	T    float64 `json:"t"`
	Dir  string  `json:"dir"`
	Line string  `json:"line"`
}

// RecordConfig configures a recording.
type RecordConfig struct {
	Binary string // the claude binary
	Dir    string // working directory
	Env    []string
	Spec   harness.Spec // mode, plan, effort... of the session; WorkDir and Env are filled in
	Steps  []Step
}

// Record runs claude as the adapter launches it, plays the steps and
// writes every line in both directions to out as JSON lines. The recording
// holds what claude printed, personal paths included: Sanitize it before
// committing it as a fixture.
func Record(ctx context.Context, cfg RecordConfig, out io.Writer) error {
	spec := cfg.Spec
	spec.WorkDir, spec.Env = cfg.Dir, cfg.Env
	l, err := (&Harness{Binary: cfg.Binary}).Launch(spec)
	if err != nil {
		return err
	}
	cmd := launchCommand(ctx, l)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("claude: %w", err)
	}
	rec := &recorder{start: time.Now(), out: out, lines: make(chan []byte, 256)}
	var wg sync.WaitGroup
	var outErr, errErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(nil, harness.MaxLine)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			rec.write("out", string(line))
			rec.lines <- line
		}
		outErr = sc.Err()
		close(rec.lines) // before draining: play must fail at once, not at a step timeout
		if outErr != nil {
			_, _ = io.Copy(io.Discard, stdout) // keep draining so claude cannot block
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(nil, harness.MaxLine)
		for sc.Scan() {
			rec.write("err", sc.Text())
		}
		if errErr = sc.Err(); errErr != nil {
			_, _ = io.Copy(io.Discard, stderr)
		}
	}()
	playErr := rec.play(ctx, stdin, cfg.Steps)
	// Closing stdin ends claude's input; it exits once it has answered.
	closeErr := stdin.Close()
	for range rec.lines {
	}
	wg.Wait()
	waitErr := cmd.Wait()
	code := 0
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		code, waitErr = ee.ExitCode(), nil
	}
	rec.write("exit", fmt.Sprint(code))
	return errors.Join(playErr, closeErr, waitErr, rec.err, outErr, errErr)
}

type recorder struct {
	start time.Time
	out   io.Writer
	lines chan []byte
	n     int

	mu  sync.Mutex
	err error
}

func (r *recorder) write(dir, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.Marshal(Entry{T: float64(time.Since(r.start).Milliseconds()) / 1000, Dir: dir, Line: line})
	if err == nil {
		_, err = r.out.Write(append(b, '\n'))
	}
	if err != nil && r.err == nil {
		r.err = err
	}
}

func (r *recorder) send(stdin io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.write("in", string(b))
	_, err = stdin.Write(append(b, '\n'))
	return err
}

func (r *recorder) play(ctx context.Context, stdin io.Writer, steps []Step) error {
	for i, st := range steps {
		var err error
		switch st.Do {
		case "user":
			err = r.send(stdin, userLine{Type: "user", Message: userMessage{Role: "user", Content: st.Text}})
		case "control":
			r.n++
			err = r.send(stdin, requestLine{Type: "control_request", RequestID: fmt.Sprintf("rec-%d", r.n), Request: st.Request})
		case "wait":
			err = r.wait(ctx, stdin, st)
		default:
			err = fmt.Errorf("unknown step %q", st.Do)
		}
		if err != nil {
			return fmt.Errorf("claude: step %d: %w", i+1, err)
		}
	}
	return nil
}

func (r *recorder) wait(ctx context.Context, stdin io.Writer, st Step) error {
	timeout := time.Duration(st.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		var data []byte
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("no %s within %v", st.For, timeout)
		case l, ok := <-r.lines:
			if !ok {
				return fmt.Errorf("claude exited before %s", st.For)
			}
			data = l
		}
		var ln line
		if json.Unmarshal(data, &ln) != nil {
			continue
		}
		switch {
		case st.For == "result" && ln.Type == "result",
			st.For == "control_response" && ln.Type == "control_response":
			return nil
		case st.For == "can_use_tool" && ln.Type == "control_request" && ln.Request != nil && ln.Request.Subtype == "can_use_tool":
			if st.Answer == "" {
				return nil
			}
			perm := permission{Behavior: "allow", UpdatedInput: ln.Request.Input}
			switch st.Answer {
			case optionAllow:
			case optionAlways:
				perm.UpdatedPermissions = ln.Request.Suggestions
			case optionDeny:
				perm = permission{Behavior: "deny", Message: denyMessage}
			default:
				return fmt.Errorf("unknown answer %q", st.Answer)
			}
			return r.send(stdin, responseLine{Type: "control_response", Response: responseInner{Subtype: "success", RequestID: ln.RequestID, Response: perm}})
		}
	}
}

// Sanitize turns a recording into a fixture: it drops stderr and noise
// (rate limit events, partial tool input and signatures, which can split
// or hide paths, and hook system lines, which can carry the output of the
// user's own hooks), reduces claude's initialize answer and init lines to
// the fields the adapter reads (they list the user's account, skills and
// plugins), removes thinking signatures, and replaces dir and home with
// /work and /Users/me, as paths (given and symlink-resolved) and as the
// dash slugs claude names project directories with. Timestamps are
// dropped.
//
// The recording is cut at the first approval request claude sent that was
// neither answered nor withdrawn (control_cancel_request): what follows is
// the recorder giving up, not claude's protocol.
func Sanitize(in io.Reader, out io.Writer, dir, home string) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(nil, harness.MaxLine)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	scrub := scrubber(dir, home)
	type kept struct {
		dir, line, requestID string
		// resolves is the approval this line settles: the request an
		// answer replies to, or one claude withdraws.
		resolves string
	}
	var entries []kept
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return fmt.Errorf("claude: recording line: %w", err)
		}
		if e.Dir == "err" {
			continue
		}
		if e.Dir == "exit" {
			entries = append(entries, kept{dir: e.Dir, line: e.Line})
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(e.Line), &v); err != nil {
			return fmt.Errorf("claude: recorded line: %w", err)
		}
		if keep := reduce(e.Dir, v); !keep {
			continue
		}
		var b strings.Builder
		le := json.NewEncoder(&b)
		le.SetEscapeHTML(false)
		if err := le.Encode(stripKey(v, "signature")); err != nil {
			return err
		}
		k := kept{dir: e.Dir, line: scrub(strings.TrimSpace(b.String()))}
		if req, _ := v["request"].(map[string]any); e.Dir == "out" && v["type"] == "control_request" && req["subtype"] == "can_use_tool" {
			k.requestID, _ = v["request_id"].(string)
		}
		switch {
		case e.Dir == "out" && v["type"] == "control_cancel_request":
			k.resolves, _ = v["request_id"].(string)
		case e.Dir == "in" && v["type"] == "control_response":
			resp, _ := v["response"].(map[string]any)
			k.resolves, _ = resp["request_id"].(string)
		}
		entries = append(entries, k)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for i, k := range entries {
		if k.requestID == "" {
			continue
		}
		answered := false
		for _, later := range entries[i+1:] {
			if later.resolves == k.requestID {
				answered = true
				break
			}
		}
		if !answered {
			entries = entries[:i]
			break
		}
	}
	for _, k := range entries {
		if err := enc.Encode(map[string]string{"dir": k.dir, "line": k.line}); err != nil {
			return err
		}
	}
	return nil
}

// scrubber returns the function that replaces dir and home in a line. For
// each it covers the given and the symlink-resolved path (macOS temporary
// directories live under /var, a link to /private/var) and the dash slug
// claude derives project directory names from. Longer paths go first, as
// dir may be inside home.
func scrubber(dir, home string) func(string) string {
	type pair struct{ from, to string }
	var pairs []pair
	add := func(p, to string) {
		if p == "" {
			return
		}
		forms := []string{p}
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			forms = append(forms, r)
		}
		for _, f := range forms {
			pairs = append(pairs, pair{f, to})
			for _, slug := range []func(string) string{
				func(p string) string { return strings.ReplaceAll(p, "/", "-") },
				func(p string) string { // claude's project directory names
					return strings.Map(func(r rune) rune {
						if isAlnum(r) {
							return r
						}
						return '-'
					}, p)
				},
			} {
				if from := slug(f); from != to && !slices.ContainsFunc(pairs, func(p pair) bool { return p.from == from }) {
					pairs = append(pairs, pair{from, slug(to)})
				}
			}
		}
	}
	add(dir, "/work")
	add(home, "/Users/me")
	slices.SortStableFunc(pairs, func(a, b pair) int { return len(b.from) - len(a.from) })
	return func(s string) string {
		for _, p := range pairs {
			s = replacePath(s, p.from, p.to)
		}
		return s
	}
}

// replacePath replaces the occurrences of the path from in s, except those
// followed by an ASCII letter or digit, which continue into a longer name:
// from "/Users/ann" leaves "/Users/anna/x" alone. Anything else after
// it (a slash, a quote, a period, "-", "_", the end) ends the path; erring
// toward scrubbing too much is intended.
func replacePath(s, from, to string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, from)
		if i < 0 {
			break
		}
		end := i + len(from)
		b.WriteString(s[:i])
		if end < len(s) && isAlnum(rune(s[end])) {
			b.WriteString(from) // part of a longer name
		} else {
			b.WriteString(to)
		}
		s = s[end:]
	}
	b.WriteString(s)
	return b.String()
}

func isAlnum(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// reduce trims a recorded line in place and reports whether to keep it.
func reduce(dir string, v map[string]any) bool {
	switch v["type"] {
	case "rate_limit_event":
		return false
	case "stream_event":
		ev, _ := v["event"].(map[string]any)
		delta, _ := ev["delta"].(map[string]any)
		if t := delta["type"]; t == "input_json_delta" || t == "signature_delta" {
			return false
		}
	case "control_response":
		if dir == "out" {
			if resp, ok := v["response"].(map[string]any); ok {
				if inner, ok := resp["response"].(map[string]any); ok && inner["commands"] != nil {
					resp["response"] = map[string]any{}
				}
			}
		}
	case "system":
		if st, _ := v["subtype"].(string); strings.Contains(st, "hook") {
			return false
		}
		if v["subtype"] == "init" {
			for k := range v {
				switch k {
				case "type", "subtype", "cwd", "session_id", "model", "permissionMode", "claude_code_version", "uuid":
				default:
					delete(v, k)
				}
			}
		}
	}
	return true
}

func stripKey(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == key {
				delete(x, k)
				continue
			}
			x[k] = stripKey(e, key)
		}
	case []any:
		for i, e := range x {
			x[i] = stripKey(e, key)
		}
	}
	return v
}
