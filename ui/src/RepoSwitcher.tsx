// The repository page's rail head: the project, one line, and the switch
// to its other repositories, one control. The project's repositories are
// never a list in the rail: with a dozen of them and the repository's own
// sections the rail ran to some thirty-five rows.
import {
  Fragment,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import type { Repo } from "./api";
import { shortcutFree } from "./Rail";
import { navigateTo, projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { Link, Teaser, matchesQuery, teaserLines } from "./ui";

/** breakable is a slug that breaks after a "-", ".", "_" or "/" only,
 *  never inside a word unless a word is wider than the line. */
export function breakable(slug: string): ReactNode {
  return slug.split(/(?<=[-._/])/).map((part, i) => (
    <Fragment key={i}>
      {i > 0 && <wbr />}
      {part}
    </Fragment>
  ));
}

const step =
  "flex h-5 w-5 items-center justify-center rounded border border-border font-mono text-[12px] leading-none text-muted hover:bg-active hover:text-ink";

/** RepoSwitcher heads the rail: wide, the project's line with ‹ › and the
 *  position, the name under it on up to three lines; narrow, one chip
 *  pinned left in the rail's row while the sections scroll under it. The
 *  name opens the project's repositories with their teasers, filterable;
 *  "[" and "]" step as ‹ › do, round the end. A project's only listed
 *  repository has no switch, only the project's line. Keyed by slug: a
 *  switch remounts it, closed. */
export function RepoSwitcher({
  projectKey,
  repos,
  slug,
}: {
  projectKey: string;
  repos: Repo[];
  slug: string;
}) {
  const [at, setAt] = useState<{
    left: number;
    top: number;
    from: RefObject<HTMLButtonElement | null>;
  } | null>(null);
  const wide = useRef<HTMLButtonElement>(null);
  const narrow = useRef<HTMLButtonElement>(null);
  const i = repos.findIndex((r) => r.slug === slug);
  const many = repos.length > 1 && i >= 0;
  const prev = many ? repos[(i - 1 + repos.length) % repos.length]!.slug : "";
  const next = many ? repos[(i + 1) % repos.length]!.slug : "";

  useEffect(() => {
    if (!many) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.key !== "[" && e.key !== "]") || !shortcutFree(e)) return;
      e.preventDefault();
      navigateTo(repoPath(projectKey, e.key === "[" ? prev : next));
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [many, projectKey, prev, next]);

  function toggle(from: RefObject<HTMLButtonElement | null>) {
    const b = from.current;
    if (at !== null || !b) {
      setAt(null);
      return;
    }
    const r = b.getBoundingClientRect();
    const width = Math.min(listWidth, window.innerWidth - 16);
    setAt({
      left: Math.max(8, Math.min(r.left, window.innerWidth - width - 8)),
      top: r.bottom + 6,
      from,
    });
  }

  const project = (
    <Link
      to={projectPath(projectKey)}
      className="min-w-0 truncate text-muted hover:text-ink"
    >
      <span aria-hidden>↑ </span>
      {S.rail.project(projectKey)}
    </Link>
  );

  return (
    <>
      <div className="mb-3 hidden min-[1180px]:block">
        <div className="mb-1.5 flex items-center gap-2 px-0.5 text-[13px] whitespace-nowrap">
          {project}
          {many && (
            <span className="ml-auto flex shrink-0 items-center gap-1.5 text-[12px] text-faint tabular-nums">
              <Link
                to={repoPath(projectKey, prev)}
                aria-label={S.rail.prev(prev)}
                title={prev}
                className={step}
              >
                ‹
              </Link>
              {S.rail.position(i + 1, repos.length)}
              <Link
                to={repoPath(projectKey, next)}
                aria-label={S.rail.next(next)}
                title={next}
                className={step}
              >
                ›
              </Link>
            </span>
          )}
        </div>
        {many && (
          <button
            ref={wide}
            type="button"
            aria-haspopup="dialog"
            aria-expanded={at !== null && at.from === wide}
            title={slug}
            onClick={() => toggle(wide)}
            className="relative block w-full rounded-ui-sm border border-border bg-panel py-1.5 pr-6 pl-2.5 text-left font-mono text-[12px] leading-[1.4] text-ink hover:border-elevated-border"
          >
            {/* Three lines hold a slug of some 60 characters, broken at its
                separators; past that the title has it whole. */}
            <span className="line-clamp-3 break-words">{breakable(slug)}</span>
            <span
              aria-hidden
              className="absolute top-[7px] right-2.5 text-[10px] text-faint"
            >
              ▾
            </span>
          </button>
        )}
      </div>
      {/* Narrow: pinned left, its own background reaching over the row's
          padding, so the chips scrolled under it do not show beside it. */}
      <div className="sticky -left-4 z-10 -ml-4 flex shrink-0 items-center self-stretch bg-bg pr-1 pl-4 sm:-left-6 sm:-ml-6 sm:pl-6 lg:-left-10 lg:-ml-10 lg:pl-10 min-[1180px]:hidden">
        {many ? (
          <button
            ref={narrow}
            type="button"
            aria-haspopup="dialog"
            aria-expanded={at !== null && at.from === narrow}
            title={slug}
            onClick={() => toggle(narrow)}
            className="flex max-w-[55vw] items-baseline gap-1.5 rounded-full border border-border bg-panel px-2.5 py-[3px] font-mono text-[12.5px] text-ink"
          >
            <span className="truncate">
              <span className="text-muted">{projectKey} / </span>
              {slug}
            </span>
            <span aria-hidden className="text-[10px] text-faint">
              ▾
            </span>
          </button>
        ) : (
          <div className="rounded-full border border-border px-2.5 py-[3px] text-[13px]">
            {project}
          </div>
        )}
      </div>
      {at !== null &&
        createPortal(
          <RepoList
            projectKey={projectKey}
            repos={repos}
            slug={slug}
            left={at.left}
            top={at.top}
            onClose={(focus) => {
              setAt(null);
              if (focus) at.from.current?.focus();
            }}
            buttons={[wide, narrow]}
          />,
          document.body,
        )}
    </>
  );
}

const listWidth = 360;

/** RepoList is the project's repositories under the switch, fixed to the
 *  page (the rail scrolls and would clip it), each with its teaser. Enter
 *  in the filter opens the first match; Escape, a press outside or a
 *  resized window closes it. */
function RepoList({
  projectKey,
  repos,
  slug,
  left,
  top,
  onClose,
  buttons,
}: {
  projectKey: string;
  repos: Repo[];
  slug: string;
  left: number;
  top: number;
  onClose: (focus: boolean) => void;
  buttons: RefObject<HTMLButtonElement | null>[];
}) {
  const [query, setQuery] = useState("");
  const box = useRef<HTMLDivElement>(null);
  // The listeners read the latest props instead of being bound again on
  // every render.
  const latest = useRef({ onClose, buttons });
  useEffect(() => {
    latest.current = { onClose, buttons };
  });
  const close = (focus: boolean) => latest.current.onClose(focus);
  const shown = repos.filter((r) => matchesQuery(query, r.slug, r.summary));

  useEffect(() => {
    // A long list opens on the repository the page shows. jsdom has no
    // scrollIntoView.
    box.current
      ?.querySelector<HTMLElement>('[aria-current="page"]')
      ?.scrollIntoView?.({ block: "nearest" });
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (box.current?.contains(t)) return;
      // The switch's own press toggles; closing here would reopen it.
      if (latest.current.buttons.some((b) => b.current?.contains(t))) return;
      latest.current.onClose(false);
    };
    const onResize = () => latest.current.onClose(false);
    document.addEventListener("pointerdown", onDown);
    window.addEventListener("resize", onResize);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      window.removeEventListener("resize", onResize);
    };
  }, []);

  return (
    <div
      ref={box}
      role="dialog"
      aria-label={S.rail.switchRepo}
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          close(true);
        }
      }}
      style={{ left, top, width: Math.min(listWidth, window.innerWidth - 16) }}
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
            navigateTo(repoPath(projectKey, first.slug));
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
            <li key={r.slug}>
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
