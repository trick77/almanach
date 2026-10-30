package inference

import (
	"context"
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

func TestDescribe(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"description": {"summary": "Zwei Repos.", "statements": []}}`)
	res := testClient(srv.Client()).Describe(context.Background(), "the project")
	if res.Outcome != OutcomeOK || res.Description == nil || res.Description.Summary != "Zwei Repos." {
		t.Fatalf("Describe = %+v", res)
	}
	if name := schemaName(t, srv); name != DescriptionSchemaName {
		t.Errorf("schema = %q", name)
	}
	srv.SetReply(`{"description": null}`)
	if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeOK || res.Description != nil {
		t.Errorf("no change = %+v, want ok and nil", res)
	}
	for _, reply := range []string{"null", "Gerne!"} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).Describe(context.Background(), "p"); res.Outcome != OutcomeUnparseable {
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
	} {
		if outcome == OutcomeOK || outcome == OutcomeUnparseable {
			t.Errorf("%s on a cancelled context: Outcome = %s, want a failure of the call", name, outcome)
		}
	}
}

func TestStrictFormatsRequireEveryProperty(t *testing.T) {
	for _, f := range []map[string]any{catalogueFormat().Schema, patchFormat().Schema, descriptionFormat().Schema} {
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
}
