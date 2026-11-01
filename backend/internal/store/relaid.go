package store

import (
	"strings"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/markup"
)

// TRANSITIONAL(br-repair): Remove when no revision holds <br>.
//
// relaid is stored text read back with a model's <br> as the newline it
// meant: text a model broke with "<br>" before the schema carried blocks
// was stored on one line, its first heading's marks lost (markup's
// headings-only rule kept the title as text). markup.Normalize reads the
// breaks; a first line that is a title (short, no sentence's end) over
// more lines is a heading again. Text without <br> is as stored.
func relaid(s string) string {
	if !strings.Contains(strings.ToLower(s), "<br") {
		return s
	}
	out := markup.Normalize(s)
	first, rest, ok := strings.Cut(out, "\n")
	title := strings.TrimSpace(first)
	if ok && !strings.HasPrefix(rest, "\n") && title != "" && utf8.RuneCountInString(title) <= relaidTitle &&
		!strings.ContainsAny(title[len(title)-1:], ".:!?;,") {
		if _, h := markup.StoredHeading(title); !h {
			if _, _, item := markup.StoredItem(title); !item {
				out = markup.Normalize(markup.Heading + title + "\n\n" + rest)
			}
		}
	}
	return out
}

// relaidTitle is the longest first line relaid reads as a lost heading.
const relaidTitle = 60

// relaidStatements is every statement's text through relaid.
func relaidStatements(ss []Statement) {
	for i := range ss {
		ss[i].Text = relaid(ss[i].Text)
	}
}
