package consolidate

import (
	"context"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
)

// Two router subjects the plan merges (one name but for case and the word
// rule) give it both their questions, stored prose, at most five.
func TestRoute_QuestionsFollowThePlan(t *testing.T) {
	x := newWorld(t, 16)
	x.models.route = inference.RouteResult{Call: called, Subjects: []inference.RouteSubject{
		{Subject: inference.Subject{Name: "Dienst-Anmeldung", Purpose: "p", Paths: []string{"auth/"}},
			Questions: []string{"Wo liegt die Straße?", "Q2", "Q3"}},
		{Subject: inference.Subject{Name: "Dienst-anmeldung", Purpose: "p", Paths: []string{"billing/"}},
			Questions: []string{"Q2", "Q4", "Q5", "Q6", "Q7"}},
	}}
	dry, err := x.w.Route(context.Background(), "tools", "svc", head)
	if err != nil {
		t.Fatal(err)
	}
	qs := dry.Questions["Service-Anmeldung"]
	if len(dry.Plan.Subjects) == 0 || dry.Plan.Subjects[0].Name != "Service-Anmeldung" ||
		len(qs) != 5 || qs[0] != "Wo liegt die Strasse?" || qs[4] != "Q5" {
		t.Errorf("subjects %+v, questions %v", dry.Plan.Subjects, dry.Questions)
	}
}

// The dry run reads the tree as a full run would, shows the router every
// file with its skeleton, and holds the picks to the plan's rules (paths
// the tree has, anchors added); it writes nothing and records no call.
func TestRoute_DryRun(t *testing.T) {
	x := newWorld(t, 16)
	x.models.route = inference.RouteResult{Call: called, Subjects: []inference.RouteSubject{{
		Subject:   inference.Subject{Name: "Anmeldung", Purpose: "Wer sich anmeldet.", Paths: []string{"auth/", "nowhere.go"}},
		Questions: []string{"Wie meldet man sich an?"},
	}}}
	dry, err := x.w.Route(context.Background(), "tools", "svc", head)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.models.prompts) != 1 {
		t.Fatalf("%d prompts, want the router's one", len(x.models.prompts))
	}
	prompt := x.models.prompts[0]
	for _, want := range []string{"Repository `svc` in project `tools`", "auth/login.go (", "at most 12 subjects"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("router prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "{listing}") || strings.Contains(prompt, "{step_kb}") {
		t.Error("a placeholder left in the router prompt")
	}
	var anmeldung []string
	for _, s := range dry.Plan.Subjects {
		if s.Name == "Anmeldung" {
			for _, f := range s.Files {
				anmeldung = append(anmeldung, f.Path)
			}
		}
	}
	if len(anmeldung) != 1 || anmeldung[0] != "auth/login.go" || dry.Questions["Anmeldung"][0] != "Wie meldet man sich an?" {
		t.Errorf("plan = %+v, questions %v: want the subject held to the tree", dry.Plan.Subjects, dry.Questions)
	}
	if !dry.Tree["auth/login.go"] || len(dry.Dropped) != 0 {
		t.Errorf("tree %v, dropped %v: want the tree's paths, nothing the router picked dropped", dry.Tree, dry.Dropped)
	}
	if dry.ReadableBytes == 0 || dry.Listing == "" || len(x.st.calls) != 0 || len(x.st.revisions) != 0 {
		t.Errorf("dry = %+v, calls %d, revisions %d: want sizes and nothing written", dry, len(x.st.calls), len(x.st.revisions))
	}
}

// A file the router picked that the plan does not hold (past the run's
// bytes here) is Dropped: a dry run tells it from one the router passed
// over.
func TestRoute_DroppedPicks(t *testing.T) {
	x := newWorld(t, 16)
	x.w.base.Limits.MaxBytes = 10
	x.models.route = inference.RouteResult{Call: called, Subjects: []inference.RouteSubject{
		{Subject: inference.Subject{Name: "Anmeldung", Purpose: "p", Paths: []string{"auth/"}}},
		{Subject: inference.Subject{Name: "Abrechnung", Purpose: "p", Paths: []string{"billing/*"}}},
	}}
	dry, err := x.w.Route(context.Background(), "tools", "svc", head)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.Dropped["auth/login.go"] || !dry.Dropped["billing/run.go"] || dry.Dropped["README.md"] || len(dry.Plan.Subjects) != 0 {
		t.Errorf("dropped %v, plan %+v: want both picks dropped past the run's bytes", dry.Dropped, dry.Plan.Subjects)
	}
}
