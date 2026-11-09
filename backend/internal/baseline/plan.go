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
	// Joined is the names of the proposed subjects folded into this one to
	// keep a plan within its cap (compact), its own first.
	Joined []string `json:"joined,omitempty"`
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
	// SkipTokens is when the part's prompt would take the run over its
	// input token budget.
	SkipTokens Skip = "tokens"
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

// Held is every path a subject holds.
func (p Plan) Held() map[string]bool {
	held := map[string]bool{}
	for _, s := range p.Subjects {
		for _, f := range s.Files {
			held[f.Path] = true
		}
	}
	return held
}

// find is the index of the subject with this name, or one joined into it
// (Joined: a proposal naming a part adds to the subject that stands for
// it), ignoring case, umlauts written out ("Datenueberpruefung" is
// "Datenüberprüfung") and a software word stored in German
// (lang.SameName); -1 when none.
func (p Plan) find(name string) int {
	for i, s := range p.Subjects {
		if lang.SameName(s.Name, name) {
			return i
		}
		for _, j := range s.Joined {
			if lang.SameName(j, name) {
				return i
			}
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
	files = inPathOrder(files)
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
	ix := index{byPath: make(map[string]File, len(files)), sorted: inPathOrder(files)}
	for _, f := range files {
		ix.byPath[f.Path] = f
	}
	return ix
}

// inPathOrder is files in path order: themselves when they are, else a
// sorted copy, the caller's slice untouched.
func inPathOrder(files []File) []File {
	byPath := func(i, j int) bool { return files[i].Path < files[j].Path }
	if sort.SliceIsSorted(files, byPath) {
		return files
	}
	out := append([]File(nil), files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// resolve turns one path the model named into readable files: the file
// itself, or every readable file under it when it names a directory (with
// or without the trailing slash), or only the files right under it when it
// is the listing's `dir/*` (the root's `*`, `./*` or `/*`); space, a
// leading "./" or "/" and a trailing "/" forgiven (normPath). A path
// naming nothing so, but one or more leading folders short of what it
// names, resolves there where only one place fits (rooted): a model once
// dropped `backend/` from every path of five subjects, which then read
// nothing. A path naming nothing readable either way resolves to nothing:
// the model inventing one, or one the filter refused.
func (ix index) resolve(p string) []File {
	p = normPath(p)
	if out := ix.exact(p); len(out) > 0 {
		return out
	}
	if root, ok := ix.rooted(p); ok {
		return ix.exact(root + p)
	}
	return nil
}

// rooted is the one leading folder path that, put before p (normPath's),
// makes it name readable files; ok false where none or several do. Only a
// path of folders is rooted, never a bare name (main.go, config): one
// folder of many could hold it. For `dir/*`, only a root whose dir holds
// files right in it counts, as only those resolve.
func (ix index) rooted(p string) (string, bool) {
	direct := strings.HasSuffix(p, "*")
	dir := strings.TrimSuffix(strings.TrimSuffix(p, "*"), "/")
	if !strings.Contains(dir, "/") {
		return "", false
	}
	dir += "/"
	roots := map[string]bool{}
	for _, f := range ix.sorted {
		at := -1
		if i := strings.Index(f.Path, "/"+dir); i >= 0 && (!direct || !strings.Contains(f.Path[i+1+len(dir):], "/")) {
			at = i + 1
		} else if !direct && strings.HasSuffix(f.Path, "/"+p) {
			at = len(f.Path) - len(p)
		}
		if at > 0 {
			roots[f.Path[:at]] = true
			if len(roots) > 1 {
				return "", false
			}
		}
	}
	for r := range roots {
		return r, true
	}
	return "", false
}

// Resolver resolves a model's paths to readable files as a plan does
// (index.resolve), for a caller that must judge them alike: the router's
// dry run telling a pick dropped from one never resolved.
type Resolver struct{ ix index }

// NewResolver is a Resolver over files.
func NewResolver(files []File) Resolver { return Resolver{ix: newIndex(files)} }

// Resolve is the readable files p names, as BuildPlan reads it.
func (r Resolver) Resolve(p string) []File { return r.ix.resolve(p) }

// exact is resolve without rooting.
func (ix index) exact(p string) []File {
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
	// A folder's anchors first: in path order a step's budget went to
	// whatever sorted early, the files every page rests on to none.
	byAnchor(out, nil)
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
// subjects of one name merged, at most maxSubjects in the model's order,
// the smallest joined before any is dropped (compact).
func BuildPlan(proposed []inference.Subject, files []File, maxSubjects int) Plan {
	return MergePlan(Plan{}, proposed, files, maxSubjects)
}

// MergePlan is BuildPlan onto an existing plan, a reconcile's catalogue
// step: the old subjects keep their files, re-sized from the tree and held
// to StepMaxBytes again in their order (files grow), those gone or no
// longer readable dropped; a proposal naming an old subject adds its files
// to it, a new name opens a new subject; past maxSubjects the new subjects
// are joined, the smallest first (compact), never an old one.
func MergePlan(old Plan, proposed []inference.Subject, files []File, maxSubjects int) Plan {
	ix := newIndex(files)
	out := Plan{Subjects: []Subject{}}
	for _, s := range old.Subjects {
		// A name and purpose stored before the word rule come out English,
		// as the catalogue writes them today (lang.Terms).
		kept := Subject{Name: lang.Terms(s.Name), Purpose: lang.Terms(s.Purpose)}
		for _, j := range s.Joined {
			kept.Joined = append(kept.Joined, lang.Terms(j))
		}
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
	olds := len(out.Subjects)
	for _, p := range proposed {
		name := lang.Prose(p.Name)
		if name == "" {
			continue
		}
		if i := out.find(name); i >= 0 {
			ix.add(&out.Subjects[i], p.Paths)
			continue
		}
		// A new subject counts only once it holds a file: one that names
		// nothing readable takes no later one's place.
		s := Subject{Name: name, Purpose: lang.Prose(p.Purpose)}
		ix.add(&s, p.Paths)
		if len(s.Files) > 0 {
			out.Subjects = append(out.Subjects, s)
		}
	}
	out.Subjects = compact(out.Subjects, olds, maxSubjects)
	return out
}

// compact holds the new subjects (those from olds on) to maxSubjects all
// told; the old ones all stay, whatever the cap (a reconcile's plan holds
// its old subjects and the cap may have been lowered since). A model asked
// for at most so many answers more at times, and cutting the last dropped
// whatever only they named, a whole data model once. So the two smallest
// new subjects join while their files fit one step (joinTo); only what no
// join brings under the cap is dropped (dropTo). An old subject never
// joins: its name is a step's, a page's section. The build subject
// (devSubject) joins only where no two others fit one step: few files by
// the router's rule, it was the smallest and went into whatever feature
// stood next to it; but joined it loses no file, dropped a subject loses
// all.
func compact(subjects []Subject, olds, maxSubjects int) []Subject {
	limit := max(maxSubjects, olds, 0)
	subjects, ok := joinTo(subjects, olds, limit, false)
	if !ok {
		subjects, _ = joinTo(subjects, olds, limit, true)
	}
	return dropTo(subjects, olds, limit)
}

// dropTo drops subjects from olds on until at most n stand: the last of
// the model's order first, the one sign of what matters it gives, the
// build subject only once no other is left to go (the router puts it last
// by rule, not by worth).
func dropTo(subjects []Subject, olds, n int) []Subject {
	subjects = append([]Subject{}, subjects...)
	for len(subjects) > max(n, 0) && len(subjects) > olds {
		victim := len(subjects) - 1
		for i := len(subjects) - 1; i >= olds; i-- {
			if !devSubject(subjects[i]) {
				victim = i
				break
			}
		}
		subjects = append(subjects[:victim:victim], subjects[victim+1:]...)
	}
	return subjects
}

// joinTo joins the two smallest subjects from olds on, while their files
// (each once: subjects may share some) fit one step, until at most n
// stand; ok is whether it got there. The larger keeps its place; a joined
// name another subject has already is numbered. The build subject joins
// only with withBuild, a join taking it is one too.
func joinTo(subjects []Subject, olds, n int, withBuild bool) ([]Subject, bool) {
	subjects = append([]Subject{}, subjects...)
	size := make([]int64, len(subjects))
	dev := make([]bool, len(subjects))
	for i, s := range subjects {
		size[i], dev[i] = s.Bytes(), devSubject(s)
	}
	for len(subjects) > max(n, 0) {
		a, b, best := -1, -1, int64(0)
		for i := olds; i < len(subjects); i++ {
			if dev[i] && !withBuild {
				continue
			}
			for j := i + 1; j < len(subjects); j++ {
				if dev[j] && !withBuild {
					continue
				}
				sum := size[i] + size[j]
				if a >= 0 && sum >= best {
					continue
				}
				if sum > StepMaxBytes {
					// Shared files count once: the join may fit still.
					if sum = unionBytes(subjects[i], subjects[j]); sum > StepMaxBytes || (a >= 0 && sum >= best) {
						continue
					}
				}
				a, b, best = i, j, sum
			}
		}
		if a < 0 {
			return subjects, false
		}
		if size[a] < size[b] {
			a, b = b, a
		}
		joined := join(subjects[a], subjects[b])
		for base, k := joined.Name, 2; nameTaken(subjects, joined.Name, a, b); k++ {
			joined.Name = fmt.Sprintf("%s (%d)", base, k)
		}
		subjects[a], size[a], dev[a] = joined, joined.Bytes(), dev[a] || dev[b]
		subjects = append(subjects[:b:b], subjects[b+1:]...)
		size = append(size[:b:b], size[b+1:]...)
		dev = append(dev[:b:b], dev[b+1:]...)
	}
	return subjects, true
}

// devSubject reports whether s is the router's build and deploy subject
// (LastArea), by name: the router's answer marks it by nothing else, and a
// flag on the stored plan would only copy the name. A subject it was joined
// into ("Export und Entwicklung und Deployment") is one too, by a part's
// name (Joined): it holds the build files still.
func devSubject(s Subject) bool {
	return slices.ContainsFunc(s.Names(), func(n string) bool { return CleanArea(n) == LastArea })
}

// nameTaken reports whether a subject other than a and b has name, or a
// part joined into it (Plan.find's rule).
func nameTaken(subjects []Subject, name string, a, b int) bool {
	for i, s := range subjects {
		if i == a || i == b {
			continue
		}
		if (Plan{Subjects: []Subject{s}}).find(name) >= 0 {
			return true
		}
	}
	return false
}

// unionBytes is the bytes of two subjects' files, a file both hold once.
func unionBytes(a, b Subject) int64 {
	n := a.Bytes()
	have := make(map[string]bool, len(a.Files))
	for _, f := range a.Files {
		have[f.Path] = true
	}
	for _, f := range b.Files {
		if !have[f.Path] {
			n += f.Size
		}
	}
	return n
}

// JoinedShown is how many of a joined subject's parts its name shows, the
// rest "und weitere", the name a heading still; and how many parts' reader
// questions its step is given.
const JoinedShown = 3

// join is subject b folded into a: a's files then b's not in a yet, the
// parts' names and purposes joined, each purpose a sentence of its own.
func join(a, b Subject) Subject {
	files := append([]File{}, a.Files...)
	have := map[string]bool{}
	for _, f := range a.Files {
		have[f.Path] = true
	}
	for _, f := range b.Files {
		if !have[f.Path] {
			files = append(files, f)
		}
	}
	parts := append(append([]string{}, a.Names()...), b.Names()...)
	return Subject{
		Name:    joinedName(parts),
		Purpose: strings.TrimSpace(sentence(a.Purpose) + " " + sentence(b.Purpose)),
		Files:   files,
		Joined:  parts,
	}
}

// joinedName is a joined subject's heading: "A und B", "A, B und C", past
// JoinedShown "A, B, C und weitere".
func joinedName(parts []string) string {
	switch {
	case len(parts) > JoinedShown:
		return strings.Join(parts[:JoinedShown], ", ") + " und weitere"
	case len(parts) > 1:
		return strings.Join(parts[:len(parts)-1], ", ") + " und " + parts[len(parts)-1]
	}
	return strings.Join(parts, "")
}

// sentence is a purpose ending as a sentence, so two joined read as two.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// Names is the proposed names a subject stands for: those joined into it,
// else its own.
func (s Subject) Names() []string {
	if len(s.Joined) > 0 {
		return s.Joined
	}
	return []string{s.Name}
}
