package ingest

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// fullPayload is a valid payload with every field set, note_call with every
// lane, rounds, review totals and one claim with a null repo_slug.
func fullPayload() Payload {
	return Payload{
		EventType:         EventType,
		ProjectKey:        "PLAT",
		RepoSlug:          "files",
		PRID:              42,
		PRKey:             "PLAT/files#42",
		PRTitle:           "Fix upload retry",
		TicketKey:         ptr("PLAT-7"),
		JiraIssueType:     ptr("Bug"),
		ChangeKind:        ptr("bugfix"),
		ReviewedCommit:    ptr("ABCDEF1234567"),
		SourceCommitSHA:   "ABCDEF1234567890",
		MergeCommitSHA:    ptr("FEDCBA0987654321"),
		MergedAt:          "2026-10-20T12:34:56+02:00",
		RunID:             777,
		ModelLabel:        ptr("gpt-x"),
		IssuesPosted:      3,
		SuggestionsPosted: 2,
		Note: json.RawMessage(`{"change_kind":"bugfix","bugfix":{"cause_kind":"race"},` +
			`"components":[{"name":"uploader","files":["internal/files/upload.go","internal/files/retry.go"]},` +
			`{"name":"store","files":["internal/files/store/db.go"]}],"rules":[]}`),
		Claims: []claim{{ProjectKey: "PLAT", RepoSlug: nil}, {ProjectKey: "PLAT", RepoSlug: ptr("files")}},
		NoteCall: &noteCall{
			Outcome: "ok", NoteStatus: "posted", Profile: ptr("prof"), Model: ptr("gpt-x"), Deployment: ptr("azure/gpt-x"),
			ReasoningSent: ptr("medium"), FinishReason: ptr("stop"), CallID: ptr("call-1"), LiteLLMVersion: ptr("1.2.3"),
			InputTokens: ptr[int64](1000), InputUncached: ptr[int64](600), CacheRead: ptr[int64](400), CacheWrite: ptr[int64](0),
			OutputTokens: ptr[int64](200), Reasoning: ptr[int64](50), CostUSD: ptr("0.012"), Cost: ptr[int64](12_000_000),
			CostInput: ptr[int64](6_000_000), CostCacheRead: ptr[int64](1_000_000), CostCacheWrite: ptr[int64](0),
			CostOutput: ptr[int64](4_000_000), CostReasoning: ptr[int64](1_000_000), CostTool: ptr[int64](0),
			KeySpendUSD: ptr("12.5"), KeySpend: ptr[int64](12_500_000_000), HeadersMS: ptr[int64](320), TotalMS: ptr[int64](2100),
			Warnings: []string{"unaccounted"},
		},
		Rounds:       rounds{Count: 3, InputTokens: 30_000, OutputTokens: 4_000, CostUSD: ptr("0.4"), CostNanoUSD: ptr[int64](400_000_000)},
		ReviewTotals: json.RawMessage(`{"findings":5}`),
	}
}

func marshal(t *testing.T, p Payload) []byte {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseFullPayload(t *testing.T) {
	got, err := Parse(marshal(t, fullPayload()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.DeliveryID != "pr_merged_note#plat/files#42#abcdef1234567890" {
		t.Errorf("DeliveryID = %q", got.DeliveryID)
	}
	if got.EventType != EventType {
		t.Errorf("EventType = %q", got.EventType)
	}
	n := got.Note
	if n.ProjectKey != "PLAT" || n.RepoSlug != "files" || n.PRID != 42 || n.PRKey != "PLAT/files#42" || n.PRTitle != "Fix upload retry" {
		t.Errorf("identity fields = %+v", n)
	}
	if n.TicketKey != "PLAT-7" || n.IssueType != "Bug" || n.ChangeKind != "bugfix" || n.ModelLabel != "gpt-x" {
		t.Errorf("optional strings = %q %q %q %q", n.TicketKey, n.IssueType, n.ChangeKind, n.ModelLabel)
	}
	if n.ReviewedCommit != "ABCDEF1234567" {
		t.Errorf("ReviewedCommit = %q, want kept as sent", n.ReviewedCommit)
	}
	if n.SourceCommit != "abcdef1234567890" || n.MergeCommit != "fedcba0987654321" {
		t.Errorf("commits = %q %q, want lowercased", n.SourceCommit, n.MergeCommit)
	}
	wantAt := time.Date(2026, 10, 20, 10, 34, 56, 0, time.UTC)
	if !n.MergedAt.Equal(wantAt) || n.MergedAt.Location() != time.UTC {
		t.Errorf("MergedAt = %v, want %v in UTC", n.MergedAt, wantAt)
	}
	if n.RunID != 777 || n.IssuesPosted != 3 || n.Suggestions != 2 {
		t.Errorf("counters = %d %d %d", n.RunID, n.IssuesPosted, n.Suggestions)
	}
	if string(n.Bugfix) != `{"cause_kind":"race"}` {
		t.Errorf("Bugfix = %s", n.Bugfix)
	}
	if n.BugfixArea != "internal/files" {
		t.Errorf("BugfixArea = %q", n.BugfixArea)
	}
	if n.BugfixComponent != "uploader" {
		t.Errorf("BugfixComponent = %q", n.BugfixComponent)
	}
	if !strings.Contains(string(n.Body), `"components"`) {
		t.Errorf("Body not kept verbatim: %s", n.Body)
	}

	if len(got.Claims) != 2 {
		t.Fatalf("Claims = %+v", got.Claims)
	}
	if got.Claims[0].ProjectKey != "PLAT" || got.Claims[0].RepoSlug != "" {
		t.Errorf("claim[0] = %+v, want null repo_slug as empty", got.Claims[0])
	}
	if got.Claims[1].RepoSlug != "files" {
		t.Errorf("claim[1] = %+v", got.Claims[1])
	}

	c := got.Call
	if c == nil {
		t.Fatal("Call = nil")
	}
	if c.Outcome != "ok" || c.NoteStatus != "posted" || c.Profile != "prof" || c.Model != "gpt-x" || c.Deployment != "azure/gpt-x" ||
		c.Reasoning != "medium" || c.Finish != "stop" || c.CallID != "call-1" || c.GatewayVer != "1.2.3" {
		t.Errorf("call strings = %+v", c)
	}
	if c.RunID == nil || *c.RunID != 777 {
		t.Errorf("Call.RunID = %v, want 777", c.RunID)
	}
	for name, lane := range map[string]struct {
		got  *int64
		want int64
	}{
		"InputTokens": {c.InputTokens, 1000}, "InputUncached": {c.InputUncached, 600}, "CacheRead": {c.CacheRead, 400},
		"CacheWrite": {c.CacheWrite, 0}, "OutputTokens": {c.OutputTokens, 200}, "ReasoningTokens": {c.ReasoningTokens, 50},
		"Cost": {c.Cost, 12_000_000}, "CostInput": {c.CostInput, 6_000_000}, "CostCacheRead": {c.CostCacheRead, 1_000_000},
		"CostCacheWrite": {c.CostCacheWrite, 0}, "CostOutput": {c.CostOutput, 4_000_000}, "CostReasoning": {c.CostReasoning, 1_000_000},
		"CostTool": {c.CostTool, 0}, "KeySpend": {c.KeySpend, 12_500_000_000}, "HeadersMS": {c.HeadersMS, 320}, "TotalMS": {c.TotalMS, 2100},
		"RoundsCost": {c.RoundsCost, 400_000_000},
	} {
		if lane.got == nil || *lane.got != lane.want {
			t.Errorf("Call.%s = %v, want %d", name, lane.got, lane.want)
		}
	}
	if len(c.Warnings) != 1 || c.Warnings[0] != "unaccounted" {
		t.Errorf("Warnings = %v", c.Warnings)
	}
	if c.Rounds != 3 || c.RoundsInputTokens != 30_000 || c.RoundsOutputTokens != 4_000 {
		t.Errorf("rounds = %d %d %d", c.Rounds, c.RoundsInputTokens, c.RoundsOutputTokens)
	}
}

// The payload noergler's emitter produces for a full note, committed on
// its side as testdata/pr_merged_note.json and copied here: the contract
// pinned from both ends. A key renamed on either side fails this test or
// theirs. Strict decoding, so an extra key noergler adds fails here first.
func TestParseNoerglerGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/noergler_pr_merged_note.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(noergler payload): %v", err)
	}
	n := got.Note
	if got.DeliveryID != "pr_merged_note#proj/repo#1#abc1234" || n.PRKey != "PROJ/repo#1" || n.ChangeKind != "bugfix" ||
		n.TicketKey != "PROJ-9" || n.IssueType != "Bug" || n.MergeCommit != "" || n.RunID != 77 || n.ModelLabel != "gpt-5.5-high" ||
		n.IssuesPosted != 2 || n.Suggestions != 1 || !n.MergedAt.Equal(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("note = %+v", n)
	}
	if len(got.Claims) != 2 || got.Claims[0].RepoSlug != "" || got.Claims[1].RepoSlug != "x" {
		t.Errorf("claims = %+v", got.Claims)
	}
	c := got.Call
	if c == nil || c.Outcome != "ok" || c.NoteStatus != "written" || c.Model != "m" || c.InputTokens != nil ||
		c.CacheRead == nil || *c.CacheRead != 90_000 || c.Cost == nil || *c.Cost != 12_300_000 || len(c.Warnings) != 0 ||
		c.Rounds != 3 || c.RoundsInputTokens != 300_000 || c.RoundsCost == nil || *c.RoundsCost != 40_000_000 {
		t.Errorf("call = %+v", c)
	}
}

func TestParseNoNoteCall(t *testing.T) {
	p := fullPayload()
	p.NoteCall = nil
	got, err := Parse(marshal(t, p))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	c := got.Call
	if c == nil {
		t.Fatal("Call = nil, want an empty call carrying the rounds")
	}
	if c.Outcome != "" || c.NoteStatus != "" || c.RunID != nil || c.InputTokens != nil {
		t.Errorf("Call = %+v, want empty outcome and no lanes", c)
	}
	if c.Rounds != 3 || c.RoundsInputTokens != 30_000 || c.RoundsOutputTokens != 4_000 || c.RoundsCost == nil || *c.RoundsCost != 400_000_000 {
		t.Errorf("rounds not copied: %+v", c)
	}
}

func TestParseBugfixLifting(t *testing.T) {
	cases := []struct {
		name       string
		kind       *string
		note       string
		wantKind   string
		wantBugfix bool
		wantArea   string
		wantComp   string
	}{
		{"bugfix with block", ptr("bugfix"), `{"bugfix":{"cause_kind":"logic"},"components":[{"name":"a","files":["x/y/a.go"]}]}`, "bugfix", true, "x/y", "a"},
		{"bugfix with null block", ptr("bugfix"), `{"bugfix":null,"components":[{"name":"a","files":["x/y/a.go"]}]}`, "bugfix", false, "", ""},
		{"bugfix without block", ptr("bugfix"), `{"components":[{"name":"a","files":["x/y/a.go"]}]}`, "bugfix", false, "", ""},
		{"feature with block", ptr("feature"), `{"bugfix":{"cause_kind":"logic"},"components":[{"name":"a","files":["x/y/a.go"]}]}`, "feature", false, "", ""},
		{"kind from note", nil, `{"change_kind":"bugfix","bugfix":{"cause_kind":"logic"},"components":[]}`, "bugfix", true, "", ""},
		{"empty kind falls back", ptr("  "), `{"change_kind":"refactor"}`, "refactor", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fullPayload()
			p.ChangeKind = tc.kind
			p.Note = json.RawMessage(tc.note)
			got, err := Parse(marshal(t, p))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			n := got.Note
			if n.ChangeKind != tc.wantKind {
				t.Errorf("ChangeKind = %q, want %q", n.ChangeKind, tc.wantKind)
			}
			if (len(n.Bugfix) > 0) != tc.wantBugfix {
				t.Errorf("Bugfix = %q, want lifted=%v", n.Bugfix, tc.wantBugfix)
			}
			if n.BugfixArea != tc.wantArea || n.BugfixComponent != tc.wantComp {
				t.Errorf("area/component = %q/%q, want %q/%q", n.BugfixArea, n.BugfixComponent, tc.wantArea, tc.wantComp)
			}
		})
	}
}

func TestCommonDir(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"one file", []string{"internal/files/a.go"}, "internal/files"},
		{"shared directory", []string{"internal/files/a.go", "internal/files/sub/b.go"}, "internal/files"},
		{"root only", []string{"a.go", "b.go"}, ""},
		{"different top dirs", []string{"x/a.go", "y/b.go"}, ""},
		{"empty", nil, ""},
		{"blank entries", []string{"  ", ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commonDir(tc.files); got != tc.want {
				t.Errorf("commonDir(%q) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}

func TestParseRejections(t *testing.T) {
	cases := []struct {
		name string
		body func(t *testing.T) []byte
		want string
	}{
		{"unknown field", func(t *testing.T) []byte {
			var m map[string]any
			if err := json.Unmarshal(marshal(t, fullPayload()), &m); err != nil {
				t.Fatal(err)
			}
			m["bogus"] = 1
			b, _ := json.Marshal(m)
			return b
		}, `unknown field "bogus"`},
		{"wrong event_type", func(t *testing.T) []byte {
			p := fullPayload()
			p.EventType = "pr_opened"
			return marshal(t, p)
		}, "event_type"},
		{"missing project_key", func(t *testing.T) []byte {
			p := fullPayload()
			p.ProjectKey = ""
			return marshal(t, p)
		}, "project_key: required"},
		{"missing pr_title", func(t *testing.T) []byte {
			p := fullPayload()
			p.PRTitle = " "
			return marshal(t, p)
		}, "pr_title: required"},
		{"missing source_commit_sha", func(t *testing.T) []byte {
			p := fullPayload()
			p.SourceCommitSHA = ""
			return marshal(t, p)
		}, "source_commit_sha: required"},
		{"missing merged_at", func(t *testing.T) []byte {
			p := fullPayload()
			p.MergedAt = ""
			return marshal(t, p)
		}, "merged_at: required"},
		{"pr_id zero", func(t *testing.T) []byte {
			p := fullPayload()
			p.PRID = 0
			return marshal(t, p)
		}, "pr_id"},
		{"short sha", func(t *testing.T) []byte {
			p := fullPayload()
			p.SourceCommitSHA = "abc123"
			return marshal(t, p)
		}, "source_commit_sha"},
		{"bad merged_at", func(t *testing.T) []byte {
			p := fullPayload()
			p.MergedAt = "yesterday"
			return marshal(t, p)
		}, "merged_at"},
		{"note null", func(t *testing.T) []byte {
			p := fullPayload()
			p.Note = json.RawMessage("null")
			return marshal(t, p)
		}, "note: required"},
		{"note not an object", func(t *testing.T) []byte {
			p := fullPayload()
			p.Note = json.RawMessage(`"just text"`)
			return marshal(t, p)
		}, "note:"},
		{"claim without project_key", func(t *testing.T) []byte {
			p := fullPayload()
			p.Claims = []claim{{ProjectKey: " "}}
			return marshal(t, p)
		}, "claims: project_key required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.body(t))
			if err == nil {
				t.Fatal("Parse accepted the body")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
