package inference

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
// the gateway's. It returns the log.
func capRun(t *testing.T, delta string) string {
	t.Helper()
	release := make(chan struct{})
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"model": "sol", "choices": [{"index": 0, "delta": `+delta+`, "finish_reason": null}]}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(gw.Close)
	t.Cleanup(func() { close(release) })
	env := map[string]string{"LLMWIRE_LITELLM_BASE_URL": gw.URL, "LLMWIRE_LITELLM_API_KEY": "k", "LLMWIRE_LITELLM_MODELS": "mimo-v2.6-pro=sol"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	var logs strings.Builder
	log := slog.New(slog.NewTextHandler(&logs, nil))
	wire, err := llmwire.FromEnv("mimo-v2.6-pro", llmwire.Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Lookup: lookup,
		CallTimeout: 100 * time.Millisecond, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{wire: wire, model: "mimo-v2.6-pro", log: log}
	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeTooLong || !errors.Is(res.Err, llmwire.ErrCallCap) {
		t.Fatalf("Outcome = %s, err %v, want too long on the call cap", res.Outcome, res.Err)
	}
	if got := logs.String(); !strings.Contains(got, "answer cut") {
		t.Errorf("no cut logged: %q", got)
	}
	return logs.String()
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
