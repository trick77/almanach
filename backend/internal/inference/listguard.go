package inference

import (
	"bytes"
	"regexp"
	"strings"
)

// listGuard watches a streamed answer for a list announced and never
// given: a paragraph that ends in a colon ("Der Ablauf lautet:", "Die
// nummerierte Liste ist:") followed by another paragraph instead of the
// list or numbered block it leads into. Every restyle cut on 2026-11-10 (7
// of them, 175k to 228k chars each, $0.64 to $0.74 or unpriced) ended in
// such paragraphs, one sentence each, the text never coming back as a
// list; the loopGuard saw no repeat, as no two were alike. Counted per
// text array (listOrphans in one text) and over the answer since its last
// list (listRunOrphans: a runaway that breaks every statement up the same
// way, a few to each), a list or numbered block clearing both. An honest
// text has one or two. The restyle's alone (callOpts.lists).
type listGuard struct {
	off bool
	// buf is the answer from the first block not yet complete.
	buf   []byte
	since int
	lead  bool
	// orphans is the current text's, run the answer's since its last list.
	orphans, run int
	cut          bool
}

const (
	// listOrphans is how many lead-ins without their list in one text cut
	// the answer, listRunOrphans how many since the answer's last list.
	listOrphans    = 8
	listRunOrphans = 24
	// listKeep bounds the buffer a stretch without a block grows to (a
	// long diagram).
	listKeep = 64 << 10
	// listEvery is how often, in chars, the new part of the answer is
	// scanned.
	listEvery = 1024
)

// blockStep is what listGuard steps through, in order: a list or numbered
// block's kind (its keys in any order: the kind alone says it is one); a
// paragraph or heading with its text whole, kind first or text first; the
// start of a text array. A statement's own text is a string after no
// block kind, never a block. A paragraph's keys are kind and text alone
// (blocks(), strict), so nothing stands between them.
var blockStep = regexp.MustCompile(
	`"kind"\s*:\s*"(?:list|numbered)"` +
		`|"kind"\s*:\s*"(paragraph|heading)"\s*,\s*"text"\s*:\s*"((?:[^"\\]|\\.)*)"` +
		`|"text"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,\s*"kind"\s*:\s*"(paragraph|heading)"` +
		`|"(?:text|summary)"\s*:\s*\[`)

// add takes a delta of the answer and reports whether it now runs away.
func (g *listGuard) add(delta string) bool {
	if g.off || g.cut {
		return g.cut
	}
	g.buf = append(g.buf, delta...)
	g.since += len(delta)
	if g.since < listEvery {
		return false
	}
	g.since = 0
	g.scan()
	return g.cut
}

// count is the lead-ins without their list that cut the answer.
func (g *listGuard) count() int { return max(g.orphans, g.run) }

// scan steps through the blocks completed since the last scan and keeps
// only what follows the last.
func (g *listGuard) scan() {
	matches := blockStep.FindAllSubmatchIndex(g.buf, -1)
	for _, m := range matches {
		var kind, text string
		switch {
		case m[2] >= 0:
			kind, text = string(g.buf[m[2]:m[3]]), string(g.buf[m[4]:m[5]])
		case m[6] >= 0:
			kind, text = string(g.buf[m[8]:m[9]]), string(g.buf[m[6]:m[7]])
		case g.buf[m[0]+1] == 'k':
			// A list or numbered block.
			g.lead, g.orphans, g.run = false, 0, 0
			continue
		default:
			// A new text array: its lead-ins are its own.
			g.lead, g.orphans = false, 0
			continue
		}
		if g.lead {
			g.orphans++
			g.run++
			if g.orphans >= listOrphans || g.run >= listRunOrphans {
				g.cut = true
				return
			}
		}
		g.lead = kind == "paragraph" && leadIn(text)
	}
	switch {
	case len(matches) > 0:
		g.buf = append(g.buf[:0], g.buf[matches[len(matches)-1][1]:]...)
	case len(g.buf) > listKeep:
		// From its last '{', where an unfinished block may start, and never
		// more than listKeep: no block runs that long.
		i := max(bytes.LastIndexByte(g.buf, '{'), len(g.buf)-listKeep)
		g.buf = append(g.buf[:0], g.buf[i:]...)
	}
}

// leadIn reports whether a block's text, as JSON escapes it, ends in a
// colon, past trailing blanks and escaped newlines or tabs.
func leadIn(text string) bool {
	for {
		t := strings.TrimRight(text, " ")
		for _, esc := range []string{`\n`, `\r`, `\t`} {
			t = strings.TrimSuffix(t, esc)
		}
		if t == text {
			return strings.HasSuffix(t, ":")
		}
		text = t
	}
}
