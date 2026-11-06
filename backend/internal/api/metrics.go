package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
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

// MetricsDeps is what /metrics needs.
type MetricsDeps struct {
	Store  MetricsStore
	Worker MetricsWorker
	// Sync is nil with the sources off: its series are left out.
	Sync MetricsSync
	// Token is BACKEND_METRICS_TOKEN; "" opens /metrics to loopback only.
	Token string
}

// metricsType is the exposition format's content type.
const metricsType = "text/plain; version=0.0.4; charset=utf-8"

func (d Deps) metrics(w http.ResponseWriter, r *http.Request) {
	m := d.Metrics
	if m == nil || !m.allowed(r) {
		if _, sent := httpx.Bearer(r); sent && m != nil {
			d.Log.WarnContext(r.Context(), "metrics: a bearer was refused")
		}
		http.NotFound(w, r)
		return
	}
	st, err := m.Store.Metrics(r.Context())
	if err != nil {
		d.Log.ErrorContext(r.Context(), "metrics: reading the store failed", "err", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "metrics unavailable")
		return
	}
	var b strings.Builder
	writeMetrics(&b, st, m.Worker, m.Sync)
	w.Header().Set("Content-Type", metricsType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// allowed: a direct loopback caller, or the metrics token's bearer.
func (m *MetricsDeps) allowed(r *http.Request) bool {
	if token, ok := httpx.Bearer(r); ok {
		return m.Token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(m.Token)) == 1
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

// writeMetrics renders the series. A time is unix seconds, 0 for none.
func writeMetrics(b *strings.Builder, st store.Metrics, wk MetricsWorker, sy MetricsSync) {
	series(b, "almanach_info", "gauge", "The running version.", sample{labels: []string{"version", version.String()}, value: 1})
	series(b, "almanach_queue_notes", "gauge", "Notes not yet folded.", sample{value: int64(st.Notes)})
	series(b, "almanach_queue_runs", "gauge", "Baseline runs open.", sample{value: int64(st.Runs)})
	series(b, "almanach_last_merge_seconds", "gauge", "When the newest merge call answered ok (the call, not its patch's apply), unix seconds.",
		sample{value: unixOrZero(st.LastMerge)})
	series(b, "almanach_last_sync_seconds", "gauge", "When any source repository last synced, unix seconds.",
		sample{value: unixOrZero(st.LastSync)})

	ws := wk.Status()
	var hold sample
	if ws.HoldUntil != nil {
		hold = sample{labels: []string{"cause", ws.HoldCause}, value: ws.HoldUntil.Unix()}
	}
	series(b, "almanach_worker_hold_until_seconds", "gauge", "When the worker's hold on the gateway ends, unix seconds; 0 when none.", hold)
	var busy int64
	if ws.Busy != nil {
		busy = ws.Busy.Since.Unix()
	}
	series(b, "almanach_worker_busy_since_seconds", "gauge", "Since when the worker is on the item in flight, unix seconds; 0 when idle.", sample{value: busy})
	hb := wk.Heartbeat()
	series(b, "almanach_worker_heartbeat_seconds", "gauge", "The worker loop's last heartbeat, unix seconds.", sample{value: unixOrZero(&hb)})

	if sy != nil {
		hb := sy.Heartbeat()
		series(b, "almanach_poller_heartbeat_seconds", "gauge", "The poller loop's last heartbeat, unix seconds.", sample{value: unixOrZero(&hb)})
		c := sy.Cycle()
		var started, next int64
		if c.Running {
			started = c.StartedAt.Unix()
		} else if c.NextAt != nil {
			next = c.NextAt.Unix()
		}
		series(b, "almanach_poller_cycle_started_seconds", "gauge", "When the sync cycle in flight started, unix seconds; 0 when none runs.", sample{value: started})
		series(b, "almanach_poller_next_cycle_seconds", "gauge", "When the next sync cycle starts on its own, unix seconds; 0 with no interval or one running.", sample{value: next})
	}

	calls := make([]sample, 0, len(st.Calls))
	costs := make([]sample, 0, len(st.Calls))
	for _, c := range st.Calls {
		l := []string{"purpose", c.Purpose, "project", c.ProjectKey}
		calls = append(calls, sample{labels: l, value: c.Calls})
		costs = append(costs, sample{labels: l, value: c.NanoUSD})
	}
	series(b, "almanach_llm_calls_total", "counter", "Almanach's own model calls by purpose and project.", calls...)
	series(b, "almanach_llm_cost_nano_usd_total", "counter", "What the priced ones of them cost, nano-USD.", costs...)
}

// sample is one line of a series: label pairs (name, value, ...) and an
// integer value: money is nano-USD and every time whole seconds, so no
// float is ever written.
type sample struct {
	labels []string
	value  int64
}

func series(b *strings.Builder, name, kind, help string, samples ...sample) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	sort.SliceStable(samples, func(i, j int) bool { return labelText(samples[i].labels) < labelText(samples[j].labels) })
	for _, s := range samples {
		b.WriteString(name)
		b.WriteString(labelText(s.labels))
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(s.value, 10))
		b.WriteByte('\n')
	}
}

// labelText is {a="x",b="y"}, "" for none; a value's backslash, quote and
// newline escaped as the format asks.
func labelText(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(pairs[i])
		b.WriteString(`="`)
		b.WriteString(esc.Replace(pairs[i+1]))
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
