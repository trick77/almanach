package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trick77/toolkit/llmwire"
	"github.com/trick77/toolkit/llmwire/llmwiretest"
)

func TestParseMerge(t *testing.T) {
	valid := `{"repository": {"summary": "Ein Dienst.", "statements": [], "added": [{"kind": "rule", "name": "n", "text": "t", "files": ["a.go"]}]}, "project": null}`
	cases := []struct {
		name    string
		content string
		ok      bool
	}{
		{"bare object", valid, true},
		{"fenced json", "```json\n" + valid + "\n```", true},
		{"fenced no language", "```\n" + valid + "\n```\n", true},
		{"padded", "  \n" + valid + "\n\n", true},
		{"null", "null", false},
		{"garbage", "Sure! Here is the merge:", false},
		{"empty", "", false},
		{"fenced garbage", "```json\nnope\n```", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ok := ParseMerge(tc.content)
			if ok != tc.ok {
				t.Fatalf("ParseMerge ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				if m.Repository != nil || m.Project != nil {
					t.Errorf("rejected answer still carries data: %+v", m)
				}
				return
			}
			if m.Repository == nil || m.Project != nil {
				t.Fatalf("Merge = %+v, want repository set and project nil", m)
			}
			if m.Repository.Summary == nil || *m.Repository.Summary != "Ein Dienst." || len(m.Repository.Statements) != 0 || len(m.Repository.Added) != 1 {
				t.Fatalf("Repository = %+v", m.Repository)
			}
			s := m.Repository.Added[0]
			if s.ID != nil || s.Kind != "rule" || s.Name != "n" || s.Text != "t" || len(s.Files) != 1 {
				t.Errorf("Statement = %+v", s)
			}
		})
	}
}

func TestMergeFormat(t *testing.T) {
	f := mergeFormat(true)
	if f.Kind != llmwire.FormatJSONSchema {
		t.Errorf("Kind = %q", f.Kind)
	}
	if f.Name != "almanach_merge" || MergeSchemaName != "almanach_merge" {
		t.Errorf("Name = %q", f.Name)
	}
	if !f.Strict {
		t.Error("Strict = false")
	}
	if f.Schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v", f.Schema["additionalProperties"])
	}
	props, _ := f.Schema["properties"].(map[string]any)
	for _, k := range []string{"repository", "project"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema lacks %q", k)
		}
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{OutcomeOK: "ok", OutcomeTimedOut: "timed_out", OutcomeUnparseable: "unparseable", OutcomeError: "error", OutcomeTooLong: "too_long", Outcome(99): "error"} {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", int(o), got, want)
		}
	}
}

func TestCostFromProvenance(t *testing.T) {
	// A direct-route call is priced off llmwire's table, a gateway call by
	// the gateway: both are a cost, only Unpriced is none.
	for _, tc := range []struct {
		prov llmwire.CostProvenance
		want *int64
	}{
		{llmwire.Reported, new(int64(1_500_000))},
		{llmwire.FromTable, new(int64(1_500_000))},
		{llmwire.Unpriced, nil},
	} {
		res := llmwire.StreamResult{Usage: llmwire.Usage{Cost: llmwire.Cost{NanoUSD: 1_500_000, Provenance: tc.prov}}}
		got := (&Client{model: "m"}).costFrom(res).NanoUSD
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: NanoUSD = %v, want %v", tc.prov, got, tc.want)
		}
	}
}

// A gateway stream without a cost on its usage (LiteLLM without
// include_cost_in_streaming_usage) leaves the call unpriced; llmwire says
// why, and that reason is the only trace of it.
func TestUnpricedCallLogsReason(t *testing.T) {
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"model": "sol", "choices": [{"index": 0, "delta": {"content": "{\"repository\": null, \"project\": null}"}, "finish_reason": null}]}`,
			`{"model": "sol", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}`,
			`{"model": "sol", "choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+frame+"\n\n")
		}
	}))
	t.Cleanup(gw.Close)
	var logs strings.Builder
	c := gatewayClient(t, gw.URL, llmwire.Config{}, slog.New(slog.NewTextHandler(&logs, nil)))

	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeOK || res.Cost.NanoUSD != nil {
		t.Fatalf("Outcome = %s (err %v), NanoUSD = %v, want ok and unpriced", res.Outcome, res.Err, res.Cost.NanoUSD)
	}
	got := logs.String()
	if !strings.Contains(got, "call unpriced") || !strings.Contains(got, "reason=") || strings.Contains(got, "reason=\"none given\"") {
		t.Errorf("an unpriced call logged no reason: %q", got)
	}
}

// streamGateway is a LiteLLM-shaped gateway answering every call with
// frames as one SSE stream, then holding the connection while hang is set;
// the client reads it with idle as its silence bound.
func streamGateway(t *testing.T, frames []string, hang bool, idle time.Duration) *Client {
	t.Helper()
	release := make(chan struct{})
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-litellm-call-id", "call-7")
		for _, f := range frames {
			_, _ = io.WriteString(w, "data: "+f+"\n\n")
		}
		w.(http.Flusher).Flush()
		if hang {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}
	}))
	t.Cleanup(gw.Close)
	t.Cleanup(func() { close(release) })
	return gatewayClient(t, gw.URL, llmwire.Config{IdleTimeout: idle}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// gatewayClient is a client on the mimo-v2.6-pro profile behind a LiteLLM
// gateway at baseURL, its wire set by cfg, the wire's own log discarded.
func gatewayClient(t *testing.T, baseURL string, cfg llmwire.Config, log *slog.Logger) *Client {
	t.Helper()
	env := map[string]string{"LLMWIRE_LITELLM_BASE_URL": baseURL, "LLMWIRE_LITELLM_API_KEY": "k", "LLMWIRE_LITELLM_MODELS": "mimo-v2.6-pro=sol"}
	cfg.Lookup = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	wire, err := llmwire.FromEnv("mimo-v2.6-pro", cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{wire: wire, model: "mimo-v2.6-pro", log: log}
}

const (
	answerFrame = `{"model": "sol", "choices": [{"index": 0, "delta": {"content": "{\"repository\": null, \"project\": null}"}, "finish_reason": null}]}`
	stopFrame   = `{"model": "sol", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}`
)

// A gateway that puts the cost on the stream's usage (LiteLLM with
// include_cost_in_streaming_usage) prices the call as reported; the model
// is the deployment its header names.
func TestStreamPricedByGateway(t *testing.T) {
	c := streamGateway(t, []string{answerFrame, stopFrame,
		`{"model": "sol", "choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "cost": 0.0015}}`, `[DONE]`},
		false, time.Minute)
	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeOK || res.Cost.NanoUSD == nil || *res.Cost.NanoUSD != 1_500_000 || res.Cost.InputTokens != 10 {
		t.Fatalf("Outcome = %s (err %v), cost %+v, want ok at 1.5 m nano-USD", res.Outcome, res.Err, res.Cost)
	}
	if res.Cost.Model != "mimo-v2.6-pro" {
		t.Errorf("model = %q, want the profile when the gateway names no deployment", res.Cost.Model)
	}
}

// A stream that ends without its finish is no answer, whatever it sent:
// an error, never the half read as the whole.
func TestStreamCutIsNoAnswer(t *testing.T) {
	res := streamGateway(t, []string{answerFrame}, false, time.Minute).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeError || res.Err == nil {
		t.Fatalf("Outcome = %s, err %v, want an error", res.Outcome, res.Err)
	}
}

// A stream cut after its usage arrived was paid for: the cost and the
// gateway's call id stay on the failed call, never marked answered.
func TestStreamCutAfterUsageKeepsItsCost(t *testing.T) {
	res := streamGateway(t, []string{answerFrame,
		`{"model": "sol", "choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15, "cost": 0.0015}}`},
		false, time.Minute).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome == OutcomeOK || res.Cost.Answered {
		t.Fatalf("Outcome = %s, cost %+v, want a failed call", res.Outcome, res.Cost)
	}
	if res.Cost.NanoUSD == nil || *res.Cost.NanoUSD != 1_500_000 || res.Cost.CallID != "call-7" {
		t.Errorf("cost %+v, want the usage's 1.5 m nano-USD and the call id", res.Cost)
	}
	// Cut before any usage: no cost, the call id all the same.
	res = streamGateway(t, []string{answerFrame}, false, time.Minute).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Cost.NanoUSD != nil || res.Cost.CallID != "call-7" {
		t.Errorf("cost %+v, want none and the call id", res.Cost)
	}
}

// A refusal of the request inside an open stream is this call's, a bad
// request as its 4xx would be; another error frame stays the gateway's.
func TestStreamRefusalIsABadRequest(t *testing.T) {
	refused := streamGateway(t, []string{`{"error": {"message": "too long", "type": "invalid_request_error", "code": "context_length_exceeded"}}`},
		false, time.Minute).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if refused.Outcome != OutcomeError || !errors.Is(refused.Err, llmwire.ErrBadRequest) {
		t.Errorf("refusal: Outcome = %s, err %v, want a bad request", refused.Outcome, refused.Err)
	}
	busy := streamGateway(t, []string{`{"error": {"message": "overloaded", "type": "server_error"}}`},
		false, time.Minute).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if busy.Outcome != OutcomeError || errors.Is(busy.Err, llmwire.ErrBadRequest) {
		t.Errorf("server error: Outcome = %s, err %v, want the gateway's", busy.Outcome, busy.Err)
	}
}

// A stream gone silent past the idle bound timed out: the worker holds and
// takes the step again, as for a call that never answered.
func TestStreamSilentTimesOut(t *testing.T) {
	res := streamGateway(t, []string{answerFrame}, true, 50*time.Millisecond).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeTimedOut || !errors.Is(res.Err, llmwire.ErrStreamIdle) {
		t.Fatalf("Outcome = %s, err %v, want timed out on the idle bound", res.Outcome, res.Err)
	}
}

// No warning from llmwire still leaves the line: the cost is what decides.
func TestLogUnpricedWithoutReason(t *testing.T) {
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil))}
	c.logUnpriced("abc", nil)
	if got := logs.String(); !strings.Contains(got, "call unpriced") || !strings.Contains(got, "call_id=abc") || !strings.Contains(got, `reason="none given"`) {
		t.Errorf("log = %q", got)
	}
}

// Every model almanach could be configured with (strict JSON) is priced on
// the direct route: a profile without a table rate, or an endpoint whose
// usage llmwire cannot price, would leave every call unpriced again.
func TestEveryModelPricedDirect(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"repository": null, "project": null}`)
	reg := llmwire.Default()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, id := range reg.Models() {
		if _, err := reg.Require(id, llmwire.Needs{JSONSchema: true}); err != nil {
			continue
		}
		t.Run(id, func(t *testing.T) {
			wire := llmwire.New(llmwire.Config{BaseURL: srv.URL, APIKey: "k", Registry: reg, Logger: quiet})
			var logs strings.Builder
			c := &Client{wire: wire, model: id, log: slog.New(slog.NewTextHandler(&logs, nil))}
			res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
			if res.Outcome != OutcomeOK {
				t.Fatalf("Outcome = %s, err %v", res.Outcome, res.Err)
			}
			if res.Cost.NanoUSD == nil || *res.Cost.NanoUSD <= 0 {
				t.Errorf("NanoUSD = %v, want priced; log: %s", res.Cost.NanoUSD, logs.String())
			}
		})
	}
}

func testClient(wire *llmwire.Client) *Client {
	return &Client{wire: wire, model: llmwiretest.ChatModel, log: slog.New(slog.NewTextHandler(io.Discard, nil)), diagrams: true}
}

func TestMergeSendsMessagesAndSchema(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"repository": null, "project": {"summary": "Zwei Repos.", "statements": []}}`)
	c := testClient(srv.Client())

	res := c.Merge(context.Background(), MergeRequest{Prompt: "fold this"})
	if res.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %s, err %v", res.Outcome, res.Err)
	}
	if res.Merge.Repository != nil || res.Merge.Project == nil || res.Merge.Project.Summary == nil || *res.Merge.Project.Summary != "Zwei Repos." {
		t.Errorf("Merge = %+v", res.Merge)
	}
	if !res.Cost.Answered || res.Cost.InputTokens != 10 || res.Cost.OutputTokens != 5 || res.Cost.Model == "" {
		t.Errorf("Cost = %+v", res.Cost)
	}

	req := srv.Last()
	if req.Model() != llmwiretest.ChatModel {
		t.Errorf("model on the wire = %q", req.Model())
	}
	msgs, _ := req.Body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", req.Body["messages"])
	}
	sys, _ := msgs[0].(map[string]any)
	usr, _ := msgs[1].(map[string]any)
	if sys["role"] != "system" || sys["content"] != SystemMessage {
		t.Errorf("system message = %v", sys)
	}
	if usr["role"] != "user" || usr["content"] != "fold this" {
		t.Errorf("user message = %v", usr)
	}
	rf, _ := req.Body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", req.Body["response_format"])
	}
	js, _ := rf["json_schema"].(map[string]any)
	if js["name"] != MergeSchemaName || js["strict"] != true || js["schema"] == nil {
		t.Errorf("json_schema = %v", js)
	}
	if !req.Stream() {
		t.Error("the merge call did not stream")
	}
}

func TestMergeUnparseable(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply("not json at all")
	c := testClient(srv.Client())
	res := c.Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeUnparseable {
		t.Fatalf("Outcome = %s", res.Outcome)
	}
	if !res.Cost.Answered {
		t.Error("an unparseable answer still cost tokens: Answered = false")
	}
	// The test profile's 16k limit holds the merge's answer and room (fit).
	answer, room := c.fit(capMerge, reasoningRoom)
	if want := fmt.Sprintf("(answer %d, reasoning room %d", answer, room); !strings.Contains(res.Err.Error(), want) {
		t.Errorf("err %v names no budget", res.Err)
	}
}

func TestMergeTimeout(t *testing.T) {
	// The endpoint never answers: the call cap gives up first.
	release := make(chan struct{})
	stuck := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(stuck.Close)
	t.Cleanup(func() { close(release) })
	wire := llmwire.New(llmwire.Config{
		BaseURL: stuck.URL, APIKey: "k", Registry: llmwiretest.Registry(), CallTimeout: 50 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	res := testClient(wire).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeTimedOut {
		t.Fatalf("Outcome = %s, err %v", res.Outcome, res.Err)
	}
	if res.Err == nil || res.Cost.Answered {
		t.Errorf("result = %+v", res)
	}
}

// Startup: the profile must do strict JSON and the gateway must list its
// alias. No answer from the gateway is a warning (folds retry); a refused
// key or an alias it does not list is a fault.
func TestStartup(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	if warning, err := testClient(srv.Client()).Startup(context.Background()); err != nil || warning != "" {
		t.Fatalf("Startup against the listing server: warning %q err %v", warning, err)
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	answer := func(status int, body string) *llmwire.Client {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(s.Close)
		return llmwire.New(llmwire.Config{BaseURL: s.URL, APIKey: "k", Registry: llmwiretest.Registry(), CallTimeout: time.Second, Logger: quiet})
	}

	if _, err := testClient(answer(401, `{"error":{"message":"bad key"}}`)).Startup(context.Background()); err == nil || !strings.Contains(err.Error(), "listing failed") || !strings.Contains(err.Error(), "401") {
		t.Errorf("401: err = %v, want the key refused", err)
	}
	if _, err := testClient(answer(200, `{"data":[{"id":"something-else"}]}`)).Startup(context.Background()); err == nil || !strings.Contains(err.Error(), "something-else") {
		t.Errorf("alias missing: err = %v, want the listing named", err)
	}
	// Configuration, not the gateway's day: a 404 (the URL without its /v1)
	// and an HTML page where the listing should be are faults.
	if _, err := testClient(answer(404, `not found`)).Startup(context.Background()); err == nil || !strings.Contains(err.Error(), "listing failed") {
		t.Errorf("404: err = %v, want a fault", err)
	}
	if _, err := testClient(answer(200, `<html>proxy</html>`)).Startup(context.Background()); err == nil || !strings.Contains(err.Error(), "listing failed") {
		t.Errorf("html body: err = %v, want a fault", err)
	}
	// A long listing is sorted and capped in the message.
	var many []string
	for i := 20; i > 0; i-- {
		many = append(many, fmt.Sprintf(`{"id":"m-%02d"}`, i))
	}
	_, err := testClient(answer(200, `{"data":[`+strings.Join(many, ",")+`]}`)).Startup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "m-01, m-02") || !strings.Contains(err.Error(), "and 8 more") || strings.Contains(err.Error(), "m-20") {
		t.Errorf("long listing: err = %v, want sorted, 12 shown, the rest counted", err)
	}

	down := llmwire.New(llmwire.Config{BaseURL: "http://127.0.0.1:1", APIKey: "k", Registry: llmwiretest.Registry(), CallTimeout: time.Second, Logger: quiet})
	warning, err := testClient(down).Startup(context.Background())
	if err != nil || !strings.Contains(warning, "did not answer") {
		t.Errorf("gateway down: warning %q err %v; want a warning only", warning, err)
	}
	warning, err = testClient(answer(503, `{"error":{"message":"overloaded"}}`)).Startup(context.Background())
	if err != nil || !strings.Contains(warning, "did not answer") {
		t.Errorf("503: warning %q err %v; want a warning only", warning, err)
	}
}

// The alias's context is the listing's max_input_tokens; none listed, or
// one llmwire cannot read, is unknown.
func TestStartupContextTokens(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	listing := func(row string) *Client {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"other","max_input_tokens":1000000},`+row+`]}`)
		}))
		t.Cleanup(s.Close)
		c := testClient(llmwire.New(llmwire.Config{BaseURL: s.URL, APIKey: "k", Registry: llmwiretest.Registry(), CallTimeout: time.Second, Logger: quiet}))
		if _, err := c.Startup(context.Background()); err != nil {
			t.Fatalf("Startup: %v", err)
		}
		return c
	}
	for row, want := range map[string]int{
		`{"id":"` + llmwiretest.ChatModel + `","max_input_tokens":200000}`:   200_000,
		`{"id":"` + llmwiretest.ChatModel + `"}`:                             0,
		`{"id":"` + llmwiretest.ChatModel + `","max_input_tokens":"200000"}`: 0,
	} {
		if got := listing(row).ContextTokens(); got != want {
			t.Errorf("%s: ContextTokens = %d, want %d", row, got, want)
		}
	}
}

// The listing's max_output_tokens holds the calls when the gateway caps the
// alias below its profile: the smaller of the two. Every call's answer and
// room then fit it with llmwire's allowance, the room squeezed before the
// answer starves (a 6k limit beside patch's 16k room), and the restyle's
// parts are sized for what a call may answer. One above the profile, none,
// or one llmwire cannot read (said in a warning) leaves the profile's.
func TestStartupOutputTokens(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	prof, err := llmwiretest.Registry().Lookup(llmwiretest.ChatModel)
	if err != nil {
		t.Fatal(err)
	}
	profile := int(prof.Limits.MaxOutput)
	listing := func(row string) (*Client, string) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Another alias's row llmwire cannot read is no warning of this one.
			_, _ = io.WriteString(w, `{"data":[{"id":"other","max_output_tokens":"1000"},`+row+`]}`)
		}))
		t.Cleanup(s.Close)
		c := testClient(llmwire.New(llmwire.Config{BaseURL: s.URL, APIKey: "k", Registry: llmwiretest.Registry(), CallTimeout: time.Second, Logger: quiet}))
		warning, err := c.Startup(context.Background())
		if err != nil {
			t.Fatalf("Startup: %v", err)
		}
		return c, warning
	}
	row := func(limit string) string {
		r := `{"id":"` + llmwiretest.ChatModel + `"`
		if limit != "" {
			r += `,"max_output_tokens":` + limit
		}
		return r + "}"
	}
	for _, c := range []struct {
		limit   string
		want    int
		warning bool
	}{
		{"12000", 12_000, false},
		// Too low for a long answer: said at startup.
		{"6000", 6_000, true},
		{fmt.Sprint(profile * 4), profile, false},
		{"", profile, false},
		{`"12000"`, profile, true},
	} {
		cl, warning := listing(row(c.limit))
		if (warning != "") != c.warning {
			t.Errorf("limit %s: warning %q", c.limit, warning)
		}
		allowance := cl.allowance(prof.Reasoning)
		for _, call := range []struct{ answer, room int }{{capPatch, reasoningRoom}, {capRestyleHi, restyleRoom}, {capBrief, reasoningRoom}} {
			answer, room := cl.fit(call.answer, call.room)
			if answer+room+allowance > c.want || answer < 1 || room < 0 || room > call.room {
				t.Errorf("limit %s: answer %d, room %d of %d asked, allowance %d: want within %d", c.limit, answer, room, call.answer, allowance, c.want)
			}
			// Squeezed, the answer keeps what it asked or three quarters of
			// what the limit leaves, the room what is over.
			if left := c.want - allowance; answer < min(call.answer, left-left/4) {
				t.Errorf("limit %s: answer %d of %d asked, want at least three quarters of %d", c.limit, answer, call.answer, left)
			}
			if wire, limit := cl.wireCap(answer + room); wire > c.want || limit != int64(c.want) {
				t.Errorf("limit %s: wire cap %d, limit %d, want within %d", c.limit, wire, limit, c.want)
			}
		}
		// A full part's honest answer, at the lowest measured 0.7 times its
		// tokens, fits the ceiling; no part is past what one call may hold.
		l := cl.RestyleLimits()
		if l.PartChars/3*7/10 > cl.restyleHi() || l.PartChars > l.OneCallMax {
			t.Errorf("limit %s: parts of %d chars, one call at most %d, for a ceiling of %d", c.limit, l.PartChars, l.OneCallMax, cl.restyleHi())
		}
	}
}

// "off" switches reasoning off where the profile allows it; any other value
// must be a level the profile lists.
func TestEffortOff(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	c := testClient(srv.Client())
	c.effort = EffortOff
	if _, err := c.Startup(context.Background()); err != nil {
		t.Fatalf("off on a model that can be switched off: %v", err)
	}
	if got := c.reasoning(); !reflect.DeepEqual(got, llmwire.ReasoningOff()) {
		t.Errorf("reasoning() = %#v, want ReasoningOff", got)
	}
	c.effort = "bogus"
	// The test profile lists "none": that is its way off, named once.
	if _, err := c.Startup(context.Background()); err == nil || !strings.Contains(err.Error(), "(accepted: none, low, medium, high)") {
		t.Errorf("bogus level: err = %v, want a refusal naming the levels, none among them, and not off", err)
	}
}

// Off is refused only where the model reasons and cannot be switched off;
// on a model that never reasons it is a no-op. A refused level offers off
// only where off switches something.
func TestCheckEffort(t *testing.T) {
	toggle := llmwire.Reasoning{Supported: true, CanBeDisabled: true, EffortValues: []string{"low", "high"}}
	fixed := llmwire.Reasoning{Supported: true, EffortValues: []string{"low", "high"}}
	never := llmwire.Reasoning{}
	for _, c := range []struct {
		name, effort string
		r            llmwire.Reasoning
		want         string // "" for no error, else a part of it
	}{
		{"unset", "", fixed, ""},
		{"a listed level", "high", fixed, ""},
		{"off where it can be switched off", EffortOff, toggle, ""},
		{"off on a model that never reasons", EffortOff, never, ""},
		{"off where it reasons and cannot be switched off", EffortOff, fixed, "cannot be switched off"},
		{"bogus where off switches something", "bogus", toggle, "(accepted: low, high, off)"},
		{"bogus where the levels list none", "bogus", llmwire.Reasoning{Supported: true, CanBeDisabled: true, EffortValues: []string{"none", "high"}}, "(accepted: none, high)"},
		{"bogus where it cannot be switched off", "bogus", fixed, "(accepted: low, high)"},
		{"bogus on a model that never reasons", "bogus", never, "takes no reasoning level"},
	} {
		err := checkEffort(c.effort, "m", c.r)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: err = %v, want none", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want one with %q", c.name, err, c.want)
		}
	}
}

func TestMergeExpiredContext(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	res := testClient(srv.Client()).Merge(ctx, MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeTimedOut {
		t.Fatalf("Outcome = %s, err %v", res.Outcome, res.Err)
	}
}
