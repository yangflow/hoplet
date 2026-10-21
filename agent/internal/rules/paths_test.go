package rules

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pat, path string
		fold, ok  bool
	}{
		{"/a/**", "/a", false, true},
		{"/a/**", "/a/b/c", false, true},
		{"/a/**", "/ab", false, false},
		{"/a/**", "/b/a", false, false},
		{"**/.env", "/x/y/.env", false, true},
		{"**/.env", "/.env", false, true},
		{"**/.env.*", "/x/.env.local", false, true},
		{"**/.env.*", "/x/.env", false, false},
		{"**/*.example", "/x/a.env.example", false, true},
		{"/a/*/c", "/a/b/c", false, true},
		{"/a/*/c", "/a/b/d/c", false, false},
		{"/a/B", "/a/b", true, true},
		{"/a/B", "/a/b", false, false},
		{"/a/**/b/**/c", "/a/x/b/y/z/c", false, true},
		{"/a/./b", "/a/b", false, true},
	}
	for _, tc := range cases {
		if got := globMatch(splitSegs(tc.pat), splitSegs(tc.path), tc.fold); got != tc.ok {
			t.Errorf("%q vs %q fold=%v: %v", tc.pat, tc.path, tc.fold, got)
		}
	}
}

func TestResolvePath(t *testing.T) {
	cases := []struct{ p, cwd, home, written string }{
		{"/a/b/../c", "/w", "/h", "/a/c"},
		{"x", "/w", "/h", "/w/x"},
		{"../x", "/w/y", "/h", "/w/x"},
		{"~/x", "/w", "/h", "/h/x"},
		{"~", "/w", "/h", "/h"},
		{"./a//b/", "/w", "/h", "/w/a/b"},
		{"/../..", "/w", "/h", "/"},
	}
	for _, tc := range cases {
		w, r := ResolvePath(tc.p, tc.cwd, tc.home)
		if w != tc.written || r == "" {
			t.Errorf("%q: %q %q, want %q", tc.p, w, r, tc.written)
		}
	}
}
