package rules

import "testing"

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://Example.COM/x":      "example.com",
		"https://example.com.:8080/": "example.com",
		"https://u:p@a.com/":         "a.com",
		"http://2130706433/":         "127.0.0.1",
		"http://0x7f.1/":             "127.0.0.1",
		"http://[::1]:80/":           "::1",
		"https://a.com\\@b.com/":     "a.com",
	}
	for in, want := range cases {
		if got, ok := hostOf(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "a.com", "https://", "https://a b.com", "https://a.com:x/", "http://1.2.3.4.5/", "http://[::1/"} {
		if h, ok := hostOf(in); ok {
			t.Errorf("%q: parsed as %q", in, h)
		}
	}
}

func TestDomainMatches(t *testing.T) {
	cases := []struct {
		pat, host string
		ok        bool
	}{
		{"a.com", "a.com", true},
		{"a.com", "b.a.com", false},
		{"*.a.com", "b.a.com", true},
		{"*.a.com", "c.b.a.com", true},
		{"*.a.com", "a.com", false},
		{"*.a.com", "xa.com", false},
		{"127.0.0.1", "127.0.0.1", true},
		{"*.0.0.1", "127.0.0.1", false},
	}
	for _, tc := range cases {
		if got := domainMatches(tc.pat, tc.host); got != tc.ok {
			t.Errorf("%q vs %q: %v", tc.pat, tc.host, got)
		}
	}
}
