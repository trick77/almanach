// Package lang is the one spelling rule: Swiss Standard German, ss never ß.
package lang

import (
	"strings"

	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/redact"
)

// Prose is a piece of the model's prose as stored: trimmed, any credential
// shape replaced (a model echoes what it read, and the input filters are
// regexes), Swiss spelling. Deterministic, so an unchanged rewrite stays
// unchanged.
func Prose(s string) string { return SwissSS(redact.Text(strings.TrimSpace(s))) }

// Laid is Prose for laid-out text, a statement's or a summary's: the
// credential shapes replaced first, while the text is as the model wrote
// it (a PEM block's header ends its line only there), then the stored
// layout (markup.Normalize), then Swiss spelling, which so sees no line as
// indented code.
func Laid(s string, headings bool) string {
	return SwissSS(markup.Normalize(redact.Text(strings.TrimSpace(s)), headings))
}

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
