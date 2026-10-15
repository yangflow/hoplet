// Command fakeclaude is a scripted stand-in for Claude Code 2.1.289 in streaming JSON mode, for tests. It
// speaks the stdio control protocol of docs/adr/0008 and appends the turns it runs to the session file.
//
// A prompt's first word selects the script of its turn:
//
//	say <text>     stream <text>, write the turn, succeed
//	stream <n>     stream n text deltas 10 ms apart, each `<unix microseconds at emission>;`, then succeed
//	tool <cmd>     ask permission for a Bash tool; allow runs it, deny is reported to the model
//	sleep          a turn that ends only when interrupted
//	fail           end the turn with an error result
//	crash          exit with status 3
//	images         answer `<n> images: <media types>` for the images that came with the prompt
//	model          answer `model: <model>`, the --model it started with or the last set_model ("default")
//	anything else  behaves like `say`
//
// A prompt's images are written into its user line before the text, as Claude Code records them.
//
// Started with --session-id the session is new: its file is created, in the store directory of the working
// directory under FAKE_CLAUDE_PROJECTS, when the first turn writes it, as Claude Code does.
//
// Environment: FAKE_CLAUDE_VERSION (default 2.1.289), FAKE_CLAUDE_PROJECTS (directory searched for
// <session>.jsonl, one level deep), FAKE_CLAUDE_LOG (file receiving one JSON line per start with the
// arguments), FAKE_CLAUDE_INIT_DELAY_MS, FAKE_CLAUDE_EXIT_ON_START (exit 4 before answering initialize),
// FAKE_CLAUDE_IGNORE_STDIN_CLOSE (keep running after stdin closes, to exercise the kill fallback).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type obj = map[string]any

var (
	outMu   sync.Mutex
	out     = bufio.NewWriter(os.Stdout)
	session string
	file    string
	cwd     string
	fresh   bool // started with --session-id: the file does not exist before the first turn
)

// storeName is the store directory name of a working directory: its real path with every character but
// ASCII letters and digits replaced by `-`.
func storeName(dir string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, real)
}

func emit(v any) {
	line, _ := json.Marshal(v)
	outMu.Lock()
	defer outMu.Unlock()
	out.Write(line)
	out.WriteByte('\n')
	out.Flush()
}

func uuid() string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1e12) }

func state(s string) {
	emit(obj{"type": "system", "subtype": "session_state_changed", "state": s, "uuid": uuid(), "session_id": session})
}

func appendLines(lines ...obj) {
	if file == "" && fresh {
		root := os.Getenv("FAKE_CLAUDE_PROJECTS")
		if root == "" {
			return
		}
		dir := filepath.Join(root, storeName(cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		file = filepath.Join(dir, session+".jsonl")
	}
	if file == "" {
		return
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	for _, l := range lines {
		b, _ := json.Marshal(l)
		f.Write(append(b, '\n'))
	}
}

// findSession looks for the session where Claude Code does: in the store directory of the working
// directory (its real path with every character but ASCII letters and digits replaced by `-`). Only when
// that directory has none does it search every directory, so tests with a loose layout still work.
func findSession(id string) string {
	root := os.Getenv("FAKE_CLAUDE_PROJECTS")
	if root == "" {
		return ""
	}
	if _, err := filepath.EvalSymlinks(cwd); err == nil {
		p := filepath.Join(root, storeName(cwd), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		p := filepath.Join(root, d.Name(), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

type turn struct {
	text      string
	images    []obj // the prompt's image blocks, as they arrived
	interrupt chan struct{}
	answers   chan obj
}

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "--version" {
			v := os.Getenv("FAKE_CLAUDE_VERSION")
			if v == "" {
				v = "2.1.289"
			}
			fmt.Printf("%s (Claude Code)\n", v)
			return
		}
	}
	for i, a := range args {
		if a == "--resume" && i+1 < len(args) {
			session = args[i+1]
		}
		if a == "--model" && i+1 < len(args) {
			model = args[i+1]
		}
		if a == "--session-id" && i+1 < len(args) {
			session, fresh = args[i+1], true
		}
	}
	cwd, _ = os.Getwd()
	if p := os.Getenv("FAKE_CLAUDE_LOG"); p != "" {
		f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		b, _ := json.Marshal(obj{"pid": os.Getpid(), "args": args, "cwd": cwd})
		f.Write(append(b, '\n'))
		f.Close()
	}
	if !fresh {
		file = findSession(session)
	}
	if file == "" && !fresh && os.Getenv("FAKE_CLAUDE_PROJECTS") != "" {
		fmt.Fprintf(os.Stderr, "No conversation found with session ID: %s\n", session)
		os.Exit(1)
	}
	if os.Getenv("FAKE_CLAUDE_EXIT_ON_START") != "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: exiting on start")
		os.Exit(4)
	}

	var cur *turn
	var curMu sync.Mutex
	// Turns run one at a time in the order they arrive. A prompt that arrives while a turn runs waits, as Claude
	// Code queues it; it is never dropped, however soon after the previous result it comes. The newest turn is
	// the one interrupts and answers go to from the moment it arrives, before it starts: an interrupt sent at once
	// waits in its channel.
	turns := make(chan *turn, 64)
	go func() {
		for t := range turns {
			turnImages = t.images
			runTurn(t, t.text)
		}
	}()

	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var m struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype string `json:"subtype"`
					Model   string `json:"model"`
				} `json:"request"`
				Message struct {
					Content []obj `json:"content"`
				} `json:"message"`
				Response struct {
					RequestID string `json:"request_id"`
					Response  obj    `json:"response"`
				} `json:"response"`
			}
			if json.Unmarshal(line, &m) == nil {
				switch m.Type {
				case "control_request":
					switch m.Request.Subtype {
					case "initialize":
						if ms, _ := strconv.Atoi(os.Getenv("FAKE_CLAUDE_INIT_DELAY_MS")); ms > 0 {
							time.Sleep(time.Duration(ms) * time.Millisecond)
						}
						emit(obj{"type": "control_response", "response": obj{"subtype": "success", "request_id": m.RequestID, "response": obj{"commands": []any{}}}})
					case "set_model":
						// As Claude Code 2.1.299 answers: the aliases it knows, an error for any other.
						switch m.Request.Model {
						case "default", "fable", "opus", "sonnet", "haiku":
							modelMu.Lock()
							model = m.Request.Model
							modelMu.Unlock()
							emit(obj{"type": "control_response", "response": obj{"subtype": "success", "request_id": m.RequestID}})
						default:
							emit(obj{"type": "control_response", "response": obj{"subtype": "error", "request_id": m.RequestID,
								"error": "Model '" + m.Request.Model + "' not found", "error_code": "catalog_unknown"}})
						}
					case "interrupt":
						curMu.Lock()
						t := cur
						curMu.Unlock()
						emit(obj{"type": "control_response", "response": obj{"subtype": "success", "request_id": m.RequestID, "response": obj{"still_queued": []any{}}}})
						if t != nil {
							select {
							case t.interrupt <- struct{}{}:
							default:
							}
						}
					}
				case "control_response":
					curMu.Lock()
					t := cur
					curMu.Unlock()
					if t != nil {
						t.answers <- m.Response.Response
					}
				case "user":
					text := ""
					var images []obj
					for _, block := range m.Message.Content {
						switch block["type"] {
						case "text":
							text, _ = block["text"].(string)
						case "image":
							images = append(images, block)
						}
					}
					t := &turn{text: text, images: images, interrupt: make(chan struct{}, 1), answers: make(chan obj, 4)}
					curMu.Lock()
					cur = t
					curMu.Unlock()
					turns <- t
				}
			}
		}
		if err != nil {
			if os.Getenv("FAKE_CLAUDE_IGNORE_STDIN_CLOSE") != "" {
				time.Sleep(time.Hour)
			}
			return
		}
	}
}

// model is the model the process runs on: --model, then the last set_model.
var (
	modelMu sync.Mutex
	model   = "default"
)

// turnImages are the image blocks of the turn that runs; its user line carries them before the text.
var turnImages []obj

func userLine(text string) obj {
	if len(turnImages) > 0 {
		content := append(append([]obj(nil), turnImages...), obj{"type": "text", "text": text})
		return obj{"type": "user", "cwd": cwd, "sessionId": session, "message": obj{"role": "user", "content": content}}
	}
	return obj{"type": "user", "cwd": cwd, "sessionId": session, "message": obj{"role": "user", "content": text}}
}

func assistantLine(text string) obj {
	return obj{"type": "assistant", "cwd": cwd, "sessionId": session, "message": obj{"role": "assistant", "content": []obj{{"type": "text", "text": text}}}}
}

func streamText(text string) {
	emit(obj{"type": "stream_event", "event": obj{"type": "content_block_start", "index": 1, "content_block": obj{"type": "text", "text": ""}}, "session_id": session, "parent_tool_use_id": nil})
	for _, w := range strings.SplitAfter(text, " ") {
		emit(obj{"type": "stream_event", "event": obj{"type": "content_block_delta", "index": 1, "delta": obj{"type": "text_delta", "text": w}}, "session_id": session, "parent_tool_use_id": nil})
	}
}

func think() {
	emit(obj{"type": "stream_event", "event": obj{"type": "content_block_start", "index": 0, "content_block": obj{"type": "thinking", "thinking": "", "signature": ""}}, "session_id": session, "parent_tool_use_id": nil})
	emit(obj{"type": "system", "subtype": "thinking_tokens", "estimated_tokens": 50, "estimated_tokens_delta": 50, "session_id": session})
	emit(obj{"type": "stream_event", "event": obj{"type": "content_block_delta", "index": 0, "delta": obj{"type": "thinking_delta", "thinking": "", "estimated_tokens": 50}}, "session_id": session, "parent_tool_use_id": nil})
	emit(obj{"type": "stream_event", "event": obj{"type": "content_block_delta", "index": 0, "delta": obj{"type": "signature_delta", "signature": "SIGNATURE-MUST-NOT-BE-RELAYED"}}, "session_id": session, "parent_tool_use_id": nil})
}

func start() {
	state("running")
	emit(obj{"type": "system", "subtype": "init", "cwd": cwd, "session_id": session})
}

func result(subtype string, isErr bool, terminal, text string) {
	emit(obj{"type": "result", "subtype": subtype, "is_error": isErr, "terminal_reason": terminal, "result": text, "session_id": session, "permission_denials": []any{}})
	state("idle")
}

func runTurn(t *turn, prompt string) {
	cmd, rest, _ := strings.Cut(prompt, " ")
	switch cmd {
	case "crash":
		fmt.Fprintln(os.Stderr, "fakeclaude: crashed")
		os.Exit(3)
	case "sleep":
		start()
		think()
		select {
		case <-t.interrupt:
		case <-time.After(30 * time.Second):
		}
		appendLines(userLine(prompt))
		result("error_during_execution", true, "aborted_tools", "")
	case "stream":
		start()
		n, _ := strconv.Atoi(rest)
		emit(obj{"type": "stream_event", "event": obj{"type": "content_block_start", "index": 1, "content_block": obj{"type": "text", "text": ""}}, "session_id": session, "parent_tool_use_id": nil})
		for i := 0; i < n; i++ {
			emit(obj{"type": "stream_event", "event": obj{"type": "content_block_delta", "index": 1, "delta": obj{"type": "text_delta", "text": strconv.FormatInt(time.Now().UnixMicro(), 10) + ";"}}, "session_id": session, "parent_tool_use_id": nil})
			time.Sleep(10 * time.Millisecond)
		}
		text := "streamed " + rest
		emit(obj{"type": "assistant", "message": obj{"role": "assistant", "content": []obj{{"type": "text", "text": text}}}, "session_id": session, "parent_tool_use_id": nil})
		appendLines(userLine(prompt), assistantLine(text))
		result("success", false, "completed", text)
	case "fail":
		start()
		appendLines(userLine(prompt))
		result("error_during_execution", true, "", "boom")
	case "tool":
		start()
		think()
		id := "toolu_" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10)
		input := obj{"command": rest}
		emit(obj{"type": "assistant", "message": obj{"role": "assistant", "content": []obj{{"type": "tool_use", "id": id, "name": "Bash", "input": input}}}, "session_id": session, "parent_tool_use_id": nil})
		state("requires_action")
		reqID := "req-" + id
		emit(obj{"type": "control_request", "request_id": reqID, "request": obj{"subtype": "can_use_tool", "tool_name": "Bash", "input": input, "tool_use_id": id}})
		select {
		case <-t.interrupt:
			emit(obj{"type": "control_cancel_request", "request_id": reqID})
			state("running")
			appendLines(userLine(prompt))
			result("error_during_execution", true, "aborted_tools", "")
		case ans := <-t.answers:
			if ans["behavior"] == "allow" {
				emit(obj{"type": "user", "message": obj{"role": "user", "content": []obj{{"type": "tool_result", "tool_use_id": id, "content": "ran: " + rest, "is_error": false}}}, "session_id": session, "parent_tool_use_id": nil})
				streamText("DONE")
				appendLines(userLine(prompt), assistantLine("DONE"))
			} else {
				emit(obj{"type": "user", "message": obj{"role": "user", "content": []obj{{"type": "tool_result", "tool_use_id": id, "content": fmt.Sprint(ans["message"]), "is_error": true}}}, "session_id": session, "parent_tool_use_id": nil})
				streamText("REFUSED")
				appendLines(userLine(prompt), assistantLine("REFUSED"))
			}
			state("running")
			result("success", false, "completed", "")
		}
	default:
		start()
		think()
		text := prompt
		if cmd == "say" {
			text = rest
		}
		if cmd == "model" {
			modelMu.Lock()
			text = "model: " + model
			modelMu.Unlock()
		}
		if cmd == "images" {
			types := make([]string, 0, len(t.images))
			for _, img := range t.images {
				src, _ := img["source"].(obj)
				mt, _ := src["media_type"].(string)
				types = append(types, mt)
			}
			text = fmt.Sprintf("%d images: %s", len(t.images), strings.Join(types, ","))
		}
		streamText(text)
		emit(obj{"type": "assistant", "message": obj{"role": "assistant", "content": []obj{{"type": "text", "text": text}}}, "session_id": session, "parent_tool_use_id": nil})
		appendLines(userLine(prompt), assistantLine(text))
		result("success", false, "completed", text)
	}
}
