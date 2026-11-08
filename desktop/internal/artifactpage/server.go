// Package artifactpage shows HTML artifacts as untrusted pages: a custom
// scheme serves each page the files of its own artifact version and
// nothing else.
package artifactpage

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/yangflow/hoplet/proto"
)

// Scheme is the URL scheme artifact pages load from.
const Scheme = "hoplet-artifact"

// URL is the address of a version's entry file.
func URL(id proto.ArtifactID, version int) string {
	return fmt.Sprintf("%s://%s/v%d/", Scheme, id, version)
}

// Reader reads a whole file of an artifact version and says its content
// type.
type Reader func(ctx context.Context, p proto.ArtifactsReadParams) (data []byte, contentType string, err error)

// owner is what a page may read.
type owner struct {
	artifact proto.ArtifactID
	version  int
	failed   func(error)
}

// relative returns the file a request asks of the owner's version, "" for
// the entry. It reports false for anything outside that version.
func (o owner) relative(r *http.Request) (string, bool) {
	if r.URL.Host != string(o.artifact) {
		return "", false
	}
	root := "/v" + strconv.Itoa(o.version)
	if r.URL.Path == root {
		return "", true
	}
	rel, ok := strings.CutPrefix(r.URL.Path, root+"/")
	if !ok {
		return "", false
	}
	if rel != "" && (strings.Contains(rel, `\`) || !filepath.IsLocal(filepath.FromSlash(rel))) {
		return "", false
	}
	return rel, true
}

// Server answers a scheme's requests, checking each against the page that
// made it. A request it refuses never reaches the Reader.
type Server struct {
	read   Reader
	caller func(*http.Request) any // the page behind a request

	mu     sync.Mutex
	owners map[any]owner
}

// NewServer makes a Server identifying requests' pages with caller.
func NewServer(read Reader, caller func(*http.Request) any) *Server {
	return &Server{read: read, caller: caller, owners: map[any]owner{}}
}

func (s *Server) bind(page any, o owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owners[page] = o
}

func (s *Server) unbind(page any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.owners, page)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	o, ok := s.owners[s.caller(r)]
	s.mu.Unlock()
	rel := ""
	if ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		rel, ok = o.relative(r)
	} else {
		ok = false
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, contentType, err := s.read(r.Context(), proto.ArtifactsReadParams{Artifact: o.artifact, Version: o.version, Path: rel})
	if err != nil {
		if rel == "" && o.failed != nil {
			o.failed(err)
		}
		http.NotFound(w, r)
		return
	}
	if t := mime.TypeByExtension(path.Ext(rel)); t != "" {
		contentType = t
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(data) // the page went away
	}
}
