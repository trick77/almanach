package manifest

import "strings"

// properties reads a Java properties file (gradle.properties): `key=value`,
// `key: value` or `key value`, `#` and `!` comments, a trailing backslash
// joining the next line, `\uXXXX` and the usual escapes. A key twice: the
// last one counts.
func properties(body []byte) map[string]string {
	text := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\r", "\n")
	out := map[string]string{}
	var logical strings.Builder
	cont := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeft(line, " \t\f")
		if !cont && (line == "" || line[0] == '#' || line[0] == '!') {
			continue
		}
		// An odd number of trailing backslashes continues the line.
		n := len(line) - len(strings.TrimRight(line, `\`))
		cont = n%2 == 1
		if cont {
			line = line[:len(line)-1]
		}
		logical.WriteString(line)
		if cont {
			continue
		}
		k, v := propertyLine(logical.String())
		logical.Reset()
		if k != "" {
			out[k] = v
		}
	}
	if logical.Len() > 0 {
		if k, v := propertyLine(logical.String()); k != "" {
			out[k] = v
		}
	}
	return out
}

// propertyLine splits one logical line at the first unescaped `=`, `:` or
// whitespace, both sides unescaped.
func propertyLine(s string) (string, string) {
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '=' || s[i] == ':' || s[i] == ' ' || s[i] == '\t' || s[i] == '\f' {
			end = i
			break
		}
	}
	key, rest := s[:end], s[end:]
	rest = strings.TrimLeft(rest, " \t\f")
	if rest != "" && (rest[0] == '=' || rest[0] == ':') {
		rest = strings.TrimLeft(rest[1:], " \t\f")
	}
	return unescapeProperty(key), unescapeProperty(rest)
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+5 <= len(s) {
				if r, ok := hexRune(s[i+1 : i+5]); ok {
					b.WriteRune(r)
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
