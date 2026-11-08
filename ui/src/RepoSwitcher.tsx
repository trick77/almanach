// The repository page's rail head: the project, one line, and the switch
// to its other repositories, one control. The project's repositories are
// never a list in the rail: with a dozen of them and the repository's own
// sections the rail ran to some thirty-five rows.
import {
  Fragment,
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import type { Repo } from "./api";
import { useEscape } from "./dialog";
import { plain } from "./format";
import { useShortcut } from "./Rail";
import { navigateTo, projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { Link, Teaser, matchesQuery, teaserLines } from "./ui";

/** breakable is a slug that breaks after a "-", ".", "_" or "/" only,
 *  never inside a word unless a word is wider than the line. No regex
 *  lookbehind: Safari before 16.4 cannot parse one, and the bundle with it
 *  would not load at all. */
export function breakable(slug: string): ReactNode {
  const parts: string[] = [];
  for (const piece of slug.split(/([-._/])/)) {
    if (/^[-._/]$/.test(piece) && parts.length > 0)
      parts[parts.length - 1] += piece;
    else if (piece !== "") parts.push(piece);
  }
  return parts.map((part, i) => (
    <Fragment key={i}>
      {i > 0 && <wbr />}
      {part}
    </Fragment>
  ));
}

const step =
  "flex h-5 w-5 items-center justify-center rounded border border-border font-mono text-[12px] leading-none text-muted hover:bg-active hover:text-ink";

/** The switch's buttons, wide and narrow, by this attribute: a press on
 *  one toggles the list, so the list's press-outside leaves it alone. */
const switchAttr = "data-repo-switch";

/** RepoSwitcher heads the rail: wide, the project's line with ‹ › and the
 *  position, the name under it on up to three lines; narrow, one chip
 *  pinned left in the rail's row while the sections scroll under it. The
 *  name opens the project's repositories with their teasers, filterable;
 *  "[" and "]" step as ‹ › do, round the end. A repository the project
 *  does not list (no longer a source) still opens the list, without
 *  steps; a project's only repository has no switch, only the project's
 *  line. Keyed by slug: a switch remounts it, closed. */
export function RepoSwitcher({
  projectKey,
  repos,
  slug,
}: {
  projectKey: string;
  repos: Repo[];
  slug: string;
}) {
  // The button that opened the list: the list stands under it, and
  // Escape or picking the page's own repository gives it the focus back.
  // Not every close does: focus tabbed out of the list stays where it went.
  const [opener, setOpener] = useState<HTMLButtonElement | null>(null);
  const close = useCallback(
    (back: boolean) => {
      setOpener(null);
      if (back) opener?.focus();
    },
    [opener],
  );
  const i = repos.findIndex((r) => r.slug === slug);
  const list = repos.some((r) => r.slug !== slug);
  const steps = i >= 0 && repos.length > 1;
  const prev = steps ? repos[(i - 1 + repos.length) % repos.length]!.slug : "";
  const next = steps ? repos[(i + 1) % repos.length]!.slug : "";

  // "[" and "]" step as ‹ and › do; a held key once, as each repeat would
  // push a page and fetch it (useShortcut).
  useShortcut(
    ["[", "]"],
    (key) => navigateTo(repoPath(projectKey, key === "[" ? prev : next)),
    steps,
  );

  function toggle(e: MouseEvent<HTMLButtonElement>) {
    setOpener(opener === null ? e.currentTarget : null);
  }

  const project = (
    <Link
      to={projectPath(projectKey)}
      className="min-w-0 truncate text-muted hover:text-ink"
    >
      <span aria-hidden>{S.rail.up} </span>
      {S.rail.project(projectKey)}
    </Link>
  );
  const switchProps = {
    type: "button" as const,
    [switchAttr]: "",
    "aria-haspopup": "dialog" as const,
    "aria-expanded": opener !== null,
    title: slug,
    onClick: toggle,
  };

  return (
    <>
      <div className="mb-3 hidden min-[1180px]:block">
        <div className="mb-1.5 flex items-center gap-2 px-0.5 text-[13px] whitespace-nowrap">
          {project}
          {steps && (
            <span className="ml-auto flex shrink-0 items-center gap-1.5 text-[12px] text-faint tabular-nums">
              <Link
                to={repoPath(projectKey, prev)}
                aria-label={S.rail.prev(prev)}
                title={prev}
                className={step}
              >
                {S.rail.prevStep}
              </Link>
              {S.rail.position(i + 1, repos.length)}
              <Link
                to={repoPath(projectKey, next)}
                aria-label={S.rail.next(next)}
                title={next}
                className={step}
              >
                {S.rail.nextStep}
              </Link>
            </span>
          )}
        </div>
        {list && (
          <button
            {...switchProps}
            className="relative block w-full rounded-ui-sm border border-border bg-panel py-1.5 pr-6 pl-2.5 text-left font-mono text-[12px] leading-[1.4] text-ink hover:border-elevated-border"
          >
            {/* Three lines hold a slug of some 60 characters, broken at its
                separators; past that the title has it whole. */}
            <span className="line-clamp-3 break-words">{breakable(slug)}</span>
            <span
              aria-hidden
              className="absolute top-[7px] right-2.5 text-[10px] text-faint"
            >
              {S.rail.opens}
            </span>
          </button>
        )}
      </div>
      {/* Narrow: pinned left, its own background reaching over the row's
          padding and height, so the chips scrolled under it do not show
          beside it. */}
      <div className="sticky -left-4 z-10 -ml-4 flex shrink-0 items-center self-stretch bg-bg pr-1 pl-4 sm:-left-6 sm:-ml-6 sm:pl-6 lg:-left-10 lg:-ml-10 lg:pl-10 min-[1180px]:hidden">
        {list ? (
          <button
            {...switchProps}
            className="flex max-w-[55vw] items-baseline gap-1.5 rounded-full border border-border bg-panel px-2.5 py-[3px] font-mono text-[12.5px] text-ink"
          >
            <span className="truncate">
              <span className="text-muted">{S.rail.chip(projectKey)}</span>
              {slug}
            </span>
            <span aria-hidden className="text-[10px] text-faint">
              {S.rail.opens}
            </span>
          </button>
        ) : (
          <div className="rounded-full border border-border px-2.5 py-[3px] text-[13px]">
            {project}
          </div>
        )}
      </div>
      {opener !== null &&
        createPortal(
          <RepoList
            projectKey={projectKey}
            repos={repos}
            slug={slug}
            anchor={opener}
            onClose={close}
          />,
          document.body,
        )}
    </>
  );
}

/** placeUnder is the list's place under its switch, kept on the screen,
 *  its width computed once for both. */
function placeUnder(anchor: HTMLElement) {
  const r = anchor.getBoundingClientRect();
  const width = Math.min(360, window.innerWidth - 16);
  return {
    left: Math.max(8, Math.min(r.left, window.innerWidth - width - 8)),
    top: r.bottom + 6,
    width,
  };
}

/** RepoList is the project's repositories under the switch, fixed to the
 *  page (the rail scrolls and would clip it), each with its teaser. Enter
 *  in the filter opens the first match. A scroll outside it or a resize
 *  moves it with its switch (a phone's keyboard, opening for the filter,
 *  scrolls or resizes the page); it closes once the switch is hidden (the
 *  other width's), on Escape, on a press outside, when the focus leaves it
 *  (Tab past its end: it is no modal, and while it stands the page's
 *  shortcuts hold off), and on picking the page's own repository. */
function RepoList({
  projectKey,
  repos,
  slug,
  anchor,
  onClose,
}: {
  projectKey: string;
  repos: Repo[];
  slug: string;
  anchor: HTMLElement;
  /** back: give the focus back to the switch. */
  onClose: (back: boolean) => void;
}) {
  const [query, setQuery] = useState("");
  const [place, setPlace] = useState(() => placeUnder(anchor));
  const box = useRef<HTMLDivElement>(null);
  useEscape(useCallback(() => onClose(true), [onClose]));
  const shown = repos.filter((r) =>
    matchesQuery(query, r.slug, plain(r.summary)),
  );

  useEffect(() => {
    // A long list opens on the repository the page shows: its own scroll
    // only, as scrollIntoView could scroll the page too, which closes it.
    const ul = box.current?.querySelector("ul");
    const cur = ul?.querySelector<HTMLElement>('[aria-current="page"]');
    if (ul && cur)
      ul.scrollTop = cur.offsetTop - ul.offsetTop - ul.clientHeight / 2;
    const outside = (t: EventTarget | null) =>
      !(t instanceof Node && box.current?.contains(t));
    const onDown = (e: PointerEvent) => {
      if (!outside(e.target)) return;
      if (e.target instanceof Element && e.target.closest(`[${switchAttr}]`))
        return;
      onClose(false);
    };
    const follow = () => {
      if (anchor.getClientRects().length === 0) onClose(false);
      else setPlace(placeUnder(anchor));
    };
    const onScroll = (e: Event) => {
      if (outside(e.target)) follow();
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", follow);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", follow);
    };
  }, [anchor, onClose]);

  // The page's own repository is where the reader is: picking it closes,
  // the focus back on the switch rather than lost to the page's top.
  function go(to: string) {
    if (to === slug) onClose(true);
    else navigateTo(repoPath(projectKey, to));
  }

  return (
    <div
      ref={box}
      role="dialog"
      aria-label={S.rail.switchRepo}
      onBlur={(e) => {
        // Focus gone elsewhere by keyboard; a press outside is onDown's,
        // and a press on a button no browser focuses has no target here.
        const to = e.relatedTarget;
        if (to instanceof Node && !box.current?.contains(to)) onClose(false);
      }}
      style={place}
      className="fixed z-50 rounded-ui border border-elevated-border bg-elevated p-1.5 shadow-panel"
    >
      <input
        type="search"
        autoFocus
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          const first = shown[0];
          if (e.key === "Enter" && first) {
            e.preventDefault();
            go(first.slug);
          }
        }}
        placeholder={S.rail.findRepo}
        aria-label={S.rail.findRepo}
        className="mb-1 h-[30px] w-full rounded-ui-sm border border-elevated-border bg-bg px-2 text-[13px] text-ink outline-none placeholder:text-faint"
      />
      <ul className="m-0 max-h-[min(60dvh,480px)] list-none overflow-y-auto p-0">
        {shown.map((r) => {
          const teaser = teaserLines(r.summary);
          const cur = r.slug === slug;
          return (
            // The link navigates itself; on the page's own repository it
            // would not, and the list closes instead.
            <li key={r.slug} onClick={cur ? () => onClose(true) : undefined}>
              <Link
                to={repoPath(projectKey, r.slug)}
                aria-current={cur ? "page" : undefined}
                className="block rounded-ui-sm px-2 py-1.5 hover:bg-elevated-hover"
              >
                <span
                  className={
                    "block font-mono text-[12.5px] break-words " +
                    (cur ? "text-accent-strong" : "text-ink")
                  }
                >
                  {breakable(r.slug)}
                </span>
                {teaser.length > 0 && (
                  <span className="mt-0.5 line-clamp-2 text-[12px] leading-snug text-muted">
                    <Teaser lines={teaser} />
                  </span>
                )}
              </Link>
            </li>
          );
        })}
        {shown.length === 0 && (
          <li className="px-2 py-1.5 text-[12.5px] text-faint">
            {S.rail.noRepo}
          </li>
        )}
      </ul>
    </div>
  );
}
