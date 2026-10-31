package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yangflow/hoplet/internal/env"
	"github.com/yangflow/hoplet/proto"
)

// probe reaches the configured probe URL the way harnesses would, through
// their proxy. It lives alone in this file so that the lint exclusion for
// requesting a URL from configuration covers nothing else.
func (s *system) probe(ctx context.Context, snap env.Snapshot, info proto.EnvInfo) proto.Check {
	target, err := url.Parse(s.probeURL)
	if err != nil {
		return proto.Check{Name: "network", Status: proto.CheckFail, Detail: "the probe URL is not a URL"}
	}
	proxy := proxyFor(snap.Vars, target)
	via := "directly"
	if proxy != nil {
		via = "through " + proxy.Redacted() + " (" + info.ProxySource + ")"
	}
	tr := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.probeURL, nil)
	if err != nil {
		return proto.Check{Name: "network", Status: proto.CheckFail, Detail: err.Error()}
	}
	check := proto.Check{Name: "network", Status: proto.CheckOK}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		check.Status = proto.CheckFail
		check.Detail = fmt.Sprintf("cannot reach %s %s: %v", s.probeURL, via, err)
	} else {
		resp.Body.Close()
		check.Detail = fmt.Sprintf("reached %s %s (HTTP %d)", s.probeURL, via, resp.StatusCode)
	}
	if info.Note != "" && proxy == nil {
		check.Detail += "; " + info.Note
		if check.Status == proto.CheckOK {
			check.Status = proto.CheckWarn
		}
	}
	return check
}

// proxyFor picks the proxy harnesses use for target from their environment:
// the variable for the target's scheme (HTTPS_PROXY for https, HTTP_PROXY
// for http), then ALL_PROXY, each in upper then lower case. NO_PROXY is
// honoured with a simple match: "*", the host itself, or a domain suffix
// ("example.com" and ".example.com" both match sub.example.com); ports and
// CIDR ranges are not understood. It returns nil for a direct connection.
func proxyFor(vars []string, target *url.URL) *url.URL {
	get := func(names ...string) string {
		for _, n := range names {
			for _, kv := range vars {
				if v, ok := strings.CutPrefix(kv, n+"="); ok && v != "" {
					return v
				}
			}
		}
		return ""
	}
	var raw string
	switch target.Scheme {
	case "https":
		raw = get("HTTPS_PROXY", "https_proxy")
	case "http":
		raw = get("HTTP_PROXY", "http_proxy")
	}
	if raw == "" {
		raw = get("ALL_PROXY", "all_proxy")
	}
	if raw == "" || bypassed(get("NO_PROXY", "no_proxy"), target.Hostname()) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

func bypassed(noProxy, host string) bool {
	host = strings.ToLower(host)
	for _, e := range strings.Split(noProxy, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "*" {
			return true
		}
		e = strings.TrimPrefix(strings.TrimPrefix(e, "*"), ".")
		if e != "" && (host == e || strings.HasSuffix(host, "."+e)) {
			return true
		}
	}
	return false
}
