package api

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/httpapi"
)

// specOps are openapi.yaml's operations as mux patterns, "GET /api/me".
// Read by line, not by a YAML parser: the spec's paths sit at two spaces,
// their methods at four.
func specOps(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	pathLine := regexp.MustCompile(`^  (/\S*):$`)
	methodLine := regexp.MustCompile(`^    (get|put|post|patch|delete|head|options|trace):$`)
	var ops []string
	var path string
	inPaths := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		// A top-level key ends the block before it; a comment does not.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			inPaths = line == "paths:"
			continue
		}
		if !inPaths {
			continue
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			path = m[1]
		} else if m := methodLine.FindStringSubmatch(line); m != nil {
			ops = append(ops, strings.ToUpper(m[1])+" "+path)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ops
}

// openapi.yaml = api.go: every route the API registers is in the spec and
// every operation in the spec is registered. The SPA's catch-all is no API
// route; a .md export is served by the route without the suffix, so the
// spec's .md path stands for that one.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	Register(srv, Deps{Store: &fakeStore{}, Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone,
		Queue: &QueueDeps{}, ProblemWindow: 90, Log: discard})
	var routes []string
	for _, p := range srv.Patterns() {
		if p != "GET /" {
			routes = append(routes, p)
		}
	}

	var spec []string
	for _, op := range specOps(t) {
		if op = strings.TrimSuffix(op, ".md"); !slices.Contains(spec, op) {
			spec = append(spec, op)
		}
	}

	for _, r := range routes {
		if !slices.Contains(spec, r) {
			t.Errorf("route %q is not in openapi.yaml", r)
		}
	}
	for _, s := range spec {
		if !slices.Contains(routes, s) {
			t.Errorf("openapi.yaml operation %q is not registered", s)
		}
	}
	if len(routes) < 20 {
		t.Errorf("only %d routes registered: the test reads too few", len(routes))
	}
}
