// Package markup is the layout a statement's text and a page's summary
// may carry: paragraphs, flat lists, **bold**, `code` spans and, in a
// statement's text, one level of #### sub-heading. Normalize puts what a
// model writes into that stored form, inside lang.Laid: after redaction,
// before the Swiss rule. Anything beyond the subset stays text. ui/src/format.ts blocks reads the same
// form, the Markdown export passes it through.
package markup

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Heading is the one sub-heading level statement text holds: under the
// export's ### statement names and the prompt frame's ### [id] lines.
const Heading = "#### "

// Normalize is model text in the stored form: blocks one blank line apart;
// a paragraph's lines as written, their indents dropped (so the Swiss rule
// skips none as code); a list item one "- " or "N. " line, its
// continuation lines joined to it; a heading, any level, a "#### " line
// where headings is set and a **bold** paragraph where not (a summary). A
// fenced block's lines become `code` spans: a "# comment" in it never
// turns into a heading. Normalize of its own output is that output.
func Normalize(s string, headings bool) string {
	var blocks []string
	var cur []string // the open paragraph's or list's lines
	list := false    // cur is a list
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, strings.Join(cur, "\n"))
		}
		cur, list = nil, false
	}
	fence := ""
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if closesFence(line, fence) {
				fence = ""
				flush()
				continue
			}
			if line != "" {
				cur = append(cur, codeSpan(line))
			}
			continue
		}
		if f := fenceOf(line); f != "" {
			// A fence never closed is no block: its line goes, the rest
			// stays text, not one column of code to the end.
			if slices.ContainsFunc(lines[i+1:], func(l string) bool { return closesFence(strings.TrimSpace(l), f) }) {
				flush()
				fence = f
			}
			continue
		}
		if line == "" {
			flush()
			continue
		}
		if title, ok := heading(line); ok {
			flush()
			if headings {
				blocks = append(blocks, Heading+title)
			} else {
				blocks = append(blocks, "**"+title+"**")
			}
			continue
		}
		if item, ok := ListItem(line); ok {
			if !list {
				flush()
				list = true
			}
			cur = append(cur, item)
			continue
		}
		if list {
			// A line under an item without a marker continues the item.
			cur[len(cur)-1] += " " + line
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return strings.Join(blocks, "\n\n")
}

// ListItem reports whether a line, indent dropped, is a list item, and
// returns it in the stored form: "- item" for -, * or + and "N. item" for
// N. or N).
func ListItem(line string) (string, bool) {
	if len(line) > 2 && strings.ContainsRune("-*+", rune(line[0])) && line[1] == ' ' {
		if rest := strings.TrimSpace(line[2:]); rest != "" {
			return "- " + rest, true
		}
		return "", false
	}
	if n, rest, ok := ordered(line); ok {
		return n + ". " + rest, true
	}
	return "", false
}

// ordered splits "12. text" or "12) text" into its number and text.
func ordered(line string) (string, string, bool) {
	n := len(line) - len(strings.TrimLeft(line, "0123456789"))
	if n == 0 || n > 9 || n+1 >= len(line) || line[n] != '.' && line[n] != ')' || line[n+1] != ' ' {
		return "", "", false
	}
	rest := strings.TrimSpace(line[n+2:])
	return line[:n], rest, rest != ""
}

// heading reports whether a line is a heading of any level and returns
// its title: markers, a closing run of #s after a blank (as CommonMark
// reads one: "C#" keeps its #) and the title's own ** dropped.
func heading(line string) (string, bool) {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return "", false
	}
	t := strings.TrimSpace(line[n:])
	if c := strings.TrimRight(t, "#"); c == "" || strings.HasSuffix(c, " ") {
		t = strings.TrimSpace(c)
	}
	if strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**") && len(t) > 4 {
		t = strings.TrimSpace(t[2 : len(t)-2])
	}
	return t, t != ""
}

// closesFence reports whether a line, trimmed, closes an open fence: the
// fence's characters alone, at least as many.
func closesFence(line, fence string) bool {
	return strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == ""
}

// fenceOf is the fence a line opens, "```" or "~~~", or "". A backtick
// fence's info string holds no backtick, as in CommonMark: a code span
// codeSpan wrote around a line with ``` in it opens nothing.
func fenceOf(line string) string {
	switch {
	case strings.HasPrefix(line, "~~~"):
		return "~~~"
	case strings.HasPrefix(line, "```") && !strings.Contains(strings.TrimLeft(line, "`"), "`"):
		return "```"
	}
	return ""
}

// codeSpan quotes a line as one `code` span, its backtick run one longer
// than the longest inside.
func codeSpan(line string) string {
	longest, run := 0, 0
	for _, r := range line {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	tick := strings.Repeat("`", longest+1)
	if longest > 0 {
		return tick + " " + line + " " + tick
	}
	return tick + line + tick
}

// StoredHeading reports whether a line, trimmed, is a sub-heading in the
// stored form and returns its title.
func StoredHeading(line string) (string, bool) {
	t, ok := strings.CutPrefix(line, Heading)
	t = strings.TrimSpace(t)
	return t, ok && t != ""
}

// StoredItem reports whether a line, trimmed, is a list item in the stored
// form exactly ("- text", "12. text") and returns its marker and text.
func StoredItem(line string) (marker, text string, ok bool) {
	item, ok := ListItem(line)
	if !ok || item != line {
		return "", "", false
	}
	if n, rest, ok := ordered(item); ok {
		return n + ". ", rest, true
	}
	return "- ", item[2:], true
}

// Plain is stored text without its layout, as ui/src/format.ts plain has
// it: the marks of a stored sub-heading and list item gone and every **
// that pairs (bolds), the words and code spans as they are.
func Plain(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if t, ok := StoredHeading(l); ok {
			l = t
		} else if _, t, ok := StoredItem(l); ok {
			l = t
		}
		lines[i] = unbold(l)
	}
	return strings.Join(lines, "\n")
}

// unbold is a line without the ** that bold, as format.ts runs reads them:
// one outside a code span pairs with the next such on the line when
// something is between; an unpaired one stays.
func unbold(l string) string {
	if !strings.Contains(l, "**") {
		return l
	}
	var marks []int
	for i := 0; i < len(l); {
		if l[i] == '`' {
			n := len(l[i:]) - len(strings.TrimLeft(l[i:], "`"))
			if j := strings.Index(l[i+n:], l[i:i+n]); j >= 0 {
				i += n + j + n
			} else {
				i += n
			}
			continue
		}
		if strings.HasPrefix(l[i:], "**") {
			marks = append(marks, i)
			i += 2
			continue
		}
		i++
	}
	drop := map[int]bool{}
	for k := 0; k+1 < len(marks); {
		if marks[k+1] > marks[k]+2 {
			drop[marks[k]], drop[marks[k+1]] = true, true
			k += 2
		} else {
			k++
		}
	}
	var b strings.Builder
	for i := 0; i < len(l); i++ {
		if drop[i] {
			i++
			continue
		}
		b.WriteByte(l[i])
	}
	return b.String()
}

// Words is what a text says, its layout aside: its words, its symbols
// (> is not <, -5 not 5) and its code spans whole, one space apart; a
// sentence's . , ; : and a sub-heading's title left out (a list drops the
// one, the other names a part and teaches nothing). Two texts with the
// same words differ only in how they are laid out.
func Words(s string) string {
	var out []string
	var word strings.Builder
	end := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	var kept []string
	for _, l := range strings.Split(s, "\n") {
		if _, ok := StoredHeading(strings.TrimSpace(l)); !ok {
			kept = append(kept, l)
		}
	}
	p := Plain(strings.Join(kept, "\n"))
	for i := 0; i < len(p); {
		if p[i] == '`' {
			// A code span is one token, its punctuation kept: != is not ==.
			n := len(p[i:]) - len(strings.TrimLeft(p[i:], "`"))
			if j := strings.Index(p[i+n:], p[i:i+n]); j >= 0 {
				end()
				out = append(out, p[i:i+n+j+n])
				i += n + j + n
				continue
			}
			i += n
			end()
			continue
		}
		r, size := utf8.DecodeRuneInString(p[i:])
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteString(p[i : i+size])
		case unicode.IsSpace(r) || strings.ContainsRune(".,;:", r):
			end()
		default:
			end()
			out = append(out, p[i:i+size])
		}
		i += size
	}
	end()
	return strings.Join(out, " ")
}
