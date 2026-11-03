package baseline

import (
	"strings"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/store"
)

// A page reads as a person would write it: inside every kind its
// statements in areas, the logical parts of the system, one order of areas
// for the whole page (the system as a whole, the parts as a reader meets
// them, building and delivering it last), overview before detail inside
// one. A page's areas and their order are its statements' order: an area
// stands where its first statement does. Without them each kind was the
// order the steps wrote in, build statements in four kinds at four places
// (user, 2026-11-02: "a human would not structure a documentation like
// that"). Area and order are no text: never a source, never a revision of
// their own.

// LastArea is the area that always ends the page: how the code is built,
// tested, checked, delivered and kept up to date, the local tools and the
// conventions of working on it, in one place (user: "build and ci/cd
// probably last, and not in multiple places").
const LastArea = "Entwicklung und Auslieferung"

// OpsArea is the area just before LastArea: what belongs to the running
// system as a whole and to no one part, its settings read at startup,
// health, logging, a dependency down. Narrow by its prompt's rule, a part's
// own setting or failure staying in the part's area: a wide one would
// hollow out the parts' chapters. Without it such statements stood
// wherever the model put them.
const OpsArea = "Betrieb"

// CleanArea is an area name as stored: Swiss prose, inner runs of space
// one, OpsArea and LastArea in their own spelling whatever case or umlauts
// the model used.
func CleanArea(a string) string {
	a = strings.Join(strings.Fields(lang.Prose(a)), " ")
	for _, fixed := range []string{OpsArea, LastArea} {
		if lang.SameButSpelling(strings.ToLower(a), strings.ToLower(fixed)) {
			return fixed
		}
	}
	return a
}

// Arrange is the page's statements in its areas' order: each area's
// statements together where its first one stands, in their order; those
// without an area after them, then OpsArea's, LastArea's last. Stable: an
// arranged page comes back as it was.
func Arrange(statements []store.Statement) []store.Statement {
	var order []string
	groups := map[string][]store.Statement{}
	for _, s := range statements {
		if _, ok := groups[s.Area]; !ok && s.Area != "" && s.Area != OpsArea && s.Area != LastArea {
			order = append(order, s.Area)
		}
		groups[s.Area] = append(groups[s.Area], s)
	}
	out := make([]store.Statement, 0, len(statements))
	for _, a := range append(order, "", OpsArea, LastArea) {
		out = append(out, groups[a]...)
	}
	return out
}

// ApplyOutline is the page's statements laid out as the map step's
// outline says: its areas in its order, each statement under the area and
// at the place it names. Never refused whole: an area without a name, an id
// the page lacks or one named twice is left out and named in bad; a
// statement the outline does not name keeps its area and joins it, or the
// end, then Arrange.
func ApplyOutline(current []store.Statement, outline []inference.OutlineArea) (out []store.Statement, bad []string) {
	byID := make(map[string]store.Statement, len(current))
	for _, s := range current {
		byID[s.ID] = s
	}
	placed := map[string]bool{}
	out = make([]store.Statement, 0, len(current))
	for _, a := range outline {
		area := CleanArea(a.Area)
		if area == "" {
			bad = append(bad, "an area without a name ("+strings.Join(a.IDs, ", ")+")")
			continue
		}
		for _, id := range a.IDs {
			s, ok := byID[id]
			switch {
			case !ok:
				bad = append(bad, id+" (unknown id)")
			case placed[id]:
				bad = append(bad, id+" (named twice)")
			default:
				placed[id] = true
				s.Area = area
				out = append(out, s)
			}
		}
	}
	for _, s := range current {
		if !placed[s.ID] {
			out = append(out, s)
		}
	}
	return Arrange(out), bad
}

// Arranged reports whether every statement of a page has its area: a page
// written before areas, or by a call that gave none, is laid out by the
// next map step.
func Arranged(statements []store.Statement) bool {
	for _, s := range statements {
		if s.Area == "" {
			return false
		}
	}
	return true
}
