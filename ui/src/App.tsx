import { useEffect, useState } from "react";
import type { Me, Sources } from "./api";
import { FaqPage } from "./FaqPage";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { RepoPage } from "./RepoPage";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import { routeFromPath, sourcesPath, type Route } from "./routing";
import { logout, useSession } from "./session";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import { btn, btnPrimary, Link } from "./ui";

/** The session gate in front of the shell: nothing that fetches renders
 *  until the session is known (see session.ts). */
export default function App() {
  const session = useSession();
  if (session.state === "in") return <Shell me={session.me} />;
  return (
    <main className="mx-auto max-w-3xl p-5 sm:p-8">
      <p className="text-sm text-muted">
        {session.state === "checking" && S.auth.checking}
        {session.state === "out" && S.auth.redirecting}
        {session.state === "halted" && session.message}
      </p>
      {session.state === "halted" && session.action === "signIn" && (
        <a href="/api/auth/login" className={btnPrimary + " mt-4"}>
          {S.auth.signIn}
        </a>
      )}
      {session.state === "halted" && session.action === "retry" && (
        <button
          type="button"
          className={btnPrimary + " mt-4"}
          onClick={() => window.location.reload()}
        >
          {S.auth.retry}
        </button>
      )}
    </main>
  );
}

/** The shell: a fixed header over one scrolling page. A top nav, not
 *  ../rongo's thread rail: the wiki has pages, not conversations. The slot
 *  on the right is the reader's name and the sign-out, in oidc mode only:
 *  in none there is nobody to name. */
function Shell({ me }: { me: Me }) {
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
        <span
          className="flex items-center gap-3 text-[13px] text-muted"
          data-slot="reader"
        >
          {me.mode === "oidc" && (
            <>
              <span className="hidden truncate sm:inline">{me.user}</span>
              <button
                type="button"
                className={btn}
                onClick={() => void logout()}
              >
                {S.auth.signOut}
              </button>
            </>
          )}
        </span>
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
