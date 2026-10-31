// Package lang is the one spelling rule: Swiss Standard German, ss never ß.
package lang

import "strings"

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue")

// SameButSpelling reports whether a and b say the same, umlauts written
// out as two letters or not: a model writing "fuer" once and "für" later
// said one thing. The prompts ask for umlauts; older text may lack them.
func SameButSpelling(a, b string) bool { return a == b || umlauts.Replace(a) == umlauts.Replace(b) }

// SwissSS writes the model's German prose with Swiss spelling: ß becomes ss
// (ẞ becomes SS). The prompt asks for it, and a model does not always obey;
// this is the blunt rule that makes it hold. Code is left alone: fenced
// blocks, indented code blocks and inline backtick spans may quote an
// identifier that really contains ß. The same rule as noergler's.
func SwissSS(s string) string {
	if !strings.ContainsAny(s, "ßẞ") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	fence := ""
	for _, line := range strings.SplitAfter(s, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			b.WriteString(line)
			continue
		}
		if f := fenceOf(trimmed); f != "" {
			fence = f
			b.WriteString(line)
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			b.WriteString(line)
			continue
		}
		b.WriteString(swissLine(line))
	}
	return b.String()
}

func fenceOf(trimmed string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(trimmed) && trimmed[n] == c {
			n++
		}
		if n >= 3 {
			return trimmed[:n]
		}
	}
	return ""
}

func swissLine(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '`' {
			j := i
			for j < len(line) && line[j] == '`' {
				j++
			}
			run := line[i:j]
			end := strings.Index(line[j:], run)
			if end < 0 {
				b.WriteString(line[i:])
				return b.String()
			}
			b.WriteString(line[i : j+end+len(run)])
			i = j + end + len(run)
			continue
		}
		next := strings.IndexByte(line[i:], '`')
		if next < 0 {
			next = len(line) - i
		}
		b.WriteString(strings.NewReplacer("ß", "ss", "ẞ", "SS").Replace(line[i : i+next]))
		i += next
	}
	return b.String()
}
