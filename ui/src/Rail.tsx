// The project and the repository page's frame: two tabs, and a rail beside
// them that holds the search and the sections of both tabs. The rail never
// leaves, so the column never moves sideways on a tab switch, and rail and
// column start on one top edge.
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { S } from "./strings";
import { page } from "./ui";

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
  /** hit: the count is a search's. */
  hit?: boolean;
}

export interface RailGroup {
  title: ReactNode;
  items: RailItem[];
}

/** shortcutFree says a page's key shortcut may act: not while typing,
 *  and not while a dialog (a diagram's full view) holds the page, whose
 *  focus would leave it for the page behind. Option and AltGr pass: e.key
 *  is the character typed, and the Swiss layouts type "[" and "]" with
 *  them (Option+5/6 on a Mac, AltGr, ctrl and alt at once, on Windows);
 *  Cmd and Ctrl alone are the browser's. */
export function shortcutFree(e: KeyboardEvent): boolean {
  if (e.metaKey || (e.ctrlKey && !e.altKey)) return false;
  const t = e.target as HTMLElement | null;
  if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)))
    return false;
  return document.querySelector('[role="dialog"]') === null;
}

/** useShortcut runs onKey when one of keys is pressed on the page while
 *  shortcutFree, a held key once. It listens from the commit that renders
 *  the control it serves on (a layout effect): a key pressed the moment the
 *  control shows is heard, never a paint later. */
export function useShortcut(
  keys: readonly string[],
  onKey: (key: string) => void,
  enabled = true,
) {
  const latest = useRef(onKey);
  useLayoutEffect(() => {
    latest.current = onKey;
  });
  const joined = keys.join("\n");
  useLayoutEffect(() => {
    if (!enabled) return;
    const wanted = joined.split("\n");
    const onDown = (e: KeyboardEvent) => {
      if (!wanted.includes(e.key) || e.repeat || !shortcutFree(e)) return;
      e.preventDefault();
      latest.current(e.key);
    };
    document.addEventListener("keydown", onDown);
    return () => document.removeEventListener("keydown", onDown);
  }, [joined, enabled]);
}

/** Rail is the search and the sections, under head when given (the
 *  repository page's project and its sibling switch, one control, never a
 *  list). A section in the other tab switches the tab, then scrolls to it;
 *  a section link never writes the address, which holds the tab. */
export function Rail({
  head,
  groups,
  tab,
  onTab,
  query,
  onQuery,
  hitLine,
  scroller,
}: {
  head?: ReactNode;
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
  const items = groups.flatMap((g) => g.items);
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

  // "/" finds, as on most sites with a search.
  useShortcut(["/"], () => input.current?.focus());

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
      {head}
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
      {/* Chips narrow keep one line; wide, a long area name wraps. */}
      <div className="flex gap-0.5 whitespace-nowrap min-[1180px]:mt-2.5 min-[1180px]:block min-[1180px]:whitespace-normal">
        {groups.map((g, gi) => (
          <div
            key={gi}
            className="flex gap-0.5 min-[1180px]:mt-4 min-[1180px]:block min-[1180px]:first:mt-0"
          >
            <div className="mb-1 ml-2.5 hidden text-[10.5px] font-medium tracking-[.12em] text-faint uppercase min-[1180px]:block">
              {g.title}
            </div>
            {g.items.map((it) => (
              <RailEntry
                key={it.id}
                item={it}
                tab={tab}
                active={it.id === active}
                onGo={go}
              />
            ))}
          </div>
        ))}
      </div>
    </nav>
  );
}

/** RailEntry is one section's link: a chip narrow, a row with a left
 *  border wide. */
function RailEntry({
  item: it,
  tab,
  active,
  onGo,
}: {
  item: RailItem;
  tab: string;
  active: boolean;
  onGo: (item: RailItem) => void;
}) {
  return (
    <a
      href={"#" + it.id}
      onClick={(e) => {
        e.preventDefault();
        onGo(it);
      }}
      aria-current={active ? "location" : undefined}
      className={
        "flex items-baseline gap-2 rounded-full px-2.5 py-[3px] text-[13.5px] min-[1180px]:rounded-none min-[1180px]:border-l-2 " +
        (active
          ? "bg-active text-ink min-[1180px]:border-accent min-[1180px]:bg-transparent"
          : "min-[1180px]:border-transparent " +
            (it.tab === tab
              ? "text-muted hover:text-ink"
              : "text-[#85837b] hover:text-ink"))
      }
    >
      <span className="min-w-0 min-[1180px]:break-words">{it.label}</span>
      {it.count !== undefined && (
        <span
          className={
            "ml-auto shrink-0 text-[12px] tabular-nums " +
            (it.hit ? "text-ochre" : "text-faint")
          }
        >
          {it.count}
        </span>
      )}
    </a>
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
 *  on. The hash may name a target after the tab (`#description/s12`, a
 *  statement's deep link, a search hit's too): target, "" for none, which
 *  a switch clears; setTarget writes one in place, the page's own control.
 *  landing is the target as the address brought it (a link opened, a
 *  reload, back), "" for one the page set itself: what useJumpTo scrolls
 *  to, so a click on a statement's control never moves the page under the
 *  reader. */
export function useHashTab(
  tabs: readonly string[],
  start: string,
): {
  tab: string;
  setTab: (tab: string) => void;
  target: string;
  setTarget: (id: string) => void;
  landing: string;
} {
  const names = tabs.join(" ");
  const read = useCallback((): At => {
    // The tab names are plain words: a hash that is not one of them, a
    // malformed escape included, is the start tab, never a crash. What
    // follows the first slash is the target, kept only with a known tab.
    const h = window.location.hash.slice(1);
    const cut = h.indexOf("/");
    const tab = cut === -1 ? h : h.slice(0, cut);
    if (!names.split(" ").includes(tab))
      return { tab: start, target: "", landed: true };
    return { tab, target: cut === -1 ? "" : h.slice(cut + 1), landed: true };
  }, [names, start]);
  const [at, setAt] = useState(read);
  // From the commit that renders the tabs (a layout effect), and the
  // address read once more as it attaches: a link followed the moment the
  // page shows is never missed.
  useLayoutEffect(() => {
    // The same address again (a canonical replace's popstate, a
    // hashchange the page wrote itself) is no change: the state object
    // stays, landed as it was, nothing re-renders.
    const sync = () => setAt((prev) => same(prev, read()));
    window.addEventListener("hashchange", sync);
    window.addEventListener("popstate", sync);
    sync();
    return () => {
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("popstate", sync);
    };
  }, [read]);
  const write = (hash: string) => {
    const { pathname, search } = window.location;
    window.history.replaceState(
      window.history.state,
      "",
      pathname + search + hash,
    );
  };
  const setTab = useCallback(
    (t: string) => {
      write(t === start ? "" : "#" + t);
      setAt({ tab: t, target: "", landed: false });
    },
    [start],
  );
  const tab = at.tab;
  const setTarget = useCallback(
    (id: string) => {
      write(anchorHash(tab, id));
      setAt({ tab, target: id, landed: false });
    },
    [tab],
  );
  return {
    tab: at.tab,
    setTab,
    target: at.target,
    setTarget,
    landing: at.landed ? at.target : "",
  };
}

/** At is where the address puts the page: its tab, the target after it,
 *  and whether the address brought both (landed) or the page wrote them. */
interface At {
  tab: string;
  target: string;
  landed: boolean;
}

/** same keeps prev when the address names the same tab and target: how
 *  they came (landed) is prev's to say, the address cannot tell. */
function same(prev: At, next: At): At {
  return prev.tab === next.tab && prev.target === next.target ? prev : next;
}

/** settleMs is how long after a landing the page may still move under
 *  the statement: content above it lands later (a repository's problem
 *  areas from the project fetch, a map or a statement's diagram laid out
 *  once elk has loaded) and pushes it down, so the jump is redone on
 *  every change of the page in that time, unless the reader scrolled,
 *  clicked or typed meanwhile: then the page is theirs. */
const settleMs = 3000;

/** useJumpTo scrolls the element a deep link names into view once it is
 *  on the page: the statements render after the fetch, so the effect runs
 *  on ready, not on mount; then again on each change of the page until it
 *  has settled (settleMs). A reload on the link lands on it again. */
export function useJumpTo(landing: string, ready: boolean) {
  useEffect(() => {
    if (landing === "" || !ready) return;
    const el = document.getElementById(landing);
    if (!el) return;
    el.scrollIntoView();
    let timer = 0;
    const again = () => {
      // Once per frame, however many mutations one layout makes.
      if (timer !== 0) return;
      timer = window.setTimeout(() => {
        timer = 0;
        el.scrollIntoView();
      }, 0);
    };
    const observer = new MutationObserver(again);
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
    });
    const stop = () => {
      observer.disconnect();
      window.clearTimeout(timer);
      for (const ev of taken) window.removeEventListener(ev, stop);
      window.clearTimeout(settled);
    };
    // pointerdown: a click (a rail entry, another statement's control)
    // or a grab of the scrollbar moves the page too, and a rail jump's
    // aria-current change is a mutation that would pull it back.
    const taken = ["wheel", "touchmove", "keydown", "pointerdown"] as const;
    for (const ev of taken) window.addEventListener(ev, stop);
    const settled = window.setTimeout(stop, settleMs);
    return stop;
  }, [landing, ready]);
}

/** anchorHash is a statement's deep link on its page: the tab, then its
 *  id (useHashTab). */
export function anchorHash(tab: string, id: string): string {
  return `#${tab}/${id}`;
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
