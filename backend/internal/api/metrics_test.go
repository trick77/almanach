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

// fakeMetricsStore serves ledger rows by id: Ledger returns those past
// afterID, summed by purpose and project.
type fakeMetricsStore struct {
	m      store.Metrics
	err    error
	ledger []ledgerFake
	asked  []int64
}

type ledgerFake struct {
	id    int64
	row   store.LedgerRow
	merge *time.Time
}

func (f *fakeMetricsStore) Metrics(context.Context) (store.Metrics, error) { return f.m, f.err }

func (f *fakeMetricsStore) Ledger(_ context.Context, after int64) (store.LedgerPart, error) {
	f.asked = append(f.asked, after)
	part := store.LedgerPart{LastID: after}
	for _, l := range f.ledger {
		if l.id > after {
			part.Rows = append(part.Rows, l.row)
			part.LastID = max(part.LastID, l.id)
			if l.merge != nil {
				part.LastMerge = l.merge
			}
		}
	}
	return part, nil
}

type fakeMetricsWorker struct {
	st   consolidate.Status
	beat time.Time
}

func (f fakeMetricsWorker) Status() consolidate.Status { return f.st }
func (f fakeMetricsWorker) Heartbeat() time.Time       { return f.beat }

type fakeMetricsSync struct {
	c           clone.CycleStatus
	beat, since time.Time
}

func (f fakeMetricsSync) Cycle() clone.CycleStatus { return f.c }
func (f fakeMetricsSync) Heartbeat() time.Time     { return f.beat }
func (f fakeMetricsSync) CycleSince() time.Time    { return f.since }

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
	m := &MetricsDeps{Store: &fakeMetricsStore{}, Worker: fakeMetricsWorker{}, Token: metricsToken}
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
	// Two refusals within a minute: one Warn.
	if got := strings.Count(buf.String(), `msg="metrics: a bearer was refused" ip=10.0.0.9`); got != 1 {
		t.Errorf("refusal Warns = %d, want 1: %s", got, buf.String())
	}
	if m.logRefusal(time.Now()) || !m.logRefusal(time.Now().Add(refusedEvery)) {
		t.Error("refusals not logged once per refusedEvery")
	}
	if strings.Contains(buf.String(), metricsToken) {
		t.Errorf("the token is in the log: %s", buf.String())
	}

	// No token set: a bearer never opens it, loopback still does.
	open := metricsHandler(t, &MetricsDeps{Store: &fakeMetricsStore{}, Worker: fakeMetricsWorker{}}, log)
	if rec := getMetrics(open, "10.0.0.9:5000", bearer("x")); rec.Code != http.StatusNotFound {
		t.Errorf("empty token: code = %d, want 404", rec.Code)
	}
	if rec := getMetrics(metricsHandler(t, nil, log), "127.0.0.1:5000", nil); rec.Code != http.StatusNotFound {
		t.Errorf("no metrics: code = %d, want 404", rec.Code)
	}
}

func TestMetricsBody(t *testing.T) {
	merge := time.Unix(1_700_000_000, 0)
	until := time.Unix(1_700_000_600, 0)
	st := &fakeMetricsStore{
		m: store.Metrics{Notes: 3, Runs: 1, Projects: map[int64]string{1: `we"ird\key`, 2: "ABC"}},
		ledger: []ledgerFake{
			{4, store.LedgerRow{Purpose: "merge", ProjectID: 1, Calls: 2, NanoUSD: 1500}, &merge},
			{6, store.LedgerRow{Purpose: "catalogue", ProjectID: 2, Calls: 1, NanoUSD: 0}, nil},
		},
	}
	// A cycle in its listings: not Running yet, the next one past due.
	pastDue := time.Unix(1_600_000_000, 0)
	m := &MetricsDeps{
		Store:  st,
		Worker: fakeMetricsWorker{st: consolidate.Status{HoldUntil: &until, HoldCause: consolidate.HoldRateLimit}, beat: time.Unix(1_700_000_100, 0)},
		Sync: fakeMetricsSync{c: clone.CycleStatus{NextAt: &pastDue}, since: time.Unix(1_700_000_200, 0),
			beat: time.Unix(1_700_000_300, 0)},
	}
	h := metricsHandler(t, m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := getMetrics(h, "127.0.0.1:1", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != metricsType {
		t.Fatalf("code %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{
		"# TYPE almanach_queue_notes gauge\nalmanach_queue_notes 3\n",
		"almanach_queue_runs 1\n",
		"almanach_last_merge_seconds 1700000000\n",
		"almanach_last_sync_seconds 0\n",
		"almanach_worker_hold_until_seconds 1700000600\n",
		`almanach_worker_hold{cause="rate_limit"} 1` + "\n",
		`almanach_worker_hold{cause="timeout"} 0` + "\n",
		"almanach_worker_busy_since_seconds 0\n",
		"almanach_worker_heartbeat_seconds 1700000100\n",
		"almanach_poller_heartbeat_seconds 1700000300\n",
		"almanach_poller_cycle_started_seconds 1700000200\n",
		"almanach_poller_next_cycle_seconds 0\n",
		`almanach_project_info{project_id="1",project="we\"ird\\key"} 1` + "\n",
		`almanach_project_info{project_id="2",project="ABC"} 1` + "\n",
		"# TYPE almanach_llm_calls_total counter\n",
		`almanach_llm_calls_total{purpose="merge",project_id="1"} 2` + "\n",
		`almanach_llm_cost_nano_usd_total{purpose="merge",project_id="1"} 1500` + "\n",
		`almanach_llm_cost_nano_usd_total{purpose="catalogue",project_id="2"} 0` + "\n",
		`almanach_info{version="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	// The next scrape reads only the rows past the last one and adds them.
	st.ledger = append(st.ledger, ledgerFake{9, store.LedgerRow{Purpose: "merge", ProjectID: 1, Calls: 1, NanoUSD: 500}, nil})
	body = getMetrics(h, "127.0.0.1:1", nil).Body.String()
	if want := `almanach_llm_cost_nano_usd_total{purpose="merge",project_id="1"} 2000` + "\n"; !strings.Contains(body, want) {
		t.Errorf("second scrape lacks %q:\n%s", want, body)
	}
	if len(st.asked) != 2 || st.asked[0] != 0 || st.asked[1] != 6 {
		t.Errorf("ledger asked after %v, want [0 6]", st.asked)
	}

	// The hold over: every cause 0, the series still there.
	m.Worker = fakeMetricsWorker{}
	body = getMetrics(h, "127.0.0.1:1", nil).Body.String()
	if !strings.Contains(body, `almanach_worker_hold{cause="rate_limit"} 0`) || !strings.Contains(body, "almanach_worker_hold_until_seconds 0\n") {
		t.Errorf("no hold:\n%s", body)
	}
	// A cause the list does not know yet still shows.
	m.Worker = fakeMetricsWorker{st: consolidate.Status{HoldUntil: &until, HoldCause: "write"}}
	if body = getMetrics(h, "127.0.0.1:1", nil).Body.String(); !strings.Contains(body, `almanach_worker_hold{cause="write"} 1`) {
		t.Errorf("unknown cause:\n%s", body)
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
	m := &MetricsDeps{Store: &fakeMetricsStore{err: errors.New("db down")}, Worker: fakeMetricsWorker{}}
	var buf bytes.Buffer
	rec := getMetrics(metricsHandler(t, m, slog.New(slog.NewTextHandler(&buf, nil))), "127.0.0.1:1", nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(buf.String(), "db down") {
		t.Errorf("code %d, log %s", rec.Code, buf.String())
	}
}

func TestLogClass(t *testing.T) {
	const bearer, basic, poll = "Bearer x", "Basic eDp5", "poll"
	for _, tc := range []struct {
		method, path, header string
		want                 httpapi.LogClass
	}{
		{"POST", "/webhooks/noergler", bearer, httpapi.LogBearer},
		{"GET", "/auth/ping", bearer, httpapi.LogBearer},
		{"GET", "/api/projects/ABC.md", bearer, httpapi.LogBearer},
		{"GET", "/api/projects/ABC/repos/x.md", bearer, httpapi.LogBearer},
		{"GET", "/api/projects/ABC.md", "", httpapi.LogRead},
		// Not a bearer as the gate reads one: a session request.
		{"GET", "/api/projects/ABC.md", basic, httpapi.LogRead},
		{"GET", "/api/projects", "", httpapi.LogRead},
		{"GET", "/api", "", httpapi.LogRead},
		{"POST", "/api/sources/ABC/sync", "", httpapi.LogRead},
		{"GET", "/api/sources/ABC", "", httpapi.LogRead},
		{"GET", "/api/sources/ABC", poll, httpapi.LogRoutine},
		{"GET", "/api/queue/count", poll, httpapi.LogRoutine},
		// A write is never a poll.
		{"POST", "/api/sources/ABC/sync", poll, httpapi.LogRead},
		{"GET", "/metrics", bearer, httpapi.LogRoutine},
		{"GET", "/", "", httpapi.LogRoutine},
		{"GET", "/assets/x.js", "", httpapi.LogRoutine},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		switch tc.header {
		case poll:
			req.Header.Set(pollHeader, "1")
		case "":
		default:
			req.Header.Set("Authorization", tc.header)
		}
		if got := logClass(req); got != tc.want {
			t.Errorf("%s %s %q: %d, want %d", tc.method, tc.path, tc.header, got, tc.want)
		}
	}
}
