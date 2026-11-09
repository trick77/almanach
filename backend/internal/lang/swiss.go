// Package lang is the one spelling rule: Swiss Standard German, ss never ß.
package lang

import (
	"strings"

	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/redact"
)

// Prose is a piece of the model's prose as stored: trimmed, any credential
// shape replaced (a model echoes what it read, and the input filters are
// regexes), Swiss spelling, the software words English (Terms).
// Deterministic, so an unchanged rewrite stays unchanged.
func Prose(s string) string { return Terms(Spelled(s)) }

// Laid is Prose for laid-out text, a statement's or a summary's: the
// credential shapes replaced first, while the text is as the model wrote
// it (a PEM block's header ends its line only there), then the stored
// layout (markup.Normalize), then Swiss spelling and Terms, which so see no
// line as indented code.
//
// A model's <br> becomes a newline first (markup.Breaks): a credential it
// split is one to the redaction, every shape of it.
func Laid(s string) string {
	return Terms(SwissSS(markup.Normalize(redact.Text(markup.Breaks(strings.TrimSpace(s))))))
}

var umlauts = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue")

// SameButSpelling reports whether a and b say the same, umlauts written
// out as two letters or not, a software word German or English: a model
// writing "fuer" once and "für" later said one thing, and so did a stored
// "Dienst" against today's "Service". The prompts ask for umlauts; older
// text may lack them, and text stored before Terms has the German word.
func SameButSpelling(a, b string) bool {
	return a == b || umlauts.Replace(Terms(a)) == umlauts.Replace(Terms(b))
}

// SameName is SameButSpelling ignoring case, for a name the model may
// write in any: a subject, an area. The words are made English before the
// case is dropped, as Terms reads nouns as written: a plan stored with
// "Nachrichten" names today's "Messages".
func SameName(a, b string) bool {
	return strings.EqualFold(a, b) || strings.EqualFold(umlauts.Replace(Terms(a)), umlauts.Replace(Terms(b)))
}

// Spelled is Prose before Terms: trimmed, redacted, Swiss. A cap on what
// the model wrote is held to it, so a word made English, a few letters
// longer, never pushes a text the model kept within the cap over it.
func Spelled(s string) string { return SwissSS(redact.Text(strings.TrimSpace(s))) }

// SwissSS writes the model's German prose with Swiss spelling: ß becomes ss
// (ẞ becomes SS). The prompt asks for it, and a model does not always obey;
// this is the blunt rule that makes it hold. Code is left alone: fenced
// blocks, indented code blocks and inline backtick spans may quote an
// identifier that really contains ß. The same rule as noergler's.
func SwissSS(s string) string {
	if !strings.ContainsAny(s, "ßẞ") {
		return s
	}
	return outsideCode(s, strings.NewReplacer("ß", "ss", "ẞ", "SS").Replace)
}

// outsideCode applies f to the prose of s alone: fenced blocks, indented
// code blocks and inline backtick spans stay as written.
func outsideCode(s string, f func(string) string) string {
	return proseIn(s, func(_, piece, _ string) string { return f(piece) })
}

// proseIn is outsideCode with each piece's surroundings: f sees the text of
// s before the piece and after it, as written, code and all.
func proseIn(s string, f func(before, piece, after string) string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	fence := ""
	off := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		at := off
		off += len(line)
		// markup's fence rule: a code span markup.CodeSpan wrote, ``` and all,
		// opens no fence here either, or every ß after it would stay.
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if markup.ClosesFence(trimmed, fence) {
				fence = ""
			}
			b.WriteString(line)
			continue
		}
		if f := markup.FenceOf(trimmed); f != "" {
			fence = f
			b.WriteString(line)
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			b.WriteString(line)
			continue
		}
		b.WriteString(proseLine(s, at, line, f))
	}
	return b.String()
}

// proseLine is proseIn on one line, at its offset in s.
func proseLine(s string, at int, line string, f func(before, piece, after string) string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '`' {
			// markup's span rule; a run never closed leaves the rest as is.
			j, ok := markup.SpanAt(line, i)
			if !ok {
				b.WriteString(line[i:])
				return b.String()
			}
			b.WriteString(line[i:j])
			i = j
			continue
		}
		next := strings.IndexByte(line[i:], '`')
		if next < 0 {
			next = len(line) - i
		}
		b.WriteString(f(s[:at+i], line[i:i+next], s[at+i+next:]))
		i += next
	}
	return b.String()
}
