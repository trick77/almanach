package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/store"
)

type fakeMetricsStore struct {
	m   store.Metrics
	err error
}

func (f fakeMetricsStore) Metrics(context.Context) (store.Metrics, error) { return f.m, f.err }

type fakeMetricsWorker struct {
	st   consolidate.Status
	beat time.Time
}

func (f fakeMetricsWorker) Status() consolidate.Status { return f.st }
func (f fakeMetricsWorker) Heartbeat() time.Time       { return f.beat }

type fakeMetricsSync struct {
	c    clone.CycleStatus
	beat time.Time
}

func (f fakeMetricsSync) Cycle() clone.CycleStatus { return f.c }
func (f fakeMetricsSync) Heartbeat() time.Time     { return f.beat }

const metricsToken = "metrics-token-metrics-token-metrics-token"

func metricsHandler(t *testing.T, m *MetricsDeps, log *slog.Logger) http.Handler {
	t.Helper()
	srv := httpapi.New(func(context.Context) error { return nil }, log)
	Register(srv, Deps{Store: &fakeStore{}, Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone,
		Queue: &QueueDeps{}, Metrics: m, ProblemWindow: 90, Log: log})
	return srv.Handler()
}

func getMetrics(h http.Handler, remote string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = remote
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Loopback opens it, as on kit's probes; a forwarded request, a remote
// one and a team's bearer get 404; the metrics token opens it from
// anywhere, a wrong one is a 404 with a Warn line.
func TestMetricsAccess(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	m := &MetricsDeps{Store: fakeMetricsStore{}, Worker: fakeMetricsWorker{}, Token: metricsToken}
	h := metricsHandler(t, m, log)
	bearer := func(tok string) http.Header { return http.Header{"Authorization": {"Bearer " + tok}} }
	for _, tc := range []struct {
		name   string
		remote string
		header http.Header
		want   int
	}{
		{"loopback", "127.0.0.1:5000", nil, http.StatusOK},
		{"loopback v6", "[::1]:5000", nil, http.StatusOK},
		{"forwarded", "127.0.0.1:5000", http.Header{"X-Forwarded-For": {"10.0.0.1"}}, http.StatusNotFound},
		{"remote", "10.0.0.9:5000", nil, http.StatusNotFound},
		{"token", "10.0.0.9:5000", bearer(metricsToken), http.StatusOK},
		{"wrong token", "10.0.0.9:5000", bearer("nope"), http.StatusNotFound},
		{"team bearer on loopback", "127.0.0.1:5000", bearer("tok-platform"), http.StatusNotFound},
	} {
		if rec := getMetrics(h, tc.remote, tc.header); rec.Code != tc.want {
			t.Errorf("%s: code = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	if !strings.Contains(buf.String(), "metrics: a bearer was refused") {
		t.Errorf("no Warn for a refused bearer: %s", buf.String())
	}
	if strings.Contains(buf.String(), metricsToken) {
		t.Errorf("the token is in the log: %s", buf.String())
	}

	// No token set: a bearer never opens it, loopback still does.
	open := metricsHandler(t, &MetricsDeps{Store: fakeMetricsStore{}, Worker: fakeMetricsWorker{}}, log)
	if rec := getMetrics(open, "10.0.0.9:5000", bearer("")); rec.Code != http.StatusNotFound {
		t.Errorf("empty token: code = %d, want 404", rec.Code)
	}
	if rec := getMetrics(metricsHandler(t, nil, log), "127.0.0.1:5000", nil); rec.Code != http.StatusNotFound {
		t.Errorf("no metrics: code = %d, want 404", rec.Code)
	}
}

func TestMetricsBody(t *testing.T) {
	merge := time.Unix(1_700_000_000, 0)
	until := time.Unix(1_700_000_600, 0)
	m := &MetricsDeps{
		Store: fakeMetricsStore{m: store.Metrics{Notes: 3, Runs: 1, LastMerge: &merge, Calls: []store.CallTotal{
			{Purpose: "merge", ProjectKey: `we"ird\key`, Calls: 2, NanoUSD: 1500},
			{Purpose: "catalogue", ProjectKey: "ABC", Calls: 1, NanoUSD: 0},
		}}},
		Worker: fakeMetricsWorker{st: consolidate.Status{HoldUntil: &until, HoldCause: consolidate.HoldRateLimit}, beat: time.Unix(1_700_000_100, 0)},
		Sync:   fakeMetricsSync{c: clone.CycleStatus{Running: true, StartedAt: time.Unix(1_700_000_200, 0)}, beat: time.Unix(1_700_000_300, 0)},
	}
	rec := getMetrics(metricsHandler(t, m, slog.New(slog.NewTextHandler(io.Discard, nil))), "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != metricsType {
		t.Fatalf("code %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE almanach_queue_notes gauge\nalmanach_queue_notes 3\n",
		"almanach_queue_runs 1\n",
		"almanach_last_merge_seconds 1700000000\n",
		"almanach_last_sync_seconds 0\n",
		`almanach_worker_hold_until_seconds{cause="rate_limit"} 1700000600` + "\n",
		"almanach_worker_busy_since_seconds 0\n",
		"almanach_worker_heartbeat_seconds 1700000100\n",
		"almanach_poller_heartbeat_seconds 1700000300\n",
		"almanach_poller_cycle_started_seconds 1700000200\n",
		"almanach_poller_next_cycle_seconds 0\n",
		"# TYPE almanach_llm_calls_total counter\n",
		`almanach_llm_calls_total{purpose="merge",project="we\"ird\\key"} 2` + "\n",
		`almanach_llm_cost_nano_usd_total{purpose="merge",project="we\"ird\\key"} 1500` + "\n",
		`almanach_llm_cost_nano_usd_total{purpose="catalogue",project="ABC"} 0` + "\n",
		`almanach_info{version="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	// Every sample line is name[{labels}] integer.
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(l, "# ") {
			continue
		}
		if i := strings.LastIndexByte(l, ' '); i < 0 || strings.ContainsAny(l[i+1:], ".e") {
			t.Errorf("not an integer sample: %q", l)
		}
	}

	// Sources off: no poller series.
	m.Sync = nil
	body = getMetrics(metricsHandler(t, m, slog.New(slog.NewTextHandler(io.Discard, nil))), "127.0.0.1:1", nil).Body.String()
	if strings.Contains(body, "almanach_poller_") {
		t.Errorf("poller series with the sources off:\n%s", body)
	}
}

func TestMetricsStoreFailure(t *testing.T) {
	m := &MetricsDeps{Store: fakeMetricsStore{err: errors.New("db down")}, Worker: fakeMetricsWorker{}}
	var buf bytes.Buffer
	rec := getMetrics(metricsHandler(t, m, slog.New(slog.NewTextHandler(&buf, nil))), "127.0.0.1:1", nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(buf.String(), "db down") {
		t.Errorf("code %d, log %s", rec.Code, buf.String())
	}
}

func TestLogClass(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		bearer       bool
		want         httpapi.LogClass
	}{
		{"POST", "/webhooks/noergler", true, httpapi.LogBearer},
		{"GET", "/auth/ping", true, httpapi.LogBearer},
		{"GET", "/api/projects/ABC.md", true, httpapi.LogBearer},
		{"GET", "/api/projects/ABC/repos/x.md", true, httpapi.LogBearer},
		{"GET", "/api/projects/ABC.md", false, httpapi.LogRead},
		{"GET", "/api/projects", false, httpapi.LogRead},
		{"GET", "/api", false, httpapi.LogRead},
		{"POST", "/api/sources/ABC/sync", false, httpapi.LogRead},
		{"GET", "/api/sources", false, httpapi.LogRead},
		{"PATCH", "/api/sources/ABC", false, httpapi.LogRead},
		{"GET", "/api/sources/ABC", false, httpapi.LogRoutine},
		{"GET", "/api/queue", false, httpapi.LogRoutine},
		{"GET", "/api/queue/count", false, httpapi.LogRoutine},
		{"GET", "/metrics", true, httpapi.LogRoutine},
		{"GET", "/", false, httpapi.LogRoutine},
		{"GET", "/assets/x.js", false, httpapi.LogRoutine},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.bearer {
			req.Header.Set("Authorization", "Bearer x")
		}
		if got := logClass(req); got != tc.want {
			t.Errorf("%s %s bearer=%v: %d, want %d", tc.method, tc.path, tc.bearer, got, tc.want)
		}
	}
}
