// Hand-rolled, like ../noergler's: a handful of routes need no router. The
// patterns mirror backend/web/embed.go, which serves the shell for exactly
// these paths and a 404 for everything else.

export type Route =
  | { page: "projects" }
  | { page: "project"; key: string }
  | { page: "repo"; key: string; slug: string }
  | { page: "note"; key: string; slug: string; pr: number }
  | { page: "sources" }
  | { page: "source"; key: string }
  | { page: "queue" }
  | { page: "deps" }
  | { page: "costs" }
  | { page: "search"; q: string };

// A project key and a repository slug are what Bitbucket allows: letters,
// digits, dot, dash, underscore, and the tilde of a personal project
// (~user). Mirrored exactly in backend/web/embed.go: a path the shell is
// served for must parse here, and the other way round.
const KEY = "[A-Za-z0-9._~-]+";
const SLUG = "[A-Za-z0-9._-]+";
const PR = "[1-9][0-9]{0,8}";

// Each prefix once: the patterns below and the builders take it from here.
export const projectsPath = "/projects";

export const sourcesPath = "/sources";

export const depsPath = "/dependencies";

export const queuePath = "/queue";

export const costsPath = "/costs";

export const searchPath = "/search";

const PROJECT = new RegExp(`^${projectsPath}/(${KEY})$`);
const REPO = new RegExp(`^${projectsPath}/(${KEY})/repos/(${SLUG})$`);
const NOTE = new RegExp(
  `^${projectsPath}/(${KEY})/repos/(${SLUG})/notes/(${PR})$`,
);
const PR_KEY = new RegExp(`^(${KEY})/(${SLUG})#(${PR})$`);
const SOURCE = new RegExp(`^${sourcesPath}/(${KEY})$`);

/** routeFromPath is the page an address names; search is the address's
 *  query string, read for the search page's phrase alone. */
export function routeFromPath(path: string, search = ""): Route {
  if (path === searchPath)
    return { page: "search", q: new URLSearchParams(search).get("q") ?? "" };
  let m = NOTE.exec(path);
  if (m) return { page: "note", key: m[1], slug: m[2], pr: Number(m[3]) };
  m = REPO.exec(path);
  if (m) return { page: "repo", key: m[1], slug: m[2] };
  m = PROJECT.exec(path);
  if (m) return { page: "project", key: m[1] };
  m = SOURCE.exec(path);
  if (m) return { page: "source", key: m[1] };
  if (path === sourcesPath) return { page: "sources" };
  if (path === queuePath) return { page: "queue" };
  if (path === depsPath) return { page: "deps" };
  if (path === costsPath) return { page: "costs" };
  return { page: "projects" };
}

/** searchFor is the search page for a phrase. */
export function searchFor(q: string): string {
  return `${searchPath}?q=${encodeURIComponent(q)}`;
}

export function sourcePath(key: string): string {
  return `${sourcesPath}/${encodeURIComponent(key)}`;
}

// Encoded, so a key the server would not serve becomes a visible 404
// rather than a link that changes the URL and shows nothing.
export function projectPath(key: string): string {
  return `${projectsPath}/${encodeURIComponent(key)}`;
}

export function repoPath(key: string, slug: string): string {
  return `${projectPath(key)}/repos/${encodeURIComponent(slug)}`;
}

/** The Markdown exports, the agent's interface: the API's own paths,
 *  which a session opens as a browser tab does. */
export function projectExportHref(key: string): string {
  return `/api${projectPath(key)}.md`;
}

export function repoExportHref(key: string, slug: string): string {
  return `/api${repoPath(key, slug)}.md`;
}

export function notePath(key: string, slug: string, pr: number): string {
  return `${repoPath(key, slug)}/notes/${pr}`;
}

/** notePathFor is the note page for a PR key the API gives as
 *  PROJECT/repo#id, or null when the key is not one the routes serve. */
export function notePathFor(prKey: string): string | null {
  const m = PR_KEY.exec(prKey);
  if (!m) return null;
  return notePath(m[1], m[2], Number(m[3]));
}

/** A click as the browser sees it: the modifiers and the button. */
export interface Click {
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  button: number;
  preventDefault: () => void;
}

/** plainClick says a link's click is the page's to handle: a modified one
 *  (new tab, new window) or another button is left to the browser. */
export function plainClick(e: Click): boolean {
  return !(e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0);
}

/** onNav is an in-app link's click handler: a plain click stays in the
 *  SPA. */
export function onNav(path: string) {
  return (e: Click) => {
    if (!plainClick(e)) return;
    e.preventDefault();
    navigateTo(path);
  };
}

/** replaceTo swaps the address for path without a history entry: a page
 *  opened under a project's or repository's old name shows its current
 *  one, the API having found it by the old. The hash stays: it is the
 *  page's tab. */
export function replaceTo(path: string) {
  if (window.location.pathname === path) return;
  window.history.replaceState({}, "", path + window.location.hash);
  window.dispatchEvent(new PopStateEvent("popstate"));
}

/** navigateTo pushes any app path. No duplicate history entry for the page
 *  you are already on. */
export function navigateTo(path: string) {
  if (window.location.pathname + window.location.search === path) return;
  window.history.pushState({}, "", path);
  // Tell the shell, which otherwise only hears back/forward (popstate).
  window.dispatchEvent(new PopStateEvent("popstate"));
}
