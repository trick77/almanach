package baseline

import (
	"strconv"
	"strings"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// A run checks what the PRs taught against the code; it does not erase it.
// A step answers with a patch over the page: an id it does not name stays
// as it is, an edit keeps the statement's id and with it its sources, and a
// statement a PR taught may be dropped only when the code no longer has any
// of its files. An edit of one may correct its text and add files, but
// keeps every file of it the tree still has: otherwise the next run would
// find it resting on nothing and drop it after all. A patch is never
// refused whole: an item that breaks a rule costs only itself (user,
// 2026-11-01). An edit without an id, of an id the page does not have, of
// one an earlier edit already took, or of a PR-taught statement that sheds
// a file the tree still has is left out, named in BadEdits, the statement
// as stored or as first edited. A drop of a statement the patch also edits
// or confirms, or of a PR-taught one whose files still exist, is left out,
// named in BadDrops, the statement kept. An unknown id to drop or confirm
// refers to nothing: it is left out, named in Unknown.

// Applied is a patch turned into the whole proposal a fold checks: every
// statement of the page in its order with edits in place and drops gone,
// then the new ones (ID nil). Checked says per statement that the step read
// the code behind it (edited, new or confirmed); Summary nil keeps the
// summary. Unknown is the drop and confirmed ids the page does not have;
// BadEdits the edits left out, BadDrops the drops left out, by id and
// reason.
type Applied struct {
	Statements []inference.Statement
	Checked    []bool
	Summary    *string
	Unknown    []string
	BadEdits   []string
	BadDrops   []string
}

// ApplyPatch applies a step's patch to the current statements. exists says
// whether a path is in the tree at the run's commit. It never refuses: what
// breaks a rule is left out and named.
func ApplyPatch(current []store.Statement, p inference.Patch, exists func(path string) bool) Applied {
	byID := make(map[string]store.Statement, len(current))
	for _, s := range current {
		byID[s.ID] = s
	}
	edits := map[string]inference.Statement{}
	var unknown, bad, badDrops []string
	for _, st := range p.Statements {
		if st.ID == nil || *st.ID == "" {
			bad = append(bad, NoIDEdit(st.Name))
			continue
		}
		id := *st.ID
		if _, ok := byID[id]; !ok {
			bad = append(bad, UnknownEdit(id))
			continue
		}
		if _, twice := edits[id]; twice {
			bad = append(bad, TwiceEdit(id))
			continue
		}
		if old := byID[id]; FromPR(old) {
			if lost := shed(old.Files, st.Files, exists); lost != "" {
				bad = append(bad, id+" (taught by a pull request, drops its file "+lost+" the tree still has)")
				continue
			}
		}
		edits[id] = st
	}
	// Confirmed before drops: a statement both confirmed and dropped was
	// checked and found right, the drop is the slip.
	confirmed := map[string]bool{}
	var unknownConfirmed []string
	for _, id := range p.Confirmed {
		if _, ok := byID[id]; !ok {
			unknownConfirmed = append(unknownConfirmed, id)
			continue
		}
		confirmed[id] = true
	}
	dropped := map[string]bool{}
	for _, id := range p.Drop {
		s, ok := byID[id]
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		switch _, edited := edits[id]; {
		case edited:
			badDrops = append(badDrops, id+" (edited too, the edit taken)")
		case confirmed[id]:
			badDrops = append(badDrops, id+" (confirmed too, kept)")
		case FromPR(s) && !gone(s.Files, exists):
			badDrops = append(badDrops, id+" (taught by a pull request, its files still exist)")
		default:
			dropped[id] = true
		}
	}

	// Unknown in the patch's order: drops, then confirmations.
	out := Applied{Unknown: append(unknown, unknownConfirmed...), BadEdits: bad, BadDrops: badDrops}
	if p.Summary != nil && strings.TrimSpace(*p.Summary) != "" {
		out.Summary = p.Summary
	}
	for _, s := range current {
		if dropped[s.ID] {
			continue
		}
		if st, ok := edits[s.ID]; ok {
			out.Statements = append(out.Statements, st)
			out.Checked = append(out.Checked, true)
			continue
		}
		id := s.ID
		out.Statements = append(out.Statements, inference.Statement{ID: &id, Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Diagram: s.Diagram})
		out.Checked = append(out.Checked, confirmed[s.ID])
	}
	for _, st := range p.Added {
		st.ID = nil
		out.Statements = append(out.Statements, st)
		out.Checked = append(out.Checked, true)
	}
	return out
}

// NoIDEdit names an edit without an id, left out, by the name it carried.
func NoIDEdit(name string) string { return strconv.Quote(name) + " (no id)" }

// UnknownEdit names an edit of an id the page lacks, left out, for
// BadEdits and a fold's log alike.
func UnknownEdit(id string) string { return id + " (unknown id)" }

// TwiceEdit names an edit of an id an earlier edit took, left out.
func TwiceEdit(id string) string { return id + " (id used twice)" }

// FromPR reports whether a pull request is among a statement's sources.
func FromPR(s store.Statement) bool {
	for _, src := range s.Sources {
		if !src.IsBaseline() {
			return true
		}
	}
	return false
}

// shed is the first old file an edit leaves out that the tree still has,
// "" when it keeps them all.
func shed(old, edited []string, exists func(string) bool) string {
	kept := make(map[string]bool, len(edited))
	for _, f := range edited {
		kept[normPath(f)] = true
	}
	for _, f := range old {
		if !kept[normPath(f)] && exists(f) {
			return f
		}
	}
	return ""
}

// gone reports whether a statement's files have all left the tree. A
// statement without files has nothing the code could show gone.
func gone(files []string, exists func(string) bool) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if exists(f) {
			return false
		}
	}
	return true
}
