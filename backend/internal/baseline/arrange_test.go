package baseline

import (
	"reflect"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

func layout(statements []store.Statement) []string {
	out := make([]string, 0, len(statements))
	for _, s := range statements {
		out = append(out, s.ID+":"+s.Area)
	}
	return out
}

// An area's statements stand together where its first one does, in their
// order; those without one after the areas, then Betrieb und Überwachung,
// Entwicklung und Auslieferung last wherever they stood.
// An arranged page comes back as it was.
func TestArrange(t *testing.T) {
	in := []store.Statement{
		{ID: "s1", Area: LastArea}, {ID: "s2", Area: "Abfrage"}, {ID: "s3"}, {ID: "s4", Area: "Export"},
		{ID: "s8", Area: OpsArea}, {ID: "s5", Area: "Abfrage"}, {ID: "s6", Area: LastArea}, {ID: "s7", Area: "Export"},
	}
	want := []string{"s2:Abfrage", "s5:Abfrage", "s4:Export", "s7:Export", "s3:", "s8:" + OpsArea, "s1:" + LastArea, "s6:" + LastArea}
	got := Arrange(in)
	if !reflect.DeepEqual(layout(got), want) {
		t.Fatalf("Arrange = %v, want %v", layout(got), want)
	}
	if again := Arrange(got); !reflect.DeepEqual(layout(again), want) {
		t.Errorf("Arrange twice = %v, want it stable", layout(again))
	}
	if got := Arrange(nil); len(got) != 0 {
		t.Errorf("Arrange(nil) = %v", got)
	}
}

// The prompts name the last two areas as the code stores them, in the
// code's order, and every prompt that lays a page out places them.
func TestLastAreaInThePrompts(t *testing.T) {
	ops, last := strings.Index(prompts.Areas, "then `"+OpsArea+"`"), strings.Index(prompts.Areas, "always last `"+LastArea+"`")
	if ops < 0 || last < 0 || ops > last {
		t.Errorf("prompts.Areas does not name %q, then %q last", OpsArea, LastArea)
	}
	for name, p := range map[string]string{"repo map": prompts.RepoMap, "project": prompts.Project} {
		if !strings.Contains(p, "`"+OpsArea+"` second to last") {
			t.Errorf("%s prompt's outline does not place %q", name, OpsArea)
		}
	}
}

func TestCleanArea(t *testing.T) {
	for in, want := range map[string]string{
		"  Abfrage   und  Suche ":        "Abfrage und Suche",
		"entwicklung und auslieferung":   LastArea,
		"ENTWICKLUNG UND AUSLIEFERUNG":   LastArea,
		"betrieb und ueberwachung":       OpsArea,
		"Betrieb":                        "Betrieb",
		"Strassenverkehr":                "Strassenverkehr",
		"Grösse":                         "Grösse",
		"Maße":                           "Masse",
		"":                               "",
		"Entwicklung und Auslieferung ✓": "Entwicklung und Auslieferung ✓",
	} {
		if got := CleanArea(in); got != want {
			t.Errorf("CleanArea(%q) = %q, want %q", in, got, want)
		}
	}
}

// The outline sets each named statement's area and place; an area without
// a name, an unknown id and one named twice are left out and named; a
// statement it does not name keeps its area and joins it.
func TestApplyOutline(t *testing.T) {
	current := []store.Statement{
		{ID: "s1", Name: "Build"}, {ID: "s2", Name: "Dienst"}, {ID: "s3", Name: "Export"},
		{ID: "s4", Name: "Liste", Area: "Abfrage"}, {ID: "s5", Name: "Filter", Area: "Abfrage"}, {ID: "s6", Name: "Alt"},
	}
	got, bad := ApplyOutline(current, []inference.OutlineArea{
		{Area: "Entwicklung und Auslieferung", IDs: []string{"s1"}},
		{Area: "Gesamtsystem", IDs: []string{"s2", "s7"}},
		{Area: "Abfrage", IDs: []string{"s5"}},
		{Area: "", IDs: []string{"s6"}},
		{Area: "Export", IDs: []string{"s3", "s2"}},
	})
	want := []string{"s2:Gesamtsystem", "s5:Abfrage", "s4:Abfrage", "s3:Export", "s6:", "s1:" + LastArea}
	if !reflect.DeepEqual(layout(got), want) {
		t.Errorf("layout = %v, want %v", layout(got), want)
	}
	wantBad := []string{"s7 (unknown id)", "an area without a name (s6)", "s2 (named twice)"}
	if !reflect.DeepEqual(bad, wantBad) {
		t.Errorf("bad = %q, want %q", bad, wantBad)
	}
	if got[0].Name != "Dienst" {
		t.Errorf("a statement laid out lost its text: %+v", got[0])
	}
	if Arranged(got) || !Arranged(got[:4]) || !Arranged(nil) {
		t.Error("Arranged: wrong for statements without an area")
	}
	none, bad := ApplyOutline(current, nil)
	if len(bad) != 0 || !reflect.DeepEqual(layout(none), layout(Arrange(current))) {
		t.Errorf("an empty outline = %v (%q), want the page arranged as it stands", layout(none), bad)
	}
}
