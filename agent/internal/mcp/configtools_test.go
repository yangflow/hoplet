package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// configCore answers every route with the status and body it is told to and records what it was asked.
func configCore(t *testing.T, status int, body string) (*Core, *[]string) {
	t.Helper()
	f := newFakeCore(t)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+r.Header.Get("Authorization"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewCore(srv.URL, f.tokens), &seen
}

func TestGetConfigAsksForTheSessionsOwnConfig(t *testing.T) {
	c, seen := configCore(t, 200, `{"provider":"claude","state":"ready","options":[{"id":"model","currentValue":"sonnet"}]}`)
	rep := c.Call(context.Background(), "s1", ToolGetConfig, nil)
	if rep.IsError || !strings.HasPrefix(rep.Text, configNotice) || !strings.Contains(rep.Text, `"currentValue":"sonnet"`) {
		t.Fatalf("get_config = %+v", rep)
	}
	if len(*seen) != 1 || !strings.HasPrefix((*seen)[0], "GET /api/sessions/s1/config? Bearer hpt_") {
		t.Fatalf("requests = %v", *seen)
	}
}

func TestListSkillsDefaultsToTheProjectAndKeepsOnlyTheFields(t *testing.T) {
	c, seen := configCore(t, 200, `{"kind":"list","scope":"project","exists":true,"entries":[{"kind":"skill","name":"deploy","description":"Ship it","path":"/x"},{"kind":"command","name":"fix","description":""}]}`)
	rep := c.Call(context.Background(), "s1", ToolListSkills, nil)
	if rep.IsError || !strings.Contains(rep.Text, `"name":"deploy"`) || !strings.Contains(rep.Text, `"scope":"project"`) || strings.Contains(rep.Text, "/x") {
		t.Fatalf("list_skills = %+v", rep)
	}
	if !strings.Contains((*seen)[0], "/api/sessions/s1/config-files?scope=project ") {
		t.Fatalf("requests = %v", *seen)
	}
	rep = c.Call(context.Background(), "s1", ToolListSkills, json.RawMessage(`{"scope":"user"}`))
	if rep.IsError || !strings.Contains((*seen)[1], "scope=user") {
		t.Fatalf("user scope = %+v %v", rep, *seen)
	}
	if rep := c.Call(context.Background(), "s1", ToolListSkills, json.RawMessage(`{"scope":"../etc"}`)); !rep.IsError {
		t.Fatalf("a bad scope must be refused: %+v", rep)
	}
}

func TestReadSkillValidatesBeforeAnyRequest(t *testing.T) {
	c, seen := configCore(t, 200, `{}`)
	bad := []string{``, `{}`, `{"name":"../x"}`, `{"name":"Deploy"}`, `{"name":"-a"}`, `{"name":"a/b"}`,
		`{"name":"` + strings.Repeat("a", 65) + `"}`, `{"name":"a","kind":"memory"}`, `{"name":"a","scope":"x"}`, `{"name":"a","offset":-1}`}
	for _, args := range bad {
		if rep := c.Call(context.Background(), "s1", ToolReadSkill, json.RawMessage(args)); !rep.IsError {
			t.Fatalf("%q must be refused: %+v", args, rep)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("a refused call reached the core: %v", *seen)
	}
}

func TestReadSkillAndMemoryRelayAPage(t *testing.T) {
	c, seen := configCore(t, 200, `{"kind":"skill","scope":"user","exists":true,"size":5,"hash":"abc","offset":10,"text":"hello"}`)
	rep := c.Call(context.Background(), "s1", ToolReadSkill, json.RawMessage(`{"name":"deploy","scope":"user","offset":10}`))
	if rep.IsError || !strings.Contains(rep.Text, `"text":"hello"`) || !strings.Contains(rep.Text, `"name":"deploy"`) || strings.Contains(rep.Text, `"next"`) {
		t.Fatalf("read_skill = %+v", rep)
	}
	if !strings.Contains((*seen)[0], "/api/sessions/s1/config-files/skill?name=deploy&offset=10&scope=user ") {
		t.Fatalf("requests = %v", *seen)
	}
	rep = c.Call(context.Background(), "s1", ToolReadMemory, nil)
	if rep.IsError || !strings.Contains((*seen)[1], "/api/sessions/s1/config-files/memory?scope=project ") {
		t.Fatalf("read_memory = %+v %v", rep, *seen)
	}
}

func TestReadMemoryCapsThePageAndMovesNext(t *testing.T) {
	text := strings.Repeat("\"line\\\n", 12000) // the escapes make the JSON form larger than the bytes
	body, _ := json.Marshal(map[string]any{"kind": "memory", "scope": "project", "exists": true, "size": len(text) + 100,
		"hash": "h", "offset": 0, "text": text, "nextOffset": len(text)})
	c, _ := configCore(t, 200, string(body))
	rep := c.Call(context.Background(), "s1", ToolReadMemory, nil)
	if rep.IsError || len(rep.Text) > MaxResultBytes {
		t.Fatalf("len %d error %v", len(rep.Text), rep.IsError)
	}
	var out struct {
		Text string `json:"text"`
		Next *int   `json:"next"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(rep.Text, configNotice+"\n")), &out); err != nil {
		t.Fatalf("the result is not whole JSON: %v", err)
	}
	if out.Next == nil || *out.Next != len(out.Text) || len(out.Text) >= len(text) {
		t.Fatalf("next = %v for %d of %d bytes", out.Next, len(out.Text), len(text))
	}
}

func TestConfigReadsRelayTheCoreRefusalCode(t *testing.T) {
	c, _ := configCore(t, 501, `{"code":"unsupported"}`)
	rep := c.Call(context.Background(), "s1", ToolReadMemory, nil)
	if !rep.IsError || !strings.Contains(rep.Text, "unsupported") {
		t.Fatalf("read_memory = %+v", rep)
	}
}
