// The project and the repository page's frame: two tabs, and a rail beside
// them that holds the search and the sections of both tabs. The rail never
// leaves, so the column never moves sideways on a tab switch, and rail and
// column start on one top edge.
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { S } from "./strings";
import { Link, page } from "./ui";

/** railTop is the one top edge: the grid's padding, the rail's and the
 *  breadcrumb's sticky offset. Change it in one place. */
const railTop = "pt-7";
const stickyTop = "min-[1180px]:top-7";

/** RailPage lays out the rail beside the column. Below 1180px the rail
 *  becomes a sticky row above the column: search, then the sections as
 *  chips. The scroller is the page's own (`page`), so sticky works. */
export function RailPage({
  rail,
  scroller,
  children,
}: {
  rail: ReactNode;
  scroller: RefObject<HTMLDivElement | null>;
  children: ReactNode;
}) {
  return (
    <div ref={scroller} className={page}>
      <div
        className={
          "mx-auto grid max-w-[1400px] items-start px-4 pb-7 sm:px-6 lg:px-10 min-[1180px]:grid-cols-[196px_minmax(0,1fr)] min-[1180px]:gap-x-11 " +
          railTop
        }
      >
        {rail}
        <div className="min-w-0">{children}</div>
      </div>
    </div>
  );
}

/** stickyCrumbs keeps the breadcrumb on the rail's top edge while the page
 *  scrolls: it sticks at the top with the grid's padding inside it, on the
 *  page's background. Narrow, where the rail row sticks, it scrolls away. */
export const stickyCrumbs =
  "z-10 bg-bg [&>nav]:mb-0 pb-2.5 min-[1180px]:sticky min-[1180px]:top-0 min-[1180px]:-mt-7 min-[1180px]:pt-7";

export interface RailItem {
  /** id is the section's element id. */
  id: string;
  label: string;
  tab: string;
  count?: number | string;
  /** dim: an empty kind, or one the search leaves without a hit. */
  dim?: boolean;
  /** hit: the count is a search's. */
  hit?: boolean;
}

export interface RailGroup {
  title: ReactNode;
  items?: RailItem[];
  /** links are pages, not sections: the project's other repositories, the
   *  ones this repository uses or is used by. */
  links?: RailLink[];
}

export interface RailLink {
  label: string;
  to: string;
  current: boolean;
  /** note sits on the right, like a count: a version. odd marks it. */
  note?: string;
  odd?: boolean;
  noteTitle?: string;
  /** sub is a line under the label: the library's other users. */
  sub?: string;
}

/** Rail is the search and the sections. A section in the other tab
 *  switches the tab, then scrolls to it; a section link never writes the
 *  address, which holds the tab. */
export function Rail({
  groups,
  tab,
  onTab,
  query,
  onQuery,
  hitLine,
  scroller,
}: {
  groups: RailGroup[];
  tab: string;
  onTab: (tab: string) => void;
  query: string;
  onQuery: (q: string) => void;
  hitLine: string;
  scroller: RefObject<HTMLDivElement | null>;
}) {
  const input = useRef<HTMLInputElement>(null);
  const pending = useRef<string | null>(null);
  const items = groups.flatMap((g) => g.items ?? []);
  const active = useActiveSection(
    items.filter((i) => i.tab === tab).map((i) => i.id),
    scroller,
    query,
  );

  // A jump into the other tab lands once that tab has rendered.
  useEffect(() => {
    const id = pending.current;
    if (id === null) return;
    pending.current = null;
    document.getElementById(id)?.scrollIntoView();
  }, [tab]);

  // "/" finds, as on most sites with a search; not while typing elsewhere,
  // and not while a dialog (a diagram's full view) holds the page: focus
  // would leave it for the page behind.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (
        t &&
        (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))
      )
        return;
      if (document.querySelector('[role="dialog"]')) return;
      e.preventDefault();
      input.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  function go(item: RailItem) {
    if (item.tab !== tab) {
      pending.current = item.id;
      onTab(item.tab);
      return;
    }
    document.getElementById(item.id)?.scrollIntoView();
  }

  return (
    <nav
      aria-label={S.rail.label}
      className={
        "sticky top-0 z-20 -mx-4 flex items-center gap-3 overflow-x-auto border-b border-border-soft bg-bg px-4 py-2.5 sm:-mx-6 sm:px-6 lg:-mx-10 lg:px-10 " +
        "min-[1180px]:mx-0 min-[1180px]:block min-[1180px]:max-h-[calc(100dvh-56px-56px)] min-[1180px]:overflow-y-auto min-[1180px]:overflow-x-visible min-[1180px]:border-0 min-[1180px]:p-0 min-[1180px]:pr-1 " +
        stickyTop
      }
    >
      <label className="relative block w-[240px] shrink-0 min-[1180px]:w-auto">
        <SearchIcon />
        <input
          ref={input}
          type="search"
          value={query}
          onChange={(e) => onQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              onQuery("");
              e.currentTarget.blur();
            }
          }}
          placeholder={S.rail.search}
          aria-label={S.rail.searchLabel}
          className="h-[34px] w-full rounded-ui-sm border border-border bg-panel pr-8 pl-8 text-[13.5px] text-ink outline-none placeholder:text-faint focus:border-elevated-border focus:bg-active"
        />
        <kbd
          aria-hidden
          className="pointer-events-none absolute top-2 right-2 rounded border border-border px-1.5 font-mono text-[11px] leading-4 text-faint"
        >
          /
        </kbd>
      </label>
      <p
        aria-live="polite"
        className="mx-0.5 mt-1.5 hidden min-h-[18px] text-[12px] text-faint min-[1180px]:block"
      >
        {hitLine}
      </p>
      <div className="flex gap-0.5 whitespace-nowrap min-[1180px]:mt-2.5 min-[1180px]:block">
        {groups.map((g, gi) => (
          <div
            key={gi}
            className="flex gap-0.5 min-[1180px]:mt-4 min-[1180px]:block min-[1180px]:first:mt-0"
          >
            <div className="mb-1 ml-2.5 hidden text-[10.5px] font-medium tracking-[.12em] text-faint uppercase min-[1180px]:block">
              {g.title}
            </div>
            {g.items?.map((it) => (
              <a
                key={it.id}
                href={"#" + it.id}
                onClick={(e) => {
                  e.preventDefault();
                  go(it);
                }}
                aria-current={it.id === active ? "location" : undefined}
                className={
                  "flex items-baseline gap-2 rounded-full px-2.5 py-[3px] text-[13.5px] min-[1180px]:rounded-none min-[1180px]:border-l-2 " +
                  (it.id === active
                    ? "bg-active text-ink min-[1180px]:border-accent min-[1180px]:bg-transparent"
                    : "min-[1180px]:border-transparent " +
                      (it.dim
                        ? "text-faint hover:text-ink"
                        : it.tab === tab
                          ? "text-muted hover:text-ink"
                          : "text-[#85837b] hover:text-ink"))
                }
              >
                {it.label}
                {it.count !== undefined && (
                  <span
                    className={
                      "ml-auto text-[12px] tabular-nums " +
                      (it.hit && !it.dim ? "text-ochre" : "text-faint")
                    }
                  >
                    {it.count}
                  </span>
                )}
              </a>
            ))}
            {g.links?.map((l) => (
              <Link
                key={l.to}
                to={l.to}
                className={
                  "block rounded-full px-2.5 py-[3px] font-mono text-[12.5px] min-[1180px]:rounded-none min-[1180px]:border-l-2 " +
                  (l.current
                    ? "border-elevated-border text-ink"
                    : "border-transparent text-muted hover:text-ink")
                }
              >
                <span className="flex items-baseline gap-2">
                  <span
                    className="min-w-0 truncate"
                    aria-current={l.current ? "page" : undefined}
                  >
                    {l.label}
                  </span>
                  {l.note !== undefined && (
                    <span
                      className={
                        "ml-auto shrink-0 text-[11.5px] tabular-nums " +
                        (l.odd ? "text-ochre" : "text-faint")
                      }
                      title={l.noteTitle}
                    >
                      {l.note}
                    </span>
                  )}
                </span>
                {l.sub !== undefined && (
                  <span className="block truncate font-sans text-[12px] text-faint">
                    {l.sub}
                  </span>
                )}
              </Link>
            ))}
          </div>
        ))}
      </div>
    </nav>
  );
}

/** useActiveSection is the section being read: the last of ids whose top
 *  has passed a line a third down the scroller. Measured on every scroll:
 *  an observer alone only hears boundary crossings and lags; and again
 *  when layout changes without one (a search filters, sections move). */
function useActiveSection(
  ids: string[],
  scroller: RefObject<HTMLDivElement | null>,
  layout: string,
): string | undefined {
  const [active, setActive] = useState<string | undefined>(ids[0]);
  const key = ids.join(" ");
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const list = key === "" ? [] : key.split(" ");
    const update = () => {
      const line = el.getBoundingClientRect().top + el.clientHeight * 0.35;
      let cur = list[0];
      for (const id of list) {
        const s = document.getElementById(id);
        if (s && s.getBoundingClientRect().top < line) cur = id;
      }
      setActive(cur);
    };
    update();
    el.addEventListener("scroll", update, { passive: true });
    return () => el.removeEventListener("scroll", update);
  }, [key, scroller, layout]);
  return active;
}

/** Tabs is the page's two views, under its header. */
export function Tabs({
  tabs,
  value,
  onChange,
}: {
  tabs: { id: string; label: string; count?: number }[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <div
      role="tablist"
      aria-label={S.tabs.label}
      className="mt-5 flex gap-0.5 border-b border-border"
    >
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          role="tab"
          aria-selected={t.id === value}
          onClick={() => onChange(t.id)}
          className={
            "-mb-px border-b-2 px-3 py-[7px] text-[14px] " +
            (t.id === value
              ? "border-accent text-ink"
              : "border-transparent text-muted hover:text-ink")
          }
        >
          {t.label}
          {t.count !== undefined && (
            <span className="ml-1.5 text-[12px] text-faint">{t.count}</span>
          )}
        </button>
      ))}
    </div>
  );
}

/** useHashTab is the page's tab, kept in the address's hash and nowhere
 *  else: a shared link or a reload opens it. The start tab has no hash. A
 *  switch replaces the address, so back and forward leave the page rather
 *  than step through its tabs, and return to it on the tab it was left
 *  on. */
export function useHashTab(
  tabs: readonly string[],
  start: string,
): [string, (tab: string) => void] {
  const names = tabs.join(" ");
  const read = useCallback(() => {
    // The tab names are plain words: a hash that is not one of them, a
    // malformed escape included, is the start tab, never a crash.
    const h = window.location.hash.slice(1);
    return names.split(" ").includes(h) ? h : start;
  }, [names, start]);
  const [tab, setTab] = useState(read);
  useEffect(() => {
    const sync = () => setTab(read());
    window.addEventListener("hashchange", sync);
    window.addEventListener("popstate", sync);
    return () => {
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("popstate", sync);
    };
  }, [read]);
  const set = useCallback(
    (t: string) => {
      const { pathname, search } = window.location;
      window.history.replaceState(
        window.history.state,
        "",
        pathname + search + (t === start ? "" : "#" + t),
      );
      setTab(t);
    },
    [start],
  );
  return [tab, set];
}

function SearchIcon() {
  return (
    <svg
      aria-hidden
      className="pointer-events-none absolute top-[10px] left-2.5 h-[14px] w-[14px] text-faint"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
    >
      <circle cx="7" cy="7" r="4.5" />
      <path d="m10.5 10.5 3 3" />
    </svg>
  );
}
