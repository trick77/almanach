package consolidate

import (
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// A run's steps as the queue page lists them: the catalogue, the overview
// and the subjects in reading order, the project step, the repository map.
// Read from the stored plan and the run's calls; the plan's shape is this
// package's, so the list is built here.

// Kinds of step.
const (
	StepCatalogue = "catalogue"
	StepOverview  = "overview"
	StepSubject   = "subject"
	StepProject   = "project"
	StepRepoMap   = "repo_map"
)

// States of a step.
const (
	StepDone    = "done"
	StepSkipped = "skipped"
	StepCurrent = "current"
	StepTodo    = "todo"
)

// RunStep is one step of a run.
type RunStep struct {
	Kind string
	// Name is a subject's, "" otherwise.
	Name string
	// Files and Bytes are what a subject reads; zero otherwise.
	Files int
	Bytes int64
	State string
	// Skip is why a skipped step was left out.
	Skip baseline.Skip
	// Calls is the model calls the step made, Failed those that did not
	// answer; At the last one's time, nil before any.
	Calls  int
	Failed int
	At     *time.Time
	// CostNanoUSD is what the step's calls cost, nil when none was priced.
	CostNanoUSD *int64
}

// RunSteps lists a run's steps: an open run's, or one that ended while
// the queue page read it. A queued run has no plan yet: its subjects are
// unknown until the catalogue, so it lists the catalogue, the project and
// the map alone.
func RunSteps(r store.BaselineRun, calls []store.RunCall) ([]RunStep, error) {
	plan, err := decodePlan(r.Plan)
	if err != nil {
		return nil, err
	}
	var steps []RunStep
	steps = append(steps, RunStep{Kind: StepCatalogue})
	if plan.Overview {
		steps = append(steps, RunStep{Kind: StepOverview})
	}
	for _, name := range plan.Read {
		s := RunStep{Kind: StepSubject, Name: name}
		if sub, ok := subjectNamed(plan.Plan, name); ok {
			s.Files, s.Bytes = len(sub.Files), sub.Bytes()
		}
		steps = append(steps, s)
	}
	steps = append(steps, RunStep{Kind: StepProject}, RunStep{Kind: StepRepoMap})
	reading := plan.steps()
	project, repoMap := len(steps)-2, len(steps)-1

	// States.
	for i := range steps {
		steps[i].State = StepTodo
	}
	done := func(i int) { steps[i].State = StepDone }
	switch r.State {
	case store.RunCataloguing:
		steps[0].State = StepCurrent
	case store.RunDescribing:
		done(0)
		for i := 0; i < reading; i++ {
			switch {
			case i < r.Step:
				done(1 + i)
			case i == r.Step:
				steps[1+i].State = StepCurrent
			}
		}
	case store.RunProject:
		for i := 0; i <= reading; i++ {
			done(i)
		}
		if plan.ProjectDone {
			done(project)
			steps[repoMap].State = StepCurrent
		} else {
			steps[project].State = StepCurrent
		}
	case store.RunDone:
		// Over at its last step, the moment the page caught it: every
		// step reached. A failed run stops anywhere, its steps unknown
		// here: left to do.
		for i := range steps {
			done(i)
		}
	}
	// Skipped parts override done.
	for i := 0; i < reading; i++ {
		name, overview := partAt(plan, i)
		s := &steps[1+i]
		if s.State != StepDone {
			continue
		}
		if overview {
			if why, ok := plan.Steps[baseline.PartOverview]; ok {
				s.State, s.Skip = StepSkipped, why
			}
			continue
		}
		for _, sk := range plan.Skipped {
			if sk == name {
				s.State, s.Skip = StepSkipped, plan.Why[name]
				break
			}
		}
	}
	if why, ok := plan.Steps[baseline.PartProject]; ok && steps[project].State == StepDone {
		steps[project].State, steps[project].Skip = StepSkipped, why
	}

	// Calls.
	for _, c := range calls {
		i := -1
		switch c.Purpose {
		case store.PurposeCatalogue:
			i = 0
		case store.PurposeOverview, store.PurposeSubject:
			if c.Step != nil && *c.Step >= 0 && *c.Step < reading {
				i = 1 + *c.Step
			}
		case store.PurposeProject, store.PurposeProjectMap:
			i = project
		case store.PurposeRepoMap:
			i = repoMap
		}
		if i < 0 {
			continue
		}
		s := &steps[i]
		s.Calls++
		if c.Outcome != inference.OutcomeOK.String() {
			s.Failed++
		}
		at := c.At
		s.At = &at
		if c.CostNanoUSD != nil {
			sum := *c.CostNanoUSD
			if s.CostNanoUSD != nil {
				sum += *s.CostNanoUSD
			}
			s.CostNanoUSD = &sum
		}
	}
	return steps, nil
}
