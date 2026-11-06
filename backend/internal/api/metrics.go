package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
	kitsecret "github.com/trick77/ship/kit/secret"
	"github.com/trick77/ship/kit/version"
)

// /metrics: the worker, the poller, the queue and the ledger in
// Prometheus's text format, written by hand (no client library beyond
// kit's). Every project's spend is in it, so no team's bearer opens it:
// a loopback caller does, as on kit's probes, and BACKEND_METRICS_TOKEN's
// bearer from anywhere.

// MetricsStore is what /metrics reads of the store.
type MetricsStore interface {
	Metrics(ctx context.Context) (store.Metrics, error)
	Ledger(ctx context.Context, afterID int64) ([]store.LedgerRow, int64, error)
}

// MetricsWorker is the worker as /metrics sees it.
type MetricsWorker interface {
	Status() consolidate.Status
	Heartbeat() time.Time
}

// MetricsSync is the poller as /metrics sees it.
type MetricsSync interface {
	Cycle() clone.CycleStatus
	Heartbeat() time.Time
}

// MetricsDeps is what /metrics needs. Used by pointer: it keeps the
// ledger's running totals between scrapes.
type MetricsDeps struct {
	Store  MetricsStore
	Worker MetricsWorker
	// Sync is nil with the sources off: its series are left out.
	Sync MetricsSync
	// Token is BACKEND_METRICS_TOKEN; "" opens /metrics to loopback only.
	Token string

	// The ledger summed so far: each scrape adds the rows past lastID, so
	// it never reads the whole ever-growing table again. A restart reads
	// it once. lmu is held across the read, so two scrapes never add the
	// same rows.
	lmu    sync.Mutex
	lastID int64
	totals map[ledgerKey]ledgerSum
}

type ledgerKey struct {
	purpose   string
	projectID int64
}

type ledgerSum struct{ calls, nano int64 }

// metricsType is the exposition format's content type.
const metricsType = "text/plain; version=0.0.4; charset=utf-8"

func (d Deps) metrics(w http.ResponseWriter, r *http.Request) {
	m := d.Metrics
	if m == nil || !m.allowed(r) {
		if _, sent := httpx.Bearer(r); sent && m != nil {
			// Its access line is routine (scrapes every few seconds): the
			// refusal is logged here, with who sent it.
			d.Log.WarnContext(r.Context(), "metrics: a bearer was refused", slog.String("ip", httpx.ClientIP(r, 0)))
		}
		http.NotFound(w, r)
		return
	}
	st, err := m.Store.Metrics(r.Context())
	if err == nil {
		err = m.addLedger(r.Context())
	}
	if err != nil {
		d.Log.ErrorContext(r.Context(), "metrics: reading the store failed", "err", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "metrics unavailable")
		return
	}
	var b strings.Builder
	m.write(&b, st)
	w.Header().Set("Content-Type", metricsType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// allowed: a direct loopback caller, or the metrics token's bearer
// (kit's Match: hashed, so a guesser learns not even its length).
func (m *MetricsDeps) allowed(r *http.Request) bool {
	if token, ok := httpx.Bearer(r); ok {
		return kitsecret.Match(token, m.Token)
	}
	return loopback(r)
}

// loopback is kit's probe rule (httpx.Health's, unexported there): the TCP
// peer is loopback and no proxy forwarded the request. A forwarding header
// only ever refuses, so a caller cannot add one to get in.
func loopback(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip"} {
		if _, ok := r.Header[h]; ok {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}

// addLedger adds the calls since the last scrape to the totals.
func (m *MetricsDeps) addLedger(ctx context.Context) error {
	m.lmu.Lock()
	defer m.lmu.Unlock()
	rows, last, err := m.Store.Ledger(ctx, m.lastID)
	if err != nil {
		return err
	}
	if m.totals == nil {
		m.totals = map[ledgerKey]ledgerSum{}
	}
	for _, r := range rows {
		k := ledgerKey{r.Purpose, r.ProjectID}
		s := m.totals[k]
		s.calls += r.Calls
		s.nano += r.NanoUSD
		m.totals[k] = s
	}
	m.lastID = last
	return nil
}

// holdCauses are the hold series' labels, every one always written (0 or
// 1), so a series never appears or goes stale with a hold.
var holdCauses = []string{consolidate.HoldTimeout, consolidate.HoldRateLimit, consolidate.HoldKeyRefused,
	consolidate.HoldUnavailable, consolidate.HoldBusy}

// write renders the series. A time is unix seconds, 0 for none.
func (m *MetricsDeps) write(b *strings.Builder, st store.Metrics) {
	series(b, "almanach_info", "gauge", "The running version.", sample{labels: labels("version", version.String()), value: 1})
	series(b, "almanach_queue_notes", "gauge", "Notes not yet folded.", sample{value: int64(st.Notes)})
	series(b, "almanach_queue_runs", "gauge", "Baseline runs open.", sample{value: int64(st.Runs)})
	series(b, "almanach_last_merge_seconds", "gauge", "When the newest merge call answered ok (the call, not its patch's apply), unix seconds.",
		sample{value: unixOrZero(st.LastMerge)})
	series(b, "almanach_last_sync_seconds", "gauge", "When any source repository last synced, unix seconds.",
		sample{value: unixOrZero(st.LastSync)})

	ws := m.Worker.Status()
	var until int64
	if ws.HoldUntil != nil {
		until = ws.HoldUntil.Unix()
	}
	series(b, "almanach_worker_hold_until_seconds", "gauge", "When the worker's hold on the gateway ends, unix seconds; 0 when none.", sample{value: until})
	causes := holdCauses
	if ws.HoldCause != "" && !slices.Contains(causes, ws.HoldCause) {
		causes = append(slices.Clone(causes), ws.HoldCause)
	}
	hold := make([]sample, 0, len(causes))
	for _, c := range causes {
		var on int64
		if ws.HoldUntil != nil && c == ws.HoldCause {
			on = 1
		}
		hold = append(hold, sample{labels: labels("cause", c), value: on})
	}
	series(b, "almanach_worker_hold", "gauge", "1 for the cause of the hold that is on, 0 for every other.", hold...)
	var busy int64
	if ws.Busy != nil {
		busy = ws.Busy.Since.Unix()
	}
	series(b, "almanach_worker_busy_since_seconds", "gauge", "Since when the worker is on the item in flight, unix seconds; 0 when idle.", sample{value: busy})
	hb := m.Worker.Heartbeat()
	series(b, "almanach_worker_heartbeat_seconds", "gauge", "The worker loop's last heartbeat, unix seconds.", sample{value: unixOrZero(&hb)})

	if m.Sync != nil {
		hb := m.Sync.Heartbeat()
		series(b, "almanach_poller_heartbeat_seconds", "gauge", "The poller loop's last heartbeat, unix seconds.", sample{value: unixOrZero(&hb)})
		c := m.Sync.Cycle()
		var started, next int64
		if c.Running {
			started = c.StartedAt.Unix()
		} else if c.NextAt != nil {
			next = c.NextAt.Unix()
		}
		series(b, "almanach_poller_cycle_started_seconds", "gauge", "When the sync cycle in flight started, unix seconds; 0 when none runs.", sample{value: started})
		series(b, "almanach_poller_next_cycle_seconds", "gauge", "When the next sync cycle is due, unix seconds; 0 with no interval or one running. Past due: the cycle is stuck before its first repository.", sample{value: next})
	}

	projects := make([]sample, 0, len(st.Projects))
	for id, key := range st.Projects {
		projects = append(projects, sample{labels: labels("project_id", strconv.FormatInt(id, 10), "project", key), value: 1})
	}
	series(b, "almanach_project_info", "gauge", "A project's current key by its id, for joining the counters.", projects...)

	m.lmu.Lock()
	calls := make([]sample, 0, len(m.totals))
	costs := make([]sample, 0, len(m.totals))
	for k, s := range m.totals {
		l := labels("purpose", k.purpose, "project_id", strconv.FormatInt(k.projectID, 10))
		calls = append(calls, sample{labels: l, value: s.calls})
		costs = append(costs, sample{labels: l, value: s.nano})
	}
	m.lmu.Unlock()
	series(b, "almanach_llm_calls_total", "counter", "Almanach's own model calls by purpose and project id.", calls...)
	series(b, "almanach_llm_cost_nano_usd_total", "counter", "What the priced ones of them cost, nano-USD.", costs...)
}

// sample is one line of a series: its label text and an integer value:
// money is nano-USD and every time whole seconds, so no float is written.
type sample struct {
	labels string
	value  int64
}

// series writes one metric, its samples in label order (the totals come
// from a map).
func series(b *strings.Builder, name, kind, help string, samples ...sample) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	slices.SortFunc(samples, func(x, y sample) int { return strings.Compare(x.labels, y.labels) })
	for _, s := range samples {
		b.WriteString(name)
		b.WriteString(s.labels)
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(s.value, 10))
		b.WriteByte('\n')
	}
}

// labelEscape escapes a label value's backslash, quote and newline, as
// the format asks.
var labelEscape = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// labels is {a="x",b="y"} of name, value pairs.
func labels(pairs ...string) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(pairs[i])
		b.WriteString(`="`)
		b.WriteString(labelEscape.Replace(pairs[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func unixOrZero(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.Unix()
}
