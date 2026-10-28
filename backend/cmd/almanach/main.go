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

	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
	"github.com/trick77/ship/kit/run"
	"github.com/trick77/ship/kit/version"

	"github.com/trick77/almanach/internal/api"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/inference"
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
	llm, err := inference.New(inference.Options{Model: app.Model, ReasoningEffort: app.ReasoningEffort, Logger: log, Lookup: os.LookupEnv})
	var warning string
	if err == nil {
		warning, err = llm.Startup(ctx)
	}
	switch {
	case err != nil:
		log.Error("LLM: " + err.Error())
		checks = append(checks, "LLM")
	case warning != "":
		log.Warn("LLM: " + warning)
	default:
		log.Info("LLM: OK", "model", app.Model)
	}
	if len(checks) > 0 {
		return fmt.Errorf("startup aborted: %d check(s) failed: %s", len(checks), strings.Join(checks, ", "))
	}

	// The worker runs on its own context, detached from the signal: the
	// server stops first, then Stop lets the fold in flight finish (a paid
	// call and its writes) and only past the grace cuts it off, before the
	// pool closes under it.
	worker := consolidate.New(db, llm, app.Limits, log)
	worker.Start(context.WithoutCancel(ctx))

	srv := httpapi.New(db.Ping, log)
	api.Register(srv, api.Deps{Store: db, Keys: keys, Worker: worker, AuthMode: app.AuthMode, ProblemWindow: app.ProblemWindow, Log: log})

	addr := net.JoinHostPort(app.Host, strconv.Itoa(app.Port))
	log.Info("listening", "addr", addr, "auth", string(app.AuthMode))
	runErr := httpx.Serve(ctx, httpx.NewServer(addr, srv.Handler()), 15*time.Second)

	worker.Stop(inference.CallTimeout + 15*time.Second)
	if runErr != nil {
		return runErr
	}
	log.Info("almanach stopped")
	return nil
}
