package claude

import (
	"strings"
	"testing"
)

func TestTitlePrefersAITitle(t *testing.T) {
	data := strings.Join([]string{
		`{"type":"user","message":{"content":"你好，测试 hoplet 导入标题"}}`,
		`{"type":"ai-title","aiTitle":"导入标题验收"}`,
	}, "\n") + "\n"
	got := titleFromData([]byte(data))
	if got != "导入标题验收" {
		t.Fatalf("got %q", got)
	}
}

func TestTitleFromUserPrompt(t *testing.T) {
	line := `{"type":"user","message":{"content":"你好，测试 hoplet 导入标题"}}` + "\n"
	got := titleFromData([]byte(line))
	if got != "你好，测试 hoplet 导入标题" {
		t.Fatalf("got %q", got)
	}
}

func TestTitleSkipsCommands(t *testing.T) {
	data := strings.Join([]string{
		`{"type":"user","message":{"content":"/compact quiet"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"Fix the cursor bug please"}]}}`,
	}, "\n") + "\n"
	got := titleFromData([]byte(data))
	if got != "Fix the cursor bug please" {
		t.Fatalf("got %q", got)
	}
}

func TestTitleEmpty(t *testing.T) {
	if got := titleFromData(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
