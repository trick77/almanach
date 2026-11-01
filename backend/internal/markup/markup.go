// Package markup is the layout a statement's text and a page's summary
// may carry, one layout for both: paragraphs, flat lists, **bold**, `code`
// spans and one level of #### sub-heading. Normalize puts what a
// model writes into that stored form, inside lang.Laid: after redaction,
// before the Swiss rule. Anything beyond the subset stays text.
// ui/src/format.ts blocks reads the same form, the Markdown export passes
// it through.
package markup

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Heading is the one sub-heading level a text or summary holds: under the
// export's ### statement names and the prompt frame's ### [id] lines.
const Heading = "#### "

// Normalize is model text in the stored form: blocks one blank line apart;
// a paragraph's lines as written, their indents dropped (so the Swiss rule
// skips none as code); a list item one "- " or "N. " line, its
// continuation lines joined to it; a heading, any level, a "#### " line;
// a rule ("* * *", "---") dropped, a block's end. A fenced block's lines
// become `code` spans: a "# comment" in it never turns into a heading.
// What was dropped is the text when nothing else is: a text that is only
// a fence's word keeps it, never empty for the page check to refuse.
// Normalize of its own output is that output: one pass is repeated until
// nothing changes (a pass only drops and joins, so few do), for marks the
// first pass leaves that the next reads anew ("* -" and "-" joined are
// "- - -", a rule) would otherwise drop an item on a later read.
func Normalize(s string) string {
	out := normalizeOnce(s)
	for range 8 {
		next := normalizeOnce(out)
		if next == out {
			break
		}
		out = next
	}
	return out
}

// normalizeOnce is one pass of Normalize.
func normalizeOnce(s string) string {
	var blocks []string
	var dropped []string // fence words left out
	var cur []string     // the open paragraph's or list's lines
	list := false        // cur is a list
	gap := false         // a blank line in an open list: an item next goes on
	lastList := false    // the last block written is a list
	flush := func() {
		switch {
		case len(cur) == 0:
		case list && lastList:
			// Two lists with nothing between (a dropped heading, a
			// blank line) are one: the next read would join them.
			blocks[len(blocks)-1] += "\n" + strings.Join(cur, "\n")
		default:
			blocks = append(blocks, strings.Join(cur, "\n"))
			lastList = list
		}
		cur, list, gap = nil, false, false
	}
	fence := ""
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if ClosesFence(line, fence) {
				fence = ""
				flush()
				continue
			}
			if line != "" {
				cur = append(cur, CodeSpan(line))
			}
			continue
		}
		if f := FenceOf(line); f != "" {
			if slices.ContainsFunc(lines[i+1:], func(l string) bool { return ClosesFence(strings.TrimSpace(l), f) }) {
				flush()
				fence = f
				continue
			}
			// A fence never closed is no block: its markers go, a fence's
			// word ("go", "yaml") with them; prose after them on the line
			// and the lines after stay text, not one column of code.
			for ; f != ""; f = FenceOf(line) {
				line = strings.TrimSpace(line[len(f):])
			}
			if line == "" {
				continue
			}
			if infoWord(line) {
				dropped = append(dropped, line)
				continue
			}
		}
		if line == "" {
			// A blank line between items keeps one list, as CommonMark
			// reads it (a loose list): the UI's blocks keeps it one too.
			if list {
				gap = true
			} else {
				flush()
			}
			continue
		}
		if gap {
			if _, ok := ListItem(line); !ok {
				flush()
			}
			gap = false
		}
		if isRule(line) {
			flush()
			continue
		}
		if title, ok := heading(line); ok {
			flush()
			blocks = append(blocks, Heading+title)
			lastList = false
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
	if len(blocks) == 0 {
		// Only what reads back as itself: a title "```" or "---" kept as a
		// line would be a fence or a rule on the next read, and gone.
		kept := slices.DeleteFunc(dropped, func(d string) bool {
			_, h := heading(d)
			_, item := ListItem(d)
			return h || item || isRule(d) || FenceOf(d) != ""
		})
		return strings.Join(kept, "\n")
	}
	return strings.Join(blocks, "\n\n")
}

// infoWord reports whether what is left of a fence never closed is its
// info string, a language's name ("go", "yaml", "c++"), dropped with the
// fence; anything else ("Hinweis:", a sentence) is text and stays.
func infoWord(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("+-._#", r) {
			return false
		}
	}
	return s != ""
}

// isRule reports whether a line is a thematic break as CommonMark reads
// one: three or more of one of - * _, blanks between allowed.
func isRule(line string) bool {
	for _, c := range []string{"-", "*", "_"} {
		if strings.Trim(line, c+" \t") == "" && strings.Count(line, c) >= 3 {
			return true
		}
	}
	return false
}

// ListItem reports whether a line, indent dropped, is a list item, and
// returns it in the stored form: "- item" for -, * or + and "N. item" for
// N. or N). A rule ("* * *", "- - -") is none.
func ListItem(line string) (string, bool) {
	if isRule(line) {
		return "", false
	}
	if len(line) > 2 && strings.ContainsRune("-*+", rune(line[0])) && line[1] == ' ' {
		// "* --" stored as "- --" would be a rule on the next read: no item.
		if rest := strings.TrimSpace(line[2:]); rest != "" && !isRule("- "+rest) {
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
// reads one: "C#" keeps its #) and the title's own ** dropped when one
// bold run is the whole title ("**Lesen** und **Schreiben**" is two).
func heading(line string) (string, bool) {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return "", false
	}
	t := strings.TrimSpace(line[n:])
	// Every closing run: one left would be closing on the next read.
	for c := strings.TrimRight(t, "#"); c != t && (c == "" || strings.HasSuffix(c, " ")); c = strings.TrimRight(t, "#") {
		t = strings.TrimSpace(c)
	}
	if strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**") && len(t) > 4 && !strings.Contains(t[2:len(t)-2], "**") {
		t = strings.TrimSpace(t[2 : len(t)-2])
	}
	return t, t != ""
}

// ClosesFence reports whether a line, trimmed, closes the fence FenceOf
// returned: the fence's character alone, at least as many.
func ClosesFence(line, fence string) bool {
	return strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == ""
}

// FenceOf is the fence a line, trimmed, opens: its whole run of three or
// more backticks or tildes, or "". As in CommonMark, a backtick fence's
// info string holds no backtick: a code span CodeSpan wrote around a line
// with ``` in it opens nothing. The one fence rule here and in lang's
// Swiss spelling, so what one reads as code the other does too.
func FenceOf(line string) string {
	for _, c := range []string{"`", "~"} {
		run := line[:len(line)-len(strings.TrimLeft(line, c))]
		if len(run) < 3 {
			continue
		}
		if c == "`" && strings.Contains(line[len(run):], "`") {
			return ""
		}
		return run
	}
	return ""
}

// CodeSpan is s as one inline code span, its backtick run one longer than
// the longest inside, set off by a blank when s holds one.
func CodeSpan(s string) string {
	longest := LongestRun(s, '`')
	tick := strings.Repeat("`", longest+1)
	if longest > 0 {
		return tick + " " + s + " " + tick
	}
	return tick + s + tick
}

// LongestRun is the length of the longest run of c in s.
func LongestRun(s string, c byte) int {
	best, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// SpanAt reads the run of backticks at s[i]: the end of the code span it
// opens, past the next same run, and true; or the run's own end and false
// when nothing after it on s closes it. The one code-span rule of unbold,
// Words and lang's Swiss spelling, as format.ts codeSpans has it.
func SpanAt(s string, i int) (int, bool) {
	n := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
	if j := strings.Index(s[i+n:], s[i:i+n]); j >= 0 {
		return i + n + j + n, true
	}
	return i + n, false
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

// Plain is stored text without its layout, line by line: the marks of a
// stored sub-heading and list item gone and every ** that pairs (bolds,
// as format.ts runs reads them), the words and code spans, backticks and
// all, as they are. format.ts plain, the search's, drops the backticks too.
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
			i, _ = SpanAt(l, i)
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

// Words is what a text says, its layout aside: its words, a sub-heading's
// title and a numbered item's number among them (a changed title, a step
// renumbered is a change), its symbols (> is not <, -5 not 5) and its code
// spans whole, one space apart. A . , ; : ends a word, as a sentence's
// mark a list drops; inside one ("1,5", "config.yaml") it is the word's.
// Two texts with the same words differ only in how they are laid out.
func Words(s string) string {
	var out []string
	var word strings.Builder
	end := func() {
		if word.Len() > 0 {
			out = append(out, word.String())
			word.Reset()
		}
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if marker, text, ok := StoredItem(strings.TrimSpace(l)); ok && marker != "- " {
			lines[i] = strings.TrimSuffix(marker, ". ") + " " + text
		}
	}
	p := Plain(strings.Join(lines, "\n"))
	inWord := func(i int) bool {
		r, _ := utf8.DecodeRuneInString(p[i:])
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	for i := 0; i < len(p); {
		if p[i] == '`' {
			// A code span is one token, its punctuation kept: != is not ==.
			end()
			j, ok := SpanAt(p, i)
			if ok {
				out = append(out, p[i:j])
			}
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(p[i:])
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word.WriteString(p[i : i+size])
		case strings.ContainsRune(".,;:", r) && word.Len() > 0 && i+size < len(p) && inWord(i+size):
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
