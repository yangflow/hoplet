package env

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

// ProxySource says where the effective proxy came from.
type ProxySource string

// Proxy sources.
const (
	ProxyFromConfig ProxySource = "config"
	ProxyFromShell  ProxySource = "shell"
	ProxyFromSystem ProxySource = "system"
	ProxyNone       ProxySource = "none"
)

// SystemProxy is the operating system's proxy setting.
type SystemProxy struct {
	URL     string   // "" when none is enabled
	NoProxy []string // hosts the system reaches directly
}

// proxyVars are the variables that carry a proxy, upper and lower case.
var proxyVars = []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"}

var noProxyVars = []string{"NO_PROXY", "no_proxy"}

// defaultNoProxy are always reached without a proxy.
var defaultNoProxy = []string{"localhost", "127.0.0.1", "::1"}

// shellProxy returns the proxy the login shell exports, if any.
func shellProxy(vars map[string]string) string {
	for _, k := range proxyVars {
		if v := vars[k]; v != "" {
			return v
		}
	}
	return ""
}

// setProxy replaces the proxy variables in vars with u (none when u is
// empty) and the no-proxy list.
func setProxy(vars map[string]string, u string, noProxy []string) {
	for _, k := range append(slices.Clone(proxyVars), noProxyVars...) {
		delete(vars, k)
	}
	if u == "" {
		return
	}
	for _, k := range proxyVars {
		vars[k] = u
	}
	list := strings.Join(noProxy, ",")
	for _, k := range noProxyVars {
		vars[k] = list
	}
}

// ReadSystemProxy reads the macOS system proxy with scutil; elsewhere there
// is none.
func ReadSystemProxy(ctx context.Context) (SystemProxy, error) {
	if runtime.GOOS != "darwin" {
		return SystemProxy{}, nil
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return SystemProxy{}, err
	}
	return parseScutil(out), nil
}

// parseScutil reads `scutil --proxy` output: HTTPS, then HTTP, then SOCKS
// is used, whichever is enabled first.
func parseScutil(out []byte) SystemProxy {
	kv := map[string]string{}
	var exceptions []string
	inList := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "ExceptionsList"):
			inList = true
		case inList && line == "}":
			inList = false
		case inList:
			if _, v, ok := strings.Cut(line, " : "); ok {
				exceptions = append(exceptions, strings.TrimSpace(v))
			}
		default:
			if k, v, ok := strings.Cut(line, " : "); ok {
				kv[k] = strings.TrimSpace(v)
			}
		}
	}
	var p SystemProxy
	for _, c := range []struct{ prefix, scheme string }{{"HTTPS", "http"}, {"HTTP", "http"}, {"SOCKS", "socks5h"}} {
		host, port := kv[c.prefix+"Proxy"], kv[c.prefix+"Port"]
		if kv[c.prefix+"Enable"] == "1" && host != "" && port != "" {
			p.URL = (&url.URL{Scheme: c.scheme, Host: net.JoinHostPort(host, port)}).String()
			break
		}
	}
	for _, e := range exceptions {
		if h, ok := noProxyEntry(e); ok {
			p.NoProxy = append(p.NoProxy, h)
		}
	}
	return p
}

// noProxyEntry converts a system exception to a NO_PROXY entry; entries
// other tools would misread (a shortened CIDR such as 169.254/16) are
// skipped.
func noProxyEntry(e string) (string, bool) {
	if strings.HasPrefix(e, "*.") {
		e = e[1:]
	}
	if strings.Contains(e, "/") {
		_, _, err := net.ParseCIDR(e)
		return e, err == nil
	}
	if e == "" || strings.ContainsAny(e, " *") {
		return "", false
	}
	return e, true
}

// redact hides a proxy URL's password.
func redact(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.User == nil {
		return u
	}
	return p.Redacted()
}
