// The wire types. They mirror the structs in backend/internal/api/api.go
// and the contract in backend/api/openapi.yaml; a field that is nullable
// there is `| null` here, never optional, so a missing value has to be
// handled rather than silently read as undefined. Fields the Go side marks
// omitempty are optional here.

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

export interface Source {
  pr_key: string;
  ticket_key?: string;
  merged_at: string;
}

export type Kind = "component" | "rule" | "invariant" | "entity";

export interface Statement {
  id: string;
  kind: Kind;
  name: string;
  /** Prose, paragraphs separated by a blank line. Never markdown. */
  text: string;
  files: string[];
  sources: Source[];
}

export interface Description {
  revision: number;
  summary: string;
  statements: Statement[];
  source_pr_key: string;
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
  repos: Repo[];
  problems: Problem[];
  problem_window_days: number;
  recent_notes: NoteSummary[];
}

export interface Revision {
  revision: number;
  source_pr_key: string;
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

/** get fetches one API path. A non-2xx is an error: the pages show their
 *  failed state rather than an empty one. */
export async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(`/api/${path}`, { signal });
  if (!res.ok) {
    throw new Error(`${path}: HTTP ${res.status}`);
  }
  return (await res.json()) as T;
}
