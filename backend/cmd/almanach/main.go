// Command almanach is the living wiki noergler feeds.
//
//	almanach serve        run the service (default)
//	almanach migrate      apply pending database migrations and exit
//	almanach backup FILE  write a consistent copy of the database to FILE and exit
//	almanach evaluate     run the baseline over a folder of repositories and score the pages (paid calls)
//	almanach version      print the version and exit
//	almanach -healthcheck probe /healthz, exit 0 on a 2xx (liveness: the container healthcheck)
//	almanach -readycheck  probe /readyz, exit 0 on a 2xx (readiness: the database answers, its disk has room)
//
// Both probes run inside the container: kit's /healthz and /readyz answer a
// loopback caller only and 404 anyone else, so an orchestrator's HTTP probe
// from the node never passes. Deploy them as exec probes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
	"github.com/trick77/ship/kit/run"
	"github.com/trick77/ship/kit/version"

	"github.com/trick77/almanach/internal/api"
	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/bitbucket"
	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/almanach/internal/teamkeys"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = withLog(serve)
	case "migrate":
		err = withLog(migrate)
	case "backup":
		err = withLog(backup)
	case "evaluate":
		err = withLog(runEval)
	case "version":
		fmt.Println(version.String())
	case "-healthcheck":
		os.Exit(httpx.Healthcheck(httpx.LocalURL(listenAddr(), "/healthz")))
	case "-readycheck":
		os.Exit(httpx.Healthcheck(httpx.LocalURL(listenAddr(), "/readyz")))
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, usage)
	default:
		fmt.Fprintf(os.Stderr, "almanach: unknown command %q\n%s\n", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("startup aborted", logging.Err(err))
		os.Exit(1)
	}
}

const usage = "usage: almanach [serve|migrate|backup <file>|evaluate -dir DIR -out DIR ...|version|-healthcheck|-readycheck]"

// optimizeGrace bounds PRAGMA optimize at shutdown: an ANALYZE of a few
// tables, seconds at most; the stop grace counts it.
const optimizeGrace = 5 * time.Second

// withLog sets up logging before anything logs, names the build, then runs.
// An unknown BACKEND_LOG_LEVEL still fails startup: logging starts at info so the
// fault surfaces with the rest of the config's.
func withLog(fn func(*slog.Logger) error) error {
	env := os.Getenv("BACKEND_ENV")
	if env == "" {
		env = "dev"
	}
	level := os.Getenv("BACKEND_LOG_LEVEL")
	if _, err := logging.ParseLevel(level); err != nil {
		level = ""
	}
	log, err := logging.Setup(logging.Options{Level: level, Format: logging.Splunk, Writer: os.Stdout, Service: "almanach", Env: env})
	if err != nil {
		fmt.Fprintln(os.Stderr, "almanach:", err)
		os.Exit(1)
	}
	log.Info("almanach version: " + version.String())
	return fn(log)
}

// listenAddr is BACKEND_ADDR as serve binds it, read without the rest of
// the config: the healthcheck needs nothing else.
func listenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("BACKEND_ADDR")); addr != "" {
		return addr
	}
	return config.DefaultAddr
}

// startModel builds the client of one llmwire profile and runs its startup
// check: strict schema support, the reasoning level, the gateway's listing.
// A gateway that does not answer is a warning; anything else is a fault,
// ok false. cutDir keeps cut answers whole (inference.Options.CutDir).
func startModel(ctx context.Context, log *slog.Logger, label, model, effort string, noDiagrams bool, cutDir string) (*inference.Client, bool) {
	llm, err := inference.New(inference.Options{Model: model, ReasoningEffort: effort, Logger: log, Lookup: os.LookupEnv,
		NoDiagrams: noDiagrams, CutDir: cutDir})
	var warning string
	if err == nil {
		warning, err = llm.Startup(ctx)
	}
	switch {
	case err != nil:
		log.Error(label + ": " + err.Error())
		return nil, false
	case warning != "":
		log.Warn(label + ": " + warning)
	default:
		log.Info(label+": OK", "model", model)
	}
	return llm, true
}

// writableDir proves the folder named by the variable env exists and takes
// a file: a volume mounted read-only or owned by another user fails here,
// at startup, not at the first clone or write.
func writableDir(env, dir string) error {
	info, err := os.Stat(dir) //nolint:gosec // the operator's configured folder, by design
	if err != nil {
		return fmt.Errorf("%s: %w", env, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s: %s is not a directory", env, dir)
	}
	probe, err := os.CreateTemp(dir, ".almanach-probe-*")
	if err != nil {
		return fmt.Errorf("%s: not writable: %w", env, err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name) //nolint:gosec // the probe file just created there
}

// openDB opens the database file after proving its folder takes the file
// and its -wal and -shm beside it. With existing set, a path that names
// no Almanach database is refused (store.OpenExisting): the driver would
// create an empty one there and a backup of it would read as success.
func openDB(ctx context.Context, path string, log *slog.Logger, existing bool) (*store.Store, error) {
	if err := writableDir("BACKEND_DB_PATH", filepath.Dir(path)); err != nil {
		return nil, err
	}
	if existing {
		return store.OpenExisting(ctx, path, log)
	}
	return store.Open(ctx, path, log)
}

// openFromEnv is the database of a command that needs BACKEND_DB_PATH and
// nothing else (migrate, backup), after the log level gate every command
// runs.
func openFromEnv(ctx context.Context, log *slog.Logger, existing bool) (*store.Store, error) {
	if _, err := logging.ParseLevel(os.Getenv("BACKEND_LOG_LEVEL")); err != nil {
		return nil, fmt.Errorf("BACKEND_LOG_LEVEL: %w", err)
	}
	path := strings.TrimSpace(os.Getenv("BACKEND_DB_PATH"))
	if path == "" {
		return nil, errors.New("environment variable BACKEND_DB_PATH is not set")
	}
	return openDB(ctx, path, log, existing)
}

// migrate applies pending migrations and exits: the init container's job.
// Only BACKEND_DB_PATH is needed.
func migrate(log *slog.Logger) error {
	ctx := context.Background()
	db, err := openFromEnv(ctx, log, false)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("migrations up to date")
	// Pages stored before a word rule read as pages written after it: the
	// rule is code's (lang.Terms), so this costs no model call, and it is
	// idempotent, a page already respelled left as it is.
	n, err := db.Respell(ctx, baseline.Respeller{})
	if err != nil {
		return fmt.Errorf("respell: %w", err)
	}
	log.Info("pages respelled", "revisions", n)
	return nil
}

// backup writes a consistent copy of the database to the file named on
// the command line and exits, while serve may run on: the way to back
// the database up, a copy of its folder under live writes being none.
// Only BACKEND_DB_PATH is needed, and it must name a file that exists.
func backup(log *slog.Logger) error {
	if len(os.Args) != 3 || strings.TrimSpace(os.Args[2]) == "" {
		return errors.New("backup: one argument, the file to write, e.g. almanach backup /data/almanach-2026-11-06.db")
	}
	dest := os.Args[2]
	// Ctrl-C or a stop of the container cancels VACUUM INTO, and Backup
	// takes its placeholder and part file away; only a kill leaves them.
	ctx, stop := run.Context(context.Background())
	defer stop()
	db, err := openFromEnv(ctx, log, true)
	if err != nil {
		return err
	}
	defer db.Close()
	start := time.Now()
	size, err := db.Backup(ctx, dest)
	if err != nil {
		return err
	}
	log.Info("backup written", "file", dest, "bytes", size, "took", time.Since(start).Round(time.Millisecond).String())
	return nil
}

func serve(log *slog.Logger) error {
	if _, err := logging.ParseLevel(os.Getenv("BACKEND_LOG_LEVEL")); err != nil {
		return fmt.Errorf("BACKEND_LOG_LEVEL: %w", err)
	}
	app, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log.Info("config: " + app.Summary())

	ctx, stop := run.Context(context.Background())
	defer stop()

	// Every check runs before the abort so the log names all the failures
	// at once rather than one per restart. serve never migrates.
	var checks []string
	db, err := openDB(ctx, app.DBPath, log, false)
	if err == nil {
		defer db.Close()
		err = db.SchemaCurrent(ctx)
	}
	if err != nil {
		log.Error("Database: " + err.Error())
		checks = append(checks, "Database")
	} else {
		log.Info("Database: OK", "path", app.DBPath)
	}
	keys, err := teamkeys.Load(app.TeamKeysPath)
	if err != nil {
		log.Error("Team keys: " + err.Error())
		checks = append(checks, "Team keys")
	} else {
		log.Info("Team keys: OK", "teams", keys.Teams())
	}
	// Cut answers beside the database: the gateway keeps none (2026-11-10).
	cutDir := filepath.Join(filepath.Dir(app.DBPath), "cuts")
	llm, ok := startModel(ctx, log, "LLM", app.Model, app.ReasoningEffort, app.Limits.MaxDiagrams == 0, cutDir)
	if !ok {
		checks = append(checks, "LLM")
	}
	// The catalogue's own profile, when the baseline has one: checked like
	// the first, at its balanced level (the merge model's level need not
	// exist on a cheaper profile).
	catalogue := llm
	if app.Sources.Enabled() && app.Baseline.CatalogueModel != "" && app.Baseline.CatalogueModel != app.Model {
		var ok bool
		if catalogue, ok = startModel(ctx, log, "LLM catalogue", app.Baseline.CatalogueModel, "", true, cutDir); !ok {
			checks = append(checks, "LLM catalogue")
		}
	}
	// The clone feature: git on PATH, a writable base folder, a usable key.
	// Off when no folder is configured; every fault is a startup fault.
	var gitBin string
	var key secret.Key
	var lister *bitbucket.Lister
	if app.Sources.Enabled() {
		var faults []string
		if b, err := gitrepo.Resolve(); err != nil {
			faults = append(faults, err.Error())
		} else {
			gitBin = b
		}
		if l, err := bitbucket.NewWithCA(app.Sources.GitCAFile); err != nil {
			faults = append(faults, "BACKEND_GIT_CA_FILE: "+err.Error())
		} else {
			lister = l
		}
		if err := writableDir("BACKEND_REPOS_DIR", app.Sources.ReposDir); err != nil {
			faults = append(faults, err.Error())
		}
		if k, err := secret.Parse(app.Sources.SecretKey); err != nil {
			faults = append(faults, "BACKEND_SOURCE_TOKEN_KEY: "+err.Error())
		} else {
			key = k
		}
		if len(faults) > 0 {
			log.Error("Sources: " + strings.Join(faults, "; "))
			checks = append(checks, "Sources")
		} else {
			log.Info("Sources: OK", "dir", app.Sources.ReposDir, "git", gitBin, "token_key", key.Enabled())
		}
	} else {
		log.Info("Sources: off (BACKEND_REPOS_DIR unset)")
	}
	// The sign-in. Discovery runs in the background from the first attempt
	// on, so boot never waits for the provider; one that does not answer
	// is a warning, like the gateway: the webhook, the ping and the probes
	// do not need it. Until it answers the login lands on a page that
	// says so and the wiki stays behind its 401.
	var oidcSvc api.OIDCService
	var sessions *kitauth.Sessions
	if app.AuthMode == config.AuthOIDC {
		o := app.OIDC
		s, err := auth.NewSessions(o.SessionKey)
		if err != nil {
			panic("session cookie: config admitted a key kit refuses: " + err.Error())
		}
		sessions = s
		provider := auth.NewProvider(auth.Config{Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: o.ClientSecret, RedirectURL: o.RedirectURL}, log)
		go provider.Run(ctx)
		oidcSvc = provider
	}
	if len(checks) > 0 {
		return fmt.Errorf("startup aborted: %d check(s) failed: %s", len(checks), strings.Join(checks, ", "))
	}

	// The worker runs on its own context, detached from the signal: the
	// server stops first, then Stop lets the fold in flight finish (a paid
	// call and its writes) and only past the grace cuts it off, before the
	// database closes under it.
	// The room on the store's disk, asked by the worker before every item:
	// a full disk is found by a syscall, not by a paid call whose write
	// then fails.
	diskFree := httpapi.DiskFree(filepath.Dir(app.DBPath), httpapi.MinFreeBytes)
	worker := consolidate.New(db, llm, app.Limits, log).WithDiskCheck(func() error { return diskFree(context.Background()) }).
		WithFoldDelay(app.FoldDelay)

	// The poller too runs past the signal: the server drains first, then
	// Stop cuts the clone in flight (the next cycle redoes it), before the
	// database closes. With the sources on, the worker also drains the
	// baseline runs, reading the clones through the poller's lock.
	var sources *api.SourcesDeps
	var poller *clone.Poller
	if app.Sources.Enabled() {
		git, err := gitrepo.New(gitBin, app.Sources.ReposDir, app.Sources.GitCAFile)
		if err != nil {
			return err
		}
		git.WithTimeouts(app.Sources.GitTimeout, app.Sources.GitCloneTimeout)
		poller = clone.New(clone.Deps{Store: db, Git: git, Lister: lister, Key: key, Interval: app.Sources.SyncInterval, Log: log,
			Describe: clone.Describe{Reconcile: app.Baseline.Reconcile, Wake: worker.Wake}, Manifests: clone.Manifests{Store: db, Reads: git}})
		// The clones take one write too: a repository's brief, pushed with
		// the source's token.
		code := clone.NewCode(poller, git).WithWrites(git, db)
		worker.WithBaseline(consolidate.Baseline{Store: db, Code: code, Catalogue: catalogue, Describer: llm, Limits: app.Baseline,
			Briefs: &consolidate.Briefs{Store: db, Model: llm, Push: code}})
		poller.Start(context.WithoutCancel(ctx))
		// Removals go through the poller, under its disk lock.
		sources = &api.SourcesDeps{Store: db, Poller: poller, Git: poller, Key: key, DefaultHost: app.Sources.BitbucketHost,
			Reconcile: app.Baseline.Reconcile, SyncInterval: app.Sources.SyncInterval, MaxCalls: app.Baseline.MaxCalls}
	}
	worker.Start(context.WithoutCancel(ctx))

	// The queue page reads the worker's and the poller's state; no poller
	// (sources off) is a nil interface, never a nil *Poller in one.
	queue := &api.QueueDeps{Store: db, Worker: worker, Budget: app.Baseline.DailyBudgetNanoUSD}
	metrics := &api.MetricsDeps{Store: db, Worker: worker, Token: app.MetricsToken}
	if poller != nil {
		queue.Sync = poller
		metrics.Sync = poller
	}

	// Readiness: the database, then the room on its disk: SQLite answers a
	// ping from a full disk, and every write after a paid call then fails.
	srv := httpapi.New(db.Ping, log, httpx.Check{Name: "disk", Run: diskFree})
	api.Register(srv, api.Deps{Store: db, Keys: keys, Worker: worker, AuthMode: app.AuthMode, TeamClaims: app.TeamClaims, ProblemWindow: app.ProblemWindow,
		Sources: sources, Queue: queue, Metrics: metrics, DailyBudgetNanoUSD: app.Baseline.DailyBudgetNanoUSD, OIDC: oidcSvc, Sessions: sessions,
		RedirectURL: app.OIDC.RedirectURL, Log: log})

	addr := app.Addr
	log.Info("listening", "addr", addr, "auth", string(app.AuthMode))
	runErr := httpx.Serve(ctx, httpx.NewServer(addr, srv.Handler()), 15*time.Second)

	if poller != nil {
		poller.Stop()
	}
	worker.Stop(inference.CallTimeout + 15*time.Second)
	// The planner's statistics, refreshed on the way out: ctx is done by
	// now, so a bound of its own, inside the stop grace (compose's
	// stop_grace_period, sized for the drain, the worker's grace and this).
	octx, cancel := context.WithTimeout(context.Background(), optimizeGrace)
	db.Optimize(octx)
	cancel()
	if runErr != nil {
		return runErr
	}
	log.Info("almanach stopped")
	return nil
}
