package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The sources routes: the first writes beside the webhook. Gated like the
// read routes, by the session in oidc mode and by nobody in none; the
// reader is logged and decides nothing. A token is sealed here and never
// read back out: the body carries has_token, never the token.

// SourceStore is what the sources routes need of the store.
type SourceStore interface {
	Sources(ctx context.Context) ([]store.SourceRow, error)
	SourceByKey(ctx context.Context, key string) (store.ProjectSource, bool, error)
	CreateSource(ctx context.Context, src store.ProjectSource) error
	UpdateSourceAccess(ctx context.Context, key, host, tokenAuth, tokenUser string, tokenEnc []byte, setToken bool) (bool, error)
	DeleteSource(ctx context.Context, key string) (bool, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	AddSourceRepo(ctx context.Context, r store.SourceRepo) error
	DeleteSourceRepo(ctx context.Context, key, slug string) (bool, error)
	RequestSync(ctx context.Context, key, slug string) (int, error)
	SetAutoDescribe(ctx context.Context, key string, on bool) (bool, error)
	ProjectSpelledOtherwise(ctx context.Context, key string) (string, bool, error)
	SourceRuns(ctx context.Context, key string) (map[string]store.RepoRuns, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	EnqueueRun(ctx context.Context, key, slug, mode, trigger, baseCommit, commit string) (int64, error)
}

// Nudger starts the next sync cycle now.
type Nudger interface{ Nudge() }

// Checkouts is what the routes need of gitrepo: where a clone lives and
// how it goes.
type Checkouts interface {
	Dir(key, slug string) (string, error)
	ProjectDir(key string) (string, error)
	Remove(key, slug string) error
	RemoveProject(key string) error
}

// SourcesDeps is the clone feature as the routes see it; nil in Deps means
// the feature is off. Git is the poller in production: removals go through
// its lock so a sync in flight cannot recreate a folder the row has lost.
type SourcesDeps struct {
	Store  SourceStore
	Poller Nudger
	Git    Checkouts
	Key    secret.Key
	sizes  sizeCache
}

// sizeCache remembers a project folder's size for a short while: the
// source page polls every three seconds while a clone runs, and a full
// walk of every clone per poll would compete with the clone for the disk.
type sizeCache struct {
	mu   sync.Mutex
	seen map[string]sizedAt
}

type sizedAt struct {
	bytes int64
	at    time.Time
}

const sizeTTL = 30 * time.Second

func (c *sizeCache) size(dir string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]sizedAt{}
	}
	if s, ok := c.seen[dir]; ok && time.Since(s.at) < sizeTTL {
		return s.bytes
	}
	n := gitrepo.DirSize(dir)
	c.seen[dir] = sizedAt{bytes: n, at: time.Now()}
	return n
}

// maxSourceBody bounds a sources write: a few fields.
const maxSourceBody = 16 << 10

func (d Deps) registerSources(s *httpapi.Server) {
	s.HandleFunc("GET /api/sources", d.sources)
	s.HandleFunc("POST /api/sources", d.createSource)
	s.HandleFunc("GET /api/sources/{key}", d.source)
	s.HandleFunc("PATCH /api/sources/{key}", d.updateSource)
	s.HandleFunc("DELETE /api/sources/{key}", d.deleteSource)
	s.HandleFunc("POST /api/sources/{key}/sync", d.syncSource)
	s.HandleFunc("POST /api/sources/{key}/repos", d.addRepo)
	s.HandleFunc("DELETE /api/sources/{key}/repos/{slug}", d.deleteRepo)
	s.HandleFunc("POST /api/sources/{key}/repos/{slug}/sync", d.syncRepo)
	s.HandleFunc("POST /api/sources/{key}/repos/{slug}/describe", d.describeRepo)
}

// enabled answers the 404 of a switched-off feature itself.
func (d Deps) enabled(w http.ResponseWriter) bool {
	if d.Sources == nil {
		httpx.WriteError(w, http.StatusNotFound, "sources are not configured (BACKEND_REPOS_DIR)")
		return false
	}
	return true
}

// jsonBody decodes a write's body: JSON content type required (a cross-site
// form cannot send one without a preflight) and bounded.
func jsonBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	return httpx.DecodeJSON(w, r, dst, maxSourceBody)
}

type sourceSummaryBody struct {
	ProjectKey   string     `json:"project_key"`
	Kind         string     `json:"kind"`
	Host         string     `json:"host"`
	HasToken     bool       `json:"has_token"`
	TokenAuth    string     `json:"token_auth"`
	TokenUser    string     `json:"token_user"`
	Repos        int        `json:"repos"`
	Ready        int        `json:"ready"`
	Active       int        `json:"active"`
	Failed       int        `json:"failed"`
	Missing      int        `json:"missing"`
	LastListedAt *time.Time `json:"last_listed_at"`
	LastError    string     `json:"last_error"`
	AutoDescribe bool       `json:"auto_describe"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type sourcesBody struct {
	Enabled bool                `json:"enabled"`
	Sources []sourceSummaryBody `json:"sources"`
}

func (d Deps) sources(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	out := sourcesBody{Enabled: d.Sources != nil, Sources: []sourceSummaryBody{}}
	if d.Sources == nil {
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	rows, err := d.Sources.Store.Sources(r.Context())
	if err != nil {
		httpx.ServerError(w, r, err, "sources")
		return
	}
	for _, s := range rows {
		out.Sources = append(out.Sources, sourceSummaryBody{ProjectKey: s.ProjectKey, Kind: s.Kind, Host: s.Host, HasToken: s.HasToken(),
			TokenAuth: s.TokenAuth, TokenUser: s.TokenUser, Repos: s.Repos, Ready: s.Ready, Active: s.Active, Failed: s.Failed, Missing: s.Missing,
			LastListedAt: s.LastListedAt, LastError: s.LastError, AutoDescribe: s.AutoDescribe, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type sourceRepoBody struct {
	Slug         string     `json:"slug"`
	CloneURL     string     `json:"clone_url"`
	Origin       string     `json:"origin"`
	Branch       string     `json:"branch"`
	HeadCommit   string     `json:"head_commit"`
	State        string     `json:"state"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	// Baseline is the newest run and the commit last documented, null
	// before the first run.
	Baseline *baselineBody `json:"baseline"`
}

// baselineBody is what the source page shows of a repository's runs.
type baselineBody struct {
	RunID            int64      `json:"run_id"`
	State            string     `json:"state"`
	Mode             string     `json:"mode"`
	Trigger          string     `json:"trigger"`
	Commit           string     `json:"commit"`
	Step             int        `json:"step"`
	Steps            int        `json:"steps"`
	Skipped          int        `json:"skipped"`
	CostUSD          string     `json:"cost_usd,omitempty"`
	Error            string     `json:"error"`
	QueuedAt         time.Time  `json:"queued_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	DocumentedCommit string     `json:"documented_commit"`
	DocumentedAt     *time.Time `json:"documented_at"`
}

func baselineOf(rr store.RepoRuns) *baselineBody {
	r := rr.Last
	return &baselineBody{RunID: r.ID, State: r.State, Mode: r.Mode, Trigger: r.Trigger, Commit: r.Commit, Step: r.Step, Steps: r.Steps,
		Skipped: r.Skipped, CostUSD: usd(rr.LastCost), Error: r.Error, QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		DocumentedCommit: rr.Documented, DocumentedAt: rr.DocumentedAt}
}

type sourceDetailBody struct {
	ProjectKey   string           `json:"project_key"`
	Kind         string           `json:"kind"`
	Host         string           `json:"host"`
	HasToken     bool             `json:"has_token"`
	TokenAuth    string           `json:"token_auth"`
	TokenUser    string           `json:"token_user"`
	Dir          string           `json:"dir"`
	DiskBytes    int64            `json:"disk_bytes"`
	LastListedAt *time.Time       `json:"last_listed_at"`
	LastError    string           `json:"last_error"`
	AutoDescribe bool             `json:"auto_describe"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Repos        []sourceRepoBody `json:"repos"`
}

func (d Deps) sourceDetail(ctx context.Context, src store.ProjectSource) (sourceDetailBody, error) {
	repos, err := d.Sources.Store.SourceRepos(ctx, src.ProjectKey)
	if err != nil {
		return sourceDetailBody{}, err
	}
	runs, err := d.Sources.Store.SourceRuns(ctx, src.ProjectKey)
	if err != nil {
		return sourceDetailBody{}, err
	}
	out := sourceDetailBody{ProjectKey: src.ProjectKey, Kind: src.Kind, Host: src.Host, HasToken: src.HasToken(), TokenAuth: src.TokenAuth,
		TokenUser: src.TokenUser, LastListedAt: src.LastListedAt, LastError: src.LastError, AutoDescribe: src.AutoDescribe,
		CreatedAt: src.CreatedAt, UpdatedAt: src.UpdatedAt, Repos: make([]sourceRepoBody, 0, len(repos))}
	if dir, err := d.Sources.Git.ProjectDir(src.ProjectKey); err == nil {
		out.Dir, out.DiskBytes = dir, d.Sources.sizes.size(dir)
	}
	for _, rp := range repos {
		body := sourceRepoBody{Slug: rp.Slug, CloneURL: rp.CloneURL, Origin: rp.Origin, Branch: rp.Branch, HeadCommit: rp.HeadCommit,
			State: rp.State, LastSyncedAt: rp.LastSyncedAt, LastError: rp.LastError, CreatedAt: rp.CreatedAt}
		if rr, ok := runs[rp.Slug]; ok {
			body.Baseline = baselineOf(rr)
		}
		out.Repos = append(out.Repos, body)
	}
	return out, nil
}

// lookupSource is the source of the path's key, or "" with the 404 written.
func (d Deps) lookupSource(w http.ResponseWriter, r *http.Request) (store.ProjectSource, bool) {
	key := r.PathValue("key")
	if !gitrepo.ValidSegment(key) {
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return store.ProjectSource{}, false
	}
	src, ok, err := d.Sources.Store.SourceByKey(r.Context(), key)
	if err != nil {
		httpx.ServerError(w, r, err, "source")
		return store.ProjectSource{}, false
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return store.ProjectSource{}, false
	}
	return src, true
}

func (d Deps) source(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	out, err := d.sourceDetail(r.Context(), src)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createSourceBody struct {
	ProjectKey string `json:"project_key"`
	Kind       string `json:"kind"`
	Host       string `json:"host"`
	Token      string `json:"token"`
	TokenAuth  string `json:"token_auth"`
	TokenUser  string `json:"token_user"`
	// AutoDescribe omitted is on, stored with the source in one write.
	AutoDescribe *bool `json:"auto_describe"`
}

// validHost is an http(s) URL with a host and nothing after the path.
func validHost(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

// validCloneURL is https with a host and no credentials: the token travels
// per command, never in a stored URL.
func validCloneURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.ContainsAny(raw, " \t\n")
}

// access validates and normalises the token settings of a create or an
// update; the message names the field.
func access(kind, tokenAuth, tokenUser string) (auth, user, problem string) {
	auth = strings.ToLower(strings.TrimSpace(tokenAuth))
	user = strings.TrimSpace(tokenUser)
	if auth == "" {
		auth = store.TokenBasic
		if kind == store.SourceBitbucket {
			auth = store.TokenBearer
		}
	}
	if auth != store.TokenBearer && auth != store.TokenBasic {
		return "", "", "token_auth: bearer or basic"
	}
	if auth == store.TokenBearer && user != "" {
		return "", "", "token_user: bearer sends no username, drop it"
	}
	return auth, user, ""
}

// seal encrypts a token for the store; without a key it names the variable.
func (d Deps) seal(token string) ([]byte, string) {
	if token == "" {
		return nil, ""
	}
	if !d.Sources.Key.Enabled() {
		return nil, "token: BACKEND_SECRET_KEY is not set, no token can be stored"
	}
	sealed, err := d.Sources.Key.Seal(token)
	if err != nil {
		return nil, "token: " + err.Error()
	}
	return sealed, ""
}

func (d Deps) createSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	var in createSourceBody
	if !jsonBody(w, r, &in) {
		return
	}
	in.ProjectKey, in.Kind, in.Host = strings.TrimSpace(in.ProjectKey), strings.ToLower(strings.TrimSpace(in.Kind)), strings.TrimRight(strings.TrimSpace(in.Host), "/")
	switch {
	case !gitrepo.ValidSegment(in.ProjectKey):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "project_key: letters, digits, dot, dash, underscore, tilde; not . or ..")
		return
	case in.Kind != store.SourceBitbucket && in.Kind != store.SourceStatic:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "kind: bitbucket or static")
		return
	case in.Kind == store.SourceBitbucket && !validHost(in.Host):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "host: an https URL is required for a Bitbucket source")
		return
	case in.Kind == store.SourceStatic && in.Host != "":
		httpx.WriteError(w, http.StatusUnprocessableEntity, "host: a static source has none")
		return
	}
	auth, user, problem := access(in.Kind, in.TokenAuth, in.TokenUser)
	if problem != "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, problem)
		return
	}
	sealed, problem := d.seal(strings.TrimSpace(in.Token))
	if problem != "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, problem)
		return
	}
	// The key is how noergler's notes find the source, exactly: a key that
	// differs from one noergler delivers only by case would have every note
	// of the project ignored.
	known, clash, err := d.Sources.Store.ProjectSpelledOtherwise(ctx, in.ProjectKey)
	if err != nil {
		httpx.ServerError(w, r, err, "project key")
		return
	}
	if clash {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "project_key: noergler delivers this project as "+known+"; use that spelling")
		return
	}
	src := store.ProjectSource{ProjectKey: in.ProjectKey, Kind: in.Kind, Host: in.Host, TokenAuth: auth, TokenUser: user, TokenEnc: sealed,
		AutoDescribe: in.AutoDescribe == nil || *in.AutoDescribe}
	if err := d.Sources.Store.CreateSource(ctx, src); err != nil {
		if errors.Is(err, store.ErrExists) {
			httpx.WriteError(w, http.StatusConflict, "a source with this project_key exists")
			return
		}
		httpx.ServerError(w, r, err, "create source")
		return
	}
	d.Log.InfoContext(ctx, "source_created", "project", src.ProjectKey, "kind", src.Kind, "has_token", src.HasToken(),
		"auto_describe", src.AutoDescribe, "user", d.user(r))
	d.Sources.Poller.Nudge()
	stored, found, err := d.Sources.Store.SourceByKey(ctx, src.ProjectKey)
	if err != nil {
		httpx.ServerError(w, r, err, "source")
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return
	}
	out, err := d.sourceDetail(ctx, stored)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

type updateSourceBody struct {
	Host         *string `json:"host"`
	Token        *string `json:"token"`
	TokenAuth    *string `json:"token_auth"`
	TokenUser    *string `json:"token_user"`
	AutoDescribe *bool   `json:"auto_describe"`
}

func (d Deps) updateSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	var in updateSourceBody
	if !jsonBody(w, r, &in) {
		return
	}
	host, tokenAuth, tokenUser := src.Host, src.TokenAuth, src.TokenUser
	if in.Host != nil {
		host = strings.TrimRight(strings.TrimSpace(*in.Host), "/")
	}
	if in.TokenAuth != nil {
		tokenAuth = *in.TokenAuth
	}
	if in.TokenUser != nil {
		tokenUser = *in.TokenUser
	}
	switch {
	case src.Kind == store.SourceBitbucket && !validHost(host):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "host: an https URL is required for a Bitbucket source")
		return
	case src.Kind == store.SourceStatic && host != "":
		httpx.WriteError(w, http.StatusUnprocessableEntity, "host: a static source has none")
		return
	}
	auth, user, problem := access(src.Kind, tokenAuth, tokenUser)
	if problem != "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, problem)
		return
	}
	var sealed []byte
	setToken := in.Token != nil
	if setToken {
		sealed, problem = d.seal(strings.TrimSpace(*in.Token))
		if problem != "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, problem)
			return
		}
	}
	updated, err := d.Sources.Store.UpdateSourceAccess(ctx, src.ProjectKey, host, auth, user, sealed, setToken)
	if err != nil {
		httpx.ServerError(w, r, err, "update source")
		return
	}
	if !updated {
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return
	}
	if in.AutoDescribe != nil {
		if _, err := d.Sources.Store.SetAutoDescribe(ctx, src.ProjectKey, *in.AutoDescribe); err != nil {
			httpx.ServerError(w, r, err, "auto describe")
			return
		}
	}
	d.Log.InfoContext(ctx, "source_updated", "project", src.ProjectKey, "token_changed", setToken, "user", d.user(r))
	d.Sources.Poller.Nudge()
	stored, found, err := d.Sources.Store.SourceByKey(ctx, src.ProjectKey)
	if err != nil {
		httpx.ServerError(w, r, err, "source")
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return
	}
	out, err := d.sourceDetail(ctx, stored)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// deleteSource removes the folder first, then the rows: a folder left
// behind by a failed row delete is re-cloned by the next cycle, which is
// harmless; rows gone with the folder still there would be an orphan.
func (d Deps) deleteSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	if err := d.Sources.Git.RemoveProject(src.ProjectKey); err != nil {
		httpx.ServerError(w, r, err, "remove clones")
		return
	}
	if _, err := d.Sources.Store.DeleteSource(ctx, src.ProjectKey); err != nil {
		httpx.ServerError(w, r, err, "delete source")
		return
	}
	d.Log.InfoContext(ctx, "source_deleted", "project", src.ProjectKey, "user", d.user(r))
	w.WriteHeader(http.StatusNoContent)
}

// hasRepo is whether the source has the slug, or false with the 404 (or
// 500) written. The slug is validated by the caller.
func (d Deps) hasRepo(w http.ResponseWriter, r *http.Request, key, slug string) bool {
	repos, err := d.Sources.Store.SourceRepos(r.Context(), key)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return false
	}
	for _, rp := range repos {
		if rp.Slug == slug {
			return true
		}
	}
	httpx.WriteError(w, http.StatusNotFound, "unknown repository")
	return false
}

type queuedBody struct {
	Status string `json:"status"`
	Repos  int    `json:"repos"`
}

func (d Deps) syncSource(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	n, err := d.Sources.Store.RequestSync(r.Context(), src.ProjectKey, "")
	if err != nil {
		httpx.ServerError(w, r, err, "request sync")
		return
	}
	d.Sources.Poller.Nudge()
	httpx.WriteJSON(w, http.StatusAccepted, queuedBody{Status: "queued", Repos: n})
}

type addRepoBody struct {
	Slug     string `json:"slug"`
	CloneURL string `json:"clone_url"`
	Branch   string `json:"branch"`
}

func (d Deps) addRepo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	if src.Kind != store.SourceStatic {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "kind: a Bitbucket source lists its repositories itself")
		return
	}
	var in addRepoBody
	if !jsonBody(w, r, &in) {
		return
	}
	in.Slug, in.CloneURL, in.Branch = strings.TrimSpace(in.Slug), strings.TrimSpace(in.CloneURL), strings.TrimSpace(in.Branch)
	switch {
	case !gitrepo.ValidSegment(in.Slug):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "slug: letters, digits, dot, dash, underscore; not . or ..")
		return
	case !validCloneURL(in.CloneURL):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: an https URL without credentials")
		return
	case strings.ContainsAny(in.Branch, " \t\n") || strings.HasPrefix(in.Branch, "-"):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "branch: not a branch name")
		return
	}
	err := d.Sources.Store.AddSourceRepo(ctx, store.SourceRepo{ProjectKey: src.ProjectKey, Slug: in.Slug, CloneURL: in.CloneURL, Branch: in.Branch})
	if errors.Is(err, store.ErrExists) {
		httpx.WriteError(w, http.StatusConflict, "a repository with this slug exists")
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err, "add repo")
		return
	}
	d.Log.InfoContext(ctx, "source_repo_added", "project", src.ProjectKey, "repo", in.Slug, "user", d.user(r))
	d.Sources.Poller.Nudge()
	out, err := d.sourceDetail(ctx, src)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (d Deps) deleteRepo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if !gitrepo.ValidSegment(slug) {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	if !d.hasRepo(w, r, src.ProjectKey, slug) {
		return
	}
	// The clone first, then the row (deleteSource's reason).
	if err := d.Sources.Git.Remove(src.ProjectKey, slug); err != nil {
		httpx.ServerError(w, r, err, "remove clone")
		return
	}
	if _, err := d.Sources.Store.DeleteSourceRepo(ctx, src.ProjectKey, slug); err != nil {
		httpx.ServerError(w, r, err, "delete repo")
		return
	}
	d.Log.InfoContext(ctx, "source_repo_deleted", "project", src.ProjectKey, "repo", slug, "user", d.user(r))
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) syncRepo(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if !gitrepo.ValidSegment(slug) {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	if !d.hasRepo(w, r, src.ProjectKey, slug) {
		return
	}
	n, err := d.Sources.Store.RequestSync(r.Context(), src.ProjectKey, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "request sync")
		return
	}
	d.Sources.Poller.Nudge()
	httpx.WriteJSON(w, http.StatusAccepted, queuedBody{Status: "queued", Repos: n})
}

type describeBody struct {
	// Mode is reconcile (the default: read what changed since the commit
	// last documented, or everything before the first run) or full (read
	// the repository afresh with a new catalogue).
	Mode string `json:"mode"`
}

type describedBody struct {
	Status string `json:"status"`
	RunID  int64  `json:"run_id"`
	Mode   string `json:"mode"`
}

// describeRepo queues a baseline run of one repository at its clone's head
// and wakes the worker. One open run per repository: a second is a 409.
func (d Deps) describeRepo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if !gitrepo.ValidSegment(slug) {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	var in describeBody
	if !jsonBody(w, r, &in) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode == "" {
		mode = store.RunReconcile
	}
	if mode != store.RunReconcile && mode != store.RunFull {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "mode: reconcile or full")
		return
	}
	repos, err := d.Sources.Store.SourceRepos(ctx, src.ProjectKey)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	var head string
	found := false
	for _, rp := range repos {
		if rp.Slug == slug {
			found, head = true, rp.HeadCommit
		}
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	if head == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "the clone has no head yet: describe it once it is synced")
		return
	}
	base := ""
	if mode == store.RunReconcile {
		doc, ok, err := d.Sources.Store.DocumentedRun(ctx, src.ProjectKey, slug)
		if err != nil {
			httpx.ServerError(w, r, err, "documented run")
			return
		}
		if ok {
			base = doc.Commit
		} else {
			mode = store.RunFull
		}
	}
	id, err := d.Sources.Store.EnqueueRun(ctx, src.ProjectKey, slug, mode, store.TriggerManual, base, head)
	switch {
	case errors.Is(err, store.ErrExists):
		httpx.WriteError(w, http.StatusConflict, "a run of this repository is open")
		return
	case errors.Is(err, store.ErrNoRepo):
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	case err != nil:
		httpx.ServerError(w, r, err, "enqueue run")
		return
	}
	d.Log.InfoContext(ctx, "baseline_queued", "project", src.ProjectKey, "repo", slug, "run_id", id, "mode", mode,
		"head", gitrepo.ShortSHA(head), "user", d.user(r))
	if d.Worker != nil {
		d.Worker.Wake()
	}
	httpx.WriteJSON(w, http.StatusAccepted, describedBody{Status: "queued", RunID: id, Mode: mode})
}
