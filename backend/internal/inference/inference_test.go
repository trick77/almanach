package inference

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trick77/llmwire"
	"github.com/trick77/llmwire/llmwiretest"
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
			if m.Repository.Summary != "Ein Dienst." || len(m.Repository.Statements) != 0 || len(m.Repository.Added) != 1 {
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
	if res.Merge.Repository != nil || res.Merge.Project == nil || res.Merge.Project.Summary != "Zwei Repos." {
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
	if req.Stream() {
		t.Error("the merge call asked for a stream")
	}
}

func TestMergeUnparseable(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply("not json at all")
	res := testClient(srv.Client()).Merge(context.Background(), MergeRequest{Prompt: "p"})
	if res.Outcome != OutcomeUnparseable {
		t.Fatalf("Outcome = %s", res.Outcome)
	}
	if !res.Cost.Answered {
		t.Error("an unparseable answer still cost tokens: Answered = false")
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

// "off" switches reasoning off where the profile allows it; any other value
// must be a level the profile lists, and the refusal names off too.
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
	if _, err := c.Startup(context.Background()); err == nil || !strings.Contains(err.Error(), EffortOff) {
		t.Errorf("bogus level: err = %v, want a refusal naming %q", err, EffortOff)
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
