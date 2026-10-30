package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/store"
)

// With the sources on, a note for a repository that is no source is
// answered and logged, never stored, and wakes nobody.
func TestWebhook_SourceIsTheStartingPoint(t *testing.T) {
	st, w := newFakeStore(), &fakeWaker{}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, w, false)
	code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(1), nil)
	if code != 202 || field(t, body, "status") != "ignored" {
		t.Fatalf("no source repository: %d %v, want 202 ignored", code, body)
	}
	if len(st.stored) != 0 || len(st.deliveries) != 0 || w.wakes != 0 {
		t.Fatalf("ignored note: stored %d, deliveries %d, wakes %d, want nothing", len(st.stored), len(st.deliveries), w.wakes)
	}
	// A bad envelope is still a 422 first.
	if code, _ := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(0), nil); code != 422 {
		t.Fatalf("invalid body: %d, want 422 before the source check", code)
	}
	st.sourceRepos = map[string]bool{"PLAT/files": true}
	if code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(2), nil); code != 202 || field(t, body, "status") != "accepted" {
		t.Fatalf("source repository: %d %v, want accepted", code, body)
	}
	if len(st.stored) != 1 || w.wakes != 1 {
		t.Fatalf("accepted note: stored %d, wakes %d", len(st.stored), w.wakes)
	}
	st.sourceErr = errors.New("db down")
	if code, _ := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(3), nil); code != 500 {
		t.Fatalf("source check failed: %d, want 500 so noergler resends", code)
	}
}

// The wiki lists the sources when the feature is on, noergler's projects
// when it is off; with it off every note is taken as before.
func TestWiki_ListsSourcesWhenOn(t *testing.T) {
	st := newFakeStore()
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	do(t, h, "GET", "/api/projects", "", "", nil)
	off := newFakeStore()
	do(t, handler(t, off, &fakeWaker{}, config.AuthNone), "GET", "/api/projects", "", "", nil)
	if !reflect.DeepEqual(st.sourcedAsked, []bool{true}) || !reflect.DeepEqual(off.sourcedAsked, []bool{false}) {
		t.Fatalf("sourced: on %v, off %v", st.sourcedAsked, off.sourcedAsked)
	}
	if code, body := do(t, handler(t, off, &fakeWaker{}, config.AuthNone), "POST", "/webhooks/noergler", "tok-platform", validPayload(1), nil); code != 202 || field(t, body, "status") != "accepted" {
		t.Fatalf("sources off: %d %v, want every note accepted", code, body)
	}
}

func describeSources() *fakeSources {
	fs := newFakeSources()
	fs.sources["tools"] = store.ProjectSource{ProjectKey: "tools", Kind: store.SourceStatic, TokenAuth: store.TokenBasic, AutoDescribe: true}
	fs.repos["tools"] = []store.SourceRepo{
		{ProjectKey: "tools", Slug: "svc", HeadCommit: strings.Repeat("b", 40), State: store.RepoReady},
		{ProjectKey: "tools", Slug: "fresh", State: store.RepoPending},
	}
	fs.runs = map[string]store.RepoRuns{}
	return fs
}

func TestDescribe_QueuesARunAndWakesTheWorker(t *testing.T) {
	fs := describeSources()
	w := &fakeWaker{}
	h, _, _ := sourcesHandlerWith(t, fs, newFakeStore(), w, false)
	head := strings.Repeat("b", 40)

	for _, c := range []struct {
		path, body string
		code       int
		want       string
	}{
		{"/api/sources/tools/repos/nope/describe", `{}`, 404, "unknown repository"},
		{"/api/sources/tools/repos/..%2F/describe", `{}`, 404, "unknown repository"},
		{"/api/sources/nope/repos/svc/describe", `{}`, 404, "unknown source"},
		{"/api/sources/tools/repos/fresh/describe", `{}`, 422, "no head yet"},
		{"/api/sources/tools/repos/svc/describe", `{"mode":"partial"}`, 422, "mode"},
	} {
		code, body := do(t, h, http.MethodPost, c.path, "", c.body, jsonHdr)
		if code != c.code || !strings.Contains(field(t, body, "error"), c.want) {
			t.Errorf("%s %s: %d %v, want %d naming %q", c.path, c.body, code, body, c.code, c.want)
		}
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{}`, nil); code != 415 {
		t.Fatalf("no content type: %d", code)
	}

	// Before any run a reconcile is a full read.
	code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{}`, jsonHdr)
	if code != 202 || field(t, body, "status") != "queued" || field(t, body, "mode") != "full" || string(body["run_id"]) != "1" {
		t.Fatalf("first describe: %d %v", code, body)
	}
	// Once documented, a reconcile reads from the documented commit; full
	// stays full.
	fs.runs["svc"] = store.RepoRuns{Last: store.BaselineRun{ProjectKey: "tools", State: store.RunDone}, Documented: strings.Repeat("a", 40)}
	do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{"mode":"reconcile"}`, jsonHdr)
	do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{"mode":"FULL"}`, jsonHdr)
	want := []string{
		"tools svc full manual  " + head,
		"tools svc reconcile manual " + strings.Repeat("a", 40) + " " + head,
		"tools svc full manual  " + head,
	}
	if !reflect.DeepEqual(fs.enqueued, want) || w.wakes != 3 {
		t.Fatalf("enqueued = %q wakes %d\nwant %q", fs.enqueued, w.wakes, want)
	}

	fs.enqueueErr = store.ErrExists
	if code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{}`, jsonHdr); code != 409 || !strings.Contains(field(t, body, "error"), "open") {
		t.Fatalf("open run: %d %v, want 409", code, body)
	}
	fs.enqueueErr = store.ErrNoRepo
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{}`, jsonHdr); code != 404 {
		t.Fatalf("repository gone meanwhile: %d, want 404", code)
	}
	fs.enqueueErr = errors.New("db down")
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/describe", "", `{}`, jsonHdr); code != 500 {
		t.Fatalf("store failure: %d, want 500", code)
	}
	if w.wakes != 3 {
		t.Fatalf("wakes = %d after refusals, want 3", w.wakes)
	}
}

func TestSourceBody_BaselineAndAutoDescribe(t *testing.T) {
	fs := describeSources()
	cost := int64(2_418_000_000)
	finished := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	fs.runs["svc"] = store.RepoRuns{
		Last:     store.BaselineRun{ID: 7, ProjectKey: "tools", Slug: "svc", Mode: store.RunFull, Trigger: store.TriggerFirstReady, State: store.RunDone, Commit: "c0ffee", Step: 5, Steps: 5, Skipped: 1, FinishedAt: &finished},
		LastCost: &cost, Documented: "c0ffee", DocumentedAt: &finished,
	}
	h, _, _ := sourcesHandler(t, fs, false)
	code, body := do(t, h, http.MethodGet, "/api/sources/tools", "", "", nil)
	if code != 200 || string(body["auto_describe"]) != "true" {
		t.Fatalf("GET: %d %v", code, body)
	}
	var repos []map[string]json.RawMessage
	if err := json.Unmarshal(body["repos"], &repos); err != nil || len(repos) != 2 {
		t.Fatalf("repos = %s", body["repos"])
	}
	bySlug := map[string]map[string]json.RawMessage{}
	for _, r := range repos {
		bySlug[field(t, r, "slug")] = r
	}
	if string(bySlug["fresh"]["baseline"]) != "null" {
		t.Errorf("fresh baseline = %s, want null", bySlug["fresh"]["baseline"])
	}
	var b map[string]json.RawMessage
	if err := json.Unmarshal(bySlug["svc"]["baseline"], &b); err != nil {
		t.Fatal(err)
	}
	if field(t, b, "state") != "done" || field(t, b, "cost_usd") != "2.418" || field(t, b, "documented_commit") != "c0ffee" ||
		string(b["skipped"]) != "1" || field(t, b, "trigger") != "first_ready" || string(b["run_id"]) != "7" {
		t.Errorf("baseline = %v", b)
	}

	if code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"auto_describe":false}`, jsonHdr); code != 200 || string(body["auto_describe"]) != "false" {
		t.Fatalf("PATCH auto_describe: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"other","kind":"static","auto_describe":false}`, jsonHdr); code != 201 || string(body["auto_describe"]) != "false" {
		t.Fatalf("POST with auto_describe off: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"third","kind":"static"}`, jsonHdr); code != 201 || !fs.sources["third"].AutoDescribe {
		t.Fatalf("POST without auto_describe: %d %v, want it on", code, body)
	}
	// A key noergler spells otherwise would have every note ignored.
	fs.noerglerKeys = []string{"PLAT"}
	if code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"plat","kind":"static"}`, jsonHdr); code != 422 ||
		!strings.Contains(field(t, body, "error"), "noergler delivers this project as PLAT") {
		t.Fatalf("POST of a key spelled otherwise: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"PLAT","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatalf("POST of noergler's spelling: %d", code)
	}
	if code, body := do(t, h, http.MethodGet, "/api/sources", "", "", nil); code != 200 || !strings.Contains(string(body["sources"]), `"auto_describe":false`) {
		t.Fatalf("list: %d %v", code, body)
	}
}

// A page a baseline wrote says so: the commit, the kinds of its sources,
// what was checked.
func TestRepo_BaselineProvenanceOnTheWire(t *testing.T) {
	st := newFakeStore()
	st.teams["PLAT"] = "platform"
	st.repos["PLAT"] = []store.RepoRow{{Slug: "files"}}
	st.revisions = []store.RevisionRow{{Revision: 3, SourceKind: store.FromBaseline, SourceCommit: "c0ffee", Statements: 1}, {Revision: 2, SourcePR: "PLAT/files#5"}}
	st.pages[store.ScopeRepository+"|PLAT|files"] = store.Page{RevisionID: 3, Revision: 3, Summary: "Repo.", SourceKind: store.FromBaseline, SourceCommit: "c0ffee",
		Statements: []store.Statement{{ID: "s1", Kind: "component", Name: "N", Text: "T", Checked: "c0ffee",
			Sources: []store.Source{{PRKey: "PLAT/files#5", MergedAt: mergedAt}, {Kind: store.FromBaseline, Commit: "c0ffee", CommittedAt: mergedAt}}}}}
	_, body := do(t, handler(t, st, nil, config.AuthNone), "GET", "/api/projects/PLAT/repos/files", "", "", nil)
	var revs []map[string]any
	if err := json.Unmarshal(body["revisions"], &revs); err != nil {
		t.Fatal(err)
	}
	if revs[0]["source_kind"] != "baseline" || revs[0]["source_commit"] != "c0ffee" || revs[0]["source_pr_key"] != "" || revs[1]["source_kind"] != "pr" {
		t.Errorf("revisions = %v", revs)
	}
	var desc struct {
		SourceKind   string `json:"source_kind"`
		SourceCommit string `json:"source_commit"`
		Statements   []struct {
			Checked string           `json:"checked"`
			Sources []map[string]any `json:"sources"`
		} `json:"statements"`
	}
	if err := json.Unmarshal(body["description"], &desc); err != nil {
		t.Fatal(err)
	}
	if desc.SourceKind != "baseline" || desc.SourceCommit != "c0ffee" || desc.Statements[0].Checked != "c0ffee" {
		t.Fatalf("description = %+v", desc)
	}
	pr, base := desc.Statements[0].Sources[0], desc.Statements[0].Sources[1]
	if pr["kind"] != "pr" || pr["pr_key"] != "PLAT/files#5" || pr["commit"] != nil {
		t.Errorf("PR source = %v", pr)
	}
	if base["kind"] != "baseline" || base["commit"] != "c0ffee" || base["pr_key"] != nil || base["merged_at"] != nil || base["committed_at"] == nil {
		t.Errorf("baseline source = %v", base)
	}
}
