package daemon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// probeTimeout bounds the network check.
const probeTimeout = 5 * time.Second

// probe reaches the configured probe URL the way harnesses would, through
// their proxy. It lives alone in this file so that the lint exclusion for
// requesting a URL from configuration covers nothing else.
func (s *system) probe(ctx context.Context, info proto.EnvInfo) proto.Check {
	via := "directly"
	if info.Proxy != "" {
		via = "through " + info.Proxy + " (" + info.ProxySource + ")"
	}
	tr := &http.Transport{Proxy: http.ProxyURL(s.proxyURL())}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.probeURL, nil)
	if err != nil {
		return proto.Check{Name: "network", Status: proto.CheckFail, Detail: err.Error()}
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return proto.Check{Name: "network", Status: proto.CheckFail, Detail: fmt.Sprintf("cannot reach %s %s: %v", s.probeURL, via, err)}
	}
	resp.Body.Close()
	return proto.Check{Name: "network", Status: proto.CheckOK, Detail: fmt.Sprintf("reached %s %s (HTTP %d)", s.probeURL, via, resp.StatusCode)}
}
