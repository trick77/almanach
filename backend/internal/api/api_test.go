package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/almanach/internal/teamkeys"
)

var mergedAt = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

// fakeStore answers from fixed rows and records what the webhook stored.
type fakeStore struct {
	accepted   bool
	ingestErr  error
	stored     []store.Note
	deliveries []store.Delivery
	calls      []*store.NoteCall
	claims     [][]store.Claim

	projects   []store.ProjectRow
	teams      map[string]string
	repos      map[string][]store.RepoRow
	pages      map[string]store.Page
	revisions  []store.RevisionRow
	notes      []store.NoteRow
	details    map[string]store.NoteDetail
	problems   []store.ProblemArea
	daysAsked  []int
	problemErr error

	sourceRepos  map[string]bool
	sourceErr    error
	sourcedAsked []bool

	renamed map[string]string
	// single are the projects of one, by key: their one repository.
	single map[string]store.RepoRow
	// graph is what Dependencies answers; graphErr fails it.
	graph    store.Graph
	graphErr error
	// libs is what Libraries answers, by project key.
	libs map[string][]store.SharedRepo
}

func (f *fakeStore) Dependencies(context.Context) (store.Graph, error) { return f.graph, f.graphErr }

// Libraries answers libs by project key; graphErr fails it.
func (f *fakeStore) Shared(_ context.Context, key, _ string) ([]store.SharedRepo, error) {
	return f.libs[key], f.graphErr
}

func (f *fakeStore) SourceRepoCount(_ context.Context, key string) (int, error) {
	if _, ok := f.single[key]; ok {
		return 1, nil
	}
	return len(f.repos[key]), nil
}

func (f *fakeStore) SingleRepos(context.Context) (map[string]store.RepoRow, error) {
	out := map[string]store.RepoRow{}
	for k, r := range f.single {
		out[k] = r
	}
	return out, nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{accepted: true, teams: map[string]string{}, repos: map[string][]store.RepoRow{},
		pages: map[string]store.Page{}, details: map[string]store.NoteDetail{}}
}

func (f *fakeStore) Ingest(_ context.Context, d store.Delivery, n store.Note, call *store.NoteCall, claims []store.Claim) (bool, error) {
	if f.ingestErr != nil {
		return false, f.ingestErr
	}
	f.deliveries = append(f.deliveries, d)
	f.stored = append(f.stored, n)
	f.calls = append(f.calls, call)
	f.claims = append(f.claims, claims)
	return f.accepted, nil
}

func (f *fakeStore) Projects(_ context.Context, sourced bool) ([]store.ProjectRow, error) {
	f.sourcedAsked = append(f.sourcedAsked, sourced)
	return f.projects, nil
}

func (f *fakeStore) IsSourceRepo(_ context.Context, key, slug string) (bool, error) {
	return f.sourceRepos[key+"/"+slug], f.sourceErr
}

func (f *fakeStore) ProjectExists(_ context.Context, key string, _ bool) (string, bool, error) {
	team, ok := f.teams[key]
	return team, ok, nil
}

func (f *fakeStore) Repos(_ context.Context, project string, _ bool) ([]store.RepoRow, error) {
	return f.repos[project], nil
}

func (f *fakeStore) RepoExists(_ context.Context, project, slug string, _ bool) (bool, error) {
	for _, r := range f.repos[project] {
		if r.Slug == slug {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) CurrentPage(_ context.Context, scope, project, repo string) (store.Page, bool, error) {
	p, ok := f.pages[scope+"|"+project+"|"+repo]
	return p, ok, nil
}

func (f *fakeStore) Revisions(context.Context, string, string, string, int) ([]store.RevisionRow, error) {
	return f.revisions, nil
}

func (f *fakeStore) Notes(context.Context, string, string, int) ([]store.NoteRow, error) {
	return f.notes, nil
}

func (f *fakeStore) NoteByKey(_ context.Context, project, repo string, prID int) (store.NoteDetail, bool, error) {
	d, ok := f.details[project+"|"+repo+"|"+itoa(prID)]
	return d, ok, nil
}

func (f *fakeStore) Problems(_ context.Context, _ string, days int) ([]store.ProblemArea, error) {
	f.daysAsked = append(f.daysAsked, days)
	return f.problems, f.problemErr
}

// ProjectKeyOf and RepoNamesOf follow renamed ("old key" → "new key",
// "key/old slug" → "key/new slug"); a name not in it is current.
func (f *fakeStore) ProjectKeyOf(_ context.Context, key string) (string, bool, error) {
	if cur, ok := f.renamed[key]; ok {
		return cur, true, nil
	}
	return key, true, nil
}

func (f *fakeStore) RepoNamesOf(_ context.Context, key, slug string) (string, string, bool, error) {
	if cur, ok := f.renamed[key+"/"+slug]; ok {
		k, s, _ := strings.Cut(cur, "/")
		return k, s, true, nil
	}
	return key, slug, true, nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type fakeWaker struct{ wakes int }

func (w *fakeWaker) Wake() { w.wakes++ }

func keysFile(t *testing.T) *teamkeys.Keys {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"platform": "tok-platform", "billing": "tok-billing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := teamkeys.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func handler(t *testing.T, st *fakeStore, w *fakeWaker, mode config.AuthMode) http.Handler {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	Register(srv, Deps{Store: st, Keys: keysFile(t), Worker: w, AuthMode: mode, ProblemWindow: 90, Log: discard})
	return srv.Handler()
}

func do(t *testing.T, h http.Handler, method, path, bearer, body string, hdr map[string]string) (int, map[string]json.RawMessage) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]json.RawMessage
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: body %q is not a JSON object: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

func field(t *testing.T, m map[string]json.RawMessage, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("body lacks %q: %v", key, m)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return string(v)
	}
	return s
}

func validPayload(prID int) string {
	return `{"event_type":"pr_merged_note","project_key":"PLAT","repo_slug":"files","pr_id":` + itoa(prID) +
		`,"pr_key":"PLAT/files#` + itoa(prID) + `","pr_title":"t","ticket_key":null,"jira_issue_type":null,"change_kind":"feature",` +
		`"reviewed_commit":null,"source_commit_sha":"abcdef1234567","merge_commit_sha":null,"merged_at":"2026-10-20T10:00:00Z",` +
		`"run_id":1,"model_label":null,"issues_posted":0,"suggestions_posted":0,"note":{"components":[]},"claims":[],` +
		`"note_call":null,"rounds":{"count":1,"input_tokens":1,"output_tokens":1,"cost_usd":null,"cost_nano_usd":null},"review_totals":null}`
}

func TestPing(t *testing.T) {
	h := handler(t, newFakeStore(), nil, config.AuthNone)
	if code, body := do(t, h, "GET", "/auth/ping", "", "", nil); code != 401 || field(t, body, "error") != "bearer token required" {
		t.Errorf("no bearer: %d %v", code, body)
	}
	if code, body := do(t, h, "GET", "/auth/ping", "tok-nobody", "", nil); code != 401 || field(t, body, "error") != "unknown token" {
		t.Errorf("unknown token: %d %v", code, body)
	}
	code, body := do(t, h, "GET", "/auth/ping", "tok-platform", "", nil)
	if code != 200 || field(t, body, "status") != "ok" || field(t, body, "team") != "platform" {
		t.Errorf("platform: %d %v", code, body)
	}
}

func TestWebhook(t *testing.T) {
	t.Run("401", func(t *testing.T) {
		st := newFakeStore()
		h := handler(t, st, &fakeWaker{}, config.AuthNone)
		if code, _ := do(t, h, "POST", "/webhooks/noergler", "", validPayload(1), nil); code != 401 {
			t.Errorf("code = %d", code)
		}
		if code, _ := do(t, h, "POST", "/webhooks/noergler", "tok-nobody", validPayload(1), nil); code != 401 {
			t.Errorf("code = %d", code)
		}
		if len(st.stored) != 0 {
			t.Errorf("stored without a bearer: %+v", st.stored)
		}
	})
	t.Run("422 names the field", func(t *testing.T) {
		st := newFakeStore()
		h := handler(t, st, &fakeWaker{}, config.AuthNone)
		code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(0), nil)
		if code != 422 {
			t.Fatalf("code = %d, body %v", code, body)
		}
		if msg := field(t, body, "error"); !strings.Contains(msg, "pr_id") {
			t.Errorf("error = %q, want the field named", msg)
		}
		if len(st.stored) != 0 {
			t.Error("an invalid body was stored")
		}
	})
	t.Run("422 malformed JSON", func(t *testing.T) {
		h := handler(t, newFakeStore(), &fakeWaker{}, config.AuthNone)
		code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", "{not json", nil)
		if code != 422 || !strings.HasPrefix(field(t, body, "error"), "body:") {
			t.Errorf("code = %d, body %v; want 422 with the body named", code, body)
		}
	})
	t.Run("202 accepted wakes the worker", func(t *testing.T) {
		st := newFakeStore()
		w := &fakeWaker{}
		h := handler(t, st, w, config.AuthNone)
		code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(7), nil)
		if code != 202 || field(t, body, "status") != "accepted" {
			t.Fatalf("code = %d, body %v", code, body)
		}
		if w.wakes != 1 {
			t.Errorf("wakes = %d, want 1", w.wakes)
		}
		if len(st.stored) != 1 {
			t.Fatalf("stored = %+v", st.stored)
		}
		if st.stored[0].Team != "platform" {
			t.Errorf("stored Team = %q, want the bearer's team", st.stored[0].Team)
		}
		d := st.deliveries[0]
		if d.ID != "pr_merged_note#plat/files#7#abcdef1234567" || d.Team != "platform" || d.EventType != "pr_merged_note" || len(d.Payload) == 0 {
			t.Errorf("delivery = %+v", d)
		}
		if st.calls[0] == nil || st.calls[0].Rounds != 1 {
			t.Errorf("call = %+v", st.calls[0])
		}
	})
	t.Run("202 deduped does not wake", func(t *testing.T) {
		st := newFakeStore()
		st.accepted = false
		w := &fakeWaker{}
		h := handler(t, st, w, config.AuthNone)
		code, body := do(t, h, "POST", "/webhooks/noergler", "tok-billing", validPayload(7), nil)
		if code != 202 || field(t, body, "status") != "deduped" {
			t.Fatalf("code = %d, body %v", code, body)
		}
		if w.wakes != 0 {
			t.Errorf("wakes = %d, want none", w.wakes)
		}
		if st.stored[0].Team != "billing" {
			t.Errorf("Team = %q", st.stored[0].Team)
		}
	})
	t.Run("500 persist failed", func(t *testing.T) {
		st := newFakeStore()
		st.ingestErr = errors.New("db down")
		w := &fakeWaker{}
		h := handler(t, st, w, config.AuthNone)
		code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(7), nil)
		if code != 500 || field(t, body, "error") != "persist failed" {
			t.Fatalf("code = %d, body %v", code, body)
		}
		if w.wakes != 0 {
			t.Errorf("wakes = %d", w.wakes)
		}
	})
}

// The SPA is the GET catch-all only: a non-GET on an unknown or GET-only
// route is the mux's 405, never the shell; an unknown GET path is the SPA
// allowlist's 404. A GET on the POST-only webhook is a GET like any other
// and lands in that allowlist too.
func TestWrongMethodIs405NotTheSPA(t *testing.T) {
	h := handler(t, newFakeStore(), nil, config.AuthNone)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/projects", 405}, {"POST", "/nope", 405}, {"GET", "/nope", 404}, {"GET", "/webhooks/noergler", 404},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestProjects(t *testing.T) {
	st := newFakeStore()
	h := handler(t, st, nil, config.AuthNone)
	code, body := do(t, h, "GET", "/api/projects", "", "", nil)
	if code != 200 || string(body["projects"]) != "[]" {
		t.Errorf("empty: %d %s", code, body["projects"])
	}
	last := mergedAt
	st.projects = []store.ProjectRow{{Key: "PLAT", Team: "platform", Repos: 2, Notes: 5, Revision: 3, LastMerge: &last}, {Key: "ZED", Team: "billing"}}
	code, body = do(t, h, "GET", "/api/projects", "", "", nil)
	var rows []map[string]any
	if err := json.Unmarshal(body["projects"], &rows); err != nil || code != 200 {
		t.Fatalf("code %d, projects %s: %v", code, body["projects"], err)
	}
	if len(rows) != 2 || rows[0]["key"] != "PLAT" || rows[0]["team"] != "platform" || rows[0]["repos"] != 2.0 || rows[0]["notes"] != 5.0 ||
		rows[0]["revision"] != 3.0 || rows[0]["last_merge"] != "2026-10-20T10:00:00Z" {
		t.Errorf("rows = %v", rows)
	}
	if rows[1]["last_merge"] != nil {
		t.Errorf("ZED last_merge = %v, want null", rows[1]["last_merge"])
	}
}

func TestProject(t *testing.T) {
	st := newFakeStore()
	h := handler(t, st, nil, config.AuthNone)
	if code, body := do(t, h, "GET", "/api/projects/NOPE", "", "", nil); code != 404 || field(t, body, "error") != "unknown project" {
		t.Errorf("unknown: %d %v", code, body)
	}
	st.teams["PLAT"] = "platform"
	st.repos["PLAT"] = []store.RepoRow{
		{Slug: "files", Revision: 2, Summary: "  Der Dienst speichert Dateien! Und mehr. ", Notes: 4},
		{Slug: "web", Revision: 0, Summary: "Kein Satzende"},
	}
	code, body := do(t, h, "GET", "/api/projects/PLAT", "", "", nil)
	if code != 200 {
		t.Fatalf("code = %d, body %v", code, body)
	}
	if string(body["description"]) != "null" {
		t.Errorf("description = %s, want null without a page", body["description"])
	}
	if field(t, body, "key") != "PLAT" || field(t, body, "team") != "platform" || string(body["problem_window_days"]) != "90" {
		t.Errorf("head = %v", body)
	}
	if string(body["problems"]) != "[]" || string(body["recent_notes"]) != "[]" {
		t.Errorf("problems = %s, recent_notes = %s, want empty arrays", body["problems"], body["recent_notes"])
	}
	var repos []map[string]any
	if err := json.Unmarshal(body["repos"], &repos); err != nil || len(repos) != 2 {
		t.Fatalf("repos = %s: %v", body["repos"], err)
	}
	if repos[0]["slug"] != "files" || repos[0]["summary"] != "Der Dienst speichert Dateien!" || repos[0]["notes"] != 4.0 || repos[0]["revision"] != 2.0 {
		t.Errorf("repos[0] = %v", repos[0])
	}
	if repos[1]["summary"] != "Kein Satzende" {
		t.Errorf("repos[1] summary = %v", repos[1]["summary"])
	}

	st.pages[store.ScopeProject+"|PLAT|"] = store.Page{RevisionID: 9, Revision: 4, Summary: "Projekt.", SourcePR: "PLAT/files#3", UpdatedAt: mergedAt,
		Statements: []store.Statement{{ID: "s1", Kind: "entity", Name: "Datei", Text: "T", Sources: []store.Source{{PRKey: "PLAT/files#3", MergedAt: mergedAt}}}}}
	_, body = do(t, h, "GET", "/api/projects/PLAT", "", "", nil)
	var desc map[string]json.RawMessage
	if err := json.Unmarshal(body["description"], &desc); err != nil || desc == nil {
		t.Fatalf("description = %s", body["description"])
	}
	if string(desc["revision"]) != "4" || field(t, desc, "summary") != "Projekt." || field(t, desc, "source_pr_key") != "PLAT/files#3" {
		t.Errorf("description = %v", desc)
	}
	if !strings.Contains(string(desc["statements"]), `"files":[]`) {
		t.Errorf("statement files = %s, want an empty array for nil", desc["statements"])
	}
	if string(body["map_expected"]) != "false" {
		t.Errorf("map_expected = %s, want false with the sources off", body["map_expected"])
	}
	if string(body["map"]) != "null" || strings.Contains(string(desc["statements"]), `"diagram"`) {
		t.Errorf("map = %s, statements = %s; want null and no diagram key", body["map"], desc["statements"])
	}

	// The map on the project, a diagram on its statement: rongo's IR as
	// stored, every list an array.
	page := st.pages[store.ScopeProject+"|PLAT|"]
	page.Map = &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "files", Label: "files", Shape: "step", Zone: "repositories"}},
		Zones: []diagram.Zone{{ID: "repositories", Title: "Repositories"}}}
	page.Statements[0].Diagram = &diagram.Diagram{Kind: diagram.KindER, Direction: "TB", Entities: []diagram.Entity{{ID: "file", Label: "Datei"}}}
	st.pages[store.ScopeProject+"|PLAT|"] = page
	_, body = do(t, h, "GET", "/api/projects/PLAT", "", "", nil)
	if want := `{"kind":"flow","direction":"LR","nodes":[{"id":"files","label":"files","shape":"step","zone":"repositories"}],"edges":[],"zones":[{"id":"repositories","title":"Repositories"}]}`; string(body["map"]) != want {
		t.Errorf("map = %s\nwant %s", body["map"], want)
	}
	if err := json.Unmarshal(body["description"], &desc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desc["statements"]), `"diagram":{"kind":"er","direction":"TB","entities":[{"id":"file","label":"Datei","attrs":[]}],"rels":[]}`) {
		t.Errorf("statements = %s", desc["statements"])
	}
}

func TestProblems(t *testing.T) {
	st := newFakeStore()
	st.teams["PLAT"] = "platform"
	h := handler(t, st, nil, config.AuthNone)
	for _, days := range []string{"0", "x", "4000", "-1"} {
		code, body := do(t, h, "GET", "/api/projects/PLAT/problems?days="+days, "", "", nil)
		if code != 400 || field(t, body, "error") != "days: 1..3650" {
			t.Errorf("days=%s: %d %v", days, code, body)
		}
	}
	if len(st.daysAsked) != 0 {
		t.Errorf("the store was asked with invalid days: %v", st.daysAsked)
	}
	if code, _ := do(t, h, "GET", "/api/projects/NOPE/problems", "", "", nil); code != 404 {
		t.Errorf("unknown project: %d", code)
	}
	st.problems = []store.ProblemArea{{RepoSlug: "files", Area: "internal/files", Component: "uploader", Bugfixes: 3, TopCause: "race", TopCauseN: 2,
		Issues: 4, Suggestions: 1, LastFixPR: "PLAT/files#9", LastFixAt: mergedAt}}
	code, body := do(t, h, "GET", "/api/projects/PLAT/problems?days=30", "", "", nil)
	if code != 200 || string(body["problem_window_days"]) != "30" {
		t.Fatalf("code = %d, body %v", code, body)
	}
	if len(st.daysAsked) != 1 || st.daysAsked[0] != 30 {
		t.Errorf("daysAsked = %v", st.daysAsked)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body["problems"], &rows); err != nil || len(rows) != 1 {
		t.Fatalf("problems = %s", body["problems"])
	}
	if rows[0]["area"] != "internal/files" || rows[0]["bugfixes"] != 3.0 || rows[0]["top_cause"] != "race" || rows[0]["top_cause_count"] != 2.0 ||
		rows[0]["last_fix_pr_key"] != "PLAT/files#9" {
		t.Errorf("row = %v", rows[0])
	}
	code, body = do(t, h, "GET", "/api/projects/PLAT/problems", "", "", nil)
	if code != 200 || string(body["problem_window_days"]) != "90" || st.daysAsked[len(st.daysAsked)-1] != 90 {
		t.Errorf("default window: %d %v, asked %v", code, body, st.daysAsked)
	}
}

func TestRepo(t *testing.T) {
	st := newFakeStore()
	st.teams["PLAT"] = "platform"
	last := mergedAt
	st.repos["PLAT"] = []store.RepoRow{{Slug: "files", Notes: 250, LastMerge: &last}}
	h := handler(t, st, nil, config.AuthNone)
	if code, body := do(t, h, "GET", "/api/projects/PLAT/repos/nope", "", "", nil); code != 404 || field(t, body, "error") != "unknown repository" {
		t.Errorf("unknown: %d %v", code, body)
	}
	st.revisions = []store.RevisionRow{{Revision: 2, SourcePR: "PLAT/files#5", Statements: 3, Chars: 400, CreatedAt: mergedAt}, {Revision: 1, SourcePR: "PLAT/files#1"}}
	st.notes = []store.NoteRow{{PRID: 5, PRKey: "PLAT/files#5", PRTitle: "t", RepoSlug: "files", ChangeKind: "bugfix", MergedAt: mergedAt, Items: 2, Issues: 1}}
	st.pages[store.ScopeRepository+"|PLAT|files"] = store.Page{RevisionID: 2, Revision: 2, Summary: "Repo.", SourcePR: "PLAT/files#5"}
	code, body := do(t, h, "GET", "/api/projects/PLAT/repos/files", "", "", nil)
	if code != 200 || field(t, body, "project_key") != "PLAT" || field(t, body, "slug") != "files" {
		t.Fatalf("code = %d, body %v", code, body)
	}
	// The whole count and the newest merge come from the repository row,
	// not from the capped list.
	if string(body["notes_total"]) != "250" || string(body["last_merge"]) == "null" {
		t.Errorf("notes_total = %s, last_merge = %s", body["notes_total"], body["last_merge"])
	}
	var revs []map[string]any
	if err := json.Unmarshal(body["revisions"], &revs); err != nil || len(revs) != 2 {
		t.Fatalf("revisions = %s", body["revisions"])
	}
	if revs[0]["revision"] != 2.0 || revs[0]["source_pr_key"] != "PLAT/files#5" || revs[0]["statements"] != 3.0 || revs[0]["chars"] != 400.0 {
		t.Errorf("revisions[0] = %v", revs[0])
	}
	var notes []map[string]any
	if err := json.Unmarshal(body["notes"], &notes); err != nil || len(notes) != 1 || notes[0]["pr_key"] != "PLAT/files#5" || notes[0]["items"] != 2.0 {
		t.Errorf("notes = %s", body["notes"])
	}
	var desc map[string]json.RawMessage
	if err := json.Unmarshal(body["description"], &desc); err != nil || field(t, desc, "summary") != "Repo." {
		t.Errorf("description = %s", body["description"])
	}
	// No map drawn yet is null, not absent; a drawn one comes as stored.
	if string(body["map"]) != "null" {
		t.Errorf("map before any was drawn = %s", body["map"])
	}
	page := st.pages[store.ScopeRepository+"|PLAT|files"]
	page.RepoMap = &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "api", Label: "HTTP-API", Shape: "step", Zone: "entry"}},
		Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}}}
	st.pages[store.ScopeRepository+"|PLAT|files"] = page
	_, body = do(t, h, "GET", "/api/projects/PLAT/repos/files", "", "", nil)
	var drawn diagram.Diagram
	if err := json.Unmarshal(body["map"], &drawn); err != nil || !diagram.Equal(&drawn, page.RepoMap) {
		t.Errorf("map = %s", body["map"])
	}
}

// A URL under an old key or slug finds the project or repository and
// answers with its current names, which the UI puts in the address bar.
func TestOldNamesAnswerWithCurrentOnes(t *testing.T) {
	st := newFakeStore()
	st.teams["OLD"] = "platform"
	st.repos["OLD"] = []store.RepoRow{{Slug: "old"}}
	st.renamed = map[string]string{"OLD": "NEW", "OLD/old": "NEW/new"}
	h := handler(t, st, nil, config.AuthNone)
	if code, body := do(t, h, "GET", "/api/projects/OLD", "", "", nil); code != 200 || field(t, body, "key") != "NEW" {
		t.Errorf("project by its old key: %d %v", code, body)
	}
	code, body := do(t, h, "GET", "/api/projects/OLD/repos/old", "", "", nil)
	if code != 200 || field(t, body, "project_key") != "NEW" || field(t, body, "slug") != "new" {
		t.Errorf("repository by its old names: %d %v", code, body)
	}
}

func TestNote(t *testing.T) {
	st := newFakeStore()
	h := handler(t, st, nil, config.AuthNone)
	for _, pr := range []string{"abc", "0", "-1", "99"} {
		if code, body := do(t, h, "GET", "/api/projects/PLAT/repos/files/notes/"+pr, "", "", nil); code != 404 || field(t, body, "error") != "unknown note" {
			t.Errorf("pr %s: %d %v", pr, code, body)
		}
	}
	cost := int64(1_234_567_890)
	rounds := int64(500_000)
	in := int64(1000)
	received := mergedAt.Add(time.Minute)
	st.details["PLAT|files|42"] = store.NoteDetail{
		Note: store.Note{ProjectKey: "PLAT", RepoSlug: "files", PRID: 42, Team: "platform", PRKey: "PLAT/files#42", PRTitle: "Fix", TicketKey: "PLAT-7",
			IssueType: "Bug", ChangeKind: "bugfix", Bugfix: json.RawMessage(`{"cause_kind":"race"}`), BugfixArea: "internal/files",
			Body: json.RawMessage(`{"components":[]}`), SourceCommit: "abcdef1234567", MergedAt: mergedAt, RunID: 9, IssuesPosted: 2},
		Items:      3,
		ReceivedAt: received,
		Call: &store.NoteCallRow{Outcome: "ok", NoteStatus: "posted", Model: "gpt-x", InputTokens: &in, CostNanoUSD: &cost, Rounds: 3,
			RoundsInputTokens: 300_000, RoundsOutputTokens: 2_400, RoundsCost: &rounds},
	}
	code, body := do(t, h, "GET", "/api/projects/PLAT/repos/files/notes/42", "", "", nil)
	if code != 200 {
		t.Fatalf("code = %d, body %v", code, body)
	}
	if field(t, body, "pr_key") != "PLAT/files#42" || field(t, body, "project_key") != "PLAT" || field(t, body, "jira_issue_type") != "Bug" ||
		field(t, body, "bugfix_area") != "internal/files" || field(t, body, "source_commit") != "abcdef1234567" || string(body["run_id"]) != "9" ||
		string(body["items"]) != "3" {
		t.Errorf("body = %v", body)
	}
	if string(body["bugfix"]) != `{"cause_kind":"race"}` || string(body["note"]) != `{"components":[]}` || string(body["consolidated_at"]) != "null" {
		t.Errorf("raw fields = %s %s %s", body["bugfix"], body["note"], body["consolidated_at"])
	}
	if _, ok := body["merge_commit"]; ok {
		t.Error("merge_commit present though empty")
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(body["note_call"], &call); err != nil || call == nil {
		t.Fatalf("note_call = %s", body["note_call"])
	}
	if field(t, call, "outcome") != "ok" || field(t, call, "note_status") != "posted" || field(t, call, "model") != "gpt-x" ||
		string(call["input_tokens"]) != "1000" || string(call["cache_read_tokens"]) != "null" || string(call["rounds"]) != "3" ||
		string(call["rounds_input_tokens"]) != "300000" || string(call["rounds_output_tokens"]) != "2400" {
		t.Errorf("note_call = %v", call)
	}
	if field(t, call, "cost_usd") != "1.235" || field(t, call, "rounds_cost_usd") != "0.001" {
		t.Errorf("usd = %s / %s", call["cost_usd"], call["rounds_cost_usd"])
	}

	// Unpriced: the cost keys are omitted, not empty strings; no call row at all: note_call null.
	d := st.details["PLAT|files|42"]
	d.Call = &store.NoteCallRow{Outcome: "error"}
	st.details["PLAT|files|42"] = d
	_, body = do(t, h, "GET", "/api/projects/PLAT/repos/files/notes/42", "", "", nil)
	call = nil
	if err := json.Unmarshal(body["note_call"], &call); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cost_usd", "rounds_cost_usd", "model"} {
		if _, ok := call[k]; ok {
			t.Errorf("%s present for an unpriced call: %s", k, call[k])
		}
	}
	d.Call = nil
	st.details["PLAT|files|42"] = d
	_, body = do(t, h, "GET", "/api/projects/PLAT/repos/files/notes/42", "", "", nil)
	if string(body["note_call"]) != "null" {
		t.Errorf("note_call = %s, want null", body["note_call"])
	}
}

func TestUSD(t *testing.T) {
	cases := []struct {
		nano *int64
		want string
	}{
		{nil, ""},
		{ptr(int64(0)), "0.000"},
		{ptr(int64(499_999)), "0.000"},
		{ptr(int64(500_000)), "0.001"},
		{ptr(int64(1_234_567_890)), "1.235"},
		{ptr(int64(1_234_499_999)), "1.234"},
		{ptr(int64(12_000_000_000)), "12.000"},
		{ptr(int64(-500_000)), "-0.001"},
		{ptr(int64(-1_234_567_890)), "-1.235"},
	}
	for _, tc := range cases {
		if got := usd(tc.nano); got != tc.want {
			t.Errorf("usd(%v) = %q, want %q", tc.nano, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestFirstSentence(t *testing.T) {
	for in, want := range map[string]string{
		"Eins. Zwei.": "Eins.", "Eins! Zwei": "Eins!", "Frage? Ja": "Frage?", "Kein Ende": "Kein Ende", "  Eins.  ": "Eins.",
		".": ".", "": "", "Ende.": "Ende.",
		"Der Dienst liest config.yaml und bedient z.B. den Webhook. Mehr.": "Der Dienst liest config.yaml und bedient z.B. den Webhook.",
		"Seit Version 2.0 läuft es bzw. es geht. Fertig.":                  "Seit Version 2.0 läuft es bzw. es geht.",
		"Liest main.go. Dann mehr.":                                        "Liest main.go.",
	} {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}

// A summary's layout never reaches the repository list: its first block,
// bold and list marks gone; a bold opening is the opening.
func TestLead(t *testing.T) {
	for in, want := range map[string]string{
		"Der **Dienst** liest\nNotizen. Mehr.\n\nZweiter.": "Der Dienst liest Notizen. Mehr.",
		"- **eins**\n- zwei":                        "eins",
		"**Der Dienst wird ersetzt.**\n\nEr liest.": "Der Dienst wird ersetzt.",
		"**Almanach** ist das Wiki, das **lebt**":   "Almanach ist das Wiki, das lebt",
		"Eins.\n\nZwei.":                            "Eins.",
		"`a**b` bleibt.":                            "`a**b` bleibt.",
		"":                                          "",
		"#### Zweck\n\nDer Dienst liest.\n\n#### Rollen": "Der Dienst liest.",
		"#### Rollen\n\n- `api`: Dienst":                 "`api`: Dienst",
		"#### Nur ein Titel":                             "Nur ein Titel",
	} {
		if got := lead(in); got != want {
			t.Errorf("lead(%q) = %q, want %q", in, got, want)
		}
	}
	if got := firstSentence(lead("**Der Dienst.** Mehr.\n\nX")); got != "Der Dienst." {
		t.Errorf("first sentence = %q", got)
	}
}
