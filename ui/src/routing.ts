// Hand-rolled, like ../noergler's: five routes do not need a router. The
// patterns mirror backend/web/embed.go, which serves the shell for exactly
// these paths and a 404 for everything else.

export type Route =
  | { page: "projects" }
  | { page: "faq" }
  | { page: "project"; key: string }
  | { page: "repo"; key: string; slug: string }
  | { page: "note"; key: string; slug: string; pr: number };

// A project key and a repository slug are what Bitbucket allows: letters,
// digits, dot, dash, underscore, and the tilde of a personal project
// (~user). Mirrored exactly in backend/web/embed.go: a path the shell is
// served for must parse here, and the other way round.
const KEY = "[A-Za-z0-9._~-]+";
const SLUG = "[A-Za-z0-9._-]+";
const PR = "[1-9][0-9]{0,8}";
const PROJECT = new RegExp(`^/projekte/(${KEY})$`);
const REPO = new RegExp(`^/projekte/(${KEY})/repos/(${SLUG})$`);
const NOTE = new RegExp(`^/projekte/(${KEY})/repos/(${SLUG})/notizen/(${PR})$`);
const PR_KEY = new RegExp(`^(${KEY})/(${SLUG})#(${PR})$`);

export function routeFromPath(path: string): Route {
  let m = NOTE.exec(path);
  if (m) return { page: "note", key: m[1], slug: m[2], pr: Number(m[3]) };
  m = REPO.exec(path);
  if (m) return { page: "repo", key: m[1], slug: m[2] };
  m = PROJECT.exec(path);
  if (m) return { page: "project", key: m[1] };
  if (path === "/faq") return { page: "faq" };
  return { page: "projects" };
}

// Encoded, so a key the server would not serve becomes a visible 404
// rather than a link that changes the URL and shows nothing.
export function projectPath(key: string): string {
  return `/projekte/${encodeURIComponent(key)}`;
}

export function repoPath(key: string, slug: string): string {
  return `${projectPath(key)}/repos/${encodeURIComponent(slug)}`;
}

export function notePath(key: string, slug: string, pr: number): string {
  return `${repoPath(key, slug)}/notizen/${pr}`;
}

/** notePathFor is the note page for a PR key the API gives as
 *  PROJECT/repo#id, or null when the key is not one the routes serve. */
export function notePathFor(prKey: string): string | null {
  const m = PR_KEY.exec(prKey);
  if (!m) return null;
  return notePath(m[1], m[2], Number(m[3]));
}

/** onNav is an in-app link's click handler: a plain click stays in the SPA,
 *  a modified one (new tab, new window) is left to the browser. */
export function onNav(path: string) {
  return (e: {
    metaKey: boolean;
    ctrlKey: boolean;
    shiftKey: boolean;
    altKey: boolean;
    button: number;
    preventDefault: () => void;
  }) => {
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0)
      return;
    e.preventDefault();
    navigateTo(path);
  };
}

/** navigateTo pushes any app path. No duplicate history entry for the page
 *  you are already on. */
export function navigateTo(path: string) {
  if (window.location.pathname === path) return;
  window.history.pushState({}, "", path);
  // Tell the shell, which otherwise only hears back/forward (popstate).
  window.dispatchEvent(new PopStateEvent("popstate"));
}
