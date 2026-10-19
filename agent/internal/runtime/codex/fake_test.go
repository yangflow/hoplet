package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// The tests run this test binary as the fake `codex`: with HOPLET_FAKE_CODEX set, TestMain serves the app
// server protocol (newline-delimited JSON-RPC) instead of running tests. The fake appends every line it
// receives to FAKE_CODEX_LOG, and the text of a prompt chooses what the turn does:
//
//	hello     one agent message, then the turn completes
//	slow      the turn waits for an interrupt (it ends interrupted) or a steer (it echoes the steer and completes)
//	cmd       a command approval; accept completes the turn, decline waits for a steer and completes
//	edit      the same with a file-change approval
//	plan      (plan collaboration) a plan item, then the turn completes
//	fail      an error notification, then the turn ends failed
//	settings  a thread/settings/updated that changes the effort, then the turn completes
//	exit      the process prints to stderr and exits with status 3
//	bad       turn/start is answered with an error
func runFake() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		v := os.Getenv("FAKE_CODEX_VERSION")
		if v == "" {
			v = "0.163.0"
		}
		fmt.Printf("codex-cli %s\n", v)
		return
	}
	f := &fake{out: bufio.NewWriter(os.Stdout), responses: make(chan map[string]any, 8), interrupt: make(chan struct{}, 4), steer: make(chan string, 4)}
	if path := os.Getenv("FAKE_CODEX_LOG"); path != "" {
		f.log, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if f.log != nil {
			_, _ = f.log.WriteString(line + "\n")
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		f.handle(m)
	}
}

type fake struct {
	mu        sync.Mutex
	out       *bufio.Writer
	log       *os.File
	thread    string
	turns     int
	curTurn   string
	responses chan map[string]any
	interrupt chan struct{}
	steer     chan string
}

func (f *fake) send(m map[string]any) {
	m["jsonrpc"] = "2.0"
	b, _ := json.Marshal(m)
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = f.out.Write(append(b, '\n'))
	_ = f.out.Flush()
}

func (f *fake) reply(id any, result any) { f.send(map[string]any{"id": id, "result": result}) }
func (f *fake) fail(id any, msg string) {
	f.send(map[string]any{"id": id, "error": map[string]any{"code": -32600, "message": msg}})
}
func (f *fake) note(method string, params map[string]any) {
	f.send(map[string]any{"method": method, "params": params})
}

func (f *fake) handle(m map[string]any) {
	method, _ := m["method"].(string)
	id := m["id"]
	params, _ := m["params"].(map[string]any)
	if method == "" {
		if id != nil {
			f.responses <- m
		}
		return
	}
	switch method {
	case "initialize":
		f.reply(id, map[string]any{"userAgent": "fake"})
	case "model/list":
		f.reply(id, map[string]any{"data": []any{
			map[string]any{"id": "m1", "model": "gpt-test", "displayName": "GPT Test", "hidden": false, "isDefault": true,
				"defaultReasoningEffort": "medium",
				"supportedReasoningEfforts": []any{
					map[string]any{"reasoningEffort": "low"}, map[string]any{"reasoningEffort": "medium"}, map[string]any{"reasoningEffort": "high"},
				}},
			map[string]any{"id": "m2", "model": "gpt-small", "displayName": "GPT Small", "hidden": false,
				"defaultReasoningEffort":    "low",
				"supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}}},
			map[string]any{"id": "m3", "model": "gpt-hidden", "displayName": "Hidden", "hidden": true},
		}})
	case "thread/start":
		f.thread = "thr-new"
		f.reply(id, map[string]any{"thread": map[string]any{"id": f.thread, "path": "/rollout"}, "model": "gpt-test"})
	case "thread/resume":
		tid, _ := params["threadId"].(string)
		if tid == "missing" {
			f.fail(id, "no rollout found for thread id missing")
			return
		}
		f.thread = tid
		f.reply(id, map[string]any{"thread": map[string]any{"id": f.thread, "path": "/rollout"}, "model": "gpt-test"})
	case "turn/start":
		f.turnStart(id, params)
	case "turn/steer":
		f.mu.Lock()
		cur := f.curTurn
		f.mu.Unlock()
		if cur == "" || params["expectedTurnId"] != cur {
			f.fail(id, "no active turn to steer")
			return
		}
		f.reply(id, map[string]any{"turnId": cur})
		f.steer <- inputText(params)
	case "turn/interrupt":
		f.reply(id, map[string]any{})
		f.interrupt <- struct{}{}
	default:
		if id != nil {
			f.fail(id, "unknown method "+method)
		}
	}
}

func inputText(params map[string]any) string {
	in, _ := params["input"].([]any)
	if len(in) == 0 {
		return ""
	}
	first, _ := in[0].(map[string]any)
	s, _ := first["text"].(string)
	return s
}

func (f *fake) turnStart(id any, params map[string]any) {
	text := inputText(params)
	if text == "bad" {
		f.fail(id, "turn refused")
		return
	}
	f.mu.Lock()
	f.turns++
	turn := fmt.Sprintf("turn-%d", f.turns)
	f.curTurn = turn
	f.mu.Unlock()
	collab := ""
	if cm, ok := params["collaborationMode"].(map[string]any); ok {
		collab, _ = cm["mode"].(string)
	}
	f.reply(id, map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}})
	f.note("turn/started", map[string]any{"threadId": f.thread, "turn": map[string]any{"id": turn, "status": "inProgress"}})
	go f.run(turn, text, collab)
}

func (f *fake) complete(turn, status string, errMsg string) {
	t := map[string]any{"id": turn, "status": status}
	if errMsg != "" {
		t["error"] = map[string]any{"message": errMsg}
	}
	f.mu.Lock()
	f.curTurn = ""
	f.mu.Unlock()
	f.note("turn/completed", map[string]any{"threadId": f.thread, "turn": t})
}

func (f *fake) delta(turn, s string) {
	f.note("item/agentMessage/delta", map[string]any{"threadId": f.thread, "turnId": turn, "itemId": "msg", "delta": s})
}

// approval raises a server request and waits for the client's answer, or an interrupt.
func (f *fake) approval(turn, method string, params map[string]any, reqID int) (decision string, interrupted bool) {
	params["threadId"], params["turnId"] = f.thread, turn
	f.send(map[string]any{"id": reqID, "method": method, "params": params})
	select {
	case r := <-f.responses:
		res, _ := r["result"].(map[string]any)
		d, _ := res["decision"].(string)
		f.note("serverRequest/resolved", map[string]any{"threadId": f.thread, "requestId": reqID})
		return d, false
	case <-f.interrupt:
		f.note("serverRequest/resolved", map[string]any{"threadId": f.thread, "requestId": reqID})
		return "", true
	case <-time.After(10 * time.Second):
		return "", false
	}
}

func (f *fake) run(turn, text, collab string) {
	switch {
	case text == "hello" || strings.HasPrefix(text, "Implement"):
		f.delta(turn, "hi")
		f.complete(turn, "completed", "")
	case text == "slow":
		select {
		case <-f.interrupt:
			f.complete(turn, "interrupted", "")
		case s := <-f.steer:
			f.delta(turn, "steered: "+s)
			f.complete(turn, "completed", "")
		case <-time.After(10 * time.Second):
		}
	case text == "cmd" || text == "edit":
		var d string
		var intr bool
		if text == "cmd" {
			f.note("item/started", map[string]any{"threadId": f.thread, "turnId": turn, "item": map[string]any{"type": "commandExecution", "id": "item-1", "command": "ls", "cwd": "/work"}})
			d, intr = f.approval(turn, "item/commandExecution/requestApproval",
				map[string]any{"itemId": "item-1", "command": "ls", "cwd": "/work", "reason": "list"}, 0)
		} else {
			d, intr = f.approval(turn, "item/fileChange/requestApproval",
				map[string]any{"itemId": "item-2", "reason": "edit", "grantRoot": "/work"}, 1)
		}
		switch {
		case intr:
			f.complete(turn, "interrupted", "")
		case d == "accept":
			if text == "cmd" {
				f.note("item/completed", map[string]any{"threadId": f.thread, "turnId": turn, "item": map[string]any{"type": "commandExecution", "id": "item-1", "status": "completed", "aggregatedOutput": "a.txt\n", "exitCode": 0}})
			}
			f.complete(turn, "completed", "")
		default:
			select {
			case s := <-f.steer:
				f.delta(turn, "steered: "+s)
			case <-time.After(2 * time.Second):
			}
			f.complete(turn, "completed", "")
		}
	case text == "waitcmd":
		_, intr := f.approval(turn, "item/commandExecution/requestApproval", map[string]any{"itemId": "item-3", "command": "rm", "cwd": "/work"}, 2)
		if intr {
			f.complete(turn, "interrupted", "")
		}
	case collab == "plan" && text == "plan":
		f.note("item/completed", map[string]any{"threadId": f.thread, "turnId": turn, "item": map[string]any{"type": "plan", "id": "plan-1", "text": "1. do it"}})
		f.complete(turn, "completed", "")
	case text == "fail":
		f.note("error", map[string]any{"threadId": f.thread, "turnId": turn, "error": map[string]any{"message": "boom"}})
		f.complete(turn, "failed", "")
	case text == "settings":
		for _, effort := range []string{"medium", "high"} {
			f.note("thread/settings/updated", map[string]any{"threadId": f.thread, "threadSettings": map[string]any{
				"approvalPolicy": "never", "approvalsReviewer": "user", "sandboxPolicy": map[string]any{"type": "dangerFullAccess"},
				"model": "gpt-test", "effort": effort, "collaborationMode": map[string]any{"mode": "plan"},
			}})
		}
		f.complete(turn, "completed", "")
	case text == "exit":
		fmt.Fprintln(os.Stderr, "fatal: the fake gave up")
		os.Exit(3)
	default:
		f.delta(turn, "echo: "+text)
		f.complete(turn, "completed", "")
	}
}
