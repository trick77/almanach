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
// digits, dot, dash, underscore. Mirrored in backend/web/embed.go.
const SEG = "[A-Za-z0-9._-]+";
const PROJECT = new RegExp(`^/projekte/(${SEG})$`);
const REPO = new RegExp(`^/projekte/(${SEG})/repos/(${SEG})$`);
const NOTE = new RegExp(
  `^/projekte/(${SEG})/repos/(${SEG})/notizen/([1-9][0-9]{0,8})$`,
);

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

export function projectPath(key: string): string {
  return `/projekte/${key}`;
}

export function repoPath(key: string, slug: string): string {
  return `/projekte/${key}/repos/${slug}`;
}

export function notePath(key: string, slug: string, pr: number): string {
  return `/projekte/${key}/repos/${slug}/notizen/${pr}`;
}

/** notePathFor is the note page for a PR key the API gives as
 *  PROJECT/repo#id, or null when the key is not in that shape. */
export function notePathFor(prKey: string): string | null {
  const m = /^([^/]+)\/([^#]+)#([1-9][0-9]*)$/.exec(prKey);
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
