package evalrun

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/trick77/toolkit/llmwire"

	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/messaging"
	"github.com/trick77/almanach/internal/store"
)

// The variants: what planned a run's full runs.
const (
	VariantCatalogue = "catalogue"
	VariantRouter    = "router"
)

// Report is one eval run, every project scored.
type Report struct {
	Model        string    `json:"model"`
	Judge        string    `json:"judge"`
	Started      time.Time `json:"started"`
	PipelineTook string    `json:"pipeline_took"`
	Took         string    `json:"took"`
	Repeat       int       `json:"repeat"`
	// Variant is what planned the full runs: "router" (-router) or
	// "catalogue"; each repository says whether its run was routed.
	Variant string `json:"variant"`
	// Projects is the first run; Runs every run; Means each repository's
	// figures averaged over the runs.
	Projects []ProjectReport   `json:"projects"`
	Runs     [][]ProjectReport `json:"runs"`
	Means    []RepoMean        `json:"means"`
	// Totals over every repository.
	Calls        int     `json:"calls"`
	CostUSD      float64 `json:"cost_usd"`
	JudgeCostUSD float64 `json:"judge_cost_usd"`
	Unpriced     int     `json:"unpriced"`
	CutAnswers   int     `json:"cut_answers"`
}

// ProjectReport is one project: its repositories and its page.
type ProjectReport struct {
	Key         string       `json:"key"`
	TimedOut    bool         `json:"timed_out"`
	Error       string       `json:"error,omitempty"`
	ProjectPage *PageStats   `json:"project_page,omitempty"`
	Repos       []RepoReport `json:"repos"`
}

// PageStats is a page's size.
type PageStats struct {
	Statements int    `json:"statements"`
	Chars      int    `json:"chars"`
	Shape      string `json:"shape,omitempty"`
}

// RepoReport is one repository: what its run cost and read, how its page
// holds up against the labels.
type RepoReport struct {
	Slug     string `json:"slug"`
	RunState string `json:"run_state"`
	RunError string `json:"run_error,omitempty"`
	Took     string `json:"took,omitempty"`
	Calls    int    `json:"calls"`
	// Outcomes counts the calls by outcome (ok, timed_out, too_long, ...).
	Outcomes map[string]int `json:"outcomes"`
	CostUSD  float64        `json:"cost_usd"`
	Unpriced int            `json:"unpriced"`
	// Planned: the run got as far as its plan. Routed: that plan is the
	// router's; a router variant's planned run that fell back to the
	// catalogue (a tree too wide) says false.
	Planned    bool  `json:"planned,omitempty"`
	Routed     bool  `json:"routed,omitempty"`
	InputBytes int64 `json:"input_bytes"`
	ReadBytes  int64 `json:"read_bytes"`
	// ReadableBytes is what the tree held that a run could read.
	ReadableBytes int64      `json:"readable_bytes"`
	Page          *PageStats `json:"page,omitempty"`
	Labelled      bool       `json:"labelled"`
	// MustReadRecall is the share of the labels' must-read files the run
	// read; MustReadMissed those it did not.
	MustReadRecall float64  `json:"must_read_recall"`
	MustReadMissed []string `json:"must_read_missed,omitempty"`
	// CitedMustRead is the share of must-read files a statement rests on.
	CitedMustRead float64 `json:"cited_must_read"`
	// Noise is the files read that the labels call noise or generated, and
	// their bytes.
	NoiseFiles []string `json:"noise_files,omitempty"`
	NoiseBytes int64    `json:"noise_bytes"`
	// Coverage is the questions' score, 1 answered, 0.5 partly, 0 missing,
	// averaged; Answers per question.
	Coverage     float64  `json:"coverage"`
	Answers      []Answer `json:"answers,omitempty"`
	JudgeCostUSD float64  `json:"judge_cost_usd"`
	JudgeError   string   `json:"judge_error,omitempty"`
	// Rules is what the page breaks of the rules code can check.
	Rules []string `json:"rules,omitempty"`
	// TopicRecall is the share of the labels' topics the code-read
	// messaging names the same way, nil without topic labels; TopicsMissed
	// those it does not, TopicsExtra those it names beyond them.
	TopicRecall  *float64 `json:"topic_recall,omitempty"`
	TopicsMissed []string `json:"topics_missed,omitempty"`
	TopicsExtra  []string `json:"topics_extra,omitempty"`
}

// topicScore holds what the poller read of a repository's topics to its
// labels: a topic found the way the labels say, missed, or found beyond
// them.
func topicScore(rep *RepoReport, want []TopicLabel, got []messaging.Topic) {
	if len(want) == 0 {
		return
	}
	read := map[string]bool{}
	for _, t := range got {
		read[t.Name+" "+t.Direction] = true
	}
	labelled := map[string]bool{}
	hit := 0
	for _, w := range want {
		k := w.Name + " " + w.Direction
		labelled[k] = true
		if read[k] {
			hit++
		} else {
			rep.TopicsMissed = append(rep.TopicsMissed, k)
		}
	}
	for _, t := range got {
		if k := t.Name + " " + t.Direction; !labelled[k] {
			rep.TopicsExtra = append(rep.TopicsExtra, k)
		}
	}
	recall := float64(hit) / float64(len(want))
	rep.TopicRecall = &recall
}

// Answer is the judge's verdict on one question.
type Answer struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Verdict  string `json:"verdict"`
	Note     string `json:"note"`
}

func score(ctx context.Context, p Project, res ProjectResult, j *judge, log *slog.Logger) ProjectReport {
	pr := ProjectReport{Key: p.Key, TimedOut: res.TimedOut, Error: res.Error}
	if res.ProjectPage != nil {
		pr.ProjectPage = stats(res.ProjectPage)
	}
	for _, r := range p.Repos {
		rr := res.Repos[r.Slug]
		rep := RepoReport{Slug: r.Slug, RunState: rr.Run.State, RunError: rr.Run.Error, Outcomes: map[string]int{},
			InputBytes: rr.Run.Bytes, ReadBytes: rr.Run.ReadBytes, ReadableBytes: rr.Run.ReadableBytes}
		if rr.Run.StartedAt != nil && rr.Run.FinishedAt != nil {
			rep.Took = rr.Run.FinishedAt.Sub(*rr.Run.StartedAt).Round(time.Second).String()
		}
		var plan struct {
			Routed bool `json:"routed"`
		}
		if len(rr.Run.Plan) > 0 && json.Unmarshal(rr.Run.Plan, &plan) == nil {
			rep.Planned, rep.Routed = true, plan.Routed
		}
		for _, c := range rr.Calls {
			rep.Calls++
			rep.Outcomes[c.Outcome]++
			if c.CostNanoUSD == nil {
				rep.Unpriced++
			} else {
				rep.CostUSD += float64(*c.CostNanoUSD) / 1e9
			}
		}
		if rr.Page != nil {
			rep.Page = stats(rr.Page)
			rep.Rules = rules(rr.Page)
		}
		if l := r.Labels; l != nil {
			rep.Labelled = true
			files(&rep, l, rr)
			topicScore(&rep, l.Topics, rr.Topics)
			if rr.Page != nil && len(l.Questions) > 0 {
				ans, cost, err := j.ask(ctx, pageText(rr.Page), l.Questions)
				rep.JudgeCostUSD = cost.USD
				if cost.Unpriced {
					rep.Unpriced++
				}
				if err != nil {
					rep.JudgeError = err.Error()
					log.Warn("eval: judge failed", "repo", r.Slug, "err", err.Error())
				}
				rep.Answers = ans
				rep.Coverage = coverage(ans, len(l.Questions))
			}
		}
		pr.Repos = append(pr.Repos, rep)
	}
	return pr
}

func stats(p *store.Page) *PageStats {
	return &PageStats{Statements: len(p.Statements), Chars: len([]rune(pageText(p))), Shape: p.Shape}
}

// files scores what the run read against the labels.
func files(rep *RepoReport, l *Labels, rr RepoResult) {
	cited := map[string]bool{}
	if rr.Page != nil {
		for _, s := range rr.Page.Statements {
			for _, f := range s.Files {
				cited[f] = true
			}
		}
	}
	citedHit := 0
	for _, m := range l.MustRead {
		if cited[m.Path] {
			citedHit++
		}
	}
	if n := len(l.MustRead); n > 0 {
		rep.CitedMustRead = float64(citedHit) / float64(n)
	}
	s := scorePicked(rr.Picked, l)
	rep.MustReadRecall, rep.MustReadMissed, rep.NoiseFiles, rep.NoiseBytes = s.recall, s.missed, s.noise, s.noiseBytes
}

// pickedScore is what the labels say of the files a run read: the share
// of must-read files among them, those missed, and the noise read.
type pickedScore struct {
	recall     float64
	missed     []string
	noise      []string
	noiseBytes int64
}

// scorePicked scores read files (path → bytes) against labels: one rule for
// a full run and the router's dry run alike.
func scorePicked(picked map[string]int64, l *Labels) pickedScore {
	var s pickedScore
	hit := 0
	for _, m := range l.MustRead {
		if _, ok := picked[m.Path]; ok {
			hit++
		} else {
			s.missed = append(s.missed, m.Path)
		}
	}
	if n := len(l.MustRead); n > 0 {
		s.recall = float64(hit) / float64(n)
	}
	noise := newGlobSet(append(append([]Glob{}, l.NeverRead...), l.Generated...))
	for path, size := range picked {
		if noise.match(path) {
			s.noise = append(s.noise, path)
			s.noiseBytes += size
		}
	}
	sort.Strings(s.noise)
	return s
}

// advice is wording a page that documents never uses.
var advice = regexp.MustCompile(`(?i)\b(sollte|sollten|empfehl\w*|besser wäre|ratsam)\b`)

// rules is what code can check of a page: no advice, no Eszett.
func rules(p *store.Page) []string {
	text := pageText(p)
	var out []string
	if m := advice.FindAllString(text, -1); len(m) > 0 {
		out = append(out, fmt.Sprintf("advice wording %d× (%s)", len(m), strings.Join(uniq(m), ", ")))
	}
	if n := strings.Count(text, "ß"); n > 0 {
		out = append(out, fmt.Sprintf("Eszett %d×", n))
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	for _, s := range in {
		seen[strings.ToLower(s)] = true
	}
	return sortedKeys(seen)
}

// pageText is a page as a reader reads it: the summary, then every
// statement under its kind and name.
func pageText(p *store.Page) string {
	var b strings.Builder
	b.WriteString(markup.Plain(p.Summary))
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n\n[%s] %s\n%s", s.Kind, s.Name, markup.Plain(s.Text))
	}
	return b.String()
}

func coverage(ans []Answer, n int) float64 {
	if n == 0 {
		return 0
	}
	var sum float64
	for _, a := range ans {
		switch a.Verdict {
		case "answered":
			sum++
		case "partial":
			sum += 0.5
		}
	}
	return sum / float64(n)
}

// judge asks a model whether a page answers the labels' questions.
type judge struct {
	wire  *llmwire.Client
	model string
}

func newJudge(model string, lookup func(string) (string, bool), log *slog.Logger) (*judge, error) {
	wire, err := llmwire.FromEnv(model, llmwire.Config{CallTimeout: 5 * time.Minute, HeaderTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
		Logger: log, Lookup: lookup})
	if err != nil {
		return nil, fmt.Errorf("judge %s: %w", model, err)
	}
	return &judge{wire: wire, model: model}, nil
}

const judgePrompt = `You check a wiki page written about a software repository. For each question, decide from the PAGE ALONE whether a reader finds the answer there. The reference answer says what the code holds; the page need not use its words.

- "answered": the page states the substance of the reference answer.
- "partial": the page has some of it, or only vaguely.
- "missing": the page does not say it, or says something the reference contradicts.

Note in one short English sentence what the page has or lacks. Judge only the page; never use outside knowledge.`

// judgeCost is what a verdict was paid, or that it came unpriced.
type judgeCost struct {
	USD      float64
	Unpriced bool
}

// judgeCap is a verdict's output cap: a short note per question, and room
// for the judge's reasoning, which shares the wire cap.
func judgeCap(questions int) int {
	return 1000 + 300*questions + 16_000
}

func (j *judge) ask(ctx context.Context, page string, qs []Question) ([]Answer, judgeCost, error) {
	type q struct {
		ID        string `json:"id"`
		Question  string `json:"question"`
		Reference string `json:"reference_answer"`
	}
	in := make([]q, len(qs))
	for i, x := range qs {
		in[i] = q{ID: x.ID, Question: x.Q, Reference: x.Answer}
	}
	qj, err := json.Marshal(in)
	if err != nil {
		return nil, judgeCost{}, err
	}
	prompt := judgePrompt + "\n\nQUESTIONS:\n" + string(qj) + "\n\nPAGE:\n" + page
	format := llmwire.ResponseFormat{Kind: llmwire.FormatJSONSchema, Name: "eval_judge", Strict: true, Schema: map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"answers"},
		"properties": map[string]any{"answers": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []any{"id", "note", "verdict"},
			"properties": map[string]any{
				"id":      map[string]any{"type": "string"},
				"verdict": map[string]any{"type": "string", "enum": []any{"answered", "partial", "missing"}},
				"note":    map[string]any{"type": "string"},
			},
		}}},
	}}
	stream, _, err := j.wire.ChatStream(ctx, llmwire.ChatRequest{Model: j.model, Reasoning: llmwire.ReasoningBalanced(),
		Messages: []llmwire.Message{llmwire.User(prompt)}, ResponseFormat: format, MaxAnswerTokens: new(judgeCap(len(qs)))})
	if err != nil {
		return nil, judgeCost{}, err
	}
	defer func() { _ = stream.Close() }()
	res, err := stream.Collect(nil)
	cost := judgeCost{Unpriced: res.Usage.Cost.Provenance == llmwire.Unpriced}
	if !cost.Unpriced {
		cost.USD = float64(res.Usage.Cost.NanoUSD) / 1e9
	}
	if err != nil {
		return nil, cost, err
	}
	var out struct {
		Answers []Answer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		return nil, cost, fmt.Errorf("judge answer: %w", err)
	}
	return align(qs, out.Answers), cost, nil
}

// align gives one verdict per question, in the questions' order: by id, or
// by position when the judge renamed an id but kept the count; a question
// without a verdict is missing, a verdict for no question is dropped.
func align(qs []Question, got []Answer) []Answer {
	byID := map[string]Answer{}
	for _, a := range got {
		byID[a.ID] = a
	}
	known := map[string]bool{}
	for _, x := range qs {
		known[x.ID] = true
	}
	out := make([]Answer, len(qs))
	for i, x := range qs {
		a, ok := byID[x.ID]
		if !ok && len(got) == len(qs) && !known[got[i].ID] {
			a, ok = got[i], true
		}
		if !ok {
			a = Answer{Verdict: "missing", Note: "the judge gave no verdict"}
		}
		a.ID, a.Category = x.ID, x.Category
		out[i] = a
	}
	return out
}

// RepoMean is one repository's figures averaged over the runs, with each
// run's coverage beside the mean.
type RepoMean struct {
	Project        string    `json:"project"`
	Slug           string    `json:"slug"`
	Runs           int       `json:"runs"`
	Coverage       float64   `json:"coverage"`
	Coverages      []float64 `json:"coverages"`
	MustReadRecall float64   `json:"must_read_recall"`
	CitedMustRead  float64   `json:"cited_must_read"`
	NoiseBytes     int64     `json:"noise_bytes"`
	ReadBytes      int64     `json:"read_bytes"`
	Chars          int       `json:"chars"`
	CostUSD        float64   `json:"cost_usd"`
	Calls          float64   `json:"calls"`
}

// means averages every repository over the runs, in the first run's order.
func means(runs [][]ProjectReport) []RepoMean {
	if len(runs) == 0 {
		return nil
	}
	type key struct{ p, r string }
	var order []key
	sums := map[key]*RepoMean{}
	for k, run := range runs {
		for _, p := range run {
			for _, x := range p.Repos {
				id := key{p.Key, x.Slug}
				m := sums[id]
				if m == nil {
					if k > 0 {
						continue
					}
					m = &RepoMean{Project: p.Key, Slug: x.Slug}
					sums[id] = m
					order = append(order, id)
				}
				m.Runs++
				m.Coverages = append(m.Coverages, x.Coverage)
				m.Coverage += x.Coverage
				m.MustReadRecall += x.MustReadRecall
				m.CitedMustRead += x.CitedMustRead
				m.NoiseBytes += x.NoiseBytes
				m.ReadBytes += x.ReadBytes
				if x.Page != nil {
					m.Chars += x.Page.Chars
				}
				m.CostUSD += x.CostUSD
				m.Calls += float64(x.Calls)
			}
		}
	}
	out := make([]RepoMean, 0, len(order))
	for _, id := range order {
		m := sums[id]
		n := float64(m.Runs)
		m.Coverage /= n
		m.MustReadRecall /= n
		m.CitedMustRead /= n
		m.NoiseBytes /= int64(m.Runs)
		m.ReadBytes /= int64(m.Runs)
		m.Chars /= m.Runs
		m.CostUSD /= n
		m.Calls /= n
		out = append(out, *m)
	}
	return out
}

func (r *Report) total() {
	runs := r.Runs
	if len(runs) == 0 {
		runs = [][]ProjectReport{r.Projects}
	}
	for _, run := range runs {
		for _, p := range run {
			for _, x := range p.Repos {
				r.Calls += x.Calls
				r.CostUSD += x.CostUSD
				r.JudgeCostUSD += x.JudgeCostUSD
				r.Unpriced += x.Unpriced
				r.CutAnswers += x.Outcomes["timed_out"] + x.Outcomes["too_long"]
			}
		}
	}
}

// Markdown is the report for a reader.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval %s\n\nmodel %s, judge %s, planned by the %s; pipeline %s, total %s\n\n", r.Started.Format("2006-01-02 15:04"), r.Model, r.Judge,
		r.Variant, r.PipelineTook, r.Took)
	if r.Variant == VariantRouter {
		runs := r.Runs
		if len(runs) == 0 {
			runs = [][]ProjectReport{r.Projects}
		}
		var fell []string
		for k, run := range runs {
			for _, p := range run {
				for _, repo := range p.Repos {
					if repo.Planned && !repo.Routed {
						fell = append(fell, fmt.Sprintf("%s/%s (run %d)", p.Key, repo.Slug, k+1))
					}
				}
			}
		}
		if len(fell) > 0 {
			fmt.Fprintf(&b, "not routed, catalogued instead: %s\n\n", strings.Join(fell, ", "))
		}
	}
	fmt.Fprintf(&b, "calls %d, cost $%.3f (+ judge $%.3f), unpriced %d, cut answers %d\n\n", r.Calls, r.CostUSD, r.JudgeCostUSD, r.Unpriced, r.CutAnswers)
	if r.Repeat > 1 {
		fmt.Fprintf(&b, "## Means over %d runs\n\n| project | repo | coverage (runs) | must-read read | must-read cited | noise kB | read kB | page chars | calls | cost |\n|---|---|---|---|---|---|---|---|---|---|\n", r.Repeat)
		for _, m := range r.Means {
			runs := make([]string, len(m.Coverages))
			for i, c := range m.Coverages {
				runs[i] = fmt.Sprintf("%.0f", 100*c)
			}
			fmt.Fprintf(&b, "| %s | %s | %.0f%% (%s) | %.0f%% | %.0f%% | %d | %d | %d | %.1f | $%.3f |\n", m.Project, m.Slug, 100*m.Coverage, strings.Join(runs, ", "),
				100*m.MustReadRecall, 100*m.CitedMustRead, m.NoiseBytes/1000, m.ReadBytes/1000, m.Chars, m.Calls, m.CostUSD)
		}
		b.WriteString("\n")
	}
	runs := r.Runs
	if len(runs) == 0 {
		runs = [][]ProjectReport{r.Projects}
	}
	for k, run := range runs {
		if len(runs) > 1 {
			fmt.Fprintf(&b, "# Run %d\n\n", k+1)
		}
		markdownRun(&b, run)
	}
	return b.String()
}

func markdownRun(b *strings.Builder, run []ProjectReport) {
	for _, p := range run {
		fmt.Fprintf(b, "## %s", p.Key)
		if p.TimedOut {
			b.WriteString(" (deadline reached)")
		}
		if p.Error != "" {
			fmt.Fprintf(b, " error: %s", p.Error)
		}
		b.WriteString("\n\n")
		if p.ProjectPage != nil {
			fmt.Fprintf(b, "project page: %d statements, %d chars, shape %s\n\n", p.ProjectPage.Statements, p.ProjectPage.Chars, p.ProjectPage.Shape)
		}
		b.WriteString("| repo | run | took | calls | cost | read kB | statements | must-read read | must-read cited | noise kB | coverage | rules |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, x := range p.Repos {
			st, ch := 0, 0
			if x.Page != nil {
				st, ch = x.Page.Statements, x.Page.Chars
			}
			fmt.Fprintf(b, "| %s | %s | %s | %d | $%.3f | %d | %d (%d chars) | %.0f%% | %.0f%% | %d | %.0f%% | %s |\n", x.Slug, x.RunState, x.Took, x.Calls, x.CostUSD,
				x.ReadBytes/1000, st, ch, 100*x.MustReadRecall, 100*x.CitedMustRead, x.NoiseBytes/1000, 100*x.Coverage, strings.Join(x.Rules, "; "))
		}
		b.WriteString("\n")
		for _, x := range p.Repos {
			if !x.Labelled {
				continue
			}
			fmt.Fprintf(b, "### %s\n\n", x.Slug)
			if x.RunError != "" {
				fmt.Fprintf(b, "run error: %s\n\n", x.RunError)
			}
			if x.JudgeError != "" {
				fmt.Fprintf(b, "judge error: %s\n\n", x.JudgeError)
			}
			for _, a := range x.Answers {
				fmt.Fprintf(b, "- %s (%s): **%s**, %s\n", a.ID, a.Category, a.Verdict, a.Note)
			}
			if len(x.MustReadMissed) > 0 {
				fmt.Fprintf(b, "\nmust-read not read: %s\n", strings.Join(x.MustReadMissed, ", "))
			}
			if len(x.NoiseFiles) > 0 {
				shown := x.NoiseFiles
				if len(shown) > 20 {
					shown = shown[:20]
				}
				fmt.Fprintf(b, "\nnoise read (%d files): %s\n", len(x.NoiseFiles), strings.Join(shown, ", "))
			}
			if x.TopicRecall != nil {
				fmt.Fprintf(b, "\ntopics found: %.0f%%", 100**x.TopicRecall)
				if len(x.TopicsMissed) > 0 {
					fmt.Fprintf(b, "; missed: %s", strings.Join(x.TopicsMissed, ", "))
				}
				if len(x.TopicsExtra) > 0 {
					fmt.Fprintf(b, "; beyond the labels: %s", strings.Join(x.TopicsExtra, ", "))
				}
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}
}
