package manifest

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// The TOML a Gradle version catalog is written in, read by hand: tables,
// bare, quoted and dotted keys, the four string kinds, arrays and inline
// tables, each across lines. Any other value (number, bool, date) is kept
// as its text; a catalog names none that counts. Arrays of tables are
// read too, so a document using them still parses.

var errTOML = errors.New("toml: malformed")

type tomlParser struct {
	s string
	i int
}

// parseTOML is a document as nested maps: a table is map[string]any, an
// array []any, a string a string.
func parseTOML(body []byte) (map[string]any, error) {
	p := &tomlParser{s: strings.ReplaceAll(string(body), "\r\n", "\n")}
	root := map[string]any{}
	cur := root
	for {
		p.skipBlank(true)
		if p.i >= len(p.s) {
			return root, nil
		}
		if p.s[p.i] == '[' {
			array := strings.HasPrefix(p.s[p.i:], "[[")
			if array {
				p.i += 2
			} else {
				p.i++
			}
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipBlank(false)
			closing := "]"
			if array {
				closing = "]]"
			}
			if !strings.HasPrefix(p.s[p.i:], closing) {
				return nil, errTOML
			}
			p.i += len(closing)
			if cur, err = tableAt(root, keys, array); err != nil {
				return nil, err
			}
		} else {
			if err := p.keyValue(cur); err != nil {
				return nil, err
			}
		}
		p.skipBlank(false)
		if p.i < len(p.s) && p.s[p.i] != '\n' {
			return nil, errTOML
		}
	}
}

// tableAt is the table a header names, made on the way; for an array of
// tables a new one appended.
func tableAt(root map[string]any, keys []string, array bool) (map[string]any, error) {
	t := root
	for n, k := range keys {
		last := n == len(keys)-1
		switch v := t[k].(type) {
		case nil:
			if last && array {
				m := map[string]any{}
				t[k] = []any{m}
				return m, nil
			}
			m := map[string]any{}
			t[k] = m
			t = m
		case map[string]any:
			if last && array {
				return nil, errTOML
			}
			t = v
		case []any:
			if len(v) == 0 {
				return nil, errTOML
			}
			if last && array {
				m := map[string]any{}
				t[k] = append(v, m)
				return m, nil
			}
			m, ok := v[len(v)-1].(map[string]any)
			if !ok {
				return nil, errTOML
			}
			t = m
		default:
			return nil, errTOML
		}
	}
	return t, nil
}

// keyValue reads `key = value` into t, a dotted key making its tables.
func (p *tomlParser) keyValue(t map[string]any) error {
	keys, err := p.key()
	if err != nil {
		return err
	}
	p.skipBlank(false)
	if p.i >= len(p.s) || p.s[p.i] != '=' {
		return errTOML
	}
	p.i++
	p.skipBlank(false)
	v, err := p.value()
	if err != nil {
		return err
	}
	for _, k := range keys[:len(keys)-1] {
		switch sub := t[k].(type) {
		case nil:
			m := map[string]any{}
			t[k] = m
			t = m
		case map[string]any:
			t = sub
		default:
			return errTOML
		}
	}
	last := keys[len(keys)-1]
	if _, dup := t[last]; dup {
		return errTOML
	}
	t[last] = v
	return nil
}

// key is a bare, quoted or dotted key, its parts in order.
func (p *tomlParser) key() ([]string, error) {
	var keys []string
	for {
		p.skipBlank(false)
		if p.i >= len(p.s) {
			return nil, errTOML
		}
		switch p.s[p.i] {
		case '"', '\'':
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			keys = append(keys, k)
		default:
			start := p.i
			for p.i < len(p.s) && bareKeyChar(p.s[p.i]) {
				p.i++
			}
			if p.i == start {
				return nil, errTOML
			}
			keys = append(keys, p.s[start:p.i])
		}
		p.skipBlank(false)
		if p.i < len(p.s) && p.s[p.i] == '.' {
			p.i++
			continue
		}
		return keys, nil
	}
}

func bareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func (p *tomlParser) value() (any, error) {
	if p.i >= len(p.s) {
		return nil, errTOML
	}
	switch p.s[p.i] {
	case '"', '\'':
		return p.str()
	case '[':
		p.i++
		arr := []any{}
		for {
			p.skipBlank(true)
			if p.i >= len(p.s) {
				return nil, errTOML
			}
			if p.s[p.i] == ']' {
				p.i++
				return arr, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.skipBlank(true)
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
			} else if p.i >= len(p.s) || p.s[p.i] != ']' {
				return nil, errTOML
			}
		}
	case '{':
		p.i++
		t := map[string]any{}
		for {
			p.skipBlank(true)
			if p.i >= len(p.s) {
				return nil, errTOML
			}
			if p.s[p.i] == '}' {
				p.i++
				return t, nil
			}
			if err := p.keyValue(t); err != nil {
				return nil, err
			}
			p.skipBlank(true)
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
			} else if p.i >= len(p.s) || p.s[p.i] != '}' {
				return nil, errTOML
			}
		}
	default:
		start := p.i
		for p.i < len(p.s) && !strings.ContainsRune(",]}\n# \t", rune(p.s[p.i])) {
			p.i++
		}
		v := p.s[start:p.i]
		if v == "" {
			return nil, errTOML
		}
		return v, nil
	}
}

// str is any of the four strings, p.i on its opening quote.
func (p *tomlParser) str() (string, error) {
	q := p.s[p.i]
	multi := strings.HasPrefix(p.s[p.i:], strings.Repeat(string(q), 3))
	if multi {
		p.i += 3
		// A newline right after the opening delimiter is trimmed.
		if p.i < len(p.s) && p.s[p.i] == '\n' {
			p.i++
		}
	} else {
		p.i++
	}
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case multi && strings.HasPrefix(p.s[p.i:], strings.Repeat(string(q), 3)):
			p.i += 3
			// Up to two more quotes just before the closing ones are content.
			for n := 0; n < 2 && p.i < len(p.s) && p.s[p.i] == q; n++ {
				b.WriteByte(q)
				p.i++
			}
			return b.String(), nil
		case !multi && c == q:
			p.i++
			return b.String(), nil
		case !multi && c == '\n':
			return "", errTOML
		case q == '"' && c == '\\':
			if err := p.escape(&b, multi); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return "", errTOML
}

// escape reads one escape of a basic string, p.i on its backslash. In a
// multi-line one a backslash ending a line drops the whitespace after it.
func (p *tomlParser) escape(b *strings.Builder, multi bool) error {
	p.i++
	if p.i >= len(p.s) {
		return errTOML
	}
	c := p.s[p.i]
	if multi && (c == ' ' || c == '\t' || c == '\n') {
		j := p.i
		for j < len(p.s) && (p.s[j] == ' ' || p.s[j] == '\t') {
			j++
		}
		if j < len(p.s) && p.s[j] == '\n' {
			for j < len(p.s) && (p.s[j] == ' ' || p.s[j] == '\t' || p.s[j] == '\n') {
				j++
			}
			p.i = j
			return nil
		}
		return errTOML
	}
	p.i++
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case '"', '\\':
		b.WriteByte(c)
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.i+n > len(p.s) {
			return errTOML
		}
		r, ok := hexRune(p.s[p.i : p.i+n])
		if !ok {
			return errTOML
		}
		b.WriteRune(r)
		p.i += n
	default:
		return errTOML
	}
	return nil
}

// hexRune is the code point hex digits name (a `\u` or `\U` escape of TOML
// or a properties file); false for a non-digit or no valid rune.
func hexRune(s string) (rune, bool) {
	if s == "" || len(s) > 8 {
		return 0, false
	}
	var r rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d rune
		switch {
		case c >= '0' && c <= '9':
			d = rune(c - '0')
		case c >= 'a' && c <= 'f':
			d = rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = rune(c-'A') + 10
		default:
			return 0, false
		}
		if r > utf8.MaxRune>>4 {
			return 0, false
		}
		r = r<<4 | d
	}
	return r, utf8.ValidRune(r)
}

// skipBlank skips spaces, tabs and a comment; newlines too when nl.
func (p *tomlParser) skipBlank(nl bool) {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t':
			p.i++
		case '\n':
			if !nl {
				return
			}
			p.i++
		case '#':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}
