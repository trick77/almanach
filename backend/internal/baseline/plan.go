package baseline

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
)

// Subject is one piece of a repository a step reads: a flow or a part, what
// it is for, and its files at the run's commit, within StepMaxBytes.
type Subject struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Files   []File `json:"files"`
}

// Bytes is the file content the subject's step reads.
func (s Subject) Bytes() int64 {
	var n int64
	for _, f := range s.Files {
		n += f.Size
	}
	return n
}

// Plan is a run's catalogue: the subjects in the order the steps read them.
// Skipped names the subjects the run could not fold (a failed call, a
// refused answer), so the next reconcile reads them again; Why is each one's Skip
// code, the first that hit it. A run from before Why has names alone.
// Steps is the overview and the project step when the run skipped them,
// by Part, never by a name: a subject's name is the model's, any string.
// Again names the subjects, and AgainOverview the overview, that folded
// but left a statement alone that a newer pull request taught or whose
// merge commit git could not place: the next reconcile reads them again,
// as it does a skipped one, but they were no skip.
type Plan struct {
	// Format is PlanFormat on every plan the worker writes; 0 is a plan
	// from before Steps, which Upgrade reads.
	Format        int             `json:"format,omitempty"`
	Subjects      []Subject       `json:"subjects"`
	Skipped       []string        `json:"skipped,omitempty"`
	Why           map[string]Skip `json:"why,omitempty"`
	Steps         map[Part]Skip   `json:"steps,omitempty"`
	Again         []string        `json:"again,omitempty"`
	AgainOverview bool            `json:"again_overview,omitempty"`
}

// PlanFormat is the plan as written now: the steps in Steps.
const PlanFormat = 2

// The overview and the project step as plans from before Steps named them
// in Skipped, beside the subjects. Upgrade moves them.
const (
	legacyOverview = "the overview"
	legacyProject  = "the project"
)

// Upgrade moves the overview and the project step out of Skipped into
// Steps, for a plan stored before Steps (Format 0), and marks it current.
// A subject such a plan names "the overview" or "the project" reads as
// that step this once; a current plan is left as it is, its subjects named
// anything.
func (p *Plan) Upgrade() {
	if p.Format >= PlanFormat {
		return
	}
	p.Format = PlanFormat
	var kept []string
	moved := false
	for _, name := range p.Skipped {
		switch name {
		case legacyOverview:
			p.SkipStep(PartOverview, p.Why[name])
		case legacyProject:
			p.SkipStep(PartProject, p.Why[name])
		default:
			kept = append(kept, name)
			continue
		}
		delete(p.Why, name)
		moved = true
	}
	if !moved {
		return
	}
	// Nil, not empty, when nothing is left: a plan as the worker builds one.
	p.Skipped = kept
	if len(p.Why) == 0 {
		p.Why = nil
	}
}

// Skip is why a run left a part out, a code the page words; the log line
// carries the detail.
type Skip string

const (
	// SkipGone is when the subject left the catalogue mid-run.
	SkipGone Skip = "gone"
	// SkipCalls is when the run's calls were spent.
	SkipCalls Skip = "calls"
	// SkipBytes is when the part would take the run over its byte budget.
	SkipBytes Skip = "bytes"
	// SkipFailed is when the model call failed.
	SkipFailed Skip = "failed"
	// SkipRefused is when the answer broke a fold rule and was thrown away.
	SkipRefused Skip = "refused"
	// SkipNoPage is when the model asked no change of a project that has no
	// page yet.
	SkipNoPage Skip = "no_page"
)

// Part is which kind of part a run skipped, as the source page names it.
type Part string

// The parts: the docs read first, a catalogue subject, the project step.
const (
	PartOverview Part = "overview"
	PartSubject  Part = "subject"
	PartProject  Part = "project"
)

// SkippedPart is one part a run left out, as the source page lists it.
type SkippedPart struct {
	Part Part
	// Name is the subject's, "" for the overview and the project.
	Name string
	// Reason is the Skip code, "" for a run from before it was kept.
	Reason Skip
}

// RunSkips is the parts a stored run plan left out, a plan from before
// Steps upgraded. It keeps format, skipped, why and steps alone, by Plan's
// own tags (TestRunSkips_ReadsAStoredPlan and
// TestPlan_UpgradeMovesTheLegacyStepNames hold them together): the
// subjects are still scanned, never built, and the source page asks for
// every repository on every poll.
func RunSkips(raw json.RawMessage) ([]SkippedPart, error) {
	var p struct {
		Format  int             `json:"format"`
		Skipped []string        `json:"skipped"`
		Why     map[string]Skip `json:"why"`
		Steps   map[Part]Skip   `json:"steps"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
	}
	plan := Plan{Format: p.Format, Skipped: p.Skipped, Why: p.Why, Steps: p.Steps}
	plan.Upgrade()
	return plan.SkippedParts(), nil
}

// SkippedParts is the plan's skipped parts in the order a run reads them:
// the overview, the subjects as the run hit them, the project step.
func (p Plan) SkippedParts() []SkippedPart {
	out := make([]SkippedPart, 0, len(p.Skipped)+len(p.Steps))
	if why, ok := p.Steps[PartOverview]; ok {
		out = append(out, SkippedPart{Part: PartOverview, Reason: why})
	}
	for _, name := range p.Skipped {
		out = append(out, SkippedPart{Part: PartSubject, Name: name, Reason: p.Why[name]})
	}
	if why, ok := p.Steps[PartProject]; ok {
		out = append(out, SkippedPart{Part: PartProject, Reason: why})
	}
	return out
}

// ReadAgain reports whether a part skipped for s is worth an automatic
// run: not a subject gone (nothing to read), nor a project the model left
// unchanged without a page (the same answer again; the next run asks).
func (s Skip) ReadAgain() bool {
	return s != SkipGone && s != SkipNoPage
}

// LeftParts reports whether a run left a part worth reading again, from
// its parts; a run from before they were named, from its skipped count.
// The poller and the source page ask it alike.
func LeftParts(parts []SkippedPart, skipped int) bool {
	if len(parts) == 0 {
		return skipped > 0
	}
	return slices.ContainsFunc(parts, func(p SkippedPart) bool { return p.Reason.ReadAgain() })
}

// SkipSubject names a subject skipped for why, once: the first reason
// stays.
func (p *Plan) SkipSubject(name string, why Skip) {
	if slices.Contains(p.Skipped, name) {
		return
	}
	p.Skipped = append(p.Skipped, name)
	if p.Why == nil {
		p.Why = map[string]Skip{}
	}
	p.Why[name] = why
}

// SkipStep marks the overview or the project step skipped for why, once:
// the first reason stays.
func (p *Plan) SkipStep(part Part, why Skip) {
	if _, ok := p.Steps[part]; ok {
		return
	}
	if p.Steps == nil {
		p.Steps = map[Part]Skip{}
	}
	p.Steps[part] = why
}

// StepSkipped reports whether the run skipped the overview or the project
// step.
func (p Plan) StepSkipped(part Part) bool {
	_, ok := p.Steps[part]
	return ok
}

// Bytes is the file content of every subject.
func (p Plan) Bytes() int64 {
	var n int64
	for _, s := range p.Subjects {
		n += s.Bytes()
	}
	return n
}

// find is the index of the subject with this name, ignoring case and
// umlauts written out ("Datenueberpruefung" is "Datenüberprüfung"); -1
// when none.
func (p Plan) find(name string) int {
	for i, s := range p.Subjects {
		if lang.SameButSpelling(strings.ToLower(s.Name), strings.ToLower(name)) {
			return i
		}
	}
	return -1
}

// Listing is the tree as the catalogue model sees it: every readable file
// with its size (a cut one marked) when they fit ListMaxEntries, else the
// tree folded to fit: a directory is one line with its file count and bytes
// until it is opened, and opened where the files are. Over the entries the
// tree was once folded to two levels whatever its shape, and a Maven or
// Gradle repository, all of it under src/main/java, became one line the
// model could only name whole. A directory is named with a trailing slash,
// the way the model may name it back; the files right under an opened one,
// as one line, `dir/*` (the root's `*`), which resolve takes for those files
// alone. The root's subdirectories are always shown, so a root of more than
// ListMaxEntries directories runs over: nothing is dropped for size.
func Listing(files []File) string {
	var b strings.Builder
	if len(files) <= ListMaxEntries {
		for _, f := range files {
			b.WriteString(fileLine(f))
		}
		return b.String()
	}
	root := buildTree(files)
	root.fold(ListMaxEntries)
	root.write(&b)
	return b.String()
}

// fileLine is one file of the listing, a cut one said to be.
func fileLine(f File) string {
	if f.Cut {
		return fmt.Sprintf("%s\t%d\thead only\n", f.Path, f.Size)
	}
	return fmt.Sprintf("%s\t%d\n", f.Path, f.Size)
}

// treeNode is a directory of the listing: its direct files, its
// subdirectories, and how it is shown (how).
type treeNode struct {
	path     string // "" for the root, else with a trailing slash
	direct   []File
	children []*treeNode
	files    int   // files under it, direct and deeper
	bytes    int64 // their bytes
	how      fold
}

// fold is how a directory is shown: one line for all of it; open, its
// subdirectories listed and its direct files one line; flat, its direct
// files each on a line.
type fold int8

const (
	folded fold = iota
	open
	flat
)

// buildTree is the files as a tree of directories, each child list in path
// order (the files sorted first: a reconcile's unplaced come in diff
// order), the root open.
func buildTree(files []File) *treeNode {
	if !sort.SliceIsSorted(files, func(i, j int) bool { return files[i].Path < files[j].Path }) {
		files = append([]File(nil), files...)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}
	root := &treeNode{how: open}
	nodes := map[string]*treeNode{"": root}
	for _, f := range files {
		dir := ""
		parent := root
		parts := strings.Split(f.Path, "/")
		for _, part := range parts[:len(parts)-1] {
			dir += part + "/"
			n := nodes[dir]
			if n == nil {
				n = &treeNode{path: dir}
				nodes[dir] = n
				parent.children = append(parent.children, n)
			}
			n.files++
			n.bytes += f.Size
			parent = n
		}
		parent.direct = append(parent.direct, f)
		root.files++
		root.bytes += f.Size
	}
	return root
}

// entries is the lines a node takes as it is shown.
func (n *treeNode) entries() int {
	switch n.how {
	case folded:
		return 1
	case open:
		c := len(n.children)
		if len(n.direct) > 0 {
			c++
		}
		return c
	default:
		return len(n.direct) + len(n.children)
	}
}

// fold opens the tree where the files are until the listing holds limit
// lines. A move shows a directory one step further: a folded one opened
// (a leaf straight to its files), an open one's direct files laid out. Of
// the moves on the listing, the one touching the most files goes first
// (an opening the files under the directory, a laying out its direct
// files), skipped when it does not fit, so the big parts are opened before
// the small, a pile of files at the root never crowds out the code
// directory beside it, and a part too wide to lay out still shows its
// subdirectories. A move never frees a line, so one skipped never fits
// later. The root starts open.
func (n *treeNode) fold(limit int) {
	n.how = open
	count := n.entries()
	q := &moves{}
	// An opened directory offers its direct files and its subdirectories.
	offer := func(d *treeNode) {
		if d.how == open && len(d.direct) > 0 {
			heap.Push(q, move{d, len(d.direct), true})
		}
		for _, c := range d.children {
			heap.Push(q, move{c, c.files, false})
		}
	}
	offer(n)
	for q.Len() > 0 {
		m := heap.Pop(q).(move)
		d := m.dir
		before, was := d.entries(), d.how
		d.how = open
		if m.layout || len(d.children) == 0 {
			d.how = flat
		}
		cost := d.entries() - before
		if count+cost > limit {
			d.how = was
			continue
		}
		count += cost
		if !m.layout {
			offer(d)
		}
	}
}

// move is one step of fold: dir opened, or with layout its direct files
// laid out; files is what it touches.
type move struct {
	dir    *treeNode
	files  int
	layout bool
}

// moves is fold's queue, the most files first, then by path.
type moves []move

func (q moves) Len() int { return len(q) }
func (q moves) Less(i, j int) bool {
	if q[i].files != q[j].files {
		return q[i].files > q[j].files
	}
	return q[i].dir.path < q[j].dir.path
}
func (q moves) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *moves) Push(x any)   { *q = append(*q, x.(move)) }
func (q *moves) Pop() any {
	old := *q
	m := old[len(old)-1]
	*q = old[:len(old)-1]
	return m
}

// write lays the tree out: at an open or flat directory its direct files
// (each, or as one `dir/*` line), then its subdirectories, in path order; a folded
// one as one line.
func (n *treeNode) write(b *strings.Builder) {
	if n.how == folded {
		fmt.Fprintf(b, "%s\t%d files\t%d bytes\n", n.path, n.files, n.bytes)
		return
	}
	if n.how == flat {
		for _, f := range n.direct {
			b.WriteString(fileLine(f))
		}
	} else if len(n.direct) > 0 {
		var bytes int64
		for _, f := range n.direct {
			bytes += f.Size
		}
		fmt.Fprintf(b, "%s*\t%d files\t%d bytes\n", n.path, len(n.direct), bytes)
	}
	for _, c := range n.children {
		c.write(b)
	}
}

// index is the readable files by path and in path order, for resolving the
// catalogue's paths.
type index struct {
	byPath map[string]File
	sorted []File
}

// newIndex takes the files as Files returns them, in path order; another
// order is sorted on a copy.
func newIndex(files []File) index {
	ix := index{byPath: make(map[string]File, len(files)), sorted: files}
	for _, f := range files {
		ix.byPath[f.Path] = f
	}
	byPath := func(i, j int) bool { return ix.sorted[i].Path < ix.sorted[j].Path }
	if !sort.SliceIsSorted(ix.sorted, byPath) {
		ix.sorted = append([]File(nil), files...)
		sort.Slice(ix.sorted, byPath)
	}
	return ix
}

// resolve turns one path the model named into readable files: the file
// itself, or every readable file under it when it names a directory (with
// or without the trailing slash), or only the files right under it when it
// is the listing's `dir/*` (the root's `*`, `./*` or `/*`). A path naming
// nothing readable resolves to nothing: the model inventing one, or one
// the filter refused.
func (ix index) resolve(p string) []File {
	p = strings.TrimSpace(p)
	if f, ok := ix.byPath[p]; ok {
		return []File{f}
	}
	direct := strings.HasSuffix(p, "*")
	prefix := strings.TrimSuffix(p, "/") + "/"
	if direct {
		prefix = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(p, "*"), "./"), "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			return nil
		}
	} else if prefix == "/" {
		return nil
	}
	start := sort.Search(len(ix.sorted), func(i int) bool { return ix.sorted[i].Path >= prefix })
	var out []File
	for _, f := range ix.sorted[start:] {
		if !strings.HasPrefix(f.Path, prefix) {
			break
		}
		if direct && strings.Contains(f.Path[len(prefix):], "/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// add puts the files of the named paths into s in the model's order, each
// once, as long as they fit StepMaxBytes; a file that does not fit is
// skipped and a smaller one after it may still be taken.
func (ix index) add(s *Subject, paths []string) {
	have := map[string]bool{}
	used := s.Bytes()
	for _, f := range s.Files {
		have[f.Path] = true
	}
	for _, p := range paths {
		for _, f := range ix.resolve(p) {
			if have[f.Path] || used+f.Size > StepMaxBytes {
				continue
			}
			have[f.Path] = true
			used += f.Size
			s.Files = append(s.Files, f)
		}
	}
}

// BuildPlan holds the catalogue's answer to rules the model cannot bend:
// only readable files of the tree, a directory expanded to its files, each
// subject within StepMaxBytes, a nameless or empty subject dropped, two
// subjects of one name merged, at most maxSubjects in the model's order.
func BuildPlan(proposed []inference.Subject, files []File, maxSubjects int) Plan {
	return MergePlan(Plan{}, proposed, files, maxSubjects)
}

// MergePlan is BuildPlan onto an existing plan, a reconcile's catalogue
// step: the old subjects keep their files, re-sized from the tree and held
// to StepMaxBytes again in their order (files grow), those gone or no
// longer readable dropped; a proposal naming an old subject adds its files
// to it, a new name opens a new subject while fewer than maxSubjects exist.
func MergePlan(old Plan, proposed []inference.Subject, files []File, maxSubjects int) Plan {
	ix := newIndex(files)
	out := Plan{Subjects: []Subject{}}
	for _, s := range old.Subjects {
		kept := Subject{Name: s.Name, Purpose: s.Purpose}
		paths := make([]string, 0, len(s.Files))
		for _, f := range s.Files {
			if _, ok := ix.byPath[f.Path]; ok {
				paths = append(paths, f.Path)
			}
		}
		ix.add(&kept, paths)
		if len(kept.Files) > 0 {
			out.Subjects = append(out.Subjects, kept)
		}
	}
	for _, p := range proposed {
		name := lang.Prose(p.Name)
		if name == "" {
			continue
		}
		if i := out.find(name); i >= 0 {
			ix.add(&out.Subjects[i], p.Paths)
			continue
		}
		// A new subject counts against the cap only once it holds a file:
		// one that names nothing readable must not take a later one's place.
		if len(out.Subjects) >= maxSubjects {
			continue
		}
		s := Subject{Name: name, Purpose: lang.Prose(p.Purpose)}
		ix.add(&s, p.Paths)
		if len(s.Files) > 0 {
			out.Subjects = append(out.Subjects, s)
		}
	}
	return out
}
