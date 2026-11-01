package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
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
	CreateSourceWith(ctx context.Context, src store.ProjectSource, first store.SourceRepo) error
	UpdateSourceAccess(ctx context.Context, key, host, tokenAuth, tokenUser string, tokenEnc []byte, setToken bool) (bool, error)
	DeleteSource(ctx context.Context, key string) (bool, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	AddSourceRepo(ctx context.Context, r store.SourceRepo) error
	DeleteSourceRepo(ctx context.Context, key, slug string) (bool, error)
	RequestSync(ctx context.Context, key, slug string) (int, error)
	ProjectSpelledOtherwise(ctx context.Context, key string) (string, bool, error)
	SourceRuns(ctx context.Context, key string) (map[string]store.RepoRuns, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	EnqueueRun(ctx context.Context, key, slug, mode, trigger, baseCommit, commit string) (int64, error)
	SourceRepoByName(ctx context.Context, key, slug string) (store.SourceRepo, bool, error)
	RenameProject(ctx context.Context, key, to string) (int64, error)
	RenameRepo(ctx context.Context, key, slug, to, cloneURL string) error
}

// Nudger starts the next sync cycle now.
type Nudger interface{ Nudge() }

// Checkouts is what the routes need of gitrepo: where a clone lives and
// how it goes. A folder is named by ids, never by names.
type Checkouts interface {
	Dir(projectID, repoID int64) (string, error)
	ProjectDir(projectID int64) (string, error)
	Remove(projectID, repoID int64) error
	RemoveProject(projectID int64) error
}

// SourcesDeps is the clone feature as the routes see it; nil in Deps means
// the feature is off. Git is the poller in production: removals go through
// its lock so a sync in flight cannot recreate a folder the row has lost.
type SourcesDeps struct {
	Store  SourceStore
	Poller Nudger
	Git    Checkouts
	Key    secret.Key
	// DefaultHost prefills a new Bitbucket source's host (BACKEND_BITBUCKET_HOST).
	DefaultHost string
	// Reconcile is the poller's reconcile interval (BACKEND_BASELINE_RECONCILE),
	// zero for buttons only: whether a skipped part is retried on its own.
	Reconcile time.Duration
	sizes     sizeCache
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
	s.HandleFunc("PATCH /api/sources/{key}/repos/{slug}", d.renameRepo)
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
	LastSyncedAt *time.Time `json:"last_synced_at"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type sourcesBody struct {
	Enabled     bool                `json:"enabled"`
	DefaultHost string              `json:"default_host"`
	Sources     []sourceSummaryBody `json:"sources"`
}

func (d Deps) sources(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	out := sourcesBody{Enabled: d.Sources != nil, Sources: []sourceSummaryBody{}}
	if d.Sources == nil {
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	out.DefaultHost = d.Sources.DefaultHost
	rows, err := d.Sources.Store.Sources(r.Context())
	if err != nil {
		httpx.ServerError(w, r, err, "sources")
		return
	}
	for _, s := range rows {
		out.Sources = append(out.Sources, sourceSummaryBody{ProjectKey: s.ProjectKey, Kind: s.Kind, Host: s.Host, HasToken: s.HasToken(),
			TokenAuth: s.TokenAuth, TokenUser: s.TokenUser, Repos: s.Repos, Ready: s.Ready, Active: s.Active, Failed: s.Failed, Missing: s.Missing,
			LastListedAt: s.LastListedAt, LastSyncedAt: s.LastSyncedAt, LastError: s.LastError, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
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
	// Dir is the clone's folder, named by ids.
	Dir string `json:"dir"`
	// Baseline is the newest run and the commit last documented, null
	// before the first run.
	Baseline *baselineBody `json:"baseline"`
}

// baselineBody is what the source page shows of a repository's runs.
type baselineBody struct {
	RunID int64  `json:"run_id"`
	State string `json:"state"`
	// MapStep: in state project, the project step is over and the run draws
	// the repository map, its last step. The state has no value of its own
	// for it (a CHECK on baseline_runs); the plan says so.
	MapStep          bool              `json:"map_step"`
	Mode             string            `json:"mode"`
	Trigger          string            `json:"trigger"`
	Commit           string            `json:"commit"`
	Step             int               `json:"step"`
	Steps            int               `json:"steps"`
	Skipped          int               `json:"skipped"`
	SkippedParts     []skippedPartBody `json:"skipped_parts"`
	CostUSD          string            `json:"cost_usd,omitempty"`
	Error            string            `json:"error"`
	QueuedAt         time.Time         `json:"queued_at"`
	StartedAt        *time.Time        `json:"started_at"`
	FinishedAt       *time.Time        `json:"finished_at"`
	DocumentedCommit string            `json:"documented_commit"`
	DocumentedAt     *time.Time        `json:"documented_at"`
	// LeftParts: a done run skipped a part worth reading again
	// (baseline.LeftParts), the rule the poller retries by.
	LeftParts bool `json:"left_parts"`
	// AutoRetry: LeftParts, and the poller reads them again on its own
	// (clone.RetriesOnItsOwn); else only the next run or a button does.
	AutoRetry bool `json:"auto_retry"`
}

// baselineOf is a repository's newest run as the page shows it; err is a
// plan that did not decode, its skipped parts left out, the body still
// whole.
func baselineOf(rr store.RepoRuns) (*baselineBody, error) {
	r := rr.Last
	parts, skipped, err := skippedOf(r.Plan)
	return &baselineBody{RunID: r.ID, State: r.State, MapStep: inMapStep(r), Mode: r.Mode, Trigger: r.Trigger, Commit: r.Commit, Step: r.Step, Steps: r.Steps,
		Skipped: r.Skipped, SkippedParts: parts, CostUSD: usd(rr.LastCost), Error: r.Error, QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		DocumentedCommit: rr.Documented, DocumentedAt: rr.DocumentedAt,
		LeftParts: r.State == store.RunDone && baseline.LeftParts(skipped, r.Skipped)}, err
}

// inMapStep reports whether a run in state project has its project step
// behind it: the worker's plan carries project_done. A plan that does not
// read says no.
func inMapStep(r store.BaselineRun) bool {
	if r.State != store.RunProject {
		return false
	}
	var p struct {
		ProjectDone bool `json:"project_done"`
	}
	return json.Unmarshal(r.Plan, &p) == nil && p.ProjectDone
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
		TokenUser: src.TokenUser, LastListedAt: src.LastListedAt, LastError: src.LastError,
		CreatedAt: src.CreatedAt, UpdatedAt: src.UpdatedAt, Repos: make([]sourceRepoBody, 0, len(repos))}
	if dir, err := d.Sources.Git.ProjectDir(src.ProjectID); err == nil {
		out.Dir, out.DiskBytes = dir, d.Sources.sizes.size(dir)
	}
	for _, rp := range repos {
		body := sourceRepoBody{Slug: rp.Slug, CloneURL: rp.CloneURL, Origin: rp.Origin, Branch: rp.Branch, HeadCommit: rp.HeadCommit,
			State: rp.State, LastSyncedAt: rp.LastSyncedAt, LastError: rp.LastError, CreatedAt: rp.CreatedAt}
		if dir, err := d.Sources.Git.Dir(rp.ProjectID, rp.RepoID); err == nil {
			body.Dir = dir
		}
		if rr, ok := runs[rp.Slug]; ok {
			var err error
			if body.Baseline, err = baselineOf(rr); err != nil {
				d.Log.WarnContext(ctx, "source page: run plan does not decode, its skipped parts left out",
					"repo", rp.Slug, "run_id", rr.Last.ID, logging.Err(err))
			}
			b := body.Baseline
			b.AutoRetry = b.LeftParts && clone.RetriesOnItsOwn(d.Sources.Reconcile, rr.Retried, rr.Documented, rp.HeadCommit)
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
	// CloneURL, Slug and Branch are a static folder's first repository,
	// created with it. Slug empty: the repository's name from the URL;
	// project_key empty: the slug.
	CloneURL string `json:"clone_url"`
	Slug     string `json:"slug"`
	Branch   string `json:"branch"`
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

// tokenChange names the field of an update that gives a source a token to
// send: a new one, or the auth or user of the stored one changed. "" for
// none, a cleared token included.
func tokenChange(src store.ProjectSource, setToken bool, sealed []byte, auth, user string) string {
	switch {
	case setToken:
		if len(sealed) > 0 {
			return "token"
		}
		return ""
	case !src.HasToken():
		return ""
	case auth != src.TokenAuth:
		return "token_auth"
	case user != src.TokenUser:
		return "token_user"
	}
	return ""
}

// seal encrypts a token for the store; without a key it names the variable.
func (d Deps) seal(token string) ([]byte, string) {
	if token == "" {
		return nil, ""
	}
	if !d.Sources.Key.Enabled() {
		return nil, "token: BACKEND_SOURCE_TOKEN_KEY is not set, no token can be stored"
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
	in.CloneURL, in.Slug, in.Branch = strings.TrimSpace(in.CloneURL), strings.TrimSpace(in.Slug), strings.TrimSpace(in.Branch)
	first := in.CloneURL != ""
	if first && in.Slug == "" {
		in.Slug = store.SlugFromURL(in.CloneURL)
	}
	if first && in.ProjectKey == "" {
		in.ProjectKey = in.Slug
	}
	switch {
	case first && in.Kind != store.SourceStatic:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: a Bitbucket source lists its repositories itself")
		return
	case first && !validCloneURL(in.CloneURL):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: an https URL without credentials")
		return
	case first && !gitrepo.ValidSegment(in.Slug):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "slug: letters, digits, dot, dash, underscore; not . or ..")
		return
	case strings.ContainsAny(in.Branch, " \t\n") || strings.HasPrefix(in.Branch, "-"):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "branch: not a branch name")
		return
	case !gitrepo.ValidSegment(in.ProjectKey):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "project_key: letters, digits, dot, dash, underscore, tilde; not . or ..")
		return
	case in.Kind != store.SourceBitbucket && in.Kind != store.SourceStatic:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "kind: bitbucket or static")
		return
	case in.Kind == store.SourceBitbucket && !gitrepo.ValidHost(in.Host):
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
	src := store.ProjectSource{ProjectKey: in.ProjectKey, Kind: in.Kind, Host: in.Host, TokenAuth: auth, TokenUser: user, TokenEnc: sealed}
	if first {
		err = d.Sources.Store.CreateSourceWith(ctx, src, store.SourceRepo{ProjectKey: in.ProjectKey, Slug: in.Slug, CloneURL: in.CloneURL,
			Branch: in.Branch})
	} else {
		err = d.Sources.Store.CreateSource(ctx, src)
	}
	if err != nil {
		if clonedElsewhere(w, err) {
			return
		}
		if errors.Is(err, store.ErrExists) {
			httpx.WriteError(w, http.StatusConflict, "a source with this project_key exists")
			return
		}
		if errors.Is(err, store.ErrNameTaken) {
			httpx.WriteError(w, http.StatusConflict, "project_key: another project had this key")
			return
		}
		httpx.ServerError(w, r, err, "create source")
		return
	}
	d.Log.InfoContext(ctx, "source_created", "project", src.ProjectKey, "kind", src.Kind, "first_repo", in.Slug, "has_token", src.HasToken(),
		"user", d.user(r))
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
	// ProjectKey renames the project, a change of case included.
	ProjectKey *string `json:"project_key"`
	Host       *string `json:"host"`
	Token      *string `json:"token"`
	TokenAuth  *string `json:"token_auth"`
	TokenUser  *string `json:"token_user"`
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
	// An unchanged project_key (a client sending the source back whole) is
	// no rename.
	if in.ProjectKey != nil && strings.TrimSpace(*in.ProjectKey) != src.ProjectKey {
		d.renameSource(w, r, src, in)
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
	case src.Kind == store.SourceBitbucket && !gitrepo.ValidHost(host):
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
	// A token reaches one host. A static folder from before addRepo kept
	// it on one may span several: no new token for it, and no new way to
	// send a stored one (its token is withheld at the clone). Clearing one
	// is never refused.
	if field := tokenChange(src, setToken, sealed, auth, user); field != "" && src.Kind == store.SourceStatic {
		repos, err := d.Sources.Store.SourceRepos(ctx, src.ProjectKey)
		if err != nil {
			httpx.ServerError(w, r, err, "source repos")
			return
		}
		if hosts := store.Origins(repos); len(hosts) > 1 {
			httpx.WriteError(w, http.StatusUnprocessableEntity, field+": this folder spans several hosts ("+strings.Join(hosts, ", ")+
				"); a token reaches one host only, remove the others first")
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
	d.Log.InfoContext(ctx, "source_updated", "project", src.ProjectKey, "token_changed", setToken, "user", d.user(r))
	d.Sources.Poller.Nudge()
	d.writeSource(w, r, src.ProjectKey, http.StatusOK)
}

// renameSource renames the source's project, a change of case included: the
// project keeps its id, pages, notes and clones, and the old key stays a
// name of it. Alone in its PATCH, so a request is the rename or the access
// change, never half of both. A key another project ever had, in any case,
// is never given away.
func (d Deps) renameSource(w http.ResponseWriter, r *http.Request, src store.ProjectSource, in updateSourceBody) {
	ctx := r.Context()
	if in.Host != nil || in.Token != nil || in.TokenAuth != nil || in.TokenUser != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "project_key: a rename comes alone, the other fields in their own PATCH")
		return
	}
	to := strings.TrimSpace(*in.ProjectKey)
	if !gitrepo.ValidSegment(to) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "project_key: letters, digits, dot, dash, underscore, tilde; not . or ..")
		return
	}
	_, err := d.Sources.Store.RenameProject(ctx, src.ProjectKey, to)
	switch {
	case errors.Is(err, store.ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, "project_key: another project had this key")
		return
	case err != nil:
		httpx.ServerError(w, r, err, "rename project")
		return
	}
	d.Log.InfoContext(ctx, "project_renamed", "project", to, "from", src.ProjectKey, "user", d.user(r))
	d.Sources.Poller.Nudge()
	d.writeSource(w, r, to, http.StatusOK)
}

// writeSource answers with the source as stored.
func (d Deps) writeSource(w http.ResponseWriter, r *http.Request, key string, status int) {
	ctx := r.Context()
	stored, found, err := d.Sources.Store.SourceByKey(ctx, key)
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
	httpx.WriteJSON(w, status, out)
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
	if err := d.Sources.Git.RemoveProject(src.ProjectID); err != nil {
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

// lookupRepo is the source's repository the path's slug names, any slug it
// ever had, or false with the 404 (or 500) written.
func (d Deps) lookupRepo(w http.ResponseWriter, r *http.Request, src store.ProjectSource) (store.SourceRepo, bool) {
	slug := r.PathValue("slug")
	if !gitrepo.ValidSegment(slug) {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return store.SourceRepo{}, false
	}
	rp, ok, err := d.Sources.Store.SourceRepoByName(r.Context(), src.ProjectKey, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "source repo")
		return store.SourceRepo{}, false
	}
	if !ok || rp.ProjectID != src.ProjectID {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return store.SourceRepo{}, false
	}
	return rp, true
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
	// No slug: the repository's name from the URL.
	if in.Slug == "" {
		in.Slug = store.SlugFromURL(in.CloneURL)
	}
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
	// One folder, one host, token or not (the store checks it with the
	// insert): the token is sent on every clone of the folder, so a second
	// host would be a second place it goes, and a token set later finds the
	// folder already on one.
	err := d.Sources.Store.AddSourceRepo(ctx, store.SourceRepo{ProjectKey: src.ProjectKey, Slug: in.Slug, CloneURL: in.CloneURL, Branch: in.Branch})
	switch {
	case errors.Is(err, store.ErrExists):
		httpx.WriteError(w, http.StatusConflict, "a repository with this slug exists")
		return
	case otherHost(w, err), clonedElsewhere(w, err):
		return
	case errors.Is(err, store.ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, "slug: another repository had this slug")
		return
	case errors.Is(err, store.ErrNoProject):
		httpx.WriteError(w, http.StatusNotFound, "unknown source")
		return
	case err != nil:
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

// otherHost writes the 422 of a clone URL off the folder's one host and
// reports whether err was one.
func otherHost(w http.ResponseWriter, err error) bool {
	var other *store.OtherHostError
	switch {
	case errors.As(err, &other) && len(other.Hosts) == 1:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: this folder's repositories are on "+other.Hosts[0]+
			"; a folder holds one host, its token goes nowhere else")
	case errors.As(err, &other):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: this folder already spans several hosts ("+strings.Join(other.Hosts, ", ")+
			"); a folder holds one host, remove the others first")
	default:
		return false
	}
	return true
}

// clonedElsewhere writes the 409 of a clone URL another source row clones
// already and reports whether err was one: one repository, one clone.
func clonedElsewhere(w http.ResponseWriter, err error) bool {
	var other *store.ClonedElsewhereError
	if !errors.As(err, &other) {
		return false
	}
	httpx.WriteError(w, http.StatusConflict, "clone_url: this repository is cloned as "+other.ProjectKey+"/"+other.Slug+
		" already; a repository is cloned once, its users find it through their manifests")
	return true
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
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	// The clone first, then the row (deleteSource's reason).
	if err := d.Sources.Git.Remove(rp.ProjectID, rp.RepoID); err != nil {
		httpx.ServerError(w, r, err, "remove clone")
		return
	}
	if _, err := d.Sources.Store.DeleteSourceRepo(ctx, src.ProjectKey, rp.Slug); err != nil {
		httpx.ServerError(w, r, err, "delete repo")
		return
	}
	d.Log.InfoContext(ctx, "source_repo_deleted", "project", src.ProjectKey, "repo", rp.Slug, "user", d.user(r))
	w.WriteHeader(http.StatusNoContent)
}

type renameRepoBody struct {
	Slug     string `json:"slug"`
	CloneURL string `json:"clone_url"`
}

// renameRepo gives a hand-entered repository a new slug and, optionally, a
// new clone URL (a sync follows). The repository keeps its id, pages, notes
// and runs; the old slug stays a name of it. A listed repository follows
// Bitbucket's name on its own.
func (d Deps) renameRepo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	var in renameRepoBody
	if !jsonBody(w, r, &in) {
		return
	}
	in.Slug, in.CloneURL = strings.TrimSpace(in.Slug), strings.TrimSpace(in.CloneURL)
	switch {
	case rp.Origin != store.OriginManual:
		httpx.WriteError(w, http.StatusUnprocessableEntity, "origin: a listed repository follows its name in Bitbucket")
		return
	case !gitrepo.ValidSegment(in.Slug):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "slug: letters, digits, dot, dash, underscore; not . or ..")
		return
	case in.CloneURL != "" && !validCloneURL(in.CloneURL):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "clone_url: an https URL without credentials")
		return
	}
	err := d.Sources.Store.RenameRepo(ctx, src.ProjectKey, rp.Slug, in.Slug, in.CloneURL)
	switch {
	case errors.Is(err, store.ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, "slug: another repository had this slug")
		return
	case errors.Is(err, store.ErrRunOpen):
		httpx.WriteError(w, http.StatusConflict, "clone_url: a run of this repository is open, change the URL once it ends")
		return
	case otherHost(w, err), clonedElsewhere(w, err):
		return
	case errors.Is(err, store.ErrNoRepo):
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	case err != nil:
		httpx.ServerError(w, r, err, "rename repo")
		return
	}
	d.Log.InfoContext(ctx, "source_repo_renamed", "project", src.ProjectKey, "repo", in.Slug, "from", rp.Slug,
		"clone_url_changed", in.CloneURL != "" && in.CloneURL != rp.CloneURL, "user", d.user(r))
	d.Sources.Poller.Nudge()
	out, err := d.sourceDetail(ctx, src)
	if err != nil {
		httpx.ServerError(w, r, err, "source repos")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
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
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	n, err := d.Sources.Store.RequestSync(r.Context(), src.ProjectKey, rp.Slug)
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
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	slug, head := rp.Slug, rp.HeadCommit
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

// skippedPartBody is one part a run left out (baseline.SkippedPart).
type skippedPartBody struct {
	Part   string `json:"part"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// skippedOf is the parts a run left out, named, from its plan. The plan is
// the worker's own JSON; one that does not decode lists none (the caller
// logs it), and the count beside it still says how many.
func skippedOf(raw json.RawMessage) ([]skippedPartBody, []baseline.SkippedPart, error) {
	out := []skippedPartBody{}
	parts, err := baseline.RunSkips(raw)
	if err != nil {
		return out, nil, err
	}
	for _, p := range parts {
		out = append(out, skippedPartBody{Part: string(p.Part), Name: p.Name, Reason: string(p.Reason)})
	}
	return out, parts, nil
}
