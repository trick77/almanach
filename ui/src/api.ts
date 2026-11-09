// The wire types. They mirror the structs in backend/internal/api/api.go
// and the contract in backend/api/openapi.yaml; a field that is nullable
// there is `| null` here, never optional, so a missing value has to be
// handled rather than silently read as undefined. Fields the Go side marks
// omitempty are optional here.

import type { DiagramIR, FlowIR } from "./diagramIR";
import { onUnauthorized } from "./signin";

export interface Project {
  key: string;
  team: string;
  repos: number;
  notes: number;
  revision: number;
  /** The project page's summary, its first sentence; "" before the first.
   *  A project of one: its repository's. */
  summary: string;
  last_merge: string | null;
  /** The repository of a project of one (a source with one repository):
   *  its page is the project's. null for every other project. */
  repo: Repo | null;
}

/** Calls of one kind (merges or runs) and what they cost. */
export interface CostSum {
  calls: number;
  /** Calls no one priced: their cost is unknown, not zero. */
  unpriced: number;
  cost_usd?: string;
}

export interface CostProject {
  project: string;
  cost_usd?: string;
  calls: number;
  merges: CostSum;
  runs: CostSum;
  /** The repositories' PROJECT.md calls. */
  briefs: CostSum;
}

export interface CostMonth {
  /** YYYY-MM, UTC. */
  month: string;
  cost_usd?: string;
  calls: number;
  projects: CostProject[];
}

/** Almanach's own ledger by month and project, never noergler's. */
export interface Costs {
  months: CostMonth[];
  /** BACKEND_DAILY_BUDGET_USD; absent when none is set. */
  budget_usd?: string;
  today: {
    cost_usd?: string;
    calls: number;
    unpriced: number;
    /** What the daily budget counts: the cost and each unpriced call as
     *  the dearest priced one. */
    budgeted_usd?: string;
  };
}

export interface Projects {
  projects: Project[];
}

/** What a statement rests on: a PR, or a baseline run that read the code
 *  at a commit. kind is absent on a body from before baselines: a PR. */
export interface Source {
  kind?: "pr" | "baseline";
  pr_key?: string;
  ticket_key?: string;
  merged_at?: string;
  commit?: string;
  committed_at?: string;
  /** The repository whose commit a baseline source is. */
  repo?: string;
}

/** decision, extension and pitfall are for the agent changing the code;
 *  extension is a repository's alone, never on a project page. An
 *  interface is one a component offers others, never the component. */
export type Kind =
  | "component"
  | "interface"
  | "flow"
  | "rule"
  | "invariant"
  | "entity"
  | "decision"
  | "extension"
  | "pitfall";

export interface Statement {
  id: string;
  kind: Kind;
  name: string;
  /** Laid-out text (format.ts blocks): paragraphs a blank line apart,
   *  flat "- " and "N. " lists, **bold**, code spans, "#### " sub-headings.
   *  No other markdown. */
  text: string;
  files: string[];
  sources: Source[];
  /** The commit a baseline run last confirmed it against, "" when none. */
  checked?: string;
  /** The part of the system it is about, its sub-chunk inside its kind;
   *  areas stand in the order of their first statement, "" (or absent)
   *  before the page was laid out. */
  area?: string;
  /** A picture beside the paragraphs, rongo's IR as the backend validated
   *  it; absent when the statement has none. */
  diagram?: DiagramIR;
}

export interface Description {
  revision: number;
  summary: string;
  statements: Statement[];
  source_kind?: "pr" | "baseline";
  source_pr_key: string;
  /** Every PR a fold of several notes took, source_pr_key the first. */
  source_pr_keys?: string[];
  source_commit?: string;
  updated_at: string;
}

export interface Repo {
  slug: string;
  revision: number;
  summary: string;
  notes: number;
  last_merge: string | null;
}

export interface NoteSummary {
  pr_id: number;
  pr_key: string;
  pr_title: string;
  repo_slug: string;
  ticket_key?: string;
  change_kind?: string;
  merged_at: string;
  items: number;
  issues_posted: number;
  suggestions_posted: number;
}

export interface Problem {
  repo_slug: string;
  area: string;
  component: string;
  bugfixes: number;
  top_cause: string;
  top_cause_count: number;
  issues_posted: number;
  suggestions_posted: number;
  last_fix_pr_key: string;
  last_fix_at: string;
}

export type ProjectShape = "system" | "collection";

export interface ProjectDetail {
  key: string;
  team: string;
  /** A project of one: its repository's page is the project's;
   *  description and map are null and none is expected. */
  single: boolean;
  description: Description | null;
  /** The architecture overview, the system at runtime in production: zones
   *  clients, entry, repositories, stores and external; a
   *  repository node's id is its slug. null before the first baseline
   *  project step wrote one. */
  map: FlowIR | null;
  /** A baseline run will draw a map: the sources are on, the project has
   *  more than one source repository, and not more than one map holds. */
  map_expected: boolean;
  /** A project step asked for the map and none was accepted: map is null,
   *  and no run is promised to draw one. */
  map_declined: boolean;
  /** What the last project step judged the project to be: one system its
   *  repositories make together, or a collection of repositories that
   *  each stand on their own (a short summary, no statements, no map).
   *  null before a step judged it, and for a project of one. */
  shape: ProjectShape | null;
  repos: Repo[];
  /** Repositories of other projects this project's repositories depend
   *  on: the map's shared zone, drawn here, not stored in the map. */
  shared: SharedRepo[];
  problems: Problem[];
  problem_window_days: number;
  recent_notes: NoteSummary[];
  /** The project page's history, newest first; empty for a project of
   *  one. */
  revisions: Revision[];
  /** The topics the project's repositories touch, read from the code. */
  topics: TopicFlow[];
  /** The interfaces the project's repositories offer, read from the code. */
  interfaces: Interface[];
}

export interface Revision {
  revision: number;
  source_kind?: "pr" | "baseline";
  source_pr_key: string;
  /** Every PR a fold of several notes took, source_pr_key the first. */
  source_pr_keys?: string[];
  source_commit?: string;
  statements: number;
  chars: number;
  created_at: string;
}

/** GET …/revisions/{n}: one revision of a page as it stood, with what it
 *  changed against the one before, by statement id. Mirrors
 *  api/revision.go. */
export interface RevisionDetail {
  scope: "repository" | "project";
  project_key: string;
  repo_slug?: string;
  revision: number;
  /** The page's current revision number. */
  current: number;
  /** The revision diffed against; 0 on the first. */
  previous: number;
  created_at: string;
  description: Description;
  added: StatementRef[];
  changed: StatementRef[];
  dropped: StatementRef[];
  /** Confirmed against the code by a baseline run, nothing else of them
   *  changed. */
  rechecked: StatementRef[];
  summary_changed: boolean;
  /** A project page's map and judged shape moved; false on a
   *  repository page. */
  map_changed: boolean;
  shape_changed: boolean;
}

export interface StatementRef {
  id: string;
  kind: Kind;
  name: string;
}

export interface RepoDetail {
  project_key: string;
  slug: string;
  description: Description | null;
  revisions: Revision[];
  /** The newest hundred; notes_total is the whole count. */
  notes: NoteSummary[];
  notes_total: number;
  last_merge: string | null;
  /** The repository map, the repository from inside: zones entry, parts,
   *  stores and external. null before a baseline run drew one. */
  map: FlowIR | null;
  /** The map step asked for the map and refused every answer: map is
   *  null until the description changes. */
  map_declined: boolean;
  /** The source repositories this one uses, by its manifests. */
  uses: Use[];
  /** The source repositories that use this one. */
  used_by: DependencyEnd[];
  /** The topics and schemas its code names, read by the poller. */
  messaging: RepoMessaging;
  /** The interfaces its code offers or only declares, read by the poller. */
  interfaces: Interface[];
}

/** An interface a repository's code offers (serves) or only declares (a
 *  contract kept for a client): an HTTP API, a gRPC or SOAP service, with
 *  every operation; never a model's. name is "" for the routes code
 *  declares without a spec. */
export interface Interface {
  key: string;
  repo: RepoRef;
  commit: string;
  kind: "http" | "grpc" | "soap";
  name: string;
  version: string;
  base_path: string;
  direction: "offers" | "declares";
  files: string[];
  endpoints: Endpoint[];
}

/** One operation: an HTTP method ("*" for every one) and path, RPC and a
 *  gRPC method's path, SOAP and an operation's name. */
export interface Endpoint {
  kind: "http" | "grpc" | "soap";
  api: string;
  method: string;
  path: string;
  summary: string;
  direction: "offers" | "declares";
  files: string[];
}

/** What a repository's code says of its messages: never a model's. */
export interface RepoMessaging {
  topics: RepoTopic[];
  schemas: MessageSchema[];
}

/** One topic a repository touches one way; declares is named without the
 *  code saying which way. peers are the source repositories on the far
 *  side. */
export interface RepoTopic {
  name: string;
  direction: "produces" | "consumes" | "declares";
  files: string[];
  peers: RepoRef[];
}

export interface MessageSchema {
  name: string;
  kind: "avro" | "proto";
  file: string;
}

/** A topic a project's repositories touch, with every source repository on
 *  each side, another project's included. */
export interface TopicFlow {
  name: string;
  producers: RepoRef[];
  consumers: RepoRef[];
  declared: RepoRef[];
}

/** One end of a dependency. */
export interface DependencyEnd {
  project: string;
  slug: string;
}

/** A repository another one depends on, with its other users. */
export interface Use extends DependencyEnd {
  also: DependencyEnd[];
}

export interface RepoRef {
  project: string;
  slug: string;
}

export interface SharedRepo {
  project: string;
  slug: string;
  used_by: RepoRef[];
}

/** GET /api/search?q=: the pages whose text holds the phrase, grouped by
 *  page in the store's order. Mirrors api/search.go. */
export interface SearchResult {
  query: string;
  pages: SearchPage[];
  /** Pages that matched beyond the fifty answered. */
  more: number;
}

export interface SearchPage {
  scope: "repository" | "project";
  project_key: string;
  repo_slug?: string;
  /** The project is one of one: the page is at the project's path. */
  single: boolean;
  /** The summary's snippet around its first hit; absent for no hit. */
  summary?: string;
  statements: SearchHit[];
}

export interface SearchHit {
  id: string;
  kind: Kind;
  name: string;
  area: string;
  /** The text around the first hit; "" when only name, area or a file
   *  matched. */
  snippet: string;
  /** The first file that matched, given when the text did not. */
  file?: string;
}

/** What a use is for: main at runtime, managed a version pin (a BOM, a
 *  platform, a parent POM), dev a build or check tool, test tests only,
 *  indirect a go.mod's dependency of a dependency. */
export type DepScope = "main" | "managed" | "dev" | "test" | "indirect";

/** GET /api/dependencies: joined from the manifests at read time, every
 *  scope, each edge its strongest. */
export interface Dependencies {
  repos: RepoRef[];
  edges: {
    from: RepoRef;
    to: RepoRef;
    coords: string[];
    scope: DepScope;
  }[];
  /** Coordinates several repositories provide: no edge to any of them. */
  ambiguous: {
    coord: string;
    providers: RepoRef[];
    /** One repository two sources clone: removing one gives the edges. */
    cloned_twice: boolean;
  }[];
}

export interface Bugfix {
  symptom: string;
  cause: string;
  cause_kind: string;
}

export interface NoteItem {
  name: string;
  text: string;
  /** Absent on the lists the sender does not type (rules, invariants,
   *  entities): the note is stored verbatim. */
  files?: string[] | null;
}

/** NoteBody is the note object as noergler's model wrote it. Kept loose:
 *  the backend stores it verbatim, so a key added there must not break
 *  this page. */
export interface NoteBody {
  summary?: string | null;
  change_kind?: string;
  bugfix?: Bugfix | null;
  components?: NoteItem[] | null;
  rules?: NoteItem[] | null;
  invariants?: NoteItem[] | null;
  entities?: NoteItem[] | null;
}

export interface NoteCall {
  outcome: string;
  note_status: string;
  model?: string;
  input_tokens: number | null;
  cache_read_tokens: number | null;
  output_tokens: number | null;
  cost_usd?: string;
  /** Every note call the PR made, summed; the lanes above are this call. */
  rounds: number;
  rounds_input_tokens: number;
  rounds_output_tokens: number;
  rounds_cost_usd?: string;
}

export interface NoteDetail extends NoteSummary {
  project_key: string;
  jira_issue_type?: string;
  reviewed_commit?: string;
  source_commit: string;
  merge_commit?: string;
  run_id?: number;
  model_label?: string;
  bugfix: Bugfix | null;
  bugfix_area?: string;
  note: NoteBody | null;
  received_at: string;
  consolidated_at: string | null;
  note_call: NoteCall | null;
  /** The current statements the note is a source of, the repository
   *  page's then the project's: where the PR's knowledge stands today. */
  taught: Taught[];
  /** The project is one of one: its repository's page is the project's,
   *  at the project's path. False whenever taught is empty. */
  single: boolean;
}

export interface Taught {
  scope: "repository" | "project";
  /** The page's current names; repo_slug absent on a project page. */
  project_key: string;
  repo_slug?: string;
  id: string;
  kind: Kind;
  name: string;
}

// The sources: what Almanach clones itself. Mirrors api/sources.go.

export type SourceKind = "bitbucket" | "static";
export type TokenAuth = "bearer" | "basic";
export type RepoState = "pending" | "syncing" | "ready" | "failed" | "missing";

export interface SourceSummary {
  project_key: string;
  kind: SourceKind;
  host: string;
  has_token: boolean;
  token_auth: TokenAuth;
  token_user: string;
  repos: number;
  ready: number;
  /** Pending or syncing. */
  active: number;
  failed: number;
  missing: number;
  /** Deactivated; ready, active and failed count the others only. */
  inactive: number;
  last_listed_at: string | null;
  /** The newest sync of any of its repositories: a static source is never
   *  listed, so this is its last run. */
  last_synced_at: string | null;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface Sources {
  enabled: boolean;
  /** BACKEND_BITBUCKET_HOST; "" when unset. */
  default_host: string;
  /** BACKEND_BASELINE_MAX_CALLS: the model calls one run may make; every
   *  repository of a new source gets a full run. 0 with the feature off. */
  max_calls: number;
  sources: SourceSummary[];
}

export interface SourceRepo {
  slug: string;
  clone_url: string;
  origin: "listed" | "manual";
  /** Empty until resolved from the remote. */
  branch: string;
  head_commit: string;
  state: RepoState;
  last_synced_at: string | null;
  last_error: string;
  created_at: string;
  /** When it was deactivated, null while it is a source. A deactivated
   *  repository stays on the source but is no source anywhere. */
  deactivated_at: string | null;
  /** The clone's folder, named by ids, never by the names. */
  dir: string;
  /** The newest baseline run, null before the first. */
  baseline: Baseline | null;
  /** The repository's .agents/PROJECT.md, null before the first request. */
  brief: RepoBrief | null;
}

/** A brief's state, the newest thing that happened: a request waiting or
 *  failed, then its push: due, done, not needed (the default branch holds
 *  the file) or refused (the text is there to copy). */
export type BriefState =
  "requested" | "failed" | "pushing" | "pushed" | "unchanged" | "push_failed";

/** RepoBrief is a repository's brief on its source page row. */
export interface RepoBrief {
  state: BriefState;
  /** Why a request is not taken yet: the day's budget is spent. */
  waiting: "" | "budget";
  /** Why the request (failed) or the push (push_failed) failed. */
  error: string;
  has_text: boolean;
  /** The page revision it was written from, 0 before the first. */
  page_revision: number;
  written_at: string | null;
  /** The branch it is pushed to. */
  branch: string;
  pushed_commit: string;
  push_at: string | null;
}

/** BriefText is the brief with its file, for the copy. */
export interface BriefText extends RepoBrief {
  markdown: string;
  /** Where the file goes in the repository. */
  path: string;
}

export type RunState =
  "queued" | "cataloguing" | "describing" | "project" | "done" | "failed";

/** Why a run left a part out; "" for a run from before it was kept. */
export type SkipReason =
  "" | "gone" | "calls" | "bytes" | "tokens" | "failed" | "refused" | "no_page";

/** One part a run left out: a subject by name, or the overview or the
 *  project step. */
export interface SkippedPart {
  part: "overview" | "subject" | "project";
  name: string;
  reason: SkipReason;
}

/** A repository's newest baseline run and the commit last documented. */
export interface Baseline {
  run_id: number;
  state: RunState;
  /** In state project, the project step is over and the run draws the
   *  repository map, its last step. */
  map_step: boolean;
  /** restyle reads no code: the stored pages alone, for a prompt epoch
   *  they are not at. Shown only while open: over, the newest reading run
   *  shows again. */
  mode: "full" | "reconcile" | "restyle";
  trigger:
    "manual" | "first_ready" | "monthly" | "retry" | "departure" | "epoch";
  commit: string;
  step: number;
  steps: number;
  skipped: number;
  /** The parts the run left out, in the order it hit them. */
  skipped_parts: SkippedPart[];
  /** What the run read, and what the tree offered: the share is how much
   *  of the code the page rests on. readable_bytes is 0 on a run that
   *  read no code. */
  read_bytes: number;
  readable_bytes: number;
  /** 3 decimals; absent when none of its calls was priced. */
  cost_usd?: string;
  error: string;
  queued_at: string;
  started_at: string | null;
  finished_at: string | null;
  documented_commit: string;
  documented_at: string | null;
  /** A done run skipped a part worth reading again; the server's rule,
   *  the one the poller retries by. */
  left_parts: boolean;
  /** left_parts, and the poller reads them again on its own; false, only
   *  the next run or a button does. */
  auto_retry: boolean;
}

export interface SourceDetail {
  project_key: string;
  /** BACKEND_BASELINE_MAX_CALLS: what a full run may cost in calls. */
  max_calls: number;
  kind: SourceKind;
  host: string;
  has_token: boolean;
  token_auth: TokenAuth;
  token_user: string;
  dir: string;
  disk_bytes: number;
  last_listed_at: string | null;
  last_error: string;
  created_at: string;
  updated_at: string;
  repos: SourceRepo[];
}

/** Why the worker does not take an item yet: a hold of the whole worker
 *  (until set) or a run step that waits (since set). */
export type WaitReason =
  | "timeout"
  | "rate_limit"
  | "key_refused"
  | "unavailable"
  | "busy"
  | "write"
  | "clone_busy"
  | "placing"
  | "budget";

export type StepKind =
  | "catalogue"
  | "overview"
  | "subject"
  | "project"
  | "repo_map"
  | "restyle"
  | "project_restyle";

export interface RunStep {
  kind: StepKind;
  /** A subject's, "" otherwise. */
  name: string;
  files: number;
  bytes: number;
  state: "done" | "skipped" | "current" | "todo";
  skip: SkipReason;
  calls: number;
  failed: number;
  at: string | null;
  cost_usd?: string;
}

export interface QueueRun {
  id: number;
  state: Exclude<RunState, "done" | "failed">;
  mode: Baseline["mode"];
  trigger: Baseline["trigger"];
  commit: string;
  base_commit: string;
  calls: number;
  queued_at: string;
  started_at: string | null;
  cost_usd?: string;
  steps: RunStep[];
  /** Why it was queued; null for a run from before reasons were kept. */
  reason: RunReason | null;
}

/** RunReason is why a run was queued, as its queuer knew it. Mirrors
 *  store.RunReason. */
export interface RunReason {
  kind:
    | "first"
    | "retry"
    | "unfinished"
    | "interval"
    | "restyle"
    | "manual"
    | "returned"
    | "departure";
  attempt?: number;
  of?: number;
  last_error?: string;
  from?: string;
  to?: string;
  days?: number;
  repo_epoch?: number;
  project_epoch?: number;
}

/** A note or a run in the queue; as the current item also a repository's
 *  brief, its call or its push. */
export interface QueueItem {
  kind: "note" | "run" | "brief";
  project: string;
  repo: string;
  pr_key?: string;
  title?: string;
  change_kind?: string;
  received_at?: string;
  /** The run of the note's repository it waits for. */
  held_by?: number;
  /** When the note's repository is folded, while it waits for the fold
   *  delay. */
  due_at?: string;
  /** How many notes the fold in flight takes; the current item, when more
   *  than one. */
  batch?: number;
  run?: QueueRun;
  attempts: number;
  /** When the worker took it; the current item only. */
  since?: string;
  wait?: { reason: WaitReason; until?: string; since?: string };
}

export interface QueueRecent {
  kind: "note" | "run";
  at: string;
  project: string;
  repo: string;
  pr_key?: string;
  title?: string;
  /** A note's merge call outcome; absent when no call was made. */
  outcome?: string;
  /** How many notes the note's call folded, when more than one: the cost
   *  is the whole call's. */
  batch?: number;
  run_id?: number;
  /** A run's mode: a restyle reads as such. */
  mode?: Baseline["mode"];
  state?: "done" | "failed";
  skipped?: number;
  error?: string;
  retry_at?: string;
  cost_usd?: string;
}

/** A note the merge call failed on: off the queue with its reason until
 *  the next run of its repository, or for good once given up. */
export interface QueueFailed {
  project: string;
  repo: string;
  pr_key: string;
  title?: string;
  at: string;
  reason: string;
  attempts: number;
  given_up: boolean;
}

export interface Queue {
  /** The pause: since when; null while the worker takes items. */
  paused: { at: string } | null;
  current: QueueItem | null;
  next: QueueItem[];
  /** The sync cycle; null with the sources off. */
  sync: {
    running: boolean;
    project?: string;
    repo?: string;
    repo_since?: string;
    done: number;
    total: number;
    finished_at: string | null;
    next_at: string | null;
  } | null;
  /** unpriced: calls cut before their usage arrived, not in cost_usd. */
  spent: {
    cost_usd?: string;
    calls: number;
    unpriced: number;
    budgeted_usd?: string;
  };
  recent: QueueRecent[];
  failed: QueueFailed[];
  poll_seconds: number;
  /** The attempt an item is given up at. */
  max_attempts: number;
  /** Items queued past the listed ones. */
  more: number;
}

/** The queue's size, for the nav. */
export interface QueueCount {
  count: number;
  /** The queue is paused: the nav says so. */
  paused: boolean;
}

/** pollHeader marks a request a timer repeats; mirrored in
 *  backend/internal/api/logclass.go. */
export const pollHeader = "X-Almanach-Poll";

/** get fetches one API path. A non-2xx is an error: the pages show their
 *  failed state rather than an empty one. */
export async function get<T>(
  path: string,
  signal?: AbortSignal,
  poll = false,
): Promise<T> {
  // A refetch on a timer says so: the server logs it at Debug, a reader's
  // own request at Info.
  const res = await fetch(`/api/${path}`, {
    signal,
    ...(poll ? { headers: { [pollHeader]: "1" } } : {}),
  });
  onUnauthorized(res.status);
  if (!res.ok) {
    throw new Error(`${path}: HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}

/** parseJSON is JSON.parse that answers null for an empty or non-JSON
 *  body (a proxy's error page) rather than throwing. */
function parseJSON(text: string): unknown {
  if (text === "") return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/** ApiError carries the server's own message, which names the field for a
 *  422, so a form can show it as it is. */
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

/** send is a write: JSON in, JSON or nothing out. The server's `error` is
 *  the message of the rejection. */
export async function send<T>(
  method: "POST" | "PATCH" | "DELETE",
  path: string,
  body?: unknown,
): Promise<T | null> {
  const res = await fetch(`/api/${path}`, {
    method,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  onUnauthorized(res.status);
  if (res.status === 204) return null;
  const parsed = parseJSON(await res.text());
  if (!res.ok) {
    const err = parsed as { error?: string } | null;
    throw new ApiError(res.status, err?.error ?? `HTTP ${res.status}`);
  }
  return parsed as T;
}

/** Me is the session check's answer: the auth mode and the reader's name
 *  (preferred username, else email, else subject), "" in none mode. */
export interface Me {
  mode: "none" | "oidc";
  user: string;
}
