package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/diagram"
)

// BaselineRun is one run as stored. Plan is the catalogue, the worker's to
// read; the store keeps it verbatim.
//
// Table: baseline_runs
type BaselineRun struct {
	ID         int64
	ProjectKey string
	Slug       string
	Mode       string
	Trigger    string
	BaseCommit string
	Commit     string
	State      string
	Step       int
	Steps      int
	Plan       json.RawMessage
	Attempts   int
	Calls      int
	Bytes      int64
	// ReadableBytes is what a full run could have read, the tree's readable
	// files at its commit; ReadBytes the distinct files its folded parts
	// read. Bytes is the budget's count, every call's input. A reconcile
	// carries the documented run's pair.
	ReadableBytes int64
	ReadBytes     int64
	Skipped       int
	Error         string
	QueuedAt      time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	// Epoch is the prompts.Epoch a full or restyle run ran under, the
	// worker's to set when it starts one; 0 on any other run. A full run
	// done with one restyles its repository's page (RestyleState).
	Epoch int
	// RepoID and ProjectID are the repository's ids; ProjectKey and Slug
	// its current names.
	RepoID    int64
	ProjectID int64
	// Reason is why it was queued (SetRunReason), nil for a run from before
	// or one whose queuer gave none.
	Reason *RunReason
}

// Open reports whether the run is neither done nor failed.
func (r BaselineRun) Open() bool { return r.State != RunDone && r.State != RunFailed }

// Brief is a repository's brief as stored. Markdown is "" before the
// first was written, and in the listing (Briefs), which carries
// HasMarkdown alone.
//
// Table: repo_briefs
type Brief struct {
	Markdown     string
	HasMarkdown  bool
	PageRevision int
	WrittenAt    *time.Time
	// RequestedAt is a request waiting for the worker, Attempts the calls
	// tried for it so far (each counted before it is made).
	RequestedAt *time.Time
	Attempts    int
	// FailedAt and Failed are the last request's failure, the brief
	// before it kept.
	FailedAt     *time.Time
	Failed       string
	PushState    string
	PushError    string
	PushedCommit string
	PushAt       *time.Time
}

// BriefJob is the worker's next brief: a request to write it (Write), or
// a written one to push (Markdown).
//
// Table: repo_briefs
type BriefJob struct {
	RepoID     int64
	ProjectKey string
	Slug       string
	Write      bool
	Markdown   string
	// PushedCommit is the commit Almanach last pushed to the branch, ""
	// for none: the one a push may replace there.
	PushedCommit string
	// RequestSeq is the request taken: WriteBrief and FailBrief end it
	// only while no other came since.
	RequestSeq int64
}

// heldRow is a source row holding a clone URL.
//
// Table: source_repos
type heldRow struct {
	repoID     int64
	projectID  int64
	projectKey string
	slug       string
}

// Note is one merged PR's note as the sender described it, lifted into
// columns beside the raw object.
//
// Table: notes
type Note struct {
	ProjectKey string
	RepoSlug   string
	PRID       int
	Team       string
	PRKey      string
	PRTitle    string
	TicketKey  string
	IssueType  string
	ChangeKind string
	// Bugfix is the note's bugfix object, nil unless a bugfix; Area and
	// Component are what the problem areas group by, computed at ingest.
	Bugfix          json.RawMessage
	BugfixArea      string
	BugfixComponent string
	Body            json.RawMessage
	ReviewedCommit  string
	SourceCommit    string
	MergeCommit     string
	MergedAt        time.Time
	RunID           int64
	ModelLabel      string
	IssuesPosted    int
	Suggestions     int
}

// RepoRef names a source repository in the dependency graph.
//
// Table: repos
type RepoRef struct {
	ProjectKey string
	Slug       string
}

// PendingNote is a note the worker has not folded yet.
//
// Table: notes
type PendingNote struct {
	ProjectKey string
	RepoSlug   string
	PRKey      string
	PRTitle    string
	ChangeKind string
	ReceivedAt time.Time
	// Attempts is the gateway failures counted against it so far.
	Attempts int
	// HeldBy is the started run of its repository it waits for, 0 when
	// none: NextQueued skips it until that run is over.
	HeldBy int64
}

// RunCall is one model call of a run, for its steps on the queue page.
//
// Table: llm_calls
type RunCall struct {
	Purpose string
	// Step is the reading step, nil for the catalogue, project and map.
	Step        *int
	Outcome     string
	CostNanoUSD *int64
	At          time.Time
}

// FoldedNote is a note the worker took off the queue: folded, refused or
// ignored. Outcome and cost are the last merge call of this delivery
// (made between its arrival and its fold), one it shared with its
// repository's other notes too; "" and nil when it made none (a note for
// a repository that is no source). Batch is how many notes that call
// folded, 1 for one: its cost is the whole call's, not this note's share.
//
// Table: notes
type FoldedNote struct {
	ProjectKey     string
	RepoSlug       string
	PRKey          string
	PRTitle        string
	ConsolidatedAt time.Time
	Outcome        string
	CostNanoUSD    *int64
	Batch          int
}

// FailedNote is a note the merge call failed on, off the queue until the
// repository's next run (RequeueFailed) or, at the worker's bound of
// attempts, for good.
//
// Table: notes
type FailedNote struct {
	ProjectKey string
	RepoSlug   string
	PRKey      string
	PRTitle    string
	FailedAt   time.Time
	Reason     string
	Attempts   int
}

// CostSum is calls and their cost; NanoUSD nil when none was priced.
//
// Table: -
type CostSum struct {
	Calls    int
	Unpriced int
	NanoUSD  *int64
}

// Spend is what Almanach's own model calls since a time cost.
//
// Table: -
type Spend struct {
	// NanoUSD sums the priced calls; nil when none was.
	NanoUSD *int64
	Calls   int
	// Unpriced is the calls the gateway billed without a price coming back:
	// answered, or cut mid-answer (a loop cut, the output cap, a stall), the
	// stream ended before its usage. Their cost is unknown, not zero. A call
	// that got no answer (refused, no headers) billed nothing and is not one.
	Unpriced int
	// Dearest is the costliest priced call of the last dearestWindow: a day
	// of nothing but unpriced calls still has one.
	Dearest int64
}

// Budgeted is the spend a daily budget counts: each unpriced call as the
// costliest priced one, never as nothing (2026-11-07: a night of unpriced
// runaways never reached the budget). Nil when nothing is known.
func (s Spend) Budgeted() *int64 {
	if s.NanoUSD == nil && (s.Unpriced == 0 || s.Dearest == 0) {
		return nil
	}
	var sum int64
	if s.NanoUSD != nil {
		sum = *s.NanoUSD
	}
	sum += int64(s.Unpriced) * s.Dearest
	return &sum
}

// ProjectRow is one project in the list.
//
// Table: projects
type ProjectRow struct {
	Key      string
	Team     string
	Repos    int
	Notes    int
	Revision int
	// Summary is the current project page's, whole; "" before the first.
	Summary   string
	LastMerge *time.Time
}

// RepoRow is one repository of a project.
//
// Table: repos
type RepoRow struct {
	Slug      string
	Revision  int
	Summary   string
	Notes     int
	LastMerge *time.Time
}

// RepoSummary is a source repository and its current summary, whole, with
// Contracts, its statements other repositories rely on: its interfaces
// and entities, in the page's order.
//
// Table: repos
type RepoSummary struct {
	Slug      string
	Summary   string
	Contracts []Statement
}

// RevisionRow is one entry of a page's history.
//
// Table: revisions
type RevisionRow struct {
	Revision int
	// SourceKind says which of SourcePR and SourceCommit made the revision.
	SourceKind   string
	SourcePR     string
	SourceCommit string
	// BatchPRKeys is every pull request a fold of several notes took.
	BatchPRKeys []string
	Statements  int
	Chars       int
	CreatedAt   time.Time
}

// NoteRow is one note as listed.
//
// Table: notes
type NoteRow struct {
	PRID        int
	PRKey       string
	PRTitle     string
	RepoSlug    string
	TicketKey   string
	ChangeKind  string
	MergedAt    time.Time
	Items       int
	Issues      int
	Suggestions int
}

// NoteDetail is one note in full.
//
// Table: notes
type NoteDetail struct {
	Note
	// Items is the count the list shows: components, rules, invariants and
	// entities.
	Items          int
	ReceivedAt     time.Time
	ConsolidatedAt *time.Time
	Call           *NoteCallRow
}

// NoteCallRow is the recorded note call, the lanes a reader wants.
//
// Table: note_calls
type NoteCallRow struct {
	Outcome         string
	NoteStatus      string
	Model           string
	InputTokens     *int64
	CacheReadTokens *int64
	OutputTokens    *int64
	CostNanoUSD     *int64
	// Rounds is every note call the PR made, summed by the sender.
	Rounds             int
	RoundsInputTokens  int64
	RoundsOutputTokens int64
	RoundsCost         *int64
}

// TaughtRow is one current statement a pull request's note is a source
// of: where it stands, and enough to name and link it.
//
// Table: revisions
type TaughtRow struct {
	Scope      string
	ProjectKey string
	RepoSlug   string
	ID         string
	Kind       string
	Name       string
}

// PageText is a current page's words, the search's read: its names and
// summary, each statement's words alone (no sources, no diagram, which
// a search neither reads nor shows).
//
// Table: pages
type PageText struct {
	Scope      string
	ProjectKey string
	RepoSlug   string
	Summary    string
	Statements []StatementText
}

// ProblemArea is one cluster of bugfixes: a repo and the directory the
// fixes' components share, with the cause class seen most and the posted
// findings on those PRs.
//
// Table: -
type ProblemArea struct {
	RepoSlug    string
	Area        string
	Component   string
	Bugfixes    int
	TopCause    string
	TopCauseN   int
	Issues      int
	Suggestions int
	LastFixPR   string
	LastFixAt   time.Time
}

// RestyleState is what is due of a repository's restyle at an epoch: its
// page, its project's. One answer for the poller's queueing and the run's
// steps, so the two never disagree and a run never ends with a page left
// due that it judged not due.
//
// Table: -
type RestyleState struct {
	RepoDue, ProjectDue bool
	// Open: a restyle is open somewhere in the wiki.
	Open bool
}

// Queue reports whether the poller queues a restyle of the repository: its
// page or its project's is due, and no restyle is open anywhere, so a bump
// trickles through the wiki one page at a time and never crowds out the
// notes and the reading.
func (st RestyleState) Queue() bool {
	return (st.RepoDue || st.ProjectDue) && !st.Open
}

// ProjectSource is one configured project source.
//
// Table: sources
type ProjectSource struct {
	// ProjectID is the project's id, ProjectKey its current key.
	ProjectID  int64
	ProjectKey string
	Kind       string
	// Host is the Bitbucket base URL; "" for a static source.
	Host      string
	TokenAuth string
	TokenUser string
	// TokenEnc is the encrypted token, nil when the source has none.
	TokenEnc     []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastListedAt *time.Time
	LastError    string
}

// HasToken reports whether a credential is stored.
func (s ProjectSource) HasToken() bool { return len(s.TokenEnc) > 0 }

// SourceRow is a source with its repositories counted by state.
//
// Table: sources
type SourceRow struct {
	ProjectSource
	Repos   int
	Ready   int
	Active  int // pending or syncing
	Failed  int
	Missing int
	// Inactive is the deactivated ones, missing or not; Ready, Active,
	// Failed and Missing count the others only.
	Inactive int
	// LastSyncedAt is the newest sync of any of its repositories, nil
	// before the first: a static source is never listed, so this is its
	// last run.
	LastSyncedAt *time.Time
}

// SourceRepo is one clone. ProjectID and RepoID are its ids, ProjectKey and
// Slug its current names.
//
// Table: source_repos
type SourceRepo struct {
	ProjectID  int64
	RepoID     int64
	ProjectKey string
	Slug       string
	CloneURL   string
	Origin     string
	// Branch is "" until resolved from the remote.
	Branch       string
	HeadCommit   string
	State        string
	LastSyncedAt *time.Time
	LastError    string
	CreatedAt    time.Time
	// DeactivatedAt is when the repository was deactivated, nil while it is
	// active; ReactivatedAt the last time it came back.
	DeactivatedAt *time.Time
	ReactivatedAt *time.Time
}

// Active reports whether the repository is a source: not deactivated.
func (r SourceRepo) Active() bool { return r.DeactivatedAt == nil }

// Page is a description as currently published.
//
// Table: pages
type Page struct {
	Scope      string
	ProjectKey string
	RepoSlug   string
	RevisionID int64
	Revision   int
	Summary    string
	Statements []Statement
	// SourceKind says which of SourcePR and SourceCommit made the revision.
	SourceKind   string
	SourcePR     string
	SourceCommit string
	// BatchPRKeys is every pull request the revision's fold took, in merge
	// order, SourcePR the first; nil for a fold of one note, or a run's.
	BatchPRKeys []string
	UpdatedAt   time.Time
	// Map is a project page's system map, nil on a repository page and on
	// a project page no baseline project step has drawn yet.
	Map *diagram.Diagram
	// RepoMap is a repository page's map of itself, nil on a project page
	// and on a repository page no run's map step has drawn yet.
	RepoMap *diagram.Diagram
	// RepoMapBasis is the description the map step last answered for, as
	// it hashed it: RepoMap drawn from it, or none when the model drew none;
	// "" before any answer. A basis that no longer matches the page's text
	// is a map due again.
	RepoMapBasis string
	// Shape is what a project page's last judgment found the project to be
	// (ShapeSystem, ShapeCollection); "" on a repository page and on a
	// project page no step has judged yet, read as a system.
	Shape string
}

// Collection reports whether the page is a collection's.
func (p Page) Collection() bool { return p.Shape == ShapeCollection }

// MapStands reports whether the map step answered for a repository page's
// description as it stands, a map drawn or none (a model that drew none
// for this text is not asked again until it changes): then a run has
// nothing new to draw from. A map stored before the tags never stands.
func (p Page) MapStands() bool {
	// TRANSITIONAL(untagged-map): Remove when every stored map carries tags.
	return p.RepoMapBasis != "" && p.RepoMapBasis == p.MapBasis() && !diagram.Untagged(p.RepoMap)
}

// MapBasis is the description a repository map is drawn from, as a hash:
// the summary and each statement's kind, name, text and files, never its
// sources or last check, which change without the text. Nor their order
// or area: laying a page out anew (baseline.Arrange) draws no map again.
// The shape hashed is what every stored basis was: change it and every map
// is due again (sorted by id once, 2026-11-02, when every page was due for
// its first layout anyway).
func (p Page) MapBasis() string {
	type part struct {
		Kind, Name, Text string
		Files            []string
	}
	byID := slices.Clone(p.Statements)
	slices.SortStableFunc(byID, func(a, b Statement) int { return strings.Compare(a.ID, b.ID) })
	parts := make([]part, 0, len(byID))
	for _, s := range byID {
		parts = append(parts, part{s.Kind, s.Name, s.Text, s.Files})
	}
	raw, _ := json.Marshal(struct {
		Summary    string
		Statements []part
	}{p.Summary, parts})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// QueuedNote is the next note the worker folds in. DeliveryID is the
// delivery that wrote this version of the note: a second note for the same
// PR replaces the row and queues it again, and the mark of a fold that
// read the first must not take the second off the queue.
//
// Table: notes
type QueuedNote struct {
	Note
	ReceivedAt time.Time
	DeliveryID string
}
