package artifactpage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yangflow/hoplet/proto"
)

type fakeReads struct {
	asked []proto.ArtifactsReadParams
	err   error
}

func (f *fakeReads) read(_ context.Context, p proto.ArtifactsReadParams) ([]byte, string, error) {
	f.asked = append(f.asked, p)
	if f.err != nil {
		return nil, "", f.err
	}
	return []byte("data"), "text/html; charset=utf-8", nil
}

func TestServerKeepsPagesToTheirVersion(t *testing.T) {
	page := "page"
	tests := []struct {
		name, url string
		caller    any
		method    string
		status    int
		path      string // what reaches the daemon, when status is 200
		ctype     string
	}{
		{name: "entry", url: "hoplet-artifact://a1/v2/", caller: page, status: 200, ctype: "text/html; charset=utf-8"},
		{name: "script", url: "hoplet-artifact://a1/v2/app.js", caller: page, status: 200, path: "app.js", ctype: "text/javascript; charset=utf-8"},
		{name: "nested image", url: "hoplet-artifact://a1/v2/img/a.png", caller: page, status: 200, path: "img/a.png", ctype: "image/png"},
		{name: "other artifact", url: "hoplet-artifact://b1/v2/app.js", caller: page, status: 404},
		{name: "other version", url: "hoplet-artifact://a1/v1/app.js", caller: page, status: 404},
		{name: "version prefix", url: "hoplet-artifact://a1/v22/app.js", caller: page, status: 404},
		{name: "out of the version", url: "hoplet-artifact://a1/v2/../v1/app.js", caller: page, status: 404},
		{name: "encoded way out", url: "hoplet-artifact://a1/v2/%2e%2e/v1/app.js", caller: page, status: 404},
		{name: "unknown page", url: "hoplet-artifact://a1/v2/app.js", caller: "other", status: 404},
		{name: "no page", url: "hoplet-artifact://a1/v2/app.js", status: 404},
		{name: "post", url: "hoplet-artifact://a1/v2/app.js", caller: page, method: http.MethodPost, status: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeReads{}
			s := NewServer(f.read, func(*http.Request) any { return tt.caller })
			s.bind(page, owner{artifact: "a1", version: 2})
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(method, tt.url, nil))
			if w.Code != tt.status {
				t.Fatalf("status %d, want %d", w.Code, tt.status)
			}
			if tt.status != 200 {
				if len(f.asked) != 0 {
					t.Fatalf("reached the daemon: %v", f.asked)
				}
				return
			}
			want := proto.ArtifactsReadParams{Artifact: "a1", Version: 2, Path: tt.path}
			if len(f.asked) != 1 || f.asked[0] != want {
				t.Errorf("asked %v, want %v", f.asked, want)
			}
			if got := w.Header().Get("Content-Type"); got != tt.ctype {
				t.Errorf("content type %q, want %q", got, tt.ctype)
			}
		})
	}
}

func TestServerReportsAFailedEntry(t *testing.T) {
	boom := errors.New("gone")
	var got []error
	f := &fakeReads{err: boom}
	s := NewServer(f.read, func(*http.Request) any { return "p" })
	s.bind("p", owner{artifact: "a1", version: 1, failed: func(err error) { got = append(got, err) }})
	for _, u := range []string{"hoplet-artifact://a1/v1/x.png", "hoplet-artifact://a1/v1/"} {
		s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, u, nil))
	}
	if len(got) != 1 || !errors.Is(got[0], boom) {
		t.Errorf("failed with %v, want one %v for the entry only", got, boom)
	}
}
