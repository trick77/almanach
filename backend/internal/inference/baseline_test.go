package inference

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/llmwire/llmwiretest"
)

// schemaName is the json_schema name the last request carried.
func schemaName(t *testing.T, srv *llmwiretest.Server) string {
	t.Helper()
	req := srv.Last()
	if req.Stream() {
		t.Error("a baseline call asked for a stream")
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
	// The merge's description has no map: the merge never draws one.
	if _, ok := description(true, RepoKinds)["properties"].(map[string]any)["map"]; ok {
		t.Error("the merge's description carries a map")
	}
	zone := mapSchema()["properties"].(map[string]any)["nodes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["zone"].(map[string]any)
	if !slices.Equal(zone["enum"].([]any), []any{"clients", "repositories", "stores", "external"}) {
		t.Errorf("map node zone = %v", zone)
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
		"merge":   description(false, RepoKinds)["properties"].(map[string]any)["statements"].(map[string]any),
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
	// Both halves parse; Proposed lists the edits, then the new ones with
	// any id the answer gave cleared.
	var d Description
	if !parse(`{"summary": "s", "statements": [{"id": "s2", "kind": "rule", "name": "a", "text": "b", "files": []}],
		"added": [{"id": "s36", "kind": "rule", "name": "c", "text": "d", "files": []}]}`, &d) {
		t.Fatal("description did not parse")
	}
	got, reason := d.Proposed()
	if reason != "" || len(got) != 2 || got[0].ID == nil || *got[0].ID != "s2" || got[1].ID != nil || got[1].Name != "c" {
		t.Errorf("Proposed = %+v, %q", got, reason)
	}
	if _, reason := Proposed([]Statement{{Name: "x"}}, nil); reason == "" {
		t.Error("an edit without an id was taken")
	}
}

// Every format offers the new kinds; a project description never offers an
// extension, a repository's change recipe.
func TestSchemaKinds(t *testing.T) {
	kindsOf := func(st map[string]any) []any {
		return st["items"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"].([]any)
	}
	merge := mergeFormat(true).Schema["properties"].(map[string]any)
	for name, tc := range map[string]struct {
		st        map[string]any
		extension bool
	}{
		"patch":         {patchFormat(true).Schema["properties"].(map[string]any)["statements"].(map[string]any), true},
		"merge repo":    {merge["repository"].(map[string]any)["properties"].(map[string]any)["statements"].(map[string]any), true},
		"merge project": {merge["project"].(map[string]any)["properties"].(map[string]any)["statements"].(map[string]any), false},
		"project":       {projectDescription(true)["properties"].(map[string]any)["statements"].(map[string]any), false},
	} {
		got := kindsOf(tc.st)
		for _, k := range []string{"component", "interface", "rule", "invariant", "entity", "decision", "pitfall"} {
			if !slices.Contains(got, any(k)) {
				t.Errorf("%s: kinds %v lack %s", name, got, k)
			}
		}
		if slices.Contains(got, any("extension")) != tc.extension {
			t.Errorf("%s: kinds %v, extension offered = %v", name, got, !tc.extension)
		}
	}
}
