package auth

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NextCookie carries the page a sign-in started from across the provider's
// round trip, so a deep link survives it.
const NextCookie = "almanach_next"

// nextTTL matches kit's transient login cookies: long enough for a
// password prompt and a second factor.
const nextTTL = 10 * time.Minute

// SafeNext is p when it is a path on this site, "/" otherwise: it starts
// with one slash, carries no scheme, host, backslash or control character
// (a browser drops a tab or newline and "/\t/x" becomes "//x"). Anything
// else could send the reader to another site after he signed in.
func SafeNext(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsRune(p, '\\') {
		return "/"
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return p
}

// SetNext remembers where the sign-in started; a path that is not this
// site's is remembered as "/".
func SetNext(w http.ResponseWriter, p string) {
	http.SetCookie(w, nextCookie(url.QueryEscape(SafeNext(p)), time.Now().Add(nextTTL), int(nextTTL.Seconds())))
}

// TakeNext is where the sign-in started, "/" without a cookie, and clears
// the cookie: it is single-use.
func TakeNext(w http.ResponseWriter, r *http.Request) string {
	http.SetCookie(w, nextCookie("", time.Unix(0, 0), -1))
	c, err := r.Cookie(NextCookie)
	if err != nil {
		return "/"
	}
	p, err := url.QueryUnescape(c.Value)
	if err != nil {
		return "/"
	}
	return SafeNext(p)
}

func nextCookie(value string, exp time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: NextCookie, Value: value, Path: "/", Expires: exp, MaxAge: maxAge,
		// Lax: the callback is a top-level navigation from the provider.
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}
}
