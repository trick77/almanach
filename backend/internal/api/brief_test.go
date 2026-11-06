package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/store"
)

// A brief is asked for on the source page and wakes the worker; its state
// rides on the repository's row, its text on a route of its own.
func TestBrief_RequestStateText(t *testing.T) {
	fs := describeSources()
	w := &fakeWaker{}
	h, _, _ := sourcesHandlerWith(t, fs, newFakeStore(), w, false)

	for path, want := range map[string]int{
		"/api/sources/tools/repos/nope/brief": 404,
		"/api/sources/nope/repos/svc/brief":   404,
	} {
		if code, _ := do(t, h, http.MethodPost, path, "", "", nil); code != want {
			t.Errorf("POST %s: %d, want %d", path, code, want)
		}
	}
	code, body := do(t, h, http.MethodPost, "/api/sources/tools/repos/svc/brief", "", "", nil)
	if code != 202 || field(t, body, "status") != "queued" || w.wakes != 1 || len(fs.requested) != 1 {
		t.Fatalf("POST brief: %d %v, wakes %d, requested %v", code, body, w.wakes, fs.requested)
	}
	if code, _ := do(t, h, http.MethodGet, "/api/sources/tools/repos/svc/brief", "", "", nil); code != 404 {
		t.Fatalf("GET before a brief was written: %d, want 404", code)
	}
	if b := repoBriefOn(t, h, "svc"); b == nil || b.State != briefRequested {
		t.Fatalf("row's brief = %+v, want requested", b)
	}

	// Written, its push refused: the row says why, the text is there to copy.
	id := fs.id("tools/svc")
	at := time.Now()
	fs.briefs[id] = store.Brief{Markdown: "## Projekt\n\nText.\n", HasMarkdown: true, PageRevision: 3, WrittenAt: &at,
		PushState: store.PushFailed, PushError: "git push: 403", PushAt: &at}
	b := repoBriefOn(t, h, "svc")
	if b == nil || b.State != briefPushFailed || b.Error != "git push: 403" || !b.HasText || b.Branch != "feature/repository-description-update" {
		t.Fatalf("row's brief = %+v", b)
	}
	code, body = do(t, h, http.MethodGet, "/api/sources/tools/repos/svc/brief", "", "", nil)
	if code != 200 || field(t, body, "markdown") != "## Projekt\n\nText.\n" || field(t, body, "path") != ".agents/PROJECT.md" ||
		field(t, body, "state") != briefPushFailed {
		t.Fatalf("GET brief: %d %v", code, body)
	}
	if b := repoBriefOn(t, h, "fresh"); b != nil {
		t.Errorf("a repository never asked has a brief: %+v", b)
	}

	// A deactivated repository takes no request.
	now := time.Now()
	fs.repos["tools"][1].DeactivatedAt = &now
	if code, _ := do(t, h, http.MethodPost, "/api/sources/tools/repos/fresh/brief", "", "", nil); code != 422 {
		t.Errorf("deactivated: %d, want 422", code)
	}
}

// A waiting request is held by the day's budget once the day's spend
// (the fake's 1.284 USD) reaches it; none without a budget or a request.
func TestBriefsOverBudget(t *testing.T) {
	at := time.Now()
	waiting := map[int64]store.Brief{1: {RequestedAt: &at}}
	for _, c := range []struct {
		budget int64
		briefs map[int64]store.Brief
		want   bool
	}{
		{1_000_000_000, waiting, true},
		{2_000_000_000, waiting, false},
		{0, waiting, false},
		{1_000_000_000, map[int64]store.Brief{1: {PushState: store.PushDue}}, false},
	} {
		d := Deps{Store: newFakeStore(), DailyBudgetNanoUSD: c.budget}
		if got, err := d.briefsOverBudget(context.Background(), c.briefs); err != nil || got != c.want {
			t.Errorf("budget %d briefs %v: %v, %v; want %v", c.budget, c.briefs, got, err, c.want)
		}
	}
}

// The states a row shows, the newest thing first.
func TestBriefOf_State(t *testing.T) {
	at := time.Now()
	for want, b := range map[string]store.Brief{
		briefRequested:  {RequestedAt: &at, FailedAt: &at, PushState: store.PushFailed},
		briefFailed:     {FailedAt: &at, Failed: "x", PushState: store.PushPushed},
		briefPushing:    {PushState: store.PushDue},
		briefPushed:     {PushState: store.PushPushed},
		briefUnchanged:  {PushState: store.PushUnchanged},
		briefPushFailed: {PushState: store.PushFailed, PushError: "e"},
	} {
		if got := briefOf(b).State; got != want {
			t.Errorf("%+v: state %q, want %q", b, got, want)
		}
	}
}

// repoBriefOn is the brief on the source page's row of slug, nil for none.
func repoBriefOn(t *testing.T, h http.Handler, slug string) *briefBody {
	t.Helper()
	code, body := do(t, h, http.MethodGet, "/api/sources/tools", "", "", nil)
	if code != 200 {
		t.Fatalf("GET source: %d %v", code, body)
	}
	var repos []sourceRepoBody
	if err := json.Unmarshal(body["repos"], &repos); err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if strings.EqualFold(r.Slug, slug) {
			return r.Brief
		}
	}
	t.Fatalf("no row %s", slug)
	return nil
}
