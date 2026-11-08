package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trick77/ship/kit/run"

	"github.com/trick77/almanach/internal/evalrun"
)

// runEval runs the baseline over a folder of repositories and scores the
// pages (evalrun): every call is paid, so it never runs in the gate.
func runEval(log *slog.Logger) error {
	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	dir := fs.String("dir", "", "folder with projects/<project>/<repo> and labels/<project>/<repo>.yaml")
	out := fs.String("out", "", "folder for the run's databases, clones and report (created)")
	model := fs.String("model", "", "llmwire profile that writes the pages")
	judge := fs.String("judge", "", "llmwire profile that scores them (default: -model)")
	envFile := fs.String("env-file", "", "KEY=VALUE file read before the environment (model keys)")
	deadline := fs.Duration("deadline", 85*time.Minute, "when the pipeline stops; what is written by then is scored")
	repeat := fs.Int("repeat", 1, "run everything this many times side by side; the report gives the means")
	split := fs.Bool("split", false, "run every repository in its own database, all side by side (no project step)")
	reasoning := fs.String("reasoning", "", "the writer's reasoning level (BACKEND_MODEL_REASONING_EFFORT): a level, off, or empty for the model's balanced one")
	routeOnly := fs.Bool("route-only", false, "the filter stage's dry run: one router call a repository, no page written, no judge (route.json, route.md)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *dir == "" || *out == "" || *model == "" {
		return errors.New("eval: -dir, -out and -model are required")
	}
	if *judge == "" {
		*judge = *model
	}
	env := map[string]string{}
	if *envFile != "" {
		var err error
		if env, err = readEnvFile(*envFile); err != nil {
			return err
		}
	}
	if *reasoning != "" {
		env["BACKEND_MODEL_REASONING_EFFORT"] = *reasoning
	}
	lookup := func(k string) (string, bool) {
		if v, ok := env[k]; ok {
			return v, true
		}
		return os.LookupEnv(k)
	}
	if err := os.MkdirAll(*out, 0o750); err != nil {
		return err
	}
	ctx, stop := run.Context(context.Background())
	defer stop()
	opt := evalrun.Options{Dir: *dir, Out: *out, Model: *model, Judge: *judge, Lookup: lookup, Deadline: *deadline, Split: *split, Repeat: *repeat, Log: log}
	if *routeOnly {
		return writeRoute(ctx, opt)
	}
	rep, err := evalrun.Run(ctx, opt)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "report.json"), b, 0o600); err != nil {
		return err
	}
	md := rep.Markdown()
	if err := os.WriteFile(filepath.Join(*out, "report.md"), []byte(md), 0o600); err != nil {
		return err
	}
	fmt.Println(md)
	return nil
}

// writeRoute runs the router's dry run and writes route.json and route.md.
func writeRoute(ctx context.Context, opt evalrun.Options) error {
	rep, err := evalrun.RouteOnly(ctx, opt)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opt.Out, "route.json"), b, 0o600); err != nil {
		return err
	}
	md := rep.Markdown()
	if err := os.WriteFile(filepath.Join(opt.Out, "route.md"), []byte(md), 0o600); err != nil {
		return err
	}
	fmt.Println(md)
	return nil
}

// readEnvFile reads KEY=VALUE lines; blank lines and # comments are
// skipped, one pair of surrounding quotes is dropped.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's file, by design
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, sc.Err()
}
