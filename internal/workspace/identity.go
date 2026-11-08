package workspace

import (
	"context"
	"net/url"
	"strings"
)

// RepoIdentity is a stable name for the git repository holding dir: its
// origin as host/path, or host:root when it has no such origin. ok is false
// when dir is not in a repository or git cannot say.
func (g Git) RepoIdentity(ctx context.Context, dir, host string) (identity string, ok bool) {
	top, err := g.output(ctx, runOpts{Dir: dir}, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(top))
	if root == "" {
		return "", false
	}
	if out, err := g.output(ctx, runOpts{Dir: dir}, "config", "--get", "remote.origin.url"); err == nil {
		if id, ok := normalizeOrigin(strings.TrimSpace(string(out))); ok {
			return id, true
		}
	}
	return host + ":" + root, true
}

// normalizeOrigin reduces a remote URL to host/path, dropping scheme, user,
// credentials, port and a trailing .git, so every spelling of one remote
// has one name. A local path has no host and is not normalized.
func normalizeOrigin(origin string) (string, bool) {
	var host, path string
	if u, err := url.Parse(origin); err == nil && u.Scheme != "" && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if before, after, found := strings.Cut(origin, ":"); found && !strings.HasPrefix(after, "//") && !strings.ContainsAny(before, "/\\") && len(before) > 1 {
		_, host, _ = cutLast(before, "@")
		path = after
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return "", false
	}
	return strings.ToLower(host) + "/" + path, true
}

// cutLast splits s around its last sep; without one, s is the after part.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return "", s, false
}
