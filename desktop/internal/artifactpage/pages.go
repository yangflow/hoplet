package artifactpage

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/yangflow/hoplet/proto"
)

// blockRules block every load but the artifact scheme's.
const blockRules = `[{"trigger":{"url-filter":".*"},"action":{"type":"block"}},` +
	`{"trigger":{"url-filter":"^` + Scheme + `://"},"action":{"type":"ignore-previous-rules"}}]`

// key names a version's page.
type key struct {
	artifact proto.ArtifactID
	version  int
}

// Pages makes and keeps the web views of artifact versions. Its methods
// other than Register and Prepare run on the UI thread.
type Pages struct {
	server *Server

	mu    sync.Mutex
	rules *mygo.ContentRules
	views map[key]*mygo.WebView
}

// New makes the page set, reading files with read.
func New(read Reader) *Pages {
	caller := func(r *http.Request) any { return mygo.CallerPage(r.Context()) }
	return &Pages{server: NewServer(read, caller), views: map[key]*mygo.WebView{}}
}

// Register installs the scheme's handler; it precedes any window.
func (p *Pages) Register() error {
	if err := mygo.Protocol.HandleFunc(Scheme, p.server.ServeHTTP); err != nil {
		return fmt.Errorf("artifactpage: register scheme: %w", err)
	}
	return nil
}

// Prepare compiles the content rules. It blocks, and no page is made
// before it has succeeded.
func (p *Pages) Prepare() error {
	rules, err := mygo.CompileContentRules("hoplet-artifact-pages", blockRules)
	if err != nil {
		return fmt.Errorf("artifactpage: compile rules: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = rules
	return nil
}

// Page returns a version's web view in win, made and loaded on first use.
func (p *Pages) Page(win *mygo.Window, id proto.ArtifactID, version int, failed func(error)) (ui.NativeWebView, error) {
	p.mu.Lock()
	rules := p.rules
	p.mu.Unlock()
	if rules == nil {
		return nil, errors.New("页面的隔离规则还没准备好")
	}
	k := key{id, version}
	if v, ok := p.views[k]; ok {
		return v, nil
	}
	v, err := win.NewWebView(mygo.WebViewOptions{Page: mygo.PageOptions{Untrusted: true, ContentRules: []*mygo.ContentRules{rules}}})
	if err != nil {
		return nil, fmt.Errorf("artifactpage: make web view: %w", err)
	}
	page, home := v.Page(), URL(id, version)
	p.server.bind(page, owner{artifact: id, version: version, failed: failed})
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if strings.HasPrefix(e.URL, home) {
			return
		}
		e.PreventDefault()
		openExternal(e.URL, e.UserInitiated)
	})
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		openExternal(req.URL, req.UserInitiated)
		return nil
	})
	page.OnDidFailLoad(func(e *mygo.LoadError) {
		if strings.HasPrefix(e.URL, home) {
			failed(errors.New(e.Description))
		}
	})
	if err := page.LoadURL(home); err != nil {
		p.server.unbind(page)
		v.Destroy()
		return nil, fmt.Errorf("artifactpage: load: %w", err)
	}
	p.views[k] = v
	return v, nil
}

// openExternal gives an http(s) link the user followed to the system browser;
// anything else is dropped.
func openExternal(raw string, byUser bool) {
	u, err := url.Parse(raw)
	if err != nil || !byUser || u.Scheme != "http" && u.Scheme != "https" {
		return
	}
	_ = mygo.Shell.OpenExternal(raw) // nothing to tell the untrusted page
}

// Reload loads a version's page again.
func (p *Pages) Reload(id proto.ArtifactID, version int) {
	if v, ok := p.views[key{id, version}]; ok {
		v.Page().Reload()
	}
}

// Close drops an artifact's pages.
func (p *Pages) Close(id proto.ArtifactID) {
	for k, v := range p.views {
		if k.artifact == id {
			p.server.unbind(v.Page())
			v.Destroy()
			delete(p.views, k)
		}
	}
}
