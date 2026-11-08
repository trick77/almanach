package evalrun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/inference"
)

// RouteReport is a router dry run: stages 0 and 1 of the filter stage over
// every repository of the folder, no page written, no describe call made.
// What the user reads before any pilot (user, 2026-11-08).
type RouteReport struct {
	Model   string      `json:"model"`
	Started time.Time   `json:"started"`
	Took    string      `json:"took"`
	Repos   []RouteRepo `json:"repos"`
	// CostUSD is the router calls' list price, those priced.
	CostUSD  float64 `json:"cost_usd"`
	Unpriced int     `json:"unpriced"`
}

// RouteRepo is one repository's picks.
type RouteRepo struct {
	Project string `json:"project"`
	Repo    string `json:"repo"`
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	// The tree, what stage 0 made of it, what the router read of that.
	Files        int            `json:"files"`
	ReadableKB   int64          `json:"readable_kb"`
	SkeletonKB   int64          `json:"skeleton_kb"`
	ListingKB    int64          `json:"listing_kb"`
	Subjects     []RouteSubject `json:"subjects"`
	PickedFiles  int            `json:"picked_files"`
	PickedKB     int64          `json:"picked_kb"`
	InputTokens  int            `json:"input_tokens"`
	CachedTokens int            `json:"cached_tokens"`
	OutputTokens int            `json:"output_tokens"`
	CostUSD      *float64       `json:"cost_usd,omitempty"`
	Billed       bool           `json:"billed"`
	// OverBudget is the subjects a run would skip past its bytes.
	OverBudget []string `json:"over_budget,omitempty"`
	// Scored against the labels, when the repository has them (Labelled):
	// a 0 of an unlabelled one is no score.
	Labelled       bool     `json:"labelled"`
	MustReadRecall float64  `json:"must_read_recall"`
	MustReadMissed []string `json:"must_read_missed,omitempty"`
	// Of the missed: a label naming no file at the commit; a file not
	// readable (left out before the router: a test, generated, binary); one
	// the router picked and the plan dropped (budget, cap); one it saw only
	// in a folded line. The rest it saw by name and passed over.
	MustReadNotInTree  []string `json:"must_read_not_in_tree,omitempty"`
	MustReadUnreadable []string `json:"must_read_unreadable,omitempty"`
	MustReadDropped    []string `json:"must_read_dropped,omitempty"`
	MustReadFolded     []string `json:"must_read_folded,omitempty"`
	NoiseFiles         []string `json:"noise_files,omitempty"`
	NoiseKB            int64    `json:"noise_kb"`
}

// RouteSubject is one subject the router made, as the plan holds it.
type RouteSubject struct {
	Name      string   `json:"name"`
	Purpose   string   `json:"purpose"`
	Files     []string `json:"files"`
	KB        int64    `json:"kb"`
	Questions []string `json:"questions,omitempty"`
}

// routeParallel is how many repositories the dry run routes at once: a
// clone and a paid call each, kept below a gateway's rate limit.
const routeParallel = 4

// RouteOnly is the router's dry run over the folder: each repository laid
// down in a database of its own, routeParallel at a time, one router call
// each, within opt.Deadline. Repeat and Split do not apply: every
// repository is its own, once.
func RouteOnly(ctx context.Context, opt Options) (*RouteReport, error) {
	e, err := setup(ctx, opt)
	if err != nil {
		return nil, err
	}
	if opt.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.Deadline)
		defer cancel()
	}
	start := time.Now()
	rep := &RouteReport{Model: opt.Model, Started: start}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, routeParallel)
	for _, p := range e.projects {
		for _, r := range p.Repos {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				rr := routeRepo(ctx, Project{Key: p.Key, Repos: []Repo{r}}, r, filepath.Join(opt.Out, p.Key, r.Slug), e, opt)
				mu.Lock()
				defer mu.Unlock()
				rep.Repos = append(rep.Repos, rr)
			}()
		}
	}
	wg.Wait()
	sort.Slice(rep.Repos, func(i, j int) bool {
		return rep.Repos[i].Project+"/"+rep.Repos[i].Repo < rep.Repos[j].Project+"/"+rep.Repos[j].Repo
	})
	for _, r := range rep.Repos {
		if r.CostUSD != nil {
			rep.CostUSD += *r.CostUSD
		} else if r.Billed {
			rep.Unpriced++
		}
	}
	rep.Took = time.Since(start).Round(time.Second).String()
	return rep, nil
}

// routeRepo is one repository's dry run.
func routeRepo(ctx context.Context, p Project, r Repo, out string, e *env, opt Options) RouteRepo {
	rr := RouteRepo{Project: p.Key, Repo: r.Slug}
	pr, err := prepare(ctx, p, out, e.gitBin, opt.Log)
	if err != nil {
		rr.Error = err.Error()
		return rr
	}
	defer pr.db.Close()
	dry, err := pr.worker(e.app, e.llm, opt.Log).Route(ctx, p.Key, r.Slug, pr.heads[r.Slug])
	if err != nil {
		rr.Error = err.Error()
		return rr
	}
	// The listing is for a human reading the picks: a failed write is
	// logged, the route's outcome stays the call's.
	if err := os.WriteFile(filepath.Join(out, "listing.txt"), []byte(dry.Listing), 0o600); err != nil {
		opt.Log.Warn("the router's listing not written", "repo", r.Slug, "err", err)
	}
	return routeReport(rr, dry, r.Labels)
}

// routeReport is a dry run's picks, scored against labels when there are any.
func routeReport(rr RouteRepo, dry consolidate.RouteDry, l *Labels) RouteRepo {
	rr.Outcome = dry.Call.Outcome.String()
	rr.Files = len(dry.Files)
	rr.ReadableKB, rr.SkeletonKB, rr.ListingKB = kib(dry.ReadableBytes), kib(dry.SkeletonBytes), kib(int64(len(dry.Listing)))
	rr.OverBudget = dry.OverBudget
	cost := dry.Call.Cost
	// Billed: an answer began to arrive or one came (store's rule), so a
	// call without a price is unknown spend, never one that cost nothing.
	rr.Billed = cost.Streamed || dry.Call.Outcome == inference.OutcomeOK
	rr.InputTokens, rr.CachedTokens, rr.OutputTokens = int(cost.InputTokens), int(cost.CachedTokens), int(cost.OutputTokens)
	if cost.NanoUSD != nil {
		usd := float64(*cost.NanoUSD) / 1e9
		rr.CostUSD = &usd
	}
	picked := map[string]int64{}
	for _, s := range dry.Plan.Subjects {
		rs := RouteSubject{Name: s.Name, Purpose: s.Purpose, Questions: dry.Questions[s.Name]}
		for _, f := range s.Files {
			rs.Files = append(rs.Files, f.Path)
			rs.KB += f.Size
			picked[f.Path] = f.Size
		}
		rs.KB = kib(rs.KB)
		rr.Subjects = append(rr.Subjects, rs)
	}
	// The overview's docs are read whatever the router picks, as a run's
	// Picked holds them: the same yardstick as today's pipeline.
	for _, d := range dry.Docs {
		picked[d.Path] = d.Size
	}
	rr.PickedFiles = len(picked)
	var bytes int64
	for _, n := range picked {
		bytes += n
	}
	rr.PickedKB = kib(bytes)
	if l == nil {
		return rr
	}
	s := scorePicked(picked, l)
	rr.Labelled = true
	rr.MustReadRecall, rr.MustReadMissed, rr.NoiseFiles, rr.NoiseKB = s.recall, s.missed, s.noise, kib(s.noiseBytes)
	readable := make(map[string]bool, len(dry.Files))
	for _, f := range dry.Files {
		readable[f.Path] = true
	}
	for _, m := range s.missed {
		switch {
		case !dry.Tree[m]:
			rr.MustReadNotInTree = append(rr.MustReadNotInTree, m)
		case !readable[m]:
			rr.MustReadUnreadable = append(rr.MustReadUnreadable, m)
		case dry.Dropped[m]:
			rr.MustReadDropped = append(rr.MustReadDropped, m)
		case dry.Folded[m]:
			rr.MustReadFolded = append(rr.MustReadFolded, m)
		}
	}
	return rr
}

func kib(n int64) int64 { return baseline.KiB(n) }

// Markdown is the dry run for a human: per repository its numbers, then
// each subject with its files and questions.
func (r *RouteReport) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Router dry run\n\n%s, %s, $%.2f", r.Model, r.Took, r.CostUSD)
	if r.Unpriced > 0 {
		fmt.Fprintf(&b, " (%d unpriced)", r.Unpriced)
	}
	b.WriteString("\n\n| repo | files | readable kB | skeleton kB | listing kB | picked kB | picked files | must-read | noise kB | outcome |\n|---|---|---|---|---|---|---|---|---|---|\n")
	cell := strings.NewReplacer("|", `\|`, "\n", " ", "\r", " ")
	for _, x := range r.Repos {
		recall := "–"
		if x.Labelled {
			recall = fmt.Sprintf("%.0f%%", x.MustReadRecall*100)
		}
		fmt.Fprintf(&b, "| %s/%s | %d | %d | %d | %d | %d | %d | %s | %d | %s |\n", x.Project, x.Repo, x.Files, x.ReadableKB, x.SkeletonKB, x.ListingKB,
			x.PickedKB, x.PickedFiles, recall, x.NoiseKB, cell.Replace(strings.TrimSpace(x.Outcome+" "+x.Error)))
	}
	for _, x := range r.Repos {
		fmt.Fprintf(&b, "\n## %s/%s\n", x.Project, x.Repo)
		if len(x.OverBudget) > 0 {
			fmt.Fprintf(&b, "\nPast the run's bytes, a run would skip: %s\n", strings.Join(x.OverBudget, ", "))
		}
		if len(x.MustReadMissed) > 0 {
			fmt.Fprintf(&b, "\nMust-read missed: %s\n", strings.Join(x.MustReadMissed, ", "))
		}
		for _, of := range []struct {
			why   string
			paths []string
		}{
			{"no file at the commit (the label)", x.MustReadNotInTree},
			{"not readable (left out before the router)", x.MustReadUnreadable},
			{"picked by the router, dropped by the plan (budget, cap)", x.MustReadDropped},
			{"only in a folded line", x.MustReadFolded},
		} {
			if len(of.paths) > 0 {
				fmt.Fprintf(&b, "\nOf those, %s: %s\n", of.why, strings.Join(of.paths, ", "))
			}
		}
		if len(x.NoiseFiles) > 0 {
			fmt.Fprintf(&b, "\nNoise picked: %s\n", strings.Join(x.NoiseFiles, ", "))
		}
		for _, s := range x.Subjects {
			fmt.Fprintf(&b, "\n### %s (%d kB)\n\n%s\n\n", s.Name, s.KB, s.Purpose)
			for _, f := range s.Files {
				fmt.Fprintf(&b, "- `%s`\n", f)
			}
			if len(s.Questions) > 0 {
				b.WriteString("\nQuestions:\n")
				for _, q := range s.Questions {
					fmt.Fprintf(&b, "- %s\n", q)
				}
			}
		}
	}
	return b.String()
}
