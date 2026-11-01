package redact

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// Text's shapes are credentials wherever they stand, with no key to judge a
// value by: a noergler note, a PR title, what the model wrote, and every
// line of code File passes on. No key=value rule runs, because a statement
// about `password := os.Getenv(…)` is the knowledge, not a leak. A match is
// replaced, never dropped: the sentence around it still says what it says.
// Each shape demands what a real value has and prose lacks (a key body after
// a PEM header, a digit in a token), because the model's prose is stored and
// a sentence cut by a false match stays cut.

// pemBlock is a private key with its key material: the header, then a line
// of base64 (a real newline, or "\n" written inside a string literal), up to
// the footer or, cut off, the end. A header alone is a name: a loader's
// prefix check or a sentence about the format keeps it.
var pemBlock = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?:\r?\n|\\n)[A-Za-z0-9+/=]{16,}(?s:.*?)(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`)

// fixedShapes need no second look: a JWT, an AWS access key id, an AWS
// secret under its own name, a jasypt value with a cipher text in it (the
// documentation's "ENC(...)" has none).
var fixedShapes = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*` +
	`|\b(?:AKIA|ASIA)[0-9A-Z]{16}\b` +
	`|(?i:aws_secret_access_key)\s*[=:]\s*\S{20,}` +
	`|\bENC\([A-Za-z0-9+/=_-]{4,}\)`)

// vendorTokens are the prefixed tokens. The rest of a real one is random
// and carries a digit; npm_config_cache_folder and sk-learn-pipeline do not.
var vendorTokens = regexp.MustCompile(`\b(?:gh[pousr]_|github_pat_|glpat-|xox[baprs]-|sk-|npm_)[A-Za-z0-9_-]{16,}`)

// authHeader is an Authorization value. German prose writes "HTTP Basic
// Authentication/Authorization" and "Bearer Token-Authentifizierung".
var authHeader = regexp.MustCompile(`\b((?i:basic)\s+)([A-Za-z0-9+/]{12,}={0,2})|\b((?i:bearer)\s+)([A-Za-z0-9._~+/-]{20,})`)

// Text returns s with every credential shape replaced by Marker; s itself
// when nothing matched.
func Text(s string) string {
	out := pemBlock.ReplaceAllString(s, Marker)
	out = urlCredentials.ReplaceAllString(out, "://"+Marker+"@")
	out = fixedShapes.ReplaceAllString(out, Marker)
	out = vendorTokens.ReplaceAllStringFunc(out, func(m string) string {
		if !strings.ContainsAny(m, "0123456789") {
			return m
		}
		return Marker
	})
	return authHeader.ReplaceAllStringFunc(out, func(m string) string {
		sub := authHeader.FindStringSubmatch(m)
		prefix, token := sub[1], sub[2]
		if prefix == "" {
			prefix, token = sub[3], sub[4]
		}
		if !randomish(token) {
			return m
		}
		return prefix + Marker
	})
}

// randomish reports a token that is no word: a digit or padding in it, or a
// capital inside a run of letters (base64's "dXNlcjpwYXNz"). A word's capital
// stands after a separator, as in "Authentication/Authorization".
func randomish(token string) bool {
	if strings.ContainsAny(token, "0123456789=") {
		return true
	}
	prev := rune(0)
	for _, r := range token {
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			return true
		}
		prev = r
	}
	return false
}

// JSON returns raw with Text applied to every string in it, keys and
// layout untouched; raw itself when nothing changed. The scan is lexical:
// an invalid document comes back as it is, never half rewritten.
func JSON(raw json.RawMessage) json.RawMessage {
	if !json.Valid(raw) {
		return raw
	}
	var out []byte
	last := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		start, end := i, i+1
		for end < len(raw) && raw[end] != '"' {
			if raw[end] == '\\' {
				end++
			}
			end++
		}
		i = end
		var s string
		if json.Unmarshal(raw[start:end+1], &s) != nil {
			continue
		}
		red := Text(s)
		if red == s {
			continue
		}
		enc, ok := encodeString(red)
		if !ok {
			continue
		}
		out = append(out, raw[last:start]...)
		out = append(out, enc...)
		last = end + 1
	}
	if out == nil {
		return raw
	}
	return append(out, raw[last:]...)
}

// encodeString is s as a JSON string literal, "<" and ">" left as they are
// so the marker reads as itself in the prompt and the page.
func encodeString(s string) ([]byte, bool) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(s) != nil {
		return nil, false
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), true
}
