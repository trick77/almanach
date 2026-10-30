import { useEffect, useState } from "react";
import type { Sources } from "./api";
import { FaqPage } from "./FaqPage";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { RepoPage } from "./RepoPage";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import { routeFromPath, sourcesPath, type Route } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import { Link } from "./ui";

/** The shell: a fixed header over one scrolling page. A top nav, not
 *  ../rongo's thread rail: the wiki has pages, not conversations. The slot
 *  on the right is for the reader's name once the API gives one. */
export default function App() {
  const [route, setRoute] = useState<Route>(() =>
    routeFromPath(window.location.pathname),
  );

  useEffect(() => {
    const onPop = () => setRoute(routeFromPath(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  useEffect(() => {
    if (route.page === "projects")
      document.title = `${S.projects.title} ${S.common.sep} ${S.app}`;
  }, [route]);

  // The Quellen entry shows only on an instance that has the feature; the
  // list route answers enabled either way.
  const { data: sources } = useFetch<Sources>("sources");
  const sourcesOn = sources?.enabled ?? false;
  const inSources = route.page === "sources" || route.page === "source";

  const nav = (label: string, to: string, current: boolean) => (
    <Link
      to={to}
      className={
        "rounded-ui-sm px-3 py-1.5 text-sm transition-colors " +
        (current
          ? "bg-active text-ink"
          : "text-muted hover:bg-active hover:text-ink")
      }
    >
      <span aria-current={current ? "page" : undefined}>{label}</span>
    </Link>
  );

  return (
    // h-dvh, not h-screen: iOS Safari counts the collapsed toolbar into 100vh.
    <div className="grid h-dvh grid-rows-[56px_1fr]">
      <header className="flex items-center gap-4 border-b border-border bg-panel px-4 sm:gap-6 sm:px-5">
        <h1 className="font-serif text-[21px] leading-7 font-medium whitespace-nowrap text-wordmark">
          <Link to="/" className="text-wordmark hover:text-ink">
            {S.app}
          </Link>
        </h1>
        <nav
          className="flex flex-1 flex-wrap items-center gap-0.5"
          aria-label={S.nav.label}
        >
          {nav(S.nav.projects, "/", route.page !== "faq" && !inSources)}
          {sourcesOn && nav(S.nav.sources, sourcesPath, inSources)}
          {nav(S.nav.faq, "/faq", route.page === "faq")}
        </nav>
        <span className="text-[13px] text-muted" data-slot="reader" />
      </header>

      <main className="min-h-0 min-w-0">
        {route.page === "projects" && <ProjectsPage />}
        {route.page === "faq" && <FaqPage />}
        {route.page === "sources" && <SourcesPage />}
        {route.page === "source" && (
          <SourcePage key={route.key} projectKey={route.key} />
        )}
        {route.page === "project" && (
          <ProjectPage key={route.key} projectKey={route.key} />
        )}
        {route.page === "repo" && (
          <RepoPage
            key={route.key + "/" + route.slug}
            projectKey={route.key}
            slug={route.slug}
          />
        )}
        {route.page === "note" && (
          <NotePage
            key={`${route.key}/${route.slug}#${route.pr}`}
            projectKey={route.key}
            slug={route.slug}
            pr={route.pr}
          />
        )}
      </main>
    </div>
  );
}
