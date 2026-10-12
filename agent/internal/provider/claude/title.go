package claude

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const titleMax = 96

// titlePeek is how far a title is looked for. Vendor ai-title lines often land after the first
// user turns (past 64 KiB on busy sessions).
const titlePeek = 512 << 10

// titleFromData is the title of a session's first lines: its latest `ai-title` line, else the earliest
// substantive user prompt (same rules as core summarizeTitle); empty when nothing is usable.
func titleFromData(data []byte) string {
	if t := aiTitleFromJSONL(data); t != "" {
		return t
	}
	return titleFromJSONL(data)
}

func aiTitleFromJSONL(data []byte) string {
	var title string
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var o struct {
			Type    string `json:"type"`
			AITitle string `json:"aiTitle"`
		}
		if json.Unmarshal(raw, &o) != nil || o.Type != "ai-title" {
			continue
		}
		t := strings.Join(strings.Fields(o.AITitle), " ")
		if t == "" {
			continue
		}
		title = clipTitle(t)
	}
	return title
}

func titleFromJSONL(data []byte) string {
	var title, fallback string
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var o struct {
			Type        string `json:"type"`
			IsMeta      bool   `json:"isMeta"`
			IsSidechain bool   `json:"isSidechain"`
			Message     *struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &o) != nil || o.Type != "user" || o.IsMeta || o.IsSidechain || o.Message == nil {
			continue
		}
		candidate := summarizeTitle(contentText(o.Message.Content))
		if candidate == "" {
			continue
		}
		if title == "" {
			if utf8.RuneCountInString(candidate) >= 8 {
				title = candidate
			} else if fallback == "" {
				fallback = candidate
			}
		}
		if title != "" {
			break
		}
	}
	if title != "" {
		return title
	}
	return fallback
}

func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return b.Text
		}
	}
	return ""
}

func summarizeTitle(text string) string {
	one := strings.Join(strings.Fields(text), " ")
	if one == "" {
		return ""
	}
	if strings.HasPrefix(one, "/") || strings.HasPrefix(one, "<") {
		return ""
	}
	return clipTitle(one)
}

func clipTitle(one string) string {
	r := []rune(one)
	if len(r) <= titleMax {
		return one
	}
	window := r[:titleMax]
	cut := -1
	seps := map[rune]bool{'。': true, '.': true, '!': true, '?': true, '！': true, '？': true}
	for i := len(window) - 1; i >= 0; i-- {
		if seps[window[i]] {
			cut = i
			break
		}
	}
	if cut >= titleMax/3 {
		return string(window[:cut+1])
	}
	return strings.TrimRight(string(window), " \t") + "…"
}
