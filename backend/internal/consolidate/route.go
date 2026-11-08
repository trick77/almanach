package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

// Router is a catalogue model that can route: stage 1 of the filter stage,
// the files to read in full by subject, from every file's skeleton.
type Router interface {
	Route(ctx context.Context, prompt string) inference.RouteResult
}

// RouteDry is what the router picked for one repository at one commit,
// held to the plan's rules as a run would hold it, nothing written: the
// filter stage's dry run (user, 2026-11-08: the picks read by the user
// before any pilot).
type RouteDry struct {
	// Files is the tree's readable files; Plan the subjects and the files
	// each reads in full, anchors added, within StepMaxBytes.
	Files []baseline.File
	Plan  baseline.Plan
	// Docs is what the overview step reads whatever the router picks
	// (baseline.Docs): a run reads them, so a dry run counts them read.
	Docs []baseline.File
	// Questions is each subject's reader questions, by its name.
	Questions map[string][]string
	// OverBudget is the subjects a run would skip, past the run's bytes.
	OverBudget []string
	Call       inference.Call
	// ReadableBytes is the tree's; SkeletonBytes stage 0's; ListingBytes
	// what the router read of it.
	ReadableBytes, SkeletonBytes, ListingBytes int64
}

// errNoRouter is a catalogue model that cannot route.
var errNoRouter = errors.New("the catalogue model has no router")

// Route runs stages 0 and 1 of the filter stage over a repository at commit
// and returns the picks; it writes nothing and records no call. A failed
// call is no error: its outcome is on Call.
func (w *Worker) Route(ctx context.Context, key, slug, commit string) (RouteDry, error) {
	router, ok := w.base.Catalogue.(Router)
	if !ok {
		return RouteDry{}, errNoRouter
	}
	r := &store.BaselineRun{ProjectKey: key, Slug: slug, Commit: commit}
	tree, err := w.base.Code.Tree(ctx, key, slug, commit)
	if err != nil {
		return RouteDry{}, err
	}
	files, contracts, err := w.readable(ctx, r, tree)
	if err != nil {
		return RouteDry{}, err
	}
	var readable int64
	for _, f := range files {
		readable += f.Size
	}
	at, err := w.base.Code.CommitTime(ctx, key, slug, commit)
	if err != nil {
		return RouteDry{}, err
	}
	z, err := w.stageZeroOf(ctx, r, files, at)
	if err != nil {
		return RouteDry{}, err
	}
	listing := baseline.RouterListing(files, z, baseline.RouterMaxBytes)
	res := router.Route(ctx, renderRouter(*r, listing, len(files), readable, w.base.Limits.MaxBytes, w.maxSubjects()))
	out := RouteDry{Files: files, Docs: baseline.Docs(files, baseline.DocsMaxBytes), Call: res.Call, Questions: map[string][]string{},
		ReadableBytes: readable, SkeletonBytes: z.SkeletonBytes, ListingBytes: int64(len(listing))}
	if res.Outcome != inference.OutcomeOK {
		return out, nil
	}
	subjects := make([]inference.Subject, 0, len(res.Subjects))
	for _, s := range res.Subjects {
		subjects = append(subjects, s.Subject)
	}
	p := baseline.BuildPlan(subjects, files, w.maxSubjects())
	p = baseline.EnsureAnchors(p, files, contracts, w.maxSubjects())
	// A run skips a part that would pass the run's byte budget and reads
	// on (the docs read first): the dry run reads what a run would.
	var bytes int64
	for _, d := range out.Docs {
		bytes += d.Size
	}
	read := p
	read.Subjects = nil
	for _, s := range p.Subjects {
		if bytes+s.Bytes() > int64(w.base.Limits.MaxBytes) {
			out.OverBudget = append(out.OverBudget, s.Name)
			continue
		}
		bytes += s.Bytes()
		read.Subjects = append(read.Subjects, s)
	}
	out.Plan = read
	// The plan names a subject as stored (lang.Prose) and merges two the
	// same but for spelling: its questions are theirs, in their order, each
	// stored prose, at most RouteMaxQuestions; never those of a router
	// subject the plan dropped, none of its paths a file of the subject.
	for _, ps := range out.Plan.Subjects {
		var qs []string
		for _, s := range res.Subjects {
			if !lang.SameName(lang.Prose(s.Name), ps.Name) || !picksAny(s.Paths, ps.Files) {
				continue
			}
			for _, q := range s.Questions {
				if q = lang.Prose(q); q != "" && !slices.Contains(qs, q) && len(qs) < inference.RouteMaxQuestions {
					qs = append(qs, q)
				}
			}
		}
		if len(qs) > 0 {
			out.Questions[ps.Name] = qs
		}
	}
	return out, nil
}

// picksAny reports whether a router subject's paths name any of files: a
// file's path, its own directory ("dir/*", "*"), or one above it ("dir/").
func picksAny(paths []string, files []baseline.File) bool {
	for _, p := range paths {
		p = strings.TrimPrefix(strings.TrimSpace(p), "./")
		for _, f := range files {
			switch {
			case p == f.Path:
				return true
			case strings.HasSuffix(p, "*"):
				dir := strings.TrimSuffix(p, "*")
				if strings.HasPrefix(f.Path, dir) && !strings.Contains(f.Path[len(dir):], "/") {
					return true
				}
			case strings.HasSuffix(p, "/") && strings.HasPrefix(f.Path, p):
				return true
			}
		}
	}
	return false
}

// renderRouter is a router call's prompt. Budgets in kB of 1024 bytes, as
// the listing gives the sizes (baseline.RouterListing).
func renderRouter(r store.BaselineRun, listing string, files int, readable int64, runBytes, maxSubjects int) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{files}", fmt.Sprint(files), "{readable_kb}", fmt.Sprint(baseline.KiB(readable)),
		"{step_kb}", fmt.Sprint(baseline.StepMaxBytes/1024), "{run_kb}", fmt.Sprint(runBytes/1024),
		"{max_subjects}", fmt.Sprint(maxSubjects), "{listing}", listing,
	).Replace(prompts.Router)
}
