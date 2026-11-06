package markup

import "strings"

// Export is model-written text in its stored layout and nothing more,
// never the structure of the Markdown file it goes into (the page's
// export, a repository's PROJECT.md). Each line loses its indent (no code
// block). A "- " or "N. " list item stays as it is, a "#### " sub-heading
// becomes sub, the level under the text's own heading (a statement's ####
// under its ### name, a summary's ### under its ##); any other line that
// would open a block as CommonMark reads it (a heading of another level,
// quote, fence, * or + item, rule, underline or HTML) gets a backslash
// before the marker: a backslashed ASCII punctuation mark reads as itself.
// Text that opens nothing, "-5" or "1.5 Sekunden", stays as written, for
// the agent reads the raw file.
func Export(s, sub string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if title, ok := StoredHeading(t); ok {
			lines[i] = sub + title
			continue
		}
		// An item's text is text again: "- # x" or "1. > y" opens nothing
		// inside the item either.
		if marker, text, ok := StoredItem(t); ok {
			lines[i] = marker + escapeBlock(text)
			continue
		}
		lines[i] = escapeBlock(t)
	}
	return strings.Join(lines, "\n")
}

// escapeBlock is a line, its indent dropped, with a backslash before the
// marker of any block it would open.
func escapeBlock(t string) string {
	if blockStart(t) {
		return `\` + t
	}
	if n := len(t) - len(strings.TrimLeft(t, "0123456789")); n > 0 && n <= 9 && n < len(t) &&
		(t[n] == '.' || t[n] == ')') && markerEnds(t[n+1:]) {
		return t[:n] + `\` + t[n:]
	}
	return t
}

// blockStart reports whether a line, its indent dropped, opens a block
// with its first character.
func blockStart(t string) bool {
	if t == "" {
		return false
	}
	switch t[0] {
	case '>':
		return true
	case '#':
		n := len(t) - len(strings.TrimLeft(t, "#"))
		return n <= 6 && markerEnds(t[n:])
	case '`', '~':
		// The fence rule: a code span Normalize wrote around a line with
		// ``` in it opens no fence and keeps no backslash.
		return FenceOf(t) != ""
	case '<':
		return len(t) > 1 && (t[1] >= 'a' && t[1] <= 'z' || t[1] >= 'A' && t[1] <= 'Z' || strings.ContainsRune("/!?", rune(t[1])))
	case '-', '+', '*':
		if markerEnds(t[1:]) {
			return true
		}
	}
	// A rule or a heading's underline: nothing but -, *, _ or = and blanks.
	return strings.Trim(t, "-*_= \t") == ""
}

// markerEnds reports whether what follows a block marker lets it open the
// block: nothing, or a blank.
func markerEnds(rest string) bool {
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}
