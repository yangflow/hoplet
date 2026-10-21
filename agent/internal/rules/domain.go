package rules

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// hostOf returns the lowercase ASCII host of a URL the way a browser-style fetcher reads it:
// the authority ends at the first / ? # or backslash, anything up to the last @ is user info,
// and a trailing dot is dropped. A URL it cannot read yields false, and then no domain rule
// matches.
func hostOf(raw string) (string, bool) {
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f {
			return "", false
		}
	}
	i := strings.Index(raw, "://")
	if i <= 0 {
		return "", false
	}
	rest := raw[i+3:]
	if end := strings.IndexAny(rest, "/?#\\"); end >= 0 {
		rest = rest[:end]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	host := rest
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end < 0 {
			return "", false
		}
		host = rest[1:end]
		if tail := rest[end+1:]; tail != "" && !validPort(tail) {
			return "", false
		}
		if net.ParseIP(host) == nil {
			return "", false
		}
		return strings.ToLower(host), true
	}
	if c := strings.LastIndex(rest, ":"); c >= 0 {
		if !validPort(rest[c:]) {
			return "", false
		}
		host = rest[:c]
	}
	if strings.Contains(host, "%") {
		d, err := url.PathUnescape(host)
		if err != nil {
			return "", false
		}
		host = d
	}
	host = toASCII(strings.TrimSuffix(host, "."))
	if host == "" {
		return "", false
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return "", false
		}
	}
	if ip, numeric, ok := canonIPv4(host); numeric {
		if !ok {
			return "", false
		}
		host = ip
	}
	return host, true
}

// canonIPv4 reads a host whose last label is a number the way a URL parser does (decimal, octal
// and hex parts, fewer than four parts) and returns the dotted form. numeric tells whether the
// host is such an address at all; ok whether it is a valid one.
func canonIPv4(host string) (ip string, numeric, ok bool) {
	parts := strings.Split(host, ".")
	last := parts[len(parts)-1]
	if last == "" {
		return "", false, false
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(last), "0x"), 16, 64); err != nil ||
		(!strings.HasPrefix(strings.ToLower(last), "0x") && strings.Trim(last, "0123456789") != "") {
		return "", false, false
	}
	if len(parts) > 4 {
		return "", true, false
	}
	var nums []uint64
	for _, p := range parts {
		v, good := parseIPv4Part(p)
		if !good {
			return "", true, false
		}
		nums = append(nums, v)
	}
	for _, v := range nums[:len(nums)-1] {
		if v > 255 {
			return "", true, false
		}
	}
	lastV := nums[len(nums)-1]
	if lastV >= 1<<(8*uint(5-len(nums))) {
		return "", true, false
	}
	n := lastV
	for i, v := range nums[:len(nums)-1] {
		n += v << (8 * uint(3-i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", n>>24&255, n>>16&255, n>>8&255, n&255), true, true
}

func parseIPv4Part(p string) (uint64, bool) {
	if p == "" {
		return 0, false
	}
	base := 10
	lp := strings.ToLower(p)
	switch {
	case strings.HasPrefix(lp, "0x"):
		base, p = 16, p[2:]
		if p == "" {
			return 0, true
		}
	case len(p) > 1 && p[0] == '0':
		base, p = 8, p[1:]
	}
	v, err := strconv.ParseUint(p, base, 64)
	return v, err == nil
}

func validPort(s string) bool {
	if !strings.HasPrefix(s, ":") {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validDomain(d string) bool {
	if strings.Count(d, "*") > 1 || (strings.Contains(d, "*") && !strings.HasPrefix(d, "*.")) {
		return false
	}
	h := strings.TrimPrefix(d, "*.")
	if h == "" || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_' || r == ':') {
			return false
		}
	}
	return true
}

func domainMatches(pattern, host string) bool {
	if sub, ok := strings.CutPrefix(pattern, "*."); ok {
		if net.ParseIP(host) != nil {
			return false
		}
		return strings.HasSuffix(host, "."+sub) && len(host) > len(sub)+1
	}
	return host == pattern
}

// toASCII lowercases a host and converts each non-ASCII label to punycode (no other
// normalisation).
func toASCII(host string) string {
	labels := strings.Split(strings.ToLower(host), ".")
	for i, l := range labels {
		for _, r := range l {
			if r > 0x7f {
				labels[i] = "xn--" + punyEncode(l)
				break
			}
		}
	}
	return strings.Join(labels, ".")
}

// punyEncode is RFC 3492 encoding.
func punyEncode(s string) string {
	const base, tmin, tmax, skew, damp = 36, 1, 26, 38, 700
	in := []rune(s)
	var out []byte
	for _, r := range in {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	adapt := func(delta, n int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / n
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	n, delta, bias := 128, 0, 72
	for h < len(in) {
		m := int(^uint32(0) >> 1)
		for _, r := range in {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range in {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tmin {
						t = tmin
					} else if t > tmax {
						t = tmax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out)
}
