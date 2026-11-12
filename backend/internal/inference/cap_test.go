package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// A restyle's cap follows the page it rewrites, never the prompt, within
// its bounds.
func TestRestyleCapFollowsThePage(t *testing.T) {
	if got := restyleCap(5, capRestyleHi); got != capRestyleLo {
		t.Errorf("short page: cap %d, want the floor %d", got, capRestyleLo)
	}
	if got := restyleCap(30_000, capRestyleHi); got != 17_000 {
		t.Errorf("30k-char page: cap %d, want 10k tokens and half again, 2k on top", got)
	}
	if got := restyleCap(1_000_000, capRestyleHi); got != capRestyleHi {
		t.Errorf("huge page: cap %d, want the ceiling %d", got, capRestyleHi)
	}
}

// A part of a restyle in parts is as large as one call's cap still follows:
// at PartChars the cap reaches the ceiling, below it not yet. One call past
// OneCallMax cannot fit even at the lowest measured answer, 0.7 times the
// page's tokens.
func TestRestyleBounds(t *testing.T) {
	for _, hi := range []int{capRestyleHi, 12_000} {
		l := restyleLimits(hi)
		if got := restyleCap(l.PartChars, hi); got != hi {
			t.Errorf("ceiling %d, part budget %d: cap %d, want the ceiling", hi, l.PartChars, got)
		}
		if got := restyleCap(l.PartChars-2, hi); got >= hi {
			t.Errorf("ceiling %d, below the part budget: cap %d, want under it", hi, got)
		}
		if lowest := l.OneCallMax / 3 * 7 / 10; lowest < hi-10 || lowest > hi {
			t.Errorf("ceiling %d, one call's limit %d: its lowest answer %d tokens", hi, l.OneCallMax, lowest)
		}
	}
	if DefaultRestyleLimits != restyleLimits(capRestyleHi) {
		t.Errorf("default limits %+v, want capRestyleHi's", DefaultRestyleLimits)
	}
}

// An answer's cap is held under the model's output limit as llmwire knows
// it, beside a reasoning room (fit): a cap and a room that together pass the
// limit would leave the reasoning nothing. The restyle's ceiling and its
// parts follow the same model limit.
func TestAnswerHeldUnderTheModelLimit(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"outline": null, "summary": null, "statements": [], "added": []}`)
	c := testClient(srv.Client())
	// The test profile's max_output is 16384: a restyle's 32k and its room
	// do not fit, the room squeezed, the answer the rest.
	p, err := llmwiretest.Registry().Lookup(llmwiretest.ChatModel)
	if err != nil {
		t.Fatal(err)
	}
	allowance := c.allowance(p.Reasoning)
	ceiling := c.restyleCeiling()
	_, room := c.fit(capRestyleHi, restyleRoom)
	if ceiling <= 0 || ceiling >= capRestyleHi || room <= 0 || room >= restyleRoom || ceiling+room+allowance > 16_384 {
		t.Fatalf("answer ceiling %d beside a room of %d and the allowance %d, want all within 16384", ceiling, room, allowance)
	}
	res := c.Restyle(context.Background(), "p", 900_000, false)
	if res.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %s (%v)", res.Outcome, res.Err)
	}
	body := srv.Last().Body
	got := body["max_tokens"]
	if got == nil {
		got = body["max_completion_tokens"]
	}
	if n, _ := got.(float64); int(n) > 16_384 || int(n) < ceiling+room {
		t.Errorf("wire cap %v, want the answer held to %d with the room %d on top, within 16384", got, ceiling, room)
	}
	if l := c.RestyleLimits(); l != restyleLimits(ceiling) || l.PartChars >= DefaultRestyleLimits.PartChars {
		t.Errorf("restyle limits %+v, want those of the held ceiling %d", l, ceiling)
	}
	// Unknown to llmwire: no hold, the defaults.
	if got := (&Client{model: "nope"}).RestyleLimits(); got != DefaultRestyleLimits {
		t.Errorf("no wire: limits %+v, want the defaults", got)
	}
}

// A restyle thinks little: its wire cap carries restyleRoom, not
// reasoningRoom.
func TestRestyleReasoningRoom(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"outline": null, "summary": null, "statements": [], "added": []}`)
	// A small page: the test profile's own output limit stays above.
	testClient(srv.Client()).Restyle(context.Background(), "p", 5, false)
	body := srv.Last().Body
	got := body["max_tokens"]
	if got == nil {
		got = body["max_completion_tokens"]
	}
	n, _ := got.(float64)
	if want := restyleCap(5, capRestyleHi) + restyleRoom; int(n) < want || int(n) > want+llmwire.DefaultReasoningOverhead {
		t.Errorf("wire cap %v, want %d plus llmwire's overhead at most", got, want)
	}
}

// A stream still answering when the call cap fires is a runaway: too long,
// the call's own failure, its tail in the log; never a gateway timeout.
func TestCallCapWhileStreamingIsTooLong(t *testing.T) {
	answer := `{"content": "{\"added\": [{\"name\": \"x\"}, {\"name\": \"x\"}"}`
	if got := capRun(t, answer); !strings.Contains(got, "added") {
		t.Errorf("no tail of the cut answer in the log: %q", got)
	}
	// Thinking to the cap without a word of answer: a runaway all the same,
	// the reasoning's tail in the log.
	if got := capRun(t, `{"reasoning_content": "hmm, again"}`); !strings.Contains(got, "hmm, again") {
		t.Errorf("no tail of the cut reasoning in the log: %q", got)
	}
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

// A restyle cut by the output cap names its budget in the error, the log
// line and the kept file: the tokens and of them the reasoning as the
// gateway counted them, the caps, the page.
func TestLengthFinishNamesTheBudget(t *testing.T) {
	var logs strings.Builder
	c := streamGateway(t, []string{answerFrame,
		`{"model": "sol", "choices": [{"index": 0, "delta": {}, "finish_reason": "length"}]}`,
		`{"model": "sol", "choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 17000, "total_tokens": 17010, "completion_tokens_details": {"reasoning_tokens": 9800}}}`,
	}, false, time.Minute)
	c.log = slog.New(slog.NewTextHandler(&logs, nil))
	c.cutDir = t.TempDir()
	res := c.Restyle(context.Background(), "p", 900, false)
	if res.Outcome != OutcomeTooLong {
		t.Fatalf("Outcome = %s, want too long", res.Outcome)
	}
	wire, limit := c.wireCap(restyleCap(900, capRestyleHi) + restyleRoom)
	want := fmt.Sprintf("output limit (17000 tokens out, 9800 of them reasoning; cap %d (answer %d, reasoning room %d, model limit %d), page 900 chars)",
		wire, restyleCap(900, capRestyleHi), restyleRoom, limit)
	if res.Err == nil || !strings.Contains(res.Err.Error(), want) {
		t.Errorf("err %v, want it to name %q", res.Err, want)
	}
	if got := logs.String(); !strings.Contains(got, "budget.reasoning_tokens=9800") || !strings.Contains(got, "budget.page_chars=900") {
		t.Errorf("cut line names no budget: %q", got)
	}
	var f cutFile
	readCut(t, c.cutDir, &f)
	if f.Cap != restyleCap(900, capRestyleHi) || f.Room != restyleRoom || f.WireCap != wire || f.MaxOutput != limit || f.PageChars != 900 || f.PromptTokens == 0 ||
		f.OutTokens == nil || *f.OutTokens != 17000 || f.ReasoningTokens == nil || *f.ReasoningTokens != 9800 {
		t.Errorf("cut file budget %+v", f.cutBudget)
	}
}

// Without the gateway's usage, the wire cap or a room the budget names what
// it knows, never a zero for what it does not.
func TestBudgetWithoutUsage(t *testing.T) {
	got := cutBudget{Cap: 8000}.String()
	if got != "cap: answer 8000" {
		t.Errorf("budget %q", got)
	}
}

// The wire cap the budget names is the max_tokens llmwire sends, its
// reasoning allowance and the model's limit taken as llmwire takes them.
func TestBudgetWireCapIsTheSentCap(t *testing.T) {
	for _, effort := range []string{"", EffortOff} {
		srv := llmwiretest.NewServer(t)
		srv.SetReply(`{"repository": null, "project": null}`)
		c := testClient(srv.Client())
		c.effort = effort
		c.Merge(context.Background(), MergeRequest{Prompt: "p"})
		body := srv.Last().Body
		sent := body["max_tokens"]
		if sent == nil {
			sent = body["max_completion_tokens"]
		}
		asked := capMerge
		if effort != EffortOff {
			asked += reasoningRoom
		}
		if n, _ := sent.(float64); int(n) == 0 {
			t.Fatalf("effort %q: no cap on the wire: %v", effort, body)
		} else if wire, _ := c.wireCap(asked); wire != int(n) {
			t.Errorf("effort %q: wire cap %d, sent %v", effort, wire, sent)
		}
	}
}

// readCut decodes the one file keepCut wrote to dir.
func readCut(t *testing.T, dir string, into any) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cut dir: %v, %d files, want 1", err, len(entries))
	}
	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

// The log keeps a long answer's head and end, not its middle.
func TestLogCutKeepsTheTail(t *testing.T) {
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil))}
	c.logCut("id", nil, cutBudget{}, answerPart("THE-START"+strings.Repeat("a", 5000)+"THE-END"))
	got := logs.String()
	if !strings.Contains(got, "THE-START") || !strings.Contains(got, "THE-END") || strings.Count(got, "a") > 2*cutTail+20 ||
		!strings.Contains(got, "what=answer") {
		t.Errorf("log = %q", got)
	}
	// The key before the window, the value in it: redacted all the same,
	// a short text and a long one redacted near its end only.
	secret := "Authorization: Bearer abcdefghijKLMNOPQRSTuvwxyz0123456789"
	for _, text := range []string{
		secret + strings.Repeat(" b", 190),
		strings.Repeat("x", 12_288) + secret + strings.Repeat(" b", 190),
	} {
		logs.Reset()
		c.logCut("id", nil, cutBudget{}, answerPart(text))
		if got := logs.String(); strings.Contains(got, "0123456789") {
			t.Errorf("a value whose key fell before the tail was logged: %q", got)
		}
	}
	// Both parts, each named; a blank answer (a think just ended) none.
	logs.Reset()
	c.logCut("id", nil, cutBudget{}, answerPart("\n\n"), reasoningPart("round and round"))
	if got := logs.String(); strings.Count(got, "answer cut") != 1 || !strings.Contains(got, "what=reasoning") ||
		!strings.Contains(got, "round and round") {
		t.Errorf("blank answer, reasoning: %q", got)
	}
	logs.Reset()
	c.logCut("id", nil, cutBudget{}, answerPart("{\"x\""), reasoningPart("long thought"))
	if got := logs.String(); strings.Count(got, "answer cut") != 2 {
		t.Errorf("both parts: %q", got)
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
