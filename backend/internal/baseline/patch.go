package baseline

import (
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
// find it resting on nothing and drop it after all. Anything else rejects
// the patch whole, like an id the page does not have.

// Applied is a patch turned into the whole proposal a fold checks: every
// statement of the page in its order with edits in place and drops gone,
// then the new ones. Checked says per statement that the step read the code
// behind it (edited, new or confirmed); Summary nil keeps the summary.
type Applied struct {
	Statements []inference.Statement
	Checked    []bool
	Summary    *string
}

// ApplyPatch applies a step's patch to the current statements. exists says
// whether a path is in the tree at the run's commit. The reason is "" when
// the patch holds, else why it is rejected.
func ApplyPatch(current []store.Statement, p inference.Patch, exists func(path string) bool) (Applied, string) {
	byID := make(map[string]store.Statement, len(current))
	for _, s := range current {
		byID[s.ID] = s
	}
	edits := map[string]inference.Statement{}
	var added []inference.Statement
	for _, st := range p.Statements {
		if st.ID == nil || *st.ID == "" {
			added = append(added, st)
			continue
		}
		id := *st.ID
		if _, ok := byID[id]; !ok {
			return Applied{}, "unknown statement id " + id
		}
		if _, twice := edits[id]; twice {
			return Applied{}, "statement id used twice " + id
		}
		if old := byID[id]; fromPR(old) {
			if lost := shed(old.Files, st.Files, exists); lost != "" {
				return Applied{}, "edit of " + id + ", which a pull request taught, drops its file " + lost + " the tree still has"
			}
		}
		edits[id] = st
	}
	dropped := map[string]bool{}
	for _, id := range p.Drop {
		s, ok := byID[id]
		if !ok {
			return Applied{}, "drop of unknown statement id " + id
		}
		if _, edited := edits[id]; edited {
			return Applied{}, "statement " + id + " both edited and dropped"
		}
		if fromPR(s) && !gone(s.Files, exists) {
			return Applied{}, "drop of " + id + ", which a pull request taught and whose files still exist"
		}
		dropped[id] = true
	}
	confirmed := map[string]bool{}
	for _, id := range p.Confirmed {
		if _, ok := byID[id]; !ok {
			return Applied{}, "confirmation of unknown statement id " + id
		}
		if dropped[id] {
			return Applied{}, "statement " + id + " both confirmed and dropped"
		}
		confirmed[id] = true
	}

	out := Applied{}
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
	for _, st := range added {
		out.Statements = append(out.Statements, st)
		out.Checked = append(out.Checked, true)
	}
	return out, ""
}

// fromPR reports whether a pull request is among a statement's sources.
func fromPR(s store.Statement) bool {
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
