package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trick77/toolkit/llmwire"
	"github.com/trick77/toolkit/llmwire/llmwiretest"
)

// Every call carries an output cap: none is unbounded.
func TestCallSendsAnOutputCap(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"repository": null, "project": null}`)
	testClient(srv.Client()).Merge(context.Background(), MergeRequest{Prompt: "p"})
	body := srv.Last().Body
	if body["max_tokens"] == nil && body["max_completion_tokens"] == nil {
		t.Fatalf("no output cap on the wire: %v", body)
	}
}

// A restyle's cap follows the page it rewrites, within its bounds.
func TestRestyleCapFollowsThePrompt(t *testing.T) {
	if got := restyleCap("short"); got != capRestyleLo {
		t.Errorf("short prompt: cap %d, want the floor %d", got, capRestyleLo)
	}
	mid := strings.Repeat("x", 30_000)
	if got := restyleCap(mid); got <= capRestyleLo || got >= capRestyleHi {
		t.Errorf("30k-char prompt: cap %d, want between the bounds", got)
	}
	if got := restyleCap(strings.Repeat("x", 1_000_000)); got != capRestyleHi {
		t.Errorf("huge prompt: cap %d, want the ceiling %d", got, capRestyleHi)
	}
}

// A stream still answering when the call cap fires is a runaway: too long,
// the call's own failure, its tail in the log; never a gateway timeout.
func TestCallCapWhileStreamingIsTooLong(t *testing.T) {
	answer := `{"content": "{\"added\": [{\"name\": \"x\"}, {\"name\": \"x\"}"}`
	if got := capRun(t, answer); !strings.Contains(got, "added") {
		t.Errorf("no tail of the cut answer in the log: %q", got)
	}
	// Thinking to the cap without a word of answer: a runaway all the same.
	capRun(t, `{"reasoning_content": "hmm, again"}`)
}

// capRun streams one delta, then hangs past the call cap: too long, never
// the gateway's. It returns the log. A fake transport, not a server: the cap
// raced the first delta under load; llmwire's own tests cover the real
// transport cancelled mid-body.
func capRun(t *testing.T, delta string) string {
	t.Helper()
	tr := &hangingStream{frame: "data: " + `{"model": "sol", "choices": [{"index": 0, "delta": ` + delta + `, "finish_reason": null}]}` + "\n\n"}
	var logs strings.Builder
	c := gatewayClient(t, "https://gateway.invalid", llmwire.Config{HTTPClient: &http.Client{Transport: tr},
		CallTimeout: time.Millisecond, IdleTimeout: time.Minute}, slog.New(slog.NewTextHandler(&logs, nil)))
	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if tr.calls.Load() == 0 {
		t.Fatalf("the fake transport was bypassed: Outcome = %s, err %v", res.Outcome, res.Err)
	}
	if res.Outcome != OutcomeTooLong || !errors.Is(res.Err, llmwire.ErrCallCap) {
		t.Fatalf("Outcome = %s, err %v, want too long on the call cap", res.Outcome, res.Err)
	}
	if got := logs.String(); !strings.Contains(got, "answer cut") {
		t.Errorf("no cut logged: %q", got)
	}
	return logs.String()
}

// hangingStream answers every request with frame, then a body that hangs
// until the request is cancelled. The frame is handed over even if the cap
// has already fired, so the cap always lands mid-stream, never before the
// first delta, whatever the cap.
type hangingStream struct {
	frame string
	calls atomic.Int64
}

func (h *hangingStream) RoundTrip(r *http.Request) (*http.Response, error) {
	h.calls.Add(1)
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
		Body: &hangingBody{ctx: r.Context(), rest: h.frame, closed: make(chan struct{})}, Request: r}, nil
}

// hangingBody hangs after its frame until the request is cancelled or the
// body closed, as a real one does.
type hangingBody struct {
	ctx    context.Context
	rest   string
	closed chan struct{}
	once   sync.Once
}

func (b *hangingBody) Read(p []byte) (int, error) {
	select {
	case <-b.closed:
		return 0, http.ErrBodyReadAfterClose
	default:
	}
	if b.rest != "" {
		n := copy(p, b.rest)
		b.rest = b.rest[n:]
		return n, nil
	}
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, http.ErrBodyReadAfterClose
	}
}

func (b *hangingBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// Cut by the output cap: too long, and the tail logged.
func TestLengthFinishLogsTheTail(t *testing.T) {
	var logs strings.Builder
	c := streamGateway(t, []string{answerFrame, `{"model": "sol", "choices": [{"index": 0, "delta": {}, "finish_reason": "length"}]}`}, false, time.Minute)
	c.log = slog.New(slog.NewTextHandler(&logs, nil))
	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeTooLong {
		t.Fatalf("Outcome = %s, want too long", res.Outcome)
	}
	if !strings.Contains(logs.String(), "answer cut") {
		t.Errorf("no cut line: %q", logs.String())
	}
}

// The log keeps only the end of a long answer.
func TestLogCutKeepsTheTail(t *testing.T) {
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil))}
	c.logCut("id", strings.Repeat("a", 5000)+"THE-END")
	got := logs.String()
	if !strings.Contains(got, "THE-END") || strings.Count(got, "a") > cutTail+20 {
		t.Errorf("log = %q", got)
	}
	// The key before the window, the value in it: redacted all the same.
	logs.Reset()
	c.logCut("id", "Authorization: Bearer abcdefghijKLMNOPQRSTuvwxyz0123456789"+strings.Repeat(" b", 190))
	if got := logs.String(); strings.Contains(got, "0123456789") {
		t.Errorf("a value whose key fell before the tail was logged: %q", got)
	}
}

// A loop is the same stretch at the answer's end over loopSpan chars; an
// answer that only repeats its structure, or repeats briefly, is none.
func TestLooping(t *testing.T) {
	para := func(s string) string { return `{"kind": "paragraph", "text": "` + s + `"}, ` }
	var varied strings.Builder
	for i := range 400 {
		varied.WriteString(para(fmt.Sprintf("Schritt %d liest die Datei und schreibt den Eintrag %d.", i, i*7)))
	}
	for name, c := range map[string]struct {
		s    string
		want bool
	}{
		"a phrase again and again":    {"Der Ablauf geht " + strings.Repeat("so weiter so fort ", 120), true},
		"a heading and its paragraph": {strings.Repeat(`{"kind": "heading", "text": "Zwei Einstiege"}, `+para("Zwei Einstiege."), 30), true},
		"three paragraphs in turn":    {strings.Repeat(para("Die Struktur:")+para("Sie besteht aus Teilen:")+para("Das ist die Struktur."), 12), true},
		"varied statements":           {varied.String(), false},
		"a short repeat":              {"Anfang " + strings.Repeat("ab ", 200), false},
		"a separator row":             {"| a | b |\n" + strings.Repeat("|---", 300), false},
		"empty":                       {"", false},
	} {
		if got := looping([]byte(c.s)); got != c.want {
			t.Errorf("%s: looping = %v, want %v", name, got, c.want)
		}
	}
}

// A streamed answer that loops is cut by the guard well before the cap:
// too long, the call's own, its tail logged. Reasoning alike: a model can
// think in circles without a word of answer.
func TestLoopGuardCutsTheStream(t *testing.T) {
	for field, want := range map[string]string{"content": "the answer repeated itself", "reasoning_content": "the reasoning repeated itself"} {
		t.Run(field, func(t *testing.T) {
			var frames atomic.Int64
			gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for r.Context().Err() == nil && frames.Load() < 100_000 {
					frames.Add(1)
					_, _ = io.WriteString(w, "data: "+`{"model": "sol", "choices": [{"index": 0, "delta": {"`+field+`": "so weiter so fort "}, "finish_reason": null}]}`+"\n\n")
					w.(http.Flusher).Flush()
				}
			}))
			t.Cleanup(gw.Close)
			var logs strings.Builder
			c := gatewayClient(t, gw.URL, llmwire.Config{CallTimeout: time.Minute, IdleTimeout: time.Minute}, slog.New(slog.NewTextHandler(&logs, nil)))
			res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
			if res.Outcome != OutcomeTooLong || res.Err == nil || !strings.Contains(res.Err.Error(), want) {
				t.Fatalf("Outcome = %s, err %v, want too long, %q", res.Outcome, res.Err, want)
			}
			if frames.Load() >= 100_000 {
				t.Error("the stream ran on: want it cut by the guard")
			}
			if !res.Cost.Streamed || res.Cost.NanoUSD != nil {
				t.Errorf("cost = %+v, want streamed, unpriced: billed without a price", res.Cost)
			}
			if !strings.Contains(logs.String(), "answer cut") {
				t.Errorf("no tail logged: %q", logs.String())
			}
		})
	}
}
