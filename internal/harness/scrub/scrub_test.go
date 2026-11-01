package scrub

import "testing"

func TestReplacePath(t *testing.T) {
	for _, tt := range []struct{ s, from, to, want string }{
		{"/Users/ann/x and /Users/anna/x", "/Users/ann", "/Users/me", "/Users/me/x and /Users/anna/x"},
		{`"/Users/ann"`, "/Users/ann", "/Users/me", `"/Users/me"`},
		{"/Users/ann", "/Users/ann", "/Users/me", "/Users/me"},
		{"/Users/ann-old", "/Users/ann", "/Users/me", "/Users/me-old"},
		{"/Users/ann_x. /Users/ann.", "/Users/ann", "/Users/me", "/Users/me_x. /Users/me."},
		{"/w1 /w11", "/w1", "/work", "/work /w11"},
		{"-Users-ann-proj", "-Users-ann", "-Users-me", "-Users-me-proj"},
		{"-Users-anna", "-Users-ann", "-Users-me", "-Users-anna"},
	} {
		if got := ReplacePath(tt.s, tt.from, tt.to); got != tt.want {
			t.Errorf("ReplacePath(%q, %q) = %q, want %q", tt.s, tt.from, got, tt.want)
		}
	}
}

func TestWord(t *testing.T) {
	for _, tt := range []struct{ s, word, want string }{
		{"ann wrote ann_x and anna and /Users/ann/x", "ann", "me wrote ann_x and anna and /Users/me/x"},
		{"an and a", "an", "an and a"},
		{`{"d":"line\nannie\tannie\"annie\\annie annie"}`, "annie", `{"d":"line\nme\tme\"me\\me me"}`},
		{`{"d":"annie annie"}`, "annie", `{"d":"me me"}`},
		{`{"d":"\u201cannie\u201d and \u00e9annie"}`, "annie", `{"d":"\u201cme\u201d and \u00e9me"}`},
		{`{"d":"\nannies and nannie"}`, "annie", `{"d":"\nannies and nannie"}`},
	} {
		if got := Word(tt.word, "me")(tt.s); got != tt.want {
			t.Errorf("Word(%q)(%q) = %q, want %q", tt.word, tt.s, got, tt.want)
		}
	}
}
