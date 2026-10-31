// Command almanach is the living wiki noergler feeds.
//
//	almanach serve        run the service (default)
//	almanach migrate      apply pending database migrations and exit
//	almanach version      print the version and exit
//	almanach -healthcheck probe /healthz, exit 0 on a 2xx (liveness: the container healthcheck)
//	almanach -readycheck  probe /readyz, exit 0 on a 2xx (readiness: the database answers)
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
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
	"github.com/trick77/ship/kit/run"
	"github.com/trick77/ship/kit/version"

	"github.com/trick77/almanach/internal/api"
	"github.com/trick77/almanach/internal/auth"
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

const usage = "usage: almanach [serve|migrate|version|-healthcheck|-readycheck]"

// withLog sets up logging before anything logs, names the build, then runs.
// An unknown LOG_LEVEL still fails startup: logging starts at info so the
// fault surfaces with the rest of the config's.
func withLog(fn func(*slog.Logger) error) error {
	env := os.Getenv("ALMANACH_ENV")
	if env == "" {
		env = "dev"
	}
	level := os.Getenv("LOG_LEVEL")
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

// listenAddr is SERVER_HOST:SERVER_PORT as serve binds it, read without the
// rest of the config: the healthcheck needs nothing else.
func listenAddr() string {
	port := strings.TrimSpace(os.Getenv("SERVER_PORT"))
	if port == "" {
		port = "8080"
	}
	return net.JoinHostPort(strings.TrimSpace(os.Getenv("SERVER_HOST")), port)
}

// startModel builds the client of one llmwire profile and runs its startup
// check: strict schema support, the reasoning level, the gateway's listing.
// A gateway that does not answer is a warning; anything else is a fault,
// ok false.
func startModel(ctx context.Context, log *slog.Logger, label, model, effort string, noDiagrams bool) (*inference.Client, bool) {
	llm, err := inference.New(inference.Options{Model: model, ReasoningEffort: effort, Logger: log, Lookup: os.LookupEnv, NoDiagrams: noDiagrams})
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

// writableDir proves the base folder exists and takes a file: a volume
// mounted read-only or owned by another user fails here, at startup, not at
// the first clone.
func writableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("ALMANACH_REPOS_DIR: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("ALMANACH_REPOS_DIR: %s is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".almanach-probe-*")
	if err != nil {
		return fmt.Errorf("ALMANACH_REPOS_DIR: not writable: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

// migrate applies pending migrations and exits: the init container's job.
// Only DATABASE_URL is needed.
func migrate(log *slog.Logger) error {
	if _, err := logging.ParseLevel(os.Getenv("LOG_LEVEL")); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
	}
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("environment variable DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn, log)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("migrations up to date")
	return nil
}

func serve(log *slog.Logger) error {
	if _, err := logging.ParseLevel(os.Getenv("LOG_LEVEL")); err != nil {
		return fmt.Errorf("LOG_LEVEL: %w", err)
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
	db, err := store.Open(ctx, app.DatabaseURL, log)
	if err == nil {
		defer db.Close()
		err = db.SchemaCurrent(ctx)
	}
	if err != nil {
		log.Error("Database: " + err.Error())
		checks = append(checks, "Database")
	} else {
		log.Info("Database: OK")
	}
	keys, err := teamkeys.Load(app.TeamKeysPath)
	if err != nil {
		log.Error("Team keys: " + err.Error())
		checks = append(checks, "Team keys")
	} else {
		log.Info("Team keys: OK", "teams", keys.Teams())
	}
	llm, ok := startModel(ctx, log, "LLM", app.Model, app.ReasoningEffort, app.Limits.MaxDiagrams == 0)
	if !ok {
		checks = append(checks, "LLM")
	}
	// The catalogue's own profile, when the baseline has one: checked like
	// the first, at its balanced level (the merge model's level need not
	// exist on a cheaper profile).
	catalogue := llm
	if app.Sources.Enabled() && app.Baseline.CatalogueModel != "" && app.Baseline.CatalogueModel != app.Model {
		var ok bool
		if catalogue, ok = startModel(ctx, log, "LLM catalogue", app.Baseline.CatalogueModel, "", true); !ok {
			checks = append(checks, "LLM catalogue")
		}
	}
	// The clone feature: git on PATH, a writable base folder, a usable key.
	// Off when no folder is configured; every fault is a startup fault.
	var gitBin string
	var key secret.Key
	if app.Sources.Enabled() {
		var faults []string
		if b, err := gitrepo.Resolve(); err != nil {
			faults = append(faults, err.Error())
		} else {
			gitBin = b
		}
		if err := writableDir(app.Sources.ReposDir); err != nil {
			faults = append(faults, err.Error())
		}
		if k, err := secret.Parse(app.Sources.SecretKey); err != nil {
			faults = append(faults, "ALMANACH_SECRET_KEY: "+err.Error())
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
		log.Info("Sources: off (ALMANACH_REPOS_DIR unset)")
	}
	// The sign-in. A provider that does not answer discovery is a warning,
	// like the gateway: the webhook, the ping and the probes do not need
	// it, and the provider retries in the background. Until it answers the
	// login routes answer 503 and the wiki stays behind its 401.
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
		if err := provider.Discover(ctx); err != nil {
			log.Warn("OIDC: provider discovery failed, sign-in unavailable until it answers", "issuer", o.Issuer, logging.Err(err))
			go provider.Run(ctx)
		} else {
			log.Info("OIDC: OK", "issuer", o.Issuer)
		}
		oidcSvc = provider
	}
	if len(checks) > 0 {
		return fmt.Errorf("startup aborted: %d check(s) failed: %s", len(checks), strings.Join(checks, ", "))
	}

	// The worker runs on its own context, detached from the signal: the
	// server stops first, then Stop lets the fold in flight finish (a paid
	// call and its writes) and only past the grace cuts it off, before the
	// pool closes under it.
	worker := consolidate.New(db, llm, app.Limits, log)

	// The poller too runs past the signal: the server drains first, then
	// Stop cuts the clone in flight (the next cycle redoes it), before the
	// pool closes. With the sources on, the worker also drains the
	// baseline runs, reading the clones through the poller's lock.
	var sources *api.SourcesDeps
	var poller *clone.Poller
	if app.Sources.Enabled() {
		git, err := gitrepo.New(gitBin, app.Sources.ReposDir, app.Sources.GitCAFile)
		if err != nil {
			return err
		}
		poller = clone.New(clone.Deps{Store: db, Git: git, Lister: bitbucket.New(nil), Key: key, Interval: app.Sources.SyncInterval, Log: log,
			Describe: clone.Describe{Reconcile: app.Baseline.Reconcile, Wake: worker.Wake}})
		worker.WithBaseline(consolidate.Baseline{Store: db, Code: clone.NewCode(poller, git), Catalogue: catalogue, Describer: llm, Limits: app.Baseline})
		poller.Start(context.WithoutCancel(ctx))
		// Removals go through the poller, under its disk lock.
		sources = &api.SourcesDeps{Store: db, Poller: poller, Git: poller, Key: key}
	}
	worker.Start(context.WithoutCancel(ctx))

	srv := httpapi.New(db.Ping, log)
	api.Register(srv, api.Deps{Store: db, Keys: keys, Worker: worker, AuthMode: app.AuthMode, ProblemWindow: app.ProblemWindow, Sources: sources,
		OIDC: oidcSvc, Sessions: sessions, RedirectURL: app.OIDC.RedirectURL, Log: log})

	addr := net.JoinHostPort(app.Host, strconv.Itoa(app.Port))
	log.Info("listening", "addr", addr, "auth", string(app.AuthMode))
	runErr := httpx.Serve(ctx, httpx.NewServer(addr, srv.Handler()), 15*time.Second)

	if poller != nil {
		poller.Stop()
	}
	worker.Stop(inference.CallTimeout + 15*time.Second)
	if runErr != nil {
		return runErr
	}
	log.Info("almanach stopped")
	return nil
}
