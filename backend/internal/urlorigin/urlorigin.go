// Package urlorigin is a URL's origin, the one normaliser for every place
// that compares two: the browser check of the auth gate and the host a
// source's token is bound to.
package urlorigin

import (
	"net/url"
	"sort"
	"strings"
)

// Of is scheme://host of s as a browser writes it in Origin: lower case,
// the scheme's default port dropped (:443 for https, :80 for http), an IPv6
// host in brackets, userinfo and path gone; "" when s has no scheme or host.
func Of(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if p := u.Port(); (scheme == "https" && p == "443") || (scheme == "http" && p == "80") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return scheme + "://" + host
}

// Same reports whether a and b are on one origin; "" matches nothing.
func Same(a, b string) bool {
	oa := Of(a)
	return oa != "" && oa == Of(b)
}

// Distinct is the origins of urls, each once, sorted; an unparsable URL
// counts as "", so it never passes for one of the others.
func Distinct(urls []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, u := range urls {
		o := Of(u)
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Strings(out)
	return out
}
