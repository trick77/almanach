// Package markup is the layout a statement's text and a page's summary
// may carry, one layout for both: paragraphs, flat lists, **bold**, `code`
// spans and one level of #### sub-heading. Normalize puts what a
// model writes into that stored form, inside lang.Laid: after redaction,
// before the Swiss rule. Anything beyond the subset stays text.
// ui/src/format.ts blocks reads the same form, the Markdown export passes
// it through.
package markup

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Heading is the one sub-heading level a text or summary holds: under the
// export's ### statement names and the prompt frame's ### [id] lines.
const Heading = "#### "

// MaxHeading is the longest title a heading line holds, in runes: a longer
// one is a whole text a model wrote on one line behind a "#### ", kept as
// text without the marks.
const MaxHeading = 120

// The kinds of a Block.
const (
	BlockParagraph = "paragraph"
	BlockHeading   = "heading"
	BlockList      = "list"
	BlockNumbered  = "numbered"
)

// Block is one block of a laid-out text as the model returns it. The
// schema carries the layout, not newlines inside a JSON string: a model
// that writes a whole text on one line (or breaks it with "<br>") still
// gives paragraphs, lists and sub-headings. Text is a paragraph's or a
// heading's, or a list's lead-in; Items a list's.
type Block struct {
	Kind  string   `json:"kind"`
	Text  string   `json:"text"`
	Items []string `json:"items"`
}

// FromBlocks is blocks as text for Normalize: a heading a "#### " line, a
// list one item a line, numbered from 1, blocks one blank line apart.
// Marks a model wrote anyway ("## " before a heading's text, "- " before
// an item) are dropped, not doubled; empty blocks and items are left out.
func FromBlocks(bs []Block) string {
	var out []string
	for _, b := range bs {
		text := strings.TrimSpace(b.Text)
		switch b.Kind {
		case BlockHeading:
			if t, ok := heading(text); ok {
				text = t
			}
			if t := strings.Join(strings.Fields(text), " "); t != "" {
				out = append(out, Heading+t)
			}
		case BlockList, BlockNumbered:
			if text != "" {
				out = append(out, text)
			}
			var lines []string
			for _, it := range b.Items {
				it = unmarked(strings.Join(strings.Fields(it), " "), b.Kind)
				if it == "" {
					continue
				}
				if isRule("- " + it) {
					// "---" as an item would be a rule, dropped on the read.
					it = CodeSpan(it)
				}
				if b.Kind == BlockList {
					lines = append(lines, "- "+it)
				} else {
					lines = append(lines, strconv.Itoa(len(lines)+1)+". "+it)
				}
			}
			if len(lines) > 0 {
				out = append(out, strings.Join(lines, "\n"))
			}
		default:
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return strings.Join(out, "\n\n")
}

// unmarked is a list item without the marker of its own block's kind a
// model wrote anyway ("- " in a list, "2. " in a numbered one); a number
// in a bullet item ("2. Stufe") is its text and stays.
func unmarked(it, kind string) string {
	if kind == BlockNumbered {
		if _, rest, ok := ordered(it); ok {
			return rest
		}
		return it
	}
	if len(it) > 2 && strings.ContainsRune("-*+", rune(it[0])) && it[1] == ' ' {
		return strings.TrimSpace(it[2:])
	}
	return it
}

// restarts reports whether a list item starts a list of its own after the
// block or item before it: a "1." after a numbered item. Two numbered
// blocks of a model's stay two lists, as the UI's blocks reads them.
func restarts(before, item string) bool {
	last := before[strings.LastIndexByte(before, '\n')+1:]
	n, _, ok := ordered(item)
	_, _, prevOrdered := ordered(last)
	return ok && n == "1" && prevOrdered
}

// brTag is an HTML line break as models write one; inlineHeading a
// heading's marks inside a line, a sub-heading a model wrote without the
// newline before it.
var (
	brTag         = regexp.MustCompile(`(?i)<br\s*/?>`)
	inlineHeading = regexp.MustCompile(`[ \t]+#{4,6}[ \t]`)
)

// Breaks is s with every <br> outside code a newline and every heading
// mark inside a line on a line of its own: a model that will not write a
// newline into a JSON string writes the tag, or nothing. Inside a code
// span or a closed fence it is code and stays. lang.Laid runs it before
// redaction, so a credential split by a <br> is one to the redaction.
func Breaks(s string) string {
	lines := strings.Split(s, "\n")
	proseLines(lines, func(i int) {
		lines[i] = mapProse(lines[i], func(p string) string {
			return inlineHeading.ReplaceAllString(brTag.ReplaceAllString(p, "\n"), "\n"+Heading)
		})
	})
	return strings.Join(lines, "\n")
}

// LayoutLost reports whether stored text lost its layout on the way in:
// a heading's or a break's marks inside a line (" #### ", "<br>") outside
// code, or a heading line of a paragraph's length. Such a text is a
// model's whole answer on one line, written before the schema carried
// blocks and Breaks; the prompt asks for it again, laid out. A long
// paragraph is no such mark: one paragraph block is stored as one.
//
// TRANSITIONAL(lost-layout): Remove when no current revision's text is
// LayoutLost.
func LayoutLost(s string) bool {
	lost := false
	lines := strings.Split(s, "\n")
	proseLines(lines, func(i int) {
		t := strings.TrimSpace(lines[i])
		if title, ok := StoredHeading(t); ok && utf8.RuneCountInString(title) > MaxHeading {
			lost = true
		}
		mapProse(t, func(p string) string {
			lost = lost || brTag.MatchString(p) || inlineHeading.MatchString(p)
			return p
		})
	})
	return lost
}

// proseLines calls f with the index of every line outside a closed fence,
// the fence's own lines left out: Normalize's fence rule, for Breaks and
// LayoutLost.
func proseLines(lines []string, f func(i int)) {
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			if ClosesFence(t, fence) {
				fence = ""
			}
			continue
		}
		if fc := FenceOf(t); fc != "" && slices.ContainsFunc(lines[i+1:], func(l string) bool { return ClosesFence(strings.TrimSpace(l), fc) }) {
			fence = fc
			continue
		}
		f(i)
	}
}

// mapProse is a line with f applied to each run of it outside code spans;
// a backtick run nothing closes is prose, as SpanAt reads it.
func mapProse(l string, f func(string) string) string {
	var b strings.Builder
	for j := 0; j < len(l); {
		if l[j] == '`' {
			end, ok := SpanAt(l, j)
			if ok {
				b.WriteString(l[j:end])
			} else {
				b.WriteString(f(l[j:end]))
			}
			j = end
			continue
		}
		next := strings.IndexByte(l[j:], '`')
		if next < 0 {
			next = len(l) - j
		}
		b.WriteString(f(l[j : j+next]))
		j += next
	}
	return b.String()
}

// Normalize is model text in the stored form: blocks one blank line apart;
// a paragraph's lines as written, their indents dropped (so the Swiss rule
// skips none as code); a list item one "- " or "N. " line, its
// continuation lines joined to it; a heading, any level, a "#### " line;
// a rule ("* * *", "---") dropped, a block's end. A fenced block's lines
// become `code` spans: a "# comment" in it never turns into a heading.
// A <br> outside code is a newline; a heading longer than MaxHeading is
// text. What was dropped is the text when nothing else is: a text that is only
// headings or a fence's word keeps the titles or the word as lines, never
// empty headings to show or an empty text for the page check to refuse.
// Normalize of its own output is that output: one pass is repeated until
// nothing changes (a pass only drops and joins, so few do), for marks the
// first pass leaves that the next reads anew ("* -" and "-" joined are
// "- - -", a rule) would otherwise drop an item on a later read.
func Normalize(s string) string {
	out := normalizeOnce(Breaks(s))
	for range 8 {
		// breaks each pass: joining a list item's lines can make a <br>.
		next := normalizeOnce(Breaks(out))
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
	var dropped []string // fence words left out, heading titles
	body := false        // a block that is no heading was written
	var cur []string     // the open paragraph's or list's lines
	list := false        // cur is a list
	gap := false         // a blank line in an open list: an item next goes on
	lastList := false    // the last block written is a list
	flush := func() {
		switch {
		case len(cur) == 0:
		case list && lastList && !restarts(blocks[len(blocks)-1], cur[0]):
			// Two lists with nothing between (a blank line) are one: the
			// next read would join them.
			blocks[len(blocks)-1] += "\n" + strings.Join(cur, "\n")
		default:
			blocks = append(blocks, strings.Join(cur, "\n"))
			lastList = list
			body = true
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
		if title, ok := heading(line); ok && utf8.RuneCountInString(title) > MaxHeading {
			// A "heading" of a paragraph's length is a model's whole text on
			// one line: its marks go, it stays text, never one long title.
			// Every run of marks at once: one left would be a heading on the
			// next read.
			flush()
			line = title
			for t, more := heading(line); more; t, more = heading(line) {
				line = t
			}
		} else if ok {
			flush()
			blocks = append(blocks, Heading+title)
			dropped = append(dropped, title)
			lastList = false
			continue
		}
		if item, ok := ListItem(line); ok {
			if !list || restarts(cur[len(cur)-1], item) {
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
	if !body {
		// Headings over nothing are no layout: their titles are the text,
		// never an empty part to show, an empty one for the page check to
		// refuse. Only what reads back as itself: a title "```" or "---"
		// kept as a line would be a fence or a rule on the next read, gone.
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
