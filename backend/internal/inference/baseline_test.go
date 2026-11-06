package inference

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/toolkit/llmwire"
	"github.com/trick77/toolkit/llmwire/llmwiretest"
)

// schemaName is the json_schema name the last request carried.
func schemaName(t *testing.T, srv *llmwiretest.Server) string {
	t.Helper()
	req := srv.Last()
	if !req.Stream() {
		t.Error("a baseline call did not stream")
	}
	rf, _ := req.Body["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if js["strict"] != true {
		t.Errorf("json_schema not strict: %v", js)
	}
	name, _ := js["name"].(string)
	return name
}

func TestCatalogue(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"subjects": [{"name": "Import", "purpose": "Liest Dateien ein.", "paths": ["import/", "cmd/import.go"]}]}`)
	res := testClient(srv.Client()).Catalogue(context.Background(), "catalogue this")
	if res.Outcome != OutcomeOK || !res.Cost.Answered {
		t.Fatalf("Outcome = %s cost %+v err %v", res.Outcome, res.Cost, res.Err)
	}
	if len(res.Catalogue.Subjects) != 1 || res.Catalogue.Subjects[0].Name != "Import" || len(res.Catalogue.Subjects[0].Paths) != 2 {
		t.Fatalf("Catalogue = %+v", res.Catalogue)
	}
	if name := schemaName(t, srv); name != CatalogueSchemaName {
		t.Errorf("schema = %q", name)
	}

	for _, reply := range []string{"no json", `{"subjects": null}`, "null"} {
		srv.SetReply(reply)
		res := testClient(srv.Client()).Catalogue(context.Background(), "p")
		if res.Outcome != OutcomeUnparseable || res.Catalogue.Subjects != nil {
			t.Errorf("reply %q: Outcome = %s catalogue %+v, want unparseable and empty", reply, res.Outcome, res.Catalogue)
		}
	}
}

func TestPatch(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply("```json\n" + `{"summary": null, "statements": [{"id": "s2", "kind": "rule", "name": "n", "text": "t", "files": []}],
		"drop": ["s3"], "confirmed": ["s1"]}` + "\n```")
	res := testClient(srv.Client()).Patch(context.Background(), "describe this part")
	if res.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %s err %v", res.Outcome, res.Err)
	}
	p := res.Patch
	if p.Summary != nil || len(p.Statements) != 1 || *p.Statements[0].ID != "s2" || len(p.Drop) != 1 || p.Confirmed[0] != "s1" {
		t.Fatalf("Patch = %+v", p)
	}
	if name := schemaName(t, srv); name != PatchSchemaName {
		t.Errorf("schema = %q", name)
	}

	srv.SetReply(`{"summary": "Neu.", "statements": [], "drop": [], "confirmed": []}`)
	if res := testClient(srv.Client()).Patch(context.Background(), "p"); res.Outcome != OutcomeOK || res.Patch.Summary == nil || *res.Patch.Summary != "Neu." {
		t.Errorf("summary patch = %+v", res)
	}
	srv.SetReply(`{"summary": null, "drop": []}`)
	if res := testClient(srv.Client()).Patch(context.Background(), "p"); res.Outcome != OutcomeUnparseable {
		t.Errorf("patch without statements: Outcome = %s, want unparseable", res.Outcome)
	}
}

const testMap = `{"kind": "flow", "direction": "LR", "nodes": [{"id": "svc", "label": "svc", "shape": "step", "zone": "repositories"}], "edges": [], "zones": [{"id": "repositories", "title": "Repositories"}]}`

func TestDescribe(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"description": {"summary": "Zwei Repos.", "statements": [{"id": null, "kind": "component", "name": "n", "text": "t", "files": [],
		"diagram": {"kind": "sequence", "actors": [{"id": "a", "label": "A"}], "rows": [{"type": "message", "from": "a", "to": "a", "label": "x", "line": "solid", "head": "filled"}]}}],
		"map": ` + testMap + `}}`)
	res := testClient(srv.Client()).Describe(context.Background(), "the project")
	if res.Outcome != OutcomeOK || res.Description == nil || res.Description.Summary != "Zwei Repos." {
		t.Fatalf("Describe = %+v", res)
	}
	d := res.Description
	if d.Map == nil || d.Map.Nodes[0].Zone != "repositories" || d.Statements[0].Diagram == nil || d.Statements[0].Diagram.Rows[0].Head != "filled" {
		t.Errorf("map %+v, diagram %+v", d.Map, d.Statements[0].Diagram)
	}
	// The outline beside a description, or beside none: no change asked
	// still lays the page out.
	srv.SetReply(`{"description": null, "outline": [{"area": "Gesamtsystem", "ids": ["s1"]}]}`)
	if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeOK || res.Description != nil ||
		len(res.Outline) != 1 || res.Outline[0].IDs[0] != "s1" {
		t.Errorf("outline without a change = %+v", res)
	}
	if props := descriptionFormat(true).Schema["properties"].(map[string]any); props["outline"] == nil {
		t.Error("the project schema asks for no outline")
	}
	if name := schemaName(t, srv); name != DescriptionSchemaName {
		t.Errorf("schema = %q", name)
	}
	srv.SetReply(`{"description": null}`)
	if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeOK || res.Description != nil {
		t.Errorf("no change = %+v, want ok and nil", res)
	}
	// A null map is an answer: whether it may be is the worker's call, and
	// it never costs the description.
	srv.SetReply(`{"description": {"summary": "x", "statements": [], "map": null}}`)
	if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeOK || res.Description == nil || res.Description.Map != nil {
		t.Errorf("null map = %+v, want the description without a map", res)
	}
	for _, reply := range []string{"null", "Gerne!", "{}", `{"other": 1}`, `{"description": {"summary": "x"}}`, `{"description": 3}`} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeUnparseable {
			t.Errorf("reply %q: Outcome = %s, want unparseable", reply, res.Outcome)
		}
	}
}

const testRepoMap = `{"kind": "flow", "direction": "LR", "nodes": [{"id": "api", "label": "HTTP-API", "shape": "step", "zone": "entry"}], "edges": [], "zones": [{"id": "entry", "title": "Eingang"}]}`

// The system map asked again alone: its own schema, the system map's
// zones, the map parsed as a repository map's is.
func TestProjectMap(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"map": {"kind": "flow", "direction": "TB", "nodes": [{"id": "svc", "label": "svc", "shape": "step", "zone": "repositories"}], "edges": [], "zones": [{"id": "repositories", "title": "Repositories"}]}}`)
	res := testClient(srv.Client()).ProjectMap(context.Background(), "the project")
	if res.Outcome != OutcomeOK || res.Map == nil || res.Map.Nodes[0].Zone != "repositories" {
		t.Fatalf("ProjectMap = %+v", res)
	}
	if name := schemaName(t, srv); name != ProjectMapSchemaName {
		t.Errorf("schema = %q", name)
	}
	srv.SetReply(`{"other": 1}`)
	if res := testClient(srv.Client()).ProjectMap(context.Background(), "p"); res.Outcome != OutcomeUnparseable {
		t.Errorf("no map key: Outcome = %s, want unparseable", res.Outcome)
	}
}

func TestRepoMap(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"map": ` + testRepoMap + `}`)
	res := testClient(srv.Client()).RepoMap(context.Background(), "the repository")
	if res.Outcome != OutcomeOK || res.Map == nil || res.Map.Nodes[0].Zone != "entry" {
		t.Fatalf("RepoMap = %+v", res)
	}
	if name := schemaName(t, srv); name != RepoMapSchemaName {
		t.Errorf("schema = %q", name)
	}
	srv.SetReply(`{"map": null}`)
	if res := testClient(srv.Client()).RepoMap(context.Background(), "p"); res.Outcome != OutcomeOK || res.Map != nil {
		t.Errorf("keep = %+v, want ok and nil", res)
	}
	for _, reply := range []string{"null", "Gerne!", "{}", `{"other": 1}`, `{"map": 3}`} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).RepoMap(context.Background(), "p"); res.Outcome != OutcomeUnparseable {
			t.Errorf("reply %q: Outcome = %s, want unparseable", reply, res.Outcome)
		}
	}
	// The outline beside the map: read, null kept, one off its shape costs
	// only itself.
	srv.SetReply(`{"map": null, "outline": [{"area": "Abfrage", "ids": ["s2", "s1"]}]}`)
	res = testClient(srv.Client()).RepoMap(context.Background(), "p")
	if res.Outcome != OutcomeOK || len(res.Outline) != 1 || res.Outline[0].Area != "Abfrage" || res.Outline[0].IDs[1] != "s1" {
		t.Errorf("outline = %+v", res)
	}
	for _, reply := range []string{`{"map": null, "outline": null}`, `{"map": null, "outline": 3}`} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).RepoMap(context.Background(), "p"); res.Outcome != OutcomeOK || res.Outline != nil {
			t.Errorf("reply %q: %+v, want ok and no outline", reply, res)
		}
	}
	if props := repoMapFormat().Schema["properties"].(map[string]any); props["outline"] == nil {
		t.Error("the repository map schema asks for no outline")
	}
}

// A restyle's answer: the patch with its new statements' origins, the
// outline beside it; the repository's kinds or the project's, no drop nor
// confirmation offered; strict at every depth.
func TestRestyle(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"summary": null, "statements": [{"id": "s1", "kind": "component", "name": "Kern", "text": "Kurz.", "files": [], "area": "Kern"}],
		"added": [{"from": ["s1", "summary"], "kind": "flow", "name": "Ablauf", "text": [{"kind": "numbered", "text": "", "items": ["a", "b"]}], "files": ["x.go"], "area": "Kern"}],
		"outline": [{"area": "Kern", "ids": ["s1"]}], "drop": ["s2"], "confirmed": ["s3"]}`)
	res := testClient(srv.Client()).Restyle(context.Background(), "restyle this", false)
	if res.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %s err %v", res.Outcome, res.Err)
	}
	p := res.Patch
	if p.Summary != nil || *p.Statements[0].ID != "s1" || len(p.Added) != 1 || !slices.Equal(p.Added[0].From, []string{"s1", "summary"}) ||
		!strings.Contains(p.Added[0].Text, "1. a") {
		t.Fatalf("Patch = %+v", p)
	}
	if len(res.Outline) != 1 || res.Outline[0].IDs[0] != "s1" {
		t.Errorf("outline = %+v", res.Outline)
	}
	if name := schemaName(t, srv); name != RestyleSchemaName {
		t.Errorf("schema = %q", name)
	}
	for _, reply := range []string{"null", "Gerne!", "{}", `{"summary": null, "added": []}`} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).Restyle(context.Background(), "p", false); res.Outcome != OutcomeUnparseable {
			t.Errorf("reply %q: Outcome = %s, want unparseable", reply, res.Outcome)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := testClient(srv.Client()).Restyle(ctx, "p", true); res.Outcome == OutcomeOK || res.Outcome == OutcomeUnparseable {
		t.Errorf("restyle on a cancelled context: Outcome = %s, want a failure of the call", res.Outcome)
	}
	for project, kinds := range map[bool][]string{false: RepoKinds, true: ProjectKinds} {
		for _, diagrams := range []bool{true, false} {
			f := restyleFormat(diagrams, project).Schema
			walkObjects(t, f)
			props := f["properties"].(map[string]any)
			// Only the project's restyle drops (repository detail) and judges
			// the shape; neither confirms.
			if (props["drop"] != nil) != project || (props["shape"] != nil) != project || props["confirmed"] != nil || props["outline"] == nil {
				t.Errorf("project %v: restyle schema properties %v", project, sortedKeys(props))
			}
			added := props["added"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
			kind := added["kind"].(map[string]any)["enum"].([]any)
			if added["from"] == nil || len(kind) != len(kinds) {
				t.Errorf("project %v: added %v, kinds %v", project, sortedKeys(added), kind)
			}
		}
	}
}

// A call that fails at the gateway says so the same way for every kind of
// step, so the worker classes them alike.
func TestBaselineCallsShareTheFailureOutcomes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := llmwiretest.NewServer(t)
	c := testClient(srv.Client())
	for name, outcome := range map[string]Outcome{
		"catalogue": c.Catalogue(ctx, "p").Outcome,
		"patch":     c.Patch(ctx, "p").Outcome,
		"describe":  c.Describe(ctx, "p").Outcome,
		"repo map":  c.RepoMap(ctx, "p").Outcome,
	} {
		if outcome == OutcomeOK || outcome == OutcomeUnparseable {
			t.Errorf("%s on a cancelled context: Outcome = %s, want a failure of the call", name, outcome)
		}
	}
}

func TestStrictFormatsRequireEveryProperty(t *testing.T) {
	for _, f := range []map[string]any{catalogueFormat().Schema, patchFormat(true).Schema, descriptionFormat(true).Schema, repoMapFormat().Schema} {
		props, _ := f["properties"].(map[string]any)
		req, _ := f["required"].([]any)
		if len(req) != len(props) || f["additionalProperties"] != false {
			t.Errorf("schema %v: required %v of %d properties", f, req, len(props))
		}
		for i := 1; i < len(req); i++ {
			if req[i-1].(string) > req[i].(string) {
				t.Errorf("required not sorted: %v", req)
			}
		}
	}
	// Strict mode holds at every depth: each object of every diagram kind,
	// the map's and the sequence rows' anyOf branches included.
	for _, diagrams := range []bool{true, false} {
		for _, f := range []map[string]any{patchFormat(diagrams).Schema, descriptionFormat(diagrams).Schema, mergeFormat(diagrams).Schema, repoMapFormat().Schema} {
			walkObjects(t, f)
		}
	}
}

// walkObjects checks every object schema under s lists each property as
// required and allows nothing else.
func walkObjects(t *testing.T, s any) {
	t.Helper()
	switch v := s.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			req, _ := v["required"].([]any)
			if len(req) != len(props) || v["additionalProperties"] != false {
				t.Errorf("object with properties %v: required %v", sortedKeys(props), req)
			}
		}
		for _, child := range v {
			walkObjects(t, child)
		}
	case []any:
		for _, child := range v {
			walkObjects(t, child)
		}
	}
}

func TestDiagramSchemas(t *testing.T) {
	desc := projectDescription(true)
	props := desc["properties"].(map[string]any)
	if _, ok := props["map"]; !ok || !slices.Contains(desc["required"].([]any), any("map")) {
		t.Errorf("project description without a required map: %v", desc["required"])
	}
	if !slices.Contains(props["map"].(map[string]any)["type"].([]any), any("null")) {
		t.Errorf("map not nullable: %v", props["map"])
	}
	// The merge's patch has no map (the merge never draws one), no
	// confirmation (a fold reads no code), a drop and a nullable summary.
	merge := mergePatch(true, RepoKinds)["properties"].(map[string]any)
	if _, ok := merge["map"]; ok {
		t.Error("the merge's patch carries a map")
	}
	if _, ok := merge["confirmed"]; ok {
		t.Error("the merge's patch offers a confirmation")
	}
	if _, ok := merge["drop"]; !ok {
		t.Error("the merge's patch offers no drop")
	}
	if !slices.Contains(merge["summary"].(map[string]any)["type"].([]any), any("null")) {
		t.Errorf("merge summary not nullable: %v", merge["summary"])
	}
	zone := mapSchema()["properties"].(map[string]any)["nodes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["zone"].(map[string]any)
	if !slices.Equal(zone["enum"].([]any), []any{"clients", "entry", "repositories", "stores", "external"}) {
		t.Errorf("map node zone = %v", zone)
	}
	// Stage and layer: the maps' alone, required there.
	items := func(m map[string]any, list string) map[string]any {
		return m["properties"].(map[string]any)[list].(map[string]any)["items"].(map[string]any)
	}
	for mapName, m := range map[string]map[string]any{"system map": mapSchema(), "repository map": repoMapSchema()} {
		tagged := map[string]map[string]any{"node stage": items(m, "nodes"), "edge layer": items(m, "edges")}
		for name, it := range tagged {
			key := strings.Fields(name)[1]
			p, ok := it["properties"].(map[string]any)[key].(map[string]any)
			if !ok || !slices.Contains(it["required"].([]any), any(key)) {
				t.Errorf("%s %s missing or not required: %v", mapName, name, it)
				continue
			}
			want := map[string][]any{"stage": {"production", "other", "none"}, "layer": {"runtime", "library", "build", "deploy", "monitoring"}}[key]
			if !slices.Equal(p["enum"].([]any), want) {
				t.Errorf("%s %s = %v", mapName, name, p["enum"])
			}
		}
	}
	for name, m := range map[string]map[string]any{"flow": flowSchema()} {
		if _, ok := items(m, "nodes")["properties"].(map[string]any)["stage"]; ok {
			t.Errorf("%s: nodes carry a stage", name)
		}
		if _, ok := items(m, "edges")["properties"].(map[string]any)["layer"]; ok {
			t.Errorf("%s: edges carry a layer", name)
		}
	}
	// No array bound past minItems: a strict schema rejects one.
	for name, m := range map[string]map[string]any{"system map": mapSchema(), "repository map": repoMapSchema(), "flow": flowSchema()} {
		props := m["properties"].(map[string]any)
		if _, ok := props["nodes"].(map[string]any)["maxItems"]; ok {
			t.Errorf("%s: nodes bounded in the schema", name)
		}
	}
	// The three kinds, the removal, and null to keep.
	branches := statementDiagram()["anyOf"].([]any)
	none := branches[3].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
	if len(branches) != 5 || !slices.Equal(none["enum"].([]any), []any{"none"}) || branches[4].(map[string]any)["type"] != "null" {
		t.Errorf("statement diagram = %v", branches)
	}
}

// BACKEND_DIAGRAM_MAX_PER_PAGE=0 offers no statement diagram anywhere; the
// project's map is asked for still.
func TestNoDiagramsSchemas(t *testing.T) {
	statementProps := func(statements map[string]any) map[string]any {
		return statements["items"].(map[string]any)["properties"].(map[string]any)
	}
	for name, st := range map[string]map[string]any{
		"patch":   patchFormat(false).Schema["properties"].(map[string]any)["statements"].(map[string]any),
		"merge":   mergePatch(false, RepoKinds)["properties"].(map[string]any)["statements"].(map[string]any),
		"project": projectDescription(false)["properties"].(map[string]any)["statements"].(map[string]any),
	} {
		if _, ok := statementProps(st)["diagram"]; ok {
			t.Errorf("%s statements offer a diagram with diagrams off", name)
		}
	}
	if _, ok := statementProps(statements(true, RepoKinds))["diagram"]; !ok {
		t.Error("statements without a diagram with diagrams on")
	}
	if _, ok := projectDescription(false)["properties"].(map[string]any)["map"]; !ok {
		t.Error("the map is gone with statement diagrams off")
	}
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"repository": null, "project": null}`)
	c := testClient(srv.Client())
	c.diagrams = false
	c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	body, _ := json.Marshal(srv.Last().Body)
	if strings.Contains(string(body), `"diagram"`) || !strings.Contains(string(body), `"statements"`) {
		t.Error("the merge request offered a diagram with diagrams off")
	}
}

// Every format splits edits from new statements: an edit's id is a
// required string, never null; a new statement has no id field at all, so
// a model cannot number one.
func TestSchemaEditsAndAdded(t *testing.T) {
	merge := mergeFormat(true).Schema["properties"].(map[string]any)
	for name, props := range map[string]map[string]any{
		"patch":         patchFormat(true).Schema["properties"].(map[string]any),
		"merge repo":    merge["repository"].(map[string]any)["properties"].(map[string]any),
		"merge project": merge["project"].(map[string]any)["properties"].(map[string]any),
		"project":       projectDescription(true)["properties"].(map[string]any),
	} {
		edit := props["statements"].(map[string]any)["items"].(map[string]any)
		id, ok := edit["properties"].(map[string]any)["id"].(map[string]any)
		if !ok || id["type"] != "string" || !slices.Contains(edit["required"].([]any), any("id")) {
			t.Errorf("%s: edit id = %v, want a required string", name, id)
		}
		add := props["added"].(map[string]any)["items"].(map[string]any)
		if _, has := add["properties"].(map[string]any)["id"]; has || add["additionalProperties"] != false {
			t.Errorf("%s: a new statement may carry an id: %v", name, add)
		}
		if _, has := add["properties"].(map[string]any)["kind"]; !has {
			t.Errorf("%s: added lacks the statement's fields", name)
		}
	}
	// Both halves parse, an id a new statement carries with them: the code
	// clears it (baseline.ApplyPatch).
	var d Description
	if !parse(`{"summary": "s", "statements": [{"id": "s2", "kind": "rule", "name": "a", "text": "b", "files": []}],
		"added": [{"id": "s36", "kind": "rule", "name": "c", "text": "d", "files": []}]}`, &d) {
		t.Fatal("description did not parse")
	}
	if len(d.Statements) != 1 || d.Statements[0].ID == nil || *d.Statements[0].ID != "s2" || len(d.Added) != 1 || d.Added[0].Name != "c" {
		t.Errorf("Description = %+v", d)
	}
}

// Every format offers the new kinds; a project description never offers an
// extension, a repository's change recipe, nor a component, how a part
// works being its repository's page's.
func TestSchemaKinds(t *testing.T) {
	kindsOf := func(st map[string]any) []any {
		return st["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any)
	}
	merge := mergeFormat(true).Schema["properties"].(map[string]any)
	for name, tc := range map[string]struct {
		st   map[string]any
		repo bool
	}{
		"patch":           {patchFormat(true).Schema["properties"].(map[string]any)["statements"].(map[string]any), true},
		"merge repo":      {merge["repository"].(map[string]any)["properties"].(map[string]any)["statements"].(map[string]any), true},
		"merge project":   {merge["project"].(map[string]any)["properties"].(map[string]any)["statements"].(map[string]any), false},
		"project":         {projectDescription(true)["properties"].(map[string]any)["statements"].(map[string]any), false},
		"project restyle": {restyleFormat(true, true).Schema["properties"].(map[string]any)["statements"].(map[string]any), false},
	} {
		got := kindsOf(tc.st)
		for _, k := range []string{"interface", "flow", "rule", "invariant", "entity", "decision", "pitfall"} {
			if !slices.Contains(got, any(k)) {
				t.Errorf("%s: kinds %v lack %s", name, got, k)
			}
		}
		for _, k := range []string{"extension", "component"} {
			if slices.Contains(got, any(k)) != tc.repo {
				t.Errorf("%s: kinds %v, %s offered = %v", name, got, k, !tc.repo)
			}
		}
	}
}

// Every project call judges the project's shape, with or without a change:
// the description's and the project restyle's, never a repository's.
func TestSchemaShape(t *testing.T) {
	for name, f := range map[string]llmwire.ResponseFormat{
		"project": descriptionFormat(true), "project restyle": restyleFormat(true, true), "repository restyle": restyleFormat(true, false),
	} {
		props := f.Schema["properties"].(map[string]any)
		want := name != "repository restyle"
		if (props["shape"] != nil) != want || (props["shape_reason"] != nil) != want {
			t.Errorf("%s: properties %v, shape asked = %v", name, sortedKeys(props), !want)
		}
		if want && !slices.Equal(props["shape"].(map[string]any)["enum"].([]any), []any{"system", "collection"}) {
			t.Errorf("%s: shape = %v", name, props["shape"])
		}
	}
}

// The shape is read with a change or without one; one off the enum is
// none, never a sunk answer.
func TestDescribe_ReadsTheShape(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	for reply, want := range map[string]Shape{
		`{"shape": "collection", "shape_reason": "Bibliotheken.", "outline": null, "description": null}`:                                                   {Kind: "collection", Reason: "Bibliotheken."},
		`{"shape": "system", "shape_reason": "", "outline": null, "description": {"summary": [], "statements": [], "added": [], "drop": [], "map": null}}`: {Kind: "system"},
		`{"shape": "heap", "shape_reason": "?", "outline": null, "description": null}`:                                                                     {},
		`{"outline": null, "description": null}`: {},
	} {
		srv.SetReply(reply)
		res := testClient(srv.Client()).Describe(context.Background(), "p")
		if res.Outcome != OutcomeOK || res.Shape != want {
			t.Errorf("reply %s: outcome %s, shape %+v, want %+v", reply, res.Outcome, res.Shape, want)
		}
	}
	srv.SetReply(`{"shape": "collection", "shape_reason": "x", "summary": null, "statements": [], "added": [], "drop": ["s1"], "outline": null}`)
	if res := testClient(srv.Client()).Restyle(context.Background(), "p", true); res.Kind != "collection" || !slices.Equal(res.Patch.Drop, []string{"s1"}) {
		t.Errorf("project restyle = %+v", res)
	}
	if res := testClient(srv.Client()).Restyle(context.Background(), "p", false); res.Shape != (Shape{}) {
		t.Errorf("repository restyle judged a shape: %+v", res.Shape)
	}
}
