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
  last_merge: string | null;
  /** False when the repositories stand alone: no project page, they are
   *  listed one by one in repositories. */
  system: boolean;
  /** A standalone project's repositories, empty for a system. */
  repositories: Repo[];
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

export interface ProjectDetail {
  key: string;
  team: string;
  /** False when the repositories stand alone: description and map are
   *  null and none is expected. */
  system: boolean;
  description: Description | null;
  /** The system map: zones clients, repositories, stores and external; a
   *  repository node's id is its slug. null before the first baseline
   *  project step wrote one. */
  map: FlowIR | null;
  /** A baseline run will draw a map: the sources are on, the project has a
   *  source repository, and not more than one map holds. */
  map_expected: boolean;
  /** A project step asked for the map and none was accepted: map is null,
   *  and no run is promised to draw one. */
  map_declined: boolean;
  repos: Repo[];
  problems: Problem[];
  problem_window_days: number;
  recent_notes: NoteSummary[];
}

export interface Revision {
  revision: number;
  source_kind?: "pr" | "baseline";
  source_pr_key: string;
  source_commit?: string;
  statements: number;
  chars: number;
  created_at: string;
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
}

// The sources: what Almanach clones itself. Mirrors api/sources.go.

export type SourceKind = "bitbucket" | "static";
export type TokenAuth = "bearer" | "basic";
export type RepoState = "pending" | "syncing" | "ready" | "failed" | "missing";

export interface SourceSummary {
  project_key: string;
  kind: SourceKind;
  /** The repositories form one system; false when each stands alone. */
  system: boolean;
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
  /** The clone's folder, named by ids, never by the names. */
  dir: string;
  /** The newest baseline run, null before the first. */
  baseline: Baseline | null;
}

export type RunState =
  "queued" | "cataloguing" | "describing" | "project" | "done" | "failed";

/** Why a run left a part out; "" for a run from before it was kept.
 *  "ceiling" and "over_ceiling" only on runs from before pages lost their
 *  ceiling. TRANSITIONAL(ceilings): Remove both when no stored run plan
 *  carries them. */
export type SkipReason =
  | ""
  | "gone"
  | "calls"
  | "bytes"
  | "ceiling"
  | "failed"
  | "refused"
  | "over_ceiling"
  | "no_page";

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
  mode: "full" | "reconcile";
  trigger: "manual" | "first_ready" | "monthly" | "retry";
  commit: string;
  step: number;
  steps: number;
  skipped: number;
  /** The parts the run left out, in the order it hit them. */
  skipped_parts: SkippedPart[];
  /** 3 decimals; absent when the gateway priced none of its calls. */
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
  kind: SourceKind;
  system: boolean;
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
  | "clone_busy"
  | "placing";

export type StepKind =
  "catalogue" | "overview" | "subject" | "project" | "repo_map";

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
  mode: "full" | "reconcile";
  trigger: Baseline["trigger"];
  commit: string;
  base_commit: string;
  calls: number;
  queued_at: string;
  started_at: string | null;
  cost_usd?: string;
  steps: RunStep[];
}

/** A note or a run in the queue. */
export interface QueueItem {
  kind: "note" | "run";
  project: string;
  repo: string;
  pr_key?: string;
  title?: string;
  change_kind?: string;
  received_at?: string;
  /** The run of the note's repository it waits for. */
  held_by?: number;
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
  run_id?: number;
  state?: "done" | "failed";
  skipped?: number;
  error?: string;
  retry_at?: string;
  cost_usd?: string;
}

export interface Queue {
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
  spent: { cost_usd?: string; calls: number };
  recent: QueueRecent[];
  poll_seconds: number;
  /** The attempt an item is given up at. */
  max_attempts: number;
  /** Items queued past the listed ones. */
  more: number;
}

/** The queue's size, for the nav. */
export interface QueueCount {
  count: number;
}

/** get fetches one API path. A non-2xx is an error: the pages show their
 *  failed state rather than an empty one. */
export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`/api/${path}`, { signal });
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
