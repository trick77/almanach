package baseline

import (
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
// Skipped names the subjects the run could not fold (a ceiling, a failed
// call), so the next reconcile reads them again; Why is each one's Skip
// code, the first that hit it. A run from before Why has names alone.
// Steps is the overview and the project step when the run skipped them,
// by Part, never by a name: a subject's name is the model's, any string.
type Plan struct {
	// Format is PlanFormat on every plan the worker writes; 0 is a plan
	// from before Steps, which Upgrade reads.
	Format   int             `json:"format,omitempty"`
	Subjects []Subject       `json:"subjects"`
	Skipped  []string        `json:"skipped,omitempty"`
	Why      map[string]Skip `json:"why,omitempty"`
	Steps    map[Part]Skip   `json:"steps,omitempty"`
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
	// SkipCeiling is when the page was at a ceiling before the call.
	SkipCeiling Skip = "ceiling"
	// SkipFailed is when the model call failed.
	SkipFailed Skip = "failed"
	// SkipRefused is when the answer broke a fold rule and was thrown away.
	SkipRefused Skip = "refused"
	// SkipOverCeiling is when the rewrite would take the page over a ceiling.
	SkipOverCeiling Skip = "over_ceiling"
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
// with its size, or, over ListMaxEntries, the files at the root and the
// directories two levels deep with their file counts and bytes. A
// directory is named with a trailing slash, the way the model may name it
// back.
func Listing(files []File) string {
	var b strings.Builder
	if len(files) <= ListMaxEntries {
		for _, f := range files {
			fmt.Fprintf(&b, "%s\t%d\n", f.Path, f.Size)
		}
		return b.String()
	}
	type dir struct {
		files int
		bytes int64
	}
	dirs := map[string]*dir{}
	var keys []string
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		if len(parts) == 1 {
			fmt.Fprintf(&b, "%s\t%d\n", f.Path, f.Size)
			continue
		}
		key := parts[0] + "/"
		if len(parts) > 2 {
			key = parts[0] + "/" + parts[1] + "/"
		}
		d := dirs[key]
		if d == nil {
			d = &dir{}
			dirs[key] = d
			keys = append(keys, key)
		}
		d.files++
		d.bytes += f.Size
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%d files\t%d bytes\n", k, dirs[k].files, dirs[k].bytes)
	}
	return b.String()
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
// or without the trailing slash). A path naming nothing readable resolves
// to nothing: the model inventing one, or one the filter refused.
func (ix index) resolve(p string) []File {
	p = strings.TrimSpace(p)
	if f, ok := ix.byPath[p]; ok {
		return []File{f}
	}
	prefix := strings.TrimSuffix(p, "/") + "/"
	if prefix == "/" {
		return nil
	}
	start := sort.Search(len(ix.sorted), func(i int) bool { return ix.sorted[i].Path >= prefix })
	var out []File
	for _, f := range ix.sorted[start:] {
		if !strings.HasPrefix(f.Path, prefix) {
			break
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
		name := strings.TrimSpace(p.Name)
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
		s := Subject{Name: name, Purpose: strings.TrimSpace(p.Purpose)}
		ix.add(&s, p.Paths)
		if len(s.Files) > 0 {
			out.Subjects = append(out.Subjects, s)
		}
	}
	return out
}
