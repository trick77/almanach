import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import type { Me, QueueCount, Sources } from "./api";
import { CostsPage } from "./CostsPage";
import { DepsPage } from "./DepsPage";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { QueuePage, queueChanged } from "./QueuePage";
import { RepoPage } from "./RepoPage";
import { RevisionPage } from "./RevisionPage";
import { SearchPage } from "./SearchPage";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import {
  costsPath,
  depsPath,
  navigateTo,
  queuePath,
  routeFromPath,
  searchFor,
  sourcesPath,
  type Route,
} from "./routing";
import { logout, useSession } from "./session";
import { loginHref } from "./signin";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import { btn, btnPrimary, Link } from "./ui";

/** queueEvery is how often the shell refreshes the queue's count. */
const queueEvery = 15000;

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
      {session.state === "halted" && session.action !== "retry" && (
        <a href={loginHref()} className={btnPrimary + " mt-4"}>
          {session.action === "again" ? S.auth.retry : S.auth.signIn}
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
  const here = () =>
    routeFromPath(window.location.pathname, window.location.search);
  const [route, setRoute] = useState<Route>(here);
  const [signOutFailed, setSignOutFailed] = useState(false);

  // From the commit that renders the shell (a layout effect): an address
  // change the moment it shows is heard.
  useLayoutEffect(() => {
    const onPop = () => setRoute(here());
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
  // The operations' pages, Betrieb's: one list, so Projekte and Betrieb
  // never both or neither read as current.
  const inOps = inSources || route.page === "queue" || route.page === "costs";
  // The queue's count beside its entry: work in flight and waiting.
  const { data: queue, reload: reloadQueue } = useFetch<QueueCount>(
    "queue/count",
    queueEvery,
  );
  const queued = queue?.count ?? 0;
  // A pause or resume on the queue page: the badge reads at once.
  useEffect(() => {
    window.addEventListener(queueChanged, reloadQueue);
    return () => window.removeEventListener(queueChanged, reloadQueue);
  }, [reloadQueue]);

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
    // Below lg the header is two rows: the wordmark, the nav and Betrieb,
    // then the search with the reader's slot. One row there would leave
    // the search no width beside the nav, Betrieb's badges and the
    // sign-out. A crowded first row (Abhängigkeiten, both badges, a phone)
    // wraps Betrieb onto a row of its own, never off the screen.
    <div className="grid h-dvh grid-rows-[auto_1fr] lg:grid-rows-[56px_1fr]">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border bg-panel px-4 py-2 sm:px-5 lg:flex-nowrap lg:gap-6 lg:py-0">
        <h1 className="font-serif text-[21px] leading-7 font-medium whitespace-nowrap text-wordmark">
          <Link to="/" className="text-wordmark hover:text-ink">
            {S.app}
          </Link>
        </h1>
        <nav
          className="flex flex-wrap items-center gap-0.5"
          aria-label={S.nav.label}
        >
          {nav(
            S.nav.projects,
            "/",
            route.page !== "deps" && route.page !== "search" && !inOps,
          )}
          {sourcesOn && nav(S.nav.deps, depsPath, route.page === "deps")}
        </nav>
        {/* A line break below lg; -mt-1 takes back its own row's gap. */}
        <span aria-hidden className="order-last -mt-1 basis-full lg:hidden" />
        <div className="order-last flex min-w-0 flex-1 justify-center lg:order-none">
          <SearchBox
            key={route.page === "search" ? route.q : ""}
            route={route}
          />
        </div>
        <OpsMenu
          route={route}
          current={inOps}
          inSources={inSources}
          sourcesOn={sourcesOn}
          queued={queued}
          paused={queue?.paused ?? false}
        />
        <span
          className="order-last flex items-center gap-3 text-[13px] text-muted empty:hidden lg:order-none"
          data-slot="reader"
        >
          {me.mode === "oidc" && (
            <>
              {signOutFailed && (
                <span role="alert" className="text-danger-ink">
                  {S.auth.signOutFailed}
                </span>
              )}
              {/* Capped: a long name must not push Betrieb off the one
                  lg row, which does not wrap. */}
              <span className="hidden max-w-[16ch] truncate sm:inline">
                {me.user}
              </span>
              <button
                type="button"
                className={btn}
                onClick={() =>
                  void logout().then((ok) => setSignOutFailed(!ok))
                }
              >
                {S.auth.signOut}
              </button>
            </>
          )}
        </span>
      </header>

      <main className="min-h-0 min-w-0">
        {route.page === "projects" && <ProjectsPage />}
        {route.page === "sources" && <SourcesPage />}
        {route.page === "queue" && <QueuePage />}
        {route.page === "deps" && <DepsPage />}
        {route.page === "costs" && <CostsPage />}
        {route.page === "search" && <SearchPage q={route.q} />}
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
        {route.page === "revision" && (
          <RevisionPage
            key={`${route.key}/${route.slug}@${route.n}`}
            projectKey={route.key}
            slug={route.slug}
            n={route.n}
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

/** OpsMenu is the operations behind one button, apart from the knowledge
 *  in the nav: Quellen (with the feature only), Queue, Kosten. The
 *  button carries the queue's count and pause mark, so they read from
 *  every page while the list is closed; current is the route's being one
 *  of them. The list closes on Escape (the focus back on the button, the
 *  key no other control's), on a press outside, when the focus leaves it
 *  (Tab: the entries are out of the tab order, the arrows walk them), on
 *  a pick (the page's own: the focus back on the button) and on every
 *  route change. While it stands the page's shortcuts hold off
 *  (shortcutFree). */
function OpsMenu({
  route,
  current,
  inSources,
  sourcesOn,
  queued,
  paused,
}: {
  route: Route;
  current: boolean;
  inSources: boolean;
  sourcesOn: boolean;
  queued: number;
  paused: boolean;
}) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLUListElement>(null);

  // Any route change closes: a pick that navigates, back and forward.
  // Set while rendering, as React advises for state that follows a prop.
  const [shownAt, setShownAt] = useState(route);
  if (shownAt !== route) {
    setShownAt(route);
    setOpen(false);
  }

  function closeBack() {
    setOpen(false);
    button.current?.focus();
  }

  useEffect(() => {
    if (!open) return;
    list.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    const onDown = (e: PointerEvent) => {
      if (e.target instanceof Node && box.current?.contains(e.target)) return;
      setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);

  function walk(e: KeyboardEvent<HTMLUListElement>) {
    const items = [
      ...(list.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ??
        []),
    ];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const to =
      e.key === "ArrowDown"
        ? (at + 1) % items.length
        : e.key === "ArrowUp"
          ? (at - 1 + items.length) % items.length
          : e.key === "Home"
            ? 0
            : e.key === "End"
              ? items.length - 1
              : -1;
    if (to < 0) return;
    e.preventDefault();
    items[to]?.focus();
  }

  const entry = (
    label: string,
    to: string,
    here: boolean,
    count = 0,
    pausedHere = false,
  ) => (
    <li role="none">
      <Link
        to={to}
        role="menuitem"
        tabIndex={-1}
        className={
          "flex items-center rounded-ui-sm px-3 py-2 text-sm outline-none hover:bg-active hover:text-ink focus-visible:bg-active focus-visible:text-ink " +
          (here ? "text-ink" : "text-muted")
        }
      >
        <span aria-current={here ? "page" : undefined}>{label}</span>
        {count > 0 && <span className={countBadge}>{count}</span>}
        {pausedHere && (
          <span className={pausedBadge}>{S.queue.pausedBadge}</span>
        )}
      </Link>
    </li>
  );

  return (
    <div
      ref={box}
      className="relative ml-auto shrink-0"
      onKeyDown={(e) => {
        if (e.key !== "Escape" || !open) return;
        // The menu's Escape alone: a pinned map box stays pinned.
        e.stopPropagation();
        closeBack();
      }}
      onBlur={(e) => {
        // Focus gone elsewhere by keyboard; a press outside is onDown's.
        const to = e.relatedTarget;
        if (open && to instanceof Node && !box.current?.contains(to))
          setOpen(false);
      }}
    >
      <button
        ref={button}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={S.nav.opsLabel(queued, paused)}
        onClick={() => setOpen(!open)}
        className={
          "flex items-center rounded-ui-sm px-3 py-1.5 text-sm whitespace-nowrap transition-colors " +
          (current
            ? "bg-active text-ink"
            : "text-muted hover:bg-active hover:text-ink")
        }
      >
        {S.nav.ops}
        {queued > 0 && (
          <span data-testid="queue-count" className={countBadge}>
            {queued}
          </span>
        )}
        {paused && (
          <span data-testid="queue-paused" className={pausedBadge}>
            {S.queue.pausedBadge}
          </span>
        )}
      </button>
      {open && (
        <ul
          ref={list}
          role="menu"
          aria-label={S.nav.opsMenu(sourcesOn)}
          onKeyDown={walk}
          onClickCapture={(e) => {
            // Before the entry's own click navigates: the page's own entry
            // navigates nowhere, and the focus would fall to the page's
            // top with the list.
            const a = (e.target as Element).closest("a");
            const { pathname, search } = window.location;
            if (a?.getAttribute("href") === pathname + search) closeBack();
            else setOpen(false);
          }}
          className="absolute top-full right-0 z-50 mt-1.5 min-w-[160px] rounded-ui border border-elevated-border bg-elevated p-1.5 shadow-panel"
        >
          {sourcesOn && entry(S.nav.sources, sourcesPath, inSources)}
          {entry(
            S.nav.queue,
            queuePath,
            route.page === "queue",
            queued,
            paused,
          )}
          {entry(S.nav.costs, costsPath, route.page === "costs")}
        </ul>
      )}
    </div>
  );
}

/** countBadge is the queue's count beside a label, pausedBadge its pause. */
const countBadge =
  "ml-1.5 rounded-full bg-accent-dim px-1.5 text-[11.5px] text-ink tabular-nums";
const pausedBadge =
  "ml-1.5 rounded-full border border-ochre px-1.5 text-[11.5px] text-ochre";

/** SearchBox is the header's search over the whole wiki: a phrase
 *  submitted goes to the search page, which holds it in its address. On
 *  that page the box shows the phrase searched (the key above remounts
 *  it per phrase, so back and forward refill it). The rail's "/" is the
 *  page's own search and stays the rail's. */
function SearchBox({ route }: { route: Route }) {
  const [q, setQ] = useState(route.page === "search" ? route.q : "");
  return (
    <form
      role="search"
      className="w-full min-w-0 shrink lg:w-auto"
      onSubmit={(e) => {
        e.preventDefault();
        if (q.trim() !== "") navigateTo(searchFor(q.trim()));
      }}
    >
      <input
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={S.search.placeholder}
        aria-label={S.search.box}
        className="h-[32px] w-full rounded-ui-sm border border-border bg-bg px-2.5 text-[13px] text-ink outline-none placeholder:text-faint focus:border-elevated-border focus:bg-active lg:max-w-[240px]"
      />
    </form>
  );
}
