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
 *  extension is a repository's alone, never on a project page. */
export type Kind =
  | "component"
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
  /** Prose, paragraphs separated by a blank line. Never markdown. */
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
  /** The newest baseline run, null before the first. */
  baseline: Baseline | null;
}

export type RunState =
  "queued" | "cataloguing" | "describing" | "project" | "done" | "failed";

/** A repository's newest baseline run and the commit last documented. */
export interface Baseline {
  run_id: number;
  state: RunState;
  mode: "full" | "reconcile";
  trigger: "manual" | "first_ready" | "monthly" | "retry";
  commit: string;
  step: number;
  steps: number;
  skipped: number;
  /** 3 decimals; absent when the gateway priced none of its calls. */
  cost_usd?: string;
  error: string;
  queued_at: string;
  started_at: string | null;
  finished_at: string | null;
  documented_commit: string;
  documented_at: string | null;
}

export interface SourceDetail {
  project_key: string;
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
