package inference

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/trick77/almanach/internal/redact"
)

// shape is what a cut or unparseable answer held, read as far as it goes:
// its top-level fields, and every element that is a statement (an object in
// an array with an id or a name), with its size and its text's blocks by
// kind. The tail of a runaway (one-sentence paragraphs, 2026-11-10) named
// no statement; the shape says which one ran, how far, and in what blocks.
type shape struct {
	top   []string
	elems []*shapeElem
	// blocks is every text's blocks by kind, a summary's too.
	blocks map[string]int
}

// shapeElem is one statement of an answer: where it stands, what it is,
// its span in the answer, open when the answer ended inside it. run is
// its text's paragraphs since its last list, longest the most of them.
type shapeElem struct {
	path, id, kind, name string
	start, end           int
	open                 bool
	blocks               map[string]int
	run, longest         int
}

func (e *shapeElem) size() int { return e.end - e.start }

// block counts one block of the statement's text: a paragraph lengthens
// the stretch without a list, a list or numbered block ends it; a heading
// neither (a runaway's headings never brought its list).
func (e *shapeElem) block(kind string) {
	e.blocks[kind]++
	switch kind {
	case "paragraph":
		e.run++
		e.longest = max(e.longest, e.run)
	case "list", "numbered":
		e.run = 0
	}
}

// blockCounts is blocks by kind as the log names them.
func blockCounts(blocks map[string]int) string {
	n := 0
	for _, c := range blocks {
		n += c
	}
	return fmt.Sprintf("%d blocks (%d paragraph, %d heading, %d list, %d numbered)", n,
		blocks["paragraph"], blocks["heading"], blocks["list"], blocks["numbered"])
}

// String is the element for the log: path, id, kind, name, size, blocks.
func (e *shapeElem) String() string {
	var b strings.Builder
	b.WriteString(e.path)
	for _, s := range []string{e.id, e.kind} {
		if s != "" {
			b.WriteString(" " + s)
		}
	}
	if e.name != "" {
		b.WriteString(" " + strconv.Quote(e.name))
	}
	fmt.Fprintf(&b, " %d chars, %s, at most %d paragraphs without a list", e.size(), blockCounts(e.blocks), e.longest)
	if e.open {
		b.WriteString(", open")
	}
	return b.String()
}

// shapeLists are the arrays whose objects are statements, at any depth (a
// merge's stand under repository and project): a diagram's nodes, an
// outline's areas are none.
var shapeLists = map[string]bool{"statements": true, "added": true, "subjects": true}

// shapeFrame is one container the walk is in.
type shapeFrame struct {
	array     bool
	name      string // its key in the parent object, an array's own
	key       string // an object's current key
	expectKey bool
	index     int
	path      string
	elem      *shapeElem // the statement this object is, nil for none
	owner     *shapeElem // the statement it stands in
	block     bool       // an object of a statement's text
	topField  int        // a root field's index in shape.top, -1 for none
}

// answerShape walks an answer from its first '{', truncated or not, token
// by token until it ends or breaks: what it read stands. A statement is an
// object in one of shapeLists.
func answerShape(content string) shape {
	sh := shape{blocks: map[string]int{}}
	base := strings.IndexByte(content, '{')
	if base < 0 {
		return sh
	}
	dec := json.NewDecoder(strings.NewReader(content[base:]))
	dec.UseNumber()
	var stack []*shapeFrame
	var all []*shapeElem
	for {
		off := base + int(dec.InputOffset())
		tok, err := dec.Token()
		if err != nil {
			break
		}
		var top *shapeFrame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		delim, isDelim := tok.(json.Delim)
		if isDelim && (delim == '}' || delim == ']') {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.elem != nil {
				f.elem.end, f.elem.open = base+int(dec.InputOffset()), false
			}
			if f.array && f.topField >= 0 {
				sh.top[f.topField] = fmt.Sprintf("%s %d", f.name, f.index)
			}
			if len(stack) > 0 && !stack[len(stack)-1].array {
				stack[len(stack)-1].expectKey = true
			}
			if len(stack) == 0 {
				break
			}
			continue
		}
		if top != nil && !top.array && top.expectKey {
			top.key, _ = tok.(string)
			top.expectKey = false
			continue
		}
		if isDelim {
			f := &shapeFrame{array: delim == '[', expectKey: delim == '{', topField: -1}
			if top != nil {
				f.owner = top.owner
				if top.array {
					f.path = top.path + "[" + strconv.Itoa(top.index) + "]"
					f.name = top.name
					top.index++
				} else {
					f.name = top.key
					f.path = strings.TrimPrefix(top.path+"."+top.key, ".")
					if len(stack) == 1 {
						f.topField = len(sh.top)
						sh.top = append(sh.top, top.key)
					}
				}
			}
			if !f.array && top != nil && top.array {
				switch {
				case top.name == "text" || top.name == "summary":
					f.block = true
				case shapeLists[top.name]:
					// The offset stands before the comma and the space the
					// token came after.
					for off < len(content) && content[off] != '{' {
						off++
					}
					f.elem = &shapeElem{path: f.path, start: off, open: true, blocks: map[string]int{}}
					f.owner = f.elem
					all = append(all, f.elem)
				}
			}
			stack = append(stack, f)
			continue
		}
		if top == nil {
			break
		}
		if top.array {
			top.index++
			continue
		}
		// A value of the current key.
		s, _ := tok.(string)
		switch {
		case top.block && top.key == "kind":
			sh.blocks[s]++
			if top.owner != nil {
				top.owner.block(s)
			}
		case top.elem != nil && top.key == "id":
			top.elem.id = s
		case top.elem != nil && top.key == "kind":
			top.elem.kind = s
		case top.elem != nil && top.key == "name":
			top.elem.name = s
		}
		if len(stack) == 1 {
			if tok == nil {
				sh.top = append(sh.top, top.key+" null")
			} else {
				sh.top = append(sh.top, top.key)
			}
		}
		top.expectKey = true
	}
	// What is still open ran to the answer's end.
	for _, f := range stack {
		if f.array && f.topField >= 0 {
			sh.top[f.topField] = fmt.Sprintf("%s %d, open", f.name, f.index)
		}
	}
	for _, e := range all {
		if e.open {
			e.end = len(content)
		}
		if e.id != "" || e.name != "" {
			sh.elems = append(sh.elems, e)
		}
	}
	slices.SortStableFunc(sh.elems, func(a, b *shapeElem) int { return b.size() - a.size() })
	return sh
}

const (
	// shapeLargest is how many statements the shape line names, largest
	// first.
	shapeLargest = 5
	// shapeHead is how much of the largest statement the line keeps: where
	// its runaway begins.
	shapeHead = 1500
)

// logShape logs an answer's shape (answerShape), one line, with the
// largest statement's head; none for an answer with no JSON. content comes
// redacted (logCut, unparseable).
func (c *Client) logShape(callID, content, file string) {
	sh := answerShape(content)
	if len(sh.top) == 0 && len(sh.elems) == 0 {
		return
	}
	largest := make([]string, 0, shapeLargest)
	for _, e := range sh.elems[:min(shapeLargest, len(sh.elems))] {
		largest = append(largest, e.String())
	}
	head := ""
	if len(sh.elems) > 0 {
		e := sh.elems[0]
		head = strings.ToValidUTF8(content[e.start:min(e.end, e.start+shapeHead)], "")
	}
	c.log.Warn("answer shape", "model", c.model, "call_id", callID, "top", strings.Join(sh.top, "; "),
		"blocks", blockCounts(sh.blocks), "statements", len(sh.elems), "largest", strings.Join(largest, " | "), "runaway_head", head, "file", file)
}

// logBlocks logs the layout of an answer that arrived whole, one line:
// its blocks by kind, the largest statement, the one with the longest
// stretch of paragraphs without a list, and, where the listGuard watched
// (leadIns >= 0), how many lead-ins it saw left without their list. A cut
// answer's shape was logged and an ok one's was not, so whether answers
// gave their lists at all (the restyle runaways gave none) and where the
// guard's thresholds sit against honest answers was nowhere to read. An
// answer with no blocks (a catalogue, a route) is no line.
func (c *Client) logBlocks(callID, content string, leadIns int) {
	sh := answerShape(content)
	if len(sh.blocks) == 0 {
		return
	}
	attrs := []any{"model", c.model, "call_id", callID, "blocks", blockCounts(sh.blocks), "statements", len(sh.elems)}
	if leadIns >= 0 {
		attrs = append(attrs, "lead_ins_without_list", leadIns)
	}
	if len(sh.elems) > 0 {
		longest := sh.elems[0]
		for _, e := range sh.elems[1:] {
			if e.longest > longest.longest {
				longest = e
			}
		}
		attrs = append(attrs, "largest", redact.Text(sh.elems[0].String()), "longest_run", redact.Text(longest.String()))
	}
	c.log.Info("answer blocks", attrs...)
}
