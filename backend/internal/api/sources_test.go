package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
)

// fakeSources is an in-memory SourceStore.
type fakeSources struct {
	sources map[string]store.ProjectSource
	repos   map[string][]store.SourceRepo
	synced  []string
	// reasons is why each queued run was, by id; reasonErr fails its write.
	reasons   map[int64]store.RunReason
	reasonErr error
	// runs is by slug; enqueued records "key slug mode trigger base commit".
	runs       map[string]store.RepoRuns
	enqueued   []string
	enqueueErr error
	// noerglerKeys are the project keys noergler delivered.
	noerglerKeys []string
	// ids are the fake's project and repository ids, handed out in the
	// order the names are first met ("key", "key/slug"); taken are names a
	// rename must refuse.
	ids   map[string]int64
	taken map[string]bool
	// cloned are clone URLs another source clones, by URL: an add or a
	// rename onto one is refused.
	cloned map[string]*store.ClonedElsewhereError
	// activeErr fails a deactivation or an activation.
	activeErr error
	// briefs is each repository's brief by id; requested every request.
	briefs    map[int64]store.Brief
	requested []int64
}

func (f *fakeSources) RequestBrief(_ context.Context, repoID int64) error {
	f.requested = append(f.requested, repoID)
	if f.briefs == nil {
		f.briefs = map[int64]store.Brief{}
	}
	b := f.briefs[repoID]
	now := time.Now()
	b.RequestedAt, b.FailedAt, b.Failed = &now, nil, ""
	f.briefs[repoID] = b
	return nil
}
func (f *fakeSources) RepoBrief(_ context.Context, repoID int64) (store.Brief, bool, error) {
	b, ok := f.briefs[repoID]
	return b, ok, nil
}
func (f *fakeSources) Briefs(_ context.Context, key string) (map[int64]store.Brief, error) {
	out := map[int64]store.Brief{}
	for _, r := range f.repos[key] {
		if b, ok := f.briefs[f.id(key+"/"+r.Slug)]; ok {
			b.Markdown = ""
			out[f.id(key+"/"+r.Slug)] = b
		}
	}
	return out, nil
}

func newFakeSources() *fakeSources {
	return &fakeSources{sources: map[string]store.ProjectSource{}, repos: map[string][]store.SourceRepo{},
		ids: map[string]int64{}, taken: map[string]bool{}}
}

func (f *fakeSources) id(name string) int64 {
	if _, ok := f.ids[name]; !ok {
		f.ids[name] = int64(len(f.ids) + 1)
	}
	return f.ids[name]
}

// withIDs is a source or repository row as the store returns it: its ids
// filled in.
func (f *fakeSources) withIDs(r store.SourceRepo) store.SourceRepo {
	r.ProjectID, r.RepoID = f.id(r.ProjectKey), f.id(r.ProjectKey+"/"+r.Slug)
	return r
}

func (f *fakeSources) Sources(context.Context) ([]store.SourceRow, error) {
	out := []store.SourceRow{}
	for _, s := range f.sources {
		row := store.SourceRow{ProjectSource: s, Repos: len(f.repos[s.ProjectKey])}
		for _, r := range f.repos[s.ProjectKey] {
			if r.State == store.RepoReady {
				row.Ready++
			}
		}
		out = append(out, row)
	}
	return out, nil
}
func (f *fakeSources) SourceByKey(_ context.Context, key string) (store.ProjectSource, bool, error) {
	s, ok := f.sources[key]
	if ok {
		s.ProjectID = f.id(key)
	}
	return s, ok, nil
}
func (f *fakeSources) SourceRepoByName(_ context.Context, key, slug string) (store.SourceRepo, bool, error) {
	for _, r := range f.repos[key] {
		if r.Slug == slug {
			return f.withIDs(r), true, nil
		}
	}
	return store.SourceRepo{}, false, nil
}
func (f *fakeSources) RenameProject(_ context.Context, key, to string) (int64, error) {
	if f.taken[to] {
		return 0, store.ErrNameTaken
	}
	s := f.sources[key]
	s.ProjectKey = to
	f.sources[to], f.repos[to] = s, f.repos[key]
	f.ids[to] = f.id(key)
	delete(f.sources, key)
	delete(f.repos, key)
	return f.ids[to], nil
}
func (f *fakeSources) RenameRepo(_ context.Context, key, slug, to, cloneURL string) error {
	if e := f.cloned[cloneURL]; e != nil {
		return e
	}
	if f.taken[key+"/"+to] {
		return store.ErrNameTaken
	}
	if rr, ok := f.runs[slug]; ok && rr.Last.Open() && cloneURL != "" {
		return store.ErrRunOpen
	}
	for i, r := range f.repos[key] {
		if r.Slug == slug {
			f.ids[key+"/"+to] = f.id(key + "/" + slug)
			r.Slug = to
			if cloneURL != "" {
				r.CloneURL, r.State = cloneURL, store.RepoPending
			}
			f.repos[key][i] = r
			return nil
		}
	}
	return store.ErrNoRepo
}
func (f *fakeSources) CreateSource(_ context.Context, s store.ProjectSource) error {
	if _, ok := f.sources[s.ProjectKey]; ok {
		return store.ErrExists
	}
	if f.taken[s.ProjectKey] {
		return store.ErrNameTaken
	}
	f.sources[s.ProjectKey] = s
	return nil
}

// CreateSourceWith is CreateSource and AddSourceRepo together, neither
// when either fails.
func (f *fakeSources) CreateSourceWith(ctx context.Context, s store.ProjectSource, first store.SourceRepo) error {
	if err := f.CreateSource(ctx, s); err != nil {
		return err
	}
	if err := f.AddSourceRepo(ctx, first); err != nil {
		delete(f.sources, s.ProjectKey)
		return &store.FirstRepoError{Err: err}
	}
	return nil
}
func (f *fakeSources) UpdateSourceAccess(_ context.Context, key, host, auth, user string, enc []byte, set bool) (bool, error) {
	s, ok := f.sources[key]
	if !ok {
		return false, nil
	}
	s.Host, s.TokenAuth, s.TokenUser = host, auth, user
	if set {
		s.TokenEnc = enc
	}
	f.sources[key] = s
	return true, nil
}
func (f *fakeSources) DeleteSource(_ context.Context, key string) (bool, error) {
	_, ok := f.sources[key]
	delete(f.sources, key)
	delete(f.repos, key)
	return ok, nil
}
func (f *fakeSources) SourceRepos(_ context.Context, key string) ([]store.SourceRepo, error) {
	out := make([]store.SourceRepo, 0, len(f.repos[key]))
	for _, r := range f.repos[key] {
		out = append(out, f.withIDs(r))
	}
	return out, nil
}
func (f *fakeSources) AddSourceRepo(_ context.Context, r store.SourceRepo) error {
	// The store's order: the host first, then the slug's uniqueness.
	if err := store.OneHost(f.repos[r.ProjectKey], r.CloneURL); err != nil {
		return err
	}
	if e := f.cloned[r.CloneURL]; e != nil {
		return e
	}
	if f.taken[r.ProjectKey+"/"+r.Slug] {
		return store.ErrNameTaken
	}
	for _, have := range f.repos[r.ProjectKey] {
		if have.Slug == r.Slug {
			return store.ErrExists
		}
	}
	r.State, r.Origin = store.RepoPending, store.OriginManual
	f.repos[r.ProjectKey] = append(f.repos[r.ProjectKey], r)
	return nil
}
func (f *fakeSources) DeleteSourceRepo(_ context.Context, key, slug string) (bool, error) {
	kept := f.repos[key][:0]
	found := false
	for _, r := range f.repos[key] {
		if r.Slug == slug {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	f.repos[key] = kept
	return found, nil
}

// DeactivateSourceRepo and ActivateSourceRepo set the row's mark, as the
// store does; a run past queued refuses both, and a deactivation queues a
// departure run.
func (f *fakeSources) DeactivateSourceRepo(_ context.Context, key, slug string) (bool, error) {
	return true, f.setActive(key, slug, false)
}

func (f *fakeSources) ActivateSourceRepo(_ context.Context, key, slug string) error {
	return f.setActive(key, slug, true)
}

func (f *fakeSources) DropQueuedRuns(_ context.Context, _, slug string) error {
	if rr, ok := f.runs[slug]; ok && rr.Last.Open() && rr.Last.State != store.RunQueued {
		return store.ErrRunOpen
	}
	return nil
}

func (f *fakeSources) setActive(key, slug string, on bool) error {
	if f.activeErr != nil {
		return f.activeErr
	}
	if rr, ok := f.runs[slug]; ok && rr.Last.Open() && rr.Last.State != store.RunQueued {
		return store.ErrRunOpen
	}
	for i, r := range f.repos[key] {
		if r.Slug == slug {
			at := time.Now()
			r.DeactivatedAt = &at
			if on {
				r.DeactivatedAt, r.ReactivatedAt = nil, &at
			}
			f.repos[key][i] = r
			return nil
		}
	}
	return store.ErrNoRepo
}

func (f *fakeSources) RequestSync(_ context.Context, key, slug string) (int, error) {
	f.synced = append(f.synced, key+"/"+slug)
	return len(f.repos[key]), nil
}
func (f *fakeSources) ProjectSpelledOtherwise(_ context.Context, key string) (string, bool, error) {
	for _, known := range f.noerglerKeys {
		if strings.EqualFold(known, key) && known != key {
			return known, true, nil
		}
	}
	return "", false, nil
}
func (f *fakeSources) SourceRuns(_ context.Context, key string) (map[string]store.RepoRuns, error) {
	out := map[string]store.RepoRuns{}
	for slug, rr := range f.runs {
		if rr.Last.ProjectKey == key {
			out[slug] = rr
		}
	}
	return out, nil
}
func (f *fakeSources) DocumentedRun(_ context.Context, key, slug string) (store.BaselineRun, bool, error) {
	rr, ok := f.runs[slug]
	if !ok || rr.Documented == "" || rr.Last.ProjectKey != key {
		return store.BaselineRun{}, false, nil
	}
	return store.BaselineRun{Commit: rr.Documented, State: store.RunDone}, true, nil
}
func (f *fakeSources) EnqueueRun(_ context.Context, key, slug, mode, trigger, base, commit string) (int64, error) {
	if f.enqueueErr != nil {
		return 0, f.enqueueErr
	}
	f.enqueued = append(f.enqueued, strings.Join([]string{key, slug, mode, trigger, base, commit}, " "))
	return int64(len(f.enqueued)), nil
}

// EnqueueRestyle records "key slug restyle commit epoch byHand" beside the runs.
func (f *fakeSources) EnqueueRestyle(_ context.Context, key, slug, commit string, epoch int, byHand bool) (int64, error) {
	if f.enqueueErr != nil {
		return 0, f.enqueueErr
	}
	f.enqueued = append(f.enqueued, strings.Join([]string{key, slug, store.RunRestyle, commit, fmt.Sprint(epoch), fmt.Sprint(byHand)}, " "))
	return int64(len(f.enqueued)), nil
}

// SetRunReason records why run id was queued; reasonErr fails it.
func (f *fakeSources) SetRunReason(_ context.Context, id int64, r store.RunReason) error {
	if f.reasons == nil {
		f.reasons = map[int64]store.RunReason{}
	}
	f.reasons[id] = r
	return f.reasonErr
}

type fakeNudger struct{ nudges int }

func (n *fakeNudger) Nudge() { n.nudges++ }

// fakeCheckouts names folders by ids, as the poller does.
// fakeCheckouts records the folders removed; removeErr fails a repository's.
type fakeCheckouts struct {
	removed   []string
	removeErr error
}

func (c *fakeCheckouts) Dir(projectID, repoID int64) (string, error) {
	return fmt.Sprintf("/base/p%d/r%d", projectID, repoID), nil
}
func (c *fakeCheckouts) ProjectDir(projectID int64) (string, error) {
	return fmt.Sprintf("/base/p%d", projectID), nil
}
func (c *fakeCheckouts) Remove(projectID, repoID int64) error {
	if c.removeErr != nil {
		return c.removeErr
	}
	c.removed = append(c.removed, fmt.Sprintf("p%d/r%d", projectID, repoID))
	return nil
}
func (c *fakeCheckouts) RemoveProject(projectID int64) error {
	c.removed = append(c.removed, fmt.Sprintf("p%d", projectID))
	return nil
}

func sourcesHandler(t *testing.T, fs *fakeSources, withKey bool) (http.Handler, *fakeNudger, *fakeCheckouts) {
	t.Helper()
	h, nudger, checkouts := sourcesHandlerWith(t, fs, newFakeStore(), &fakeWaker{}, withKey)
	return h, nudger, checkouts
}

// sourcesHandlerWith is sourcesHandler with the wiki's store and the
// worker's waker given, for the routes where the two features meet.
func sourcesHandlerWith(t *testing.T, fs *fakeSources, st *fakeStore, w *fakeWaker, withKey bool) (http.Handler, *fakeNudger, *fakeCheckouts) {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	var key secret.Key
	if withKey {
		var err error
		key, err = secret.Parse(base64.StdEncoding.EncodeToString(make([]byte, secret.KeySize)))
		if err != nil {
			t.Fatal(err)
		}
	}
	nudger, checkouts := &fakeNudger{}, &fakeCheckouts{}
	Register(srv, Deps{Store: st, Keys: keysFile(t), Worker: w, AuthMode: config.AuthNone, ProblemWindow: 90, Log: discard,
		Sources: &SourcesDeps{Store: fs, Poller: nudger, Git: checkouts, Key: key, DefaultHost: "https://forge.example.invalid", MaxCalls: 16}})
	return srv.Handler(), nudger, checkouts
}

var jsonHdr = map[string]string{"Content-Type": "application/json"}

func TestSources_Disabled(t *testing.T) {
	h := handler(t, newFakeStore(), &fakeWaker{}, config.AuthNone)
	code, body := do(t, h, http.MethodGet, "/api/sources", "", "", nil)
	if code != 200 || string(body["enabled"]) != "false" || string(body["sources"]) != "[]" {
		t.Fatalf("GET disabled: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"X","kind":"static"}`, jsonHdr)
	if code != 404 || !strings.Contains(field(t, body, "error"), "BACKEND_REPOS_DIR") {
		t.Fatalf("POST disabled: %d %v", code, body)
	}
}

func TestSources_CreateValidatesAndSeals(t *testing.T) {
	fs := newFakeSources()
	h, nudger, _ := sourcesHandler(t, fs, true)

	for _, c := range []struct{ body, want string }{
		{`{"project_key":"..","kind":"static"}`, "project_key"},
		{`{"project_key":"X","kind":"svn"}`, "kind"},
		{`{"project_key":"X","kind":"bitbucket"}`, "host"},
		{`{"project_key":"X","kind":"bitbucket","host":"http://forge.example.invalid"}`, "host"},
		{`{"project_key":"X","kind":"bitbucket","host":"https://forge.example.invalid?"}`, "host"},
		{`{"project_key":"X","kind":"static","host":"https://x"}`, "host"},
		{`{"project_key":"X","kind":"static","token_auth":"digest"}`, "token_auth"},
		{`{"project_key":"X","kind":"bitbucket","host":"https://x","token_user":"me"}`, "token_user"},
	} {
		code, body := do(t, h, http.MethodPost, "/api/sources", "", c.body, jsonHdr)
		if code != 422 || !strings.Contains(field(t, body, "error"), c.want) {
			t.Errorf("%s: %d %v, want 422 naming %s", c.body, code, body, c.want)
		}
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"X","kind":"static"}`, nil); code != 415 {
		t.Fatalf("no content type: %d", code)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `nope`, jsonHdr); code != 400 {
		t.Fatalf("not json: %d", code)
	}

	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid/","token":"pat"}`, jsonHdr)
	if code != 201 {
		t.Fatalf("create bitbucket: %d %v", code, body)
	}
	if field(t, body, "host") != "https://forge.example.invalid" || field(t, body, "token_auth") != "bearer" || string(body["has_token"]) != "true" {
		t.Fatalf("created body: %v", body)
	}
	if _, leaked := body["token"]; leaked {
		t.Fatal("token in the response")
	}
	src := fs.sources["SHOP"]
	if !src.HasToken() || strings.Contains(string(src.TokenEnc), "pat") {
		t.Fatalf("stored token not sealed: %q", src.TokenEnc)
	}
	if nudger.nudges != 1 {
		t.Fatalf("nudges = %d", nudger.nudges)
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"SHOP","kind":"static"}`, jsonHdr)
	if code != 409 {
		t.Fatalf("duplicate: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static"}`, jsonHdr)
	if code != 201 || field(t, body, "token_auth") != "basic" || string(body["has_token"]) != "false" {
		t.Fatalf("create static: %d %v", code, body)
	}

	code, body = do(t, h, http.MethodGet, "/api/sources", "", "", nil)
	if code != 200 || string(body["enabled"]) != "true" || field(t, body, "default_host") != "https://forge.example.invalid" {
		t.Fatalf("list: %d %v", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body["sources"], &list); err != nil || len(list) != 2 {
		t.Fatalf("list sources: %v %v", err, string(body["sources"]))
	}
}

// A Bitbucket source keeps its Bitbucket project's key: another key is a
// 422 and leaves it as it was; its case may change, the one fix for a key
// typed otherwise than noergler spells it; the key sent back unchanged with
// an access change is no rename.
func TestSources_RenameBitbucketRefused(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, false)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"shop","kind":"bitbucket","host":"https://forge.example.invalid"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	code, body := do(t, h, http.MethodPatch, "/api/sources/shop", "", `{"project_key":"OTHER"}`, jsonHdr)
	if code != 422 || !strings.Contains(fmt.Sprint(body), "Bitbucket") {
		t.Errorf("rename to another key: %d %v, want 422", code, body)
	}
	if _, ok := fs.sources["shop"]; !ok {
		t.Fatal("a refused rename renamed")
	}
	if code, body := do(t, h, http.MethodPatch, "/api/sources/shop", "", `{"project_key":"SHOP"}`, jsonHdr); code != 200 || field(t, body, "project_key") != "SHOP" {
		t.Errorf("case-only rename: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPatch, "/api/sources/SHOP", "", `{"project_key":"SHOP","host":"https://forge2.example.invalid"}`, jsonHdr); code != 200 || fs.sources["SHOP"].Host != "https://forge2.example.invalid" {
		t.Errorf("unchanged key with a host change: %d %v", code, body)
	}
}

// A Bitbucket source's key names a Bitbucket project: a key Bitbucket never
// makes is refused at create, and one noergler delivers in another case is
// taken in noergler's spelling, not refused (the form cannot type another).
func TestSources_CreateBitbucketKey(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, false)
	for _, key := range []string{"my.shop", "1SHOP", "SH-OP"} {
		code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"`+key+`","kind":"bitbucket","host":"https://forge.example.invalid"}`, jsonHdr)
		if code != 422 || !strings.Contains(field(t, body, "error"), "Bitbucket project key") {
			t.Errorf("key %s: %d %v, want 422", key, code, body)
		}
	}
	fs.noerglerKeys = []string{"Shop"}
	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid"}`, jsonHdr)
	if code != 201 || field(t, body, "project_key") != "Shop" {
		t.Fatalf("noergler's spelling taken: %d %v", code, body)
	}
}

// A project key is renamed through the source, a change of case included;
// a hand-entered repository through its own PATCH. A name another had is a
// 409, a listed repository follows Bitbucket (422), an open run blocks.
func TestSources_Rename(t *testing.T) {
	fs := newFakeSources()
	h, nudger, _ := sourcesHandler(t, fs, false)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"ACME","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/ACME/repos", "", `{"slug":"cli","clone_url":"https://forge.example.invalid/cli.git"}`, jsonHdr); code != 201 {
		t.Fatal("add repo")
	}
	fs.repos["ACME"] = append(fs.repos["ACME"], store.SourceRepo{ProjectKey: "ACME", Slug: "ui", Origin: store.OriginListed, State: store.RepoReady,
		CloneURL: "https://forge.example.invalid/ui.git"})

	code, body := do(t, h, http.MethodPatch, "/api/sources/ACME", "", `{"project_key":"acme"}`, jsonHdr)
	if code != 200 || field(t, body, "project_key") != "acme" {
		t.Fatalf("case-only rename: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodGet, "/api/sources/acme", "", "", nil); code != 200 {
		t.Errorf("source under its new key: %d", code)
	}
	fs.taken["taken"] = true
	if code, body := do(t, h, http.MethodPatch, "/api/sources/acme", "", `{"project_key":"taken"}`, jsonHdr); code != 409 {
		t.Errorf("rename to a taken key: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodPatch, "/api/sources/acme", "", `{"project_key":"a b"}`, jsonHdr); code != 422 {
		t.Errorf("rename to a bad key: %d", code)
	}
	// A rename comes alone: never half a rename, half an access change.
	if code, _ := do(t, h, http.MethodPatch, "/api/sources/acme", "", `{"project_key":"ACME2","token_user":"me"}`, jsonHdr); code != 422 {
		t.Errorf("rename with another field: %d, want 422", code)
	}
	if _, ok := fs.sources["acme"]; !ok {
		t.Fatal("a refused rename renamed")
	}
	// The source sent back whole, its key unchanged, is an access update.
	if code, body := do(t, h, http.MethodPatch, "/api/sources/acme", "", `{"project_key":"acme","token_user":"me"}`, jsonHdr); code != 200 || fs.sources["acme"].TokenUser != "me" {
		t.Errorf("unchanged key with another field: %d %v", code, body)
	}
	// A key or slug another had is a 409 on create, not a 500.
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"taken","kind":"static"}`, jsonHdr); code != 409 {
		t.Errorf("new source under a taken key: %d, want 409", code)
	}
	fs.taken["acme/gone"] = true
	if code, _ := do(t, h, http.MethodPost, "/api/sources/acme/repos", "", `{"slug":"gone","clone_url":"https://forge.example.invalid/gone.git"}`, jsonHdr); code != 409 {
		t.Errorf("new repo under a taken slug: %d, want 409", code)
	}

	nudges := nudger.nudges
	code, body = do(t, h, http.MethodPatch, "/api/sources/acme/repos/cli", "", `{"slug":"core","clone_url":"https://forge.example.invalid/core.git"}`, jsonHdr)
	if code != 200 || nudger.nudges != nudges+1 {
		t.Fatalf("repo rename: %d %v nudges=%d", code, body, nudger.nudges)
	}
	if r, ok, _ := fs.SourceRepoByName(context.Background(), "acme", "core"); !ok || r.CloneURL != "https://forge.example.invalid/core.git" || r.State != store.RepoPending {
		t.Errorf("renamed repo: %+v %v", r, ok)
	}
	fs.taken["acme/other"] = true
	for _, c := range []struct {
		path, body string
		want       int
	}{
		{"/api/sources/acme/repos/core", `{"slug":"other"}`, 409},
		{"/api/sources/acme/repos/ui", `{"slug":"web"}`, 422},
		{"/api/sources/acme/repos/core", `{"slug":"x","clone_url":"git@forge:x.git"}`, 422},
		{"/api/sources/acme/repos/core", `{"slug":"../x"}`, 422},
		{"/api/sources/acme/repos/nope", `{"slug":"x"}`, 404},
	} {
		if code, body := do(t, h, http.MethodPatch, c.path, "", c.body, jsonHdr); code != c.want {
			t.Errorf("PATCH %s %s: %d %v, want %d", c.path, c.body, code, body, c.want)
		}
	}
	fs.runs = map[string]store.RepoRuns{"core": {Last: store.BaselineRun{ProjectKey: "acme", State: store.RunDescribing}}}
	if code, _ := do(t, h, http.MethodPatch, "/api/sources/acme/repos/core", "", `{"slug":"x","clone_url":"https://forge.example.invalid/x.git"}`, jsonHdr); code != 409 {
		t.Errorf("new clone URL with an open run: %d, want 409", code)
	}
}

func TestSources_TokenNeedsKey(t *testing.T) {
	h, _, _ := sourcesHandler(t, newFakeSources(), false)
	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static","token":"pat"}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "BACKEND_SOURCE_TOKEN_KEY") {
		t.Fatalf("token without key: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatalf("public source without key: %d", code)
	}
}

func TestSources_UpdateReposSyncDelete(t *testing.T) {
	fs := newFakeSources()
	h, nudger, checkouts := sourcesHandler(t, fs, true)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static","token":"pat"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid"}`, jsonHdr); code != 201 {
		t.Fatal("create bitbucket")
	}

	// PATCH: an omitted token keeps, "" clears, a value replaces.
	before := fs.sources["tools"].TokenEnc
	code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"token_user":"me"}`, jsonHdr)
	if code != 200 || field(t, body, "token_user") != "me" || string(fs.sources["tools"].TokenEnc) != string(before) {
		t.Fatalf("patch keep: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"token":""}`, jsonHdr)
	if code != 200 || string(body["has_token"]) != "false" {
		t.Fatalf("patch clear: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/SHOP", "", `{"host":""}`, jsonHdr)
	if code != 422 {
		t.Fatalf("patch bitbucket without host: %d %v", code, body)
	}
	if code, _ := do(t, h, http.MethodPatch, "/api/sources/nope", "", `{}`, jsonHdr); code != 404 {
		t.Fatal("patch unknown")
	}

	// Repos: static only, validated, 409 on a duplicate.
	code, body = do(t, h, http.MethodPost, "/api/sources/SHOP/repos", "", `{"slug":"ui","clone_url":"https://x/ui.git"}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "kind") {
		t.Fatalf("add repo to bitbucket: %d %v", code, body)
	}
	for _, c := range []struct{ body, want string }{
		{`{"slug":"..","clone_url":"https://x/a.git"}`, "slug"},
		{`{"slug":"a","clone_url":"http://x/a.git"}`, "clone_url"},
		{`{"slug":"a","clone_url":"https://u:p@x/a.git"}`, "clone_url"},
		{`{"slug":"a","clone_url":"ssh://git@x/a.git"}`, "clone_url"},
		{`{"slug":"a","clone_url":"https://x/a.git","branch":"-x"}`, "branch"},
	} {
		code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", c.body, jsonHdr)
		if code != 422 || !strings.Contains(field(t, body, "error"), c.want) {
			t.Errorf("%s: %d %v, want 422 naming %s", c.body, code, body, c.want)
		}
	}
	code, body = do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"cli","clone_url":"https://github.com/acme/cli.git","branch":"main"}`, jsonHdr)
	if code != 201 {
		t.Fatalf("add repo: %d %v", code, body)
	}
	var repos []map[string]any
	if err := json.Unmarshal(body["repos"], &repos); err != nil || len(repos) != 1 || repos[0]["state"] != "pending" || repos[0]["branch"] != "main" {
		t.Fatalf("repos after add: %v %s", err, body["repos"])
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"cli","clone_url":"https://github.com/acme/cli.git"}`, jsonHdr); code != 409 {
		t.Fatal("duplicate repo")
	}

	// Sync requests nudge; the detail carries the dir.
	nudges := nudger.nudges
	code, body = do(t, h, http.MethodPost, "/api/sources/tools/sync", "", "", nil)
	if code != 202 || field(t, body, "status") != "queued" || nudger.nudges != nudges+1 {
		t.Fatalf("sync source: %d %v nudges=%d", code, body, nudger.nudges)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/sync", "", "", nil); code != 202 {
		t.Fatal("sync repo")
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/nope/sync", "", "", nil); code != 404 {
		t.Fatal("sync of an unknown repo was not a 404")
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos/f%C3%BCr/sync", "", "", nil); code != 404 || body == nil {
		t.Fatalf("sync of a bad slug: %d %v, want a written 404", code, body)
	}
	if code, body := do(t, h, http.MethodDelete, "/api/sources/tools/repos/a%20b", "", "", nil); code != 404 || body == nil {
		t.Fatalf("delete of a bad slug: %d %v, want a written 404", code, body)
	}
	if len(fs.synced) != 2 || fs.synced[0] != "tools/" || fs.synced[1] != "tools/cli" {
		t.Fatalf("sync requests = %v", fs.synced)
	}
	code, body = do(t, h, http.MethodGet, "/api/sources/tools", "", "", nil)
	if code != 200 || field(t, body, "dir") != fmt.Sprintf("/base/p%d", fs.id("tools")) {
		t.Fatalf("detail: %d %v", code, body)
	}

	// Deletes remove the checkout, then the row; unknown is 404.
	if code, _ := do(t, h, http.MethodDelete, "/api/sources/tools/repos/cli", "", "", nil); code != 204 {
		t.Fatal("delete repo")
	}
	if code, _ := do(t, h, http.MethodDelete, "/api/sources/tools/repos/cli", "", "", nil); code != 404 {
		t.Fatal("delete repo twice")
	}
	if code, _ := do(t, h, http.MethodDelete, "/api/sources/tools", "", "", nil); code != 204 {
		t.Fatal("delete source")
	}
	if code, _ := do(t, h, http.MethodGet, "/api/sources/tools", "", "", nil); code != 404 {
		t.Fatal("deleted source still served")
	}
	// Folders by ids: the repository's, then the project's.
	if want := fmt.Sprintf("p%d/r%d,p%d", fs.id("tools"), fs.id("tools/cli"), fs.id("tools")); strings.Join(checkouts.removed, ",") != want {
		t.Fatalf("checkouts removed = %v", checkouts.removed)
	}
}

// Deactivation: the row first, then the clone, the detail back with the
// mark; a run past queued refuses it before the clone is touched.
// Activation clears the mark and nudges the poller.
func TestSources_DeactivateActivate(t *testing.T) {
	fs := newFakeSources()
	h, nudger, checkouts := sourcesHandler(t, fs, true)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"cli","clone_url":"https://github.com/acme/cli.git"}`, jsonHdr); code != 201 {
		t.Fatal("add repo")
	}
	deactivatedAt := func(body map[string]json.RawMessage) string {
		var repos []map[string]any
		if err := json.Unmarshal(body["repos"], &repos); err != nil || len(repos) != 1 {
			t.Fatalf("repos: %v %s", err, body["repos"])
		}
		v, ok := repos[0]["deactivated_at"]
		if !ok {
			t.Fatal("deactivated_at is not on the wire")
		}
		s, _ := v.(string)
		return s
	}

	fs.runs = map[string]store.RepoRuns{"cli": {Last: store.BaselineRun{State: store.RunDescribing}}}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/deactivate", "", "", nil); code != 409 {
		t.Fatalf("deactivate under a running run: %d, want 409", code)
	}
	if len(checkouts.removed) != 0 {
		t.Fatalf("a refused deactivation removed the clone: %v", checkouts.removed)
	}
	fs.runs = nil

	code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/deactivate", "", "", nil)
	if code != 200 || deactivatedAt(body) == "" {
		t.Fatalf("deactivate: %d %v", code, body)
	}
	// The folder before the row, and once more after it.
	if want := fmt.Sprintf("p%d/r%d", fs.id("tools"), fs.id("tools/cli")); strings.Join(checkouts.removed, ",") != want+","+want {
		t.Fatalf("checkouts removed = %v, want %s twice", checkouts.removed, want)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/nope/deactivate", "", "", nil); code != 404 {
		t.Fatal("deactivate of an unknown repo was not a 404")
	}

	nudges := nudger.nudges
	code, body = do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/activate", "", "", nil)
	if code != 200 || deactivatedAt(body) != "" || nudger.nudges != nudges+1 {
		t.Fatalf("activate: %d %v nudges=%d", code, body, nudger.nudges)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/nope/activate", "", "", nil); code != 404 {
		t.Fatal("activate of an unknown repo was not a 404")
	}

	// What the store says, as a status; a clone that would not go is a 500.
	for _, c := range []struct {
		err  error
		want int
	}{{store.ErrRunOpen, 409}, {store.ErrNoRepo, 404}, {errors.New("disk"), 500}} {
		fs.activeErr = c.err
		for _, action := range []string{"deactivate", "activate"} {
			if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/"+action, "", "", nil); code != c.want {
				t.Errorf("%s with %v: %d, want %d", action, c.err, code, c.want)
			}
		}
	}
	// A clone that would not go: a 500, the row untouched, so the button
	// tries again.
	fs.activeErr, checkouts.removeErr = nil, errors.New("busy disk")
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/cli/deactivate", "", "", nil); code != 500 {
		t.Errorf("a clone that would not go: %d, want 500", code)
	}
	if fs.repos["tools"][0].DeactivatedAt != nil {
		t.Error("a deactivation whose clone would not go deactivated the row")
	}
}

// With the sources off, the routes are 404 like every other sources route.
func TestSources_DeactivateOff(t *testing.T) {
	off := handler(t, newFakeStore(), &fakeWaker{}, config.AuthNone)
	for _, action := range []string{"deactivate", "activate"} {
		if code, _ := do(t, off, http.MethodPost, "/api/sources/tools/repos/cli/"+action, "", "", nil); code != 404 {
			t.Errorf("%s with the sources off: %d", action, code)
		}
	}
}

// A static folder holds one host, token or not, so its token reaches one
// host only; a folder from before the rule that spans several gets no
// token, and clearing one is never refused.
func TestSources_StaticFolderHoldsOneHost(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, true)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"a","clone_url":"https://github.com/acme/a.git"}`, jsonHdr); code != 201 {
		t.Fatalf("first repo: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"b","clone_url":"https://GitHub.COM/acme/b.git"}`, jsonHdr); code != 201 {
		t.Fatalf("same host in another case: %d %v", code, body)
	}
	for _, off := range []string{"https://gitlab.com/acme/c.git", "https://github.com:8443/acme/c.git", "https://github.com.evil.example/acme/c.git"} {
		code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"c","clone_url":"`+off+`"}`, jsonHdr)
		if code != 422 || !strings.HasPrefix(field(t, body, "error"), "clone_url:") || !strings.Contains(field(t, body, "error"), "https://github.com") {
			t.Errorf("%s: %d %v, want 422 naming clone_url and the folder's host", off, code, body)
		}
	}
	if code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"d","clone_url":"https://github.com:443/acme/d.git"}`, jsonHdr); code != 201 {
		t.Fatalf("same host on its default port: %d %v", code, body)
	}
	if len(fs.repos["tools"]) != 3 {
		t.Fatalf("rows after the refusals: %v", fs.repos["tools"])
	}
	if code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"token":"pat"}`, jsonHdr); code != 200 || string(body["has_token"]) != "true" {
		t.Fatalf("token on a one-host folder: %d %v", code, body)
	}

	// A folder from before the rule, on two hosts (a missing row counts):
	// an add names both hosts, and no new token or new way to send the
	// stored one.
	fs.repos["tools"] = append(fs.repos["tools"], store.SourceRepo{ProjectKey: "tools", Slug: "c", CloneURL: "https://gitlab.com/acme/c.git", State: store.RepoMissing})
	code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos", "", `{"slug":"e","clone_url":"https://github.com/acme/e.git"}`, jsonHdr)
	if msg := field(t, body, "error"); code != 422 || !strings.Contains(msg, "several hosts (https://github.com, https://gitlab.com)") {
		t.Fatalf("add to a mixed folder: %d %v", code, body)
	}
	for patch, name := range map[string]string{`{"token":"other"}`: "token:", `{"token_user":"me"}`: "token_user:", `{"token_auth":"bearer"}`: "token_auth:"} {
		code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", patch, jsonHdr)
		if msg := field(t, body, "error"); code != 422 || !strings.HasPrefix(msg, name) || !strings.Contains(msg, "https://gitlab.com") {
			t.Errorf("%s on a mixed folder with a token: %d %v, want 422 naming %s", patch, code, body, name)
		}
	}
	if code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"token":""}`, jsonHdr); code != 200 || string(body["has_token"]) != "false" {
		t.Fatalf("clearing the token of a mixed folder: %d %v", code, body)
	}
	if code, body := do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"token_user":"me","auto_describe":false}`, jsonHdr); code != 200 {
		t.Fatalf("a change without a token on a mixed folder: %d %v", code, body)
	}
}

// A map is expected with the sources on, for a project of more than one
// source repository and no more of them than one map holds; a project of
// one has no project page and so no map.
func TestProject_MapExpected(t *testing.T) {
	st := newFakeStore()
	st.teams["PLAT"] = ""
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	expected := func() string {
		_, body := do(t, h, http.MethodGet, "/api/projects/PLAT", "", "", nil)
		return string(body["map_expected"])
	}
	if got := expected(); got != "false" {
		t.Errorf("no source repository: map_expected = %s", got)
	}
	st.repos["PLAT"] = []store.RepoRow{{Slug: "files"}}
	if got := expected(); got != "false" {
		t.Errorf("a project of one: map_expected = %s", got)
	}
	st.repos["PLAT"] = []store.RepoRow{{Slug: "files"}, {Slug: "portal"}}
	if got := expected(); got != "true" {
		t.Errorf("two source repositories: map_expected = %s", got)
	}
	// The sentinel: asked for, none accepted. No map, declined, still
	// expected.
	st.pages[store.ScopeProject+"|PLAT|"] = store.Page{RevisionID: 1, Revision: 1, Summary: "P.", Map: diagram.Sentinel()}
	_, body := do(t, h, http.MethodGet, "/api/projects/PLAT", "", "", nil)
	if string(body["map"]) != "null" || string(body["map_declined"]) != "true" || string(body["map_expected"]) != "true" {
		t.Errorf("sentinel: map %s, declined %s, expected %s", body["map"], body["map_declined"], body["map_expected"])
	}
	// A collection draws none, and says what it was judged.
	st.pages[store.ScopeProject+"|PLAT|"] = store.Page{RevisionID: 1, Revision: 1, Summary: "P.", Shape: store.ShapeCollection}
	_, body = do(t, h, http.MethodGet, "/api/projects/PLAT", "", "", nil)
	if string(body["map_expected"]) != "false" || string(body["shape"]) != `"collection"` {
		t.Errorf("collection: expected %s, shape %s", body["map_expected"], body["shape"])
	}
	delete(st.pages, store.ScopeProject+"|PLAT|")
	_, body = do(t, h, http.MethodGet, "/api/projects/PLAT", "", "", nil)
	if string(body["map_declined"]) != "false" || string(body["shape"]) != "null" {
		t.Errorf("no page: map_declined = %s, shape %s", body["map_declined"], body["shape"])
	}
	st.repos["PLAT"] = nil
	for i := range diagram.MapMaxNodes {
		st.repos["PLAT"] = append(st.repos["PLAT"], store.RepoRow{Slug: fmt.Sprintf("r%02d", i)})
	}
	if got := expected(); got != "false" {
		t.Errorf("%d source repositories: map_expected = %s, want false, too many for a map", diagram.MapMaxNodes, got)
	}
}
