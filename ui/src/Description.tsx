import type { ReactNode } from "react";
import type { Description, Kind, Source, Statement } from "./api";
import { Diagram } from "./Diagram";
import { day, plain, shortSHA } from "./format";
import { anchorHash, type RailItem } from "./Rail";
import { notePathFor, plainClick } from "./routing";
import { S } from "./strings";
import {
  Chip,
  Doc,
  Empty,
  Inline,
  Marked,
  Summary,
  eyebrow,
  kindLabelStyle,
  matchesQuery,
  mono,
  useCopied,
} from "./ui";

/** DESCRIPTION is the description tab's hash on both pages, the first
 *  part of a statement's deep link (anchorHash): a note page links into
 *  it too. */
export const DESCRIPTION = "description";

/** kindLabel names one statement's kind, as a label over its name: the
 *  project page's entities are the shared ones and say so. A kind the UI
 *  does not know shows as stored. */
export function kindLabel(kind: Kind, project: boolean): string {
  return (
    (project ? S.projectStatementKind[kind] : undefined) ??
    S.statementKind[kind] ??
    kind
  );
}

/** The ids of a description's sections, the rail's anchors. */
export const summaryId = "summary";
/** areaId is an area's chapter, by the area's place among the page's
 *  areas (areaNames), so a search leaves it where it was. */
export const areaId = (i: number) => `area-${i}`;
/** statementId is a statement's block, its deep link's target and a search
 *  hit's: the bare id (`s12`), which no section id ("summary", "area-N")
 *  meets. */
export const statementId = (id: string) => id;

/** areaOf is a statement's area, "" for none. An area named as the
 *  summary (any case, trimmed) reads as none: the rail would name
 *  Überblick twice, the summary and the chapter. A rule here, not in the
 *  prompt, as it holds for every stored page at once. */
const areaOf = (s: Statement) => {
  const area = s.area ?? "";
  return area.trim().toLowerCase() === S.rail.summary.toLowerCase() ? "" : area;
};

/** areaNames is a page's areas, each once, in the page's order, the one
 *  of statements in no area ("") among them: every statement stands in a
 *  chapter. */
export function areaNames(all: Statement[]): string[] {
  return [...new Set(all.map(areaOf))];
}

/** areaTitle is an area's chapter title: the statements in no area are
 *  "Weiteres" beside named areas and plainly the statements on a page
 *  that has none, one not laid out yet. */
export function areaTitle(area: string, names: string[]): string {
  if (area !== "") return area;
  return names.length === 1 ? S.areas.all : S.areas.otherArea;
}

/** plainOf is a statement's text as the search reads it (plain), parsed
 *  once per statement, not twice per keystroke (rail and list). */
const plainCache = new WeakMap<Statement, string>();
function plainOf(s: Statement): string {
  let p = plainCache.get(s);
  if (p === undefined) {
    p = plain(s.text);
    plainCache.set(s, p);
  }
  return p;
}

/** statementMatches is the search's test on a statement: its name, its
 *  text, its files and its area. */
export function statementMatches(s: Statement, query: string): boolean {
  return matchesQuery(query, s.name, plainOf(s), s.area ?? "", ...s.files);
}

/** shownStatements is what a description shows of all: what the search
 *  matches and, with kinds (a question chip), only those kinds. */
export function shownStatements(
  all: Statement[],
  query: string,
  kinds?: readonly Kind[],
): Statement[] {
  return all.filter(
    (s) =>
      statementMatches(s, query) &&
      (kinds === undefined || kinds.includes(s.kind)),
  );
}

/** descriptionRail is the rail's part of a description: the summary and
 *  each area with its count, the shown statements' count while a search
 *  or a chip (kinds) narrows them, and the search's line under it. lead
 *  is the sections before the summary (a map), extra the ones after it
 *  (problem areas, dependencies). An area left without a shown statement
 *  has no chapter to point at and is left out. */
export function descriptionRail(
  description: Description | null,
  query: string,
  tab: string,
  lead: RailItem[] = [],
  extra: RailItem[] = [],
  kinds?: readonly Kind[],
): { items: RailItem[]; hitLine: string } {
  const statements = description?.statements ?? [];
  const searching = query.trim() !== "";
  const narrowed = searching || kinds !== undefined;
  const shown = shownStatements(statements, query, kinds);
  const names = areaNames(statements);
  const items: RailItem[] = [
    ...lead,
    { id: summaryId, label: S.rail.summary, tab },
    ...extra,
    ...names.flatMap((area, i): RailItem[] => {
      const n = shown.filter((s) => areaOf(s) === area).length;
      return n === 0
        ? []
        : [
            {
              id: areaId(i),
              label: plain(areaTitle(area, names)),
              tab,
              count: n,
              hit: narrowed,
            },
          ];
    }),
  ];
  // The search's line, counting what the page shows: a chip pressed
  // narrows the hits and what they are out of.
  const pool = shownStatements(statements, "", kinds).length;
  const hitLine = !searching
    ? ""
    : shown.length === 0
      ? S.rail.noHits
      : S.rail.hits(shown.length, pool, new Set(shown.map(areaOf)).size);
  return { items, hitLine };
}

/** DescriptionView is a page's description: the summary laid out as a
 *  statement's text is (sub-headings, lists, bold), then the statements
 *  in chapters by area, a person's way to structure documentation: all a
 *  part of the system holds in one place, whatever kind (user, 2026-11-02:
 *  "a human would not structure a documentation like that", of a page
 *  split by kind first). Each statement is a block with its kind as a
 *  label, its name, its text laid out (Doc), its files and its sources as
 *  citation chips. pageRepo is the repository a repository page shows,
 *  whose slug its chips leave out; without it the page is a project's.
 *  query keeps the statements it matches and sets the matches off; the
 *  summary stays, marked. kinds (a question chip) keeps those kinds
 *  alone, the search on top. lead comes first (a map), afterSummary
 *  under the summary (a repository's problem areas), tail after the
 *  chapters (the facts read from the code). tab is the tab the view
 *  stands on, for each statement's own address (anchorHash); target the
 *  statement the address names, set off; onTarget makes one the target,
 *  the page writing its address. */
export function DescriptionView({
  description,
  emptyHint,
  pageRepo,
  query = "",
  kinds,
  lead,
  afterSummary,
  tail,
  tab,
  target = "",
  onTarget,
}: {
  description: Description | null;
  emptyHint: string;
  pageRepo?: string;
  query?: string;
  kinds?: readonly Kind[];
  lead?: ReactNode;
  afterSummary?: ReactNode;
  tail?: ReactNode;
  tab?: string;
  target?: string;
  onTarget?: (id: string) => void;
}) {
  if (description === null) {
    return (
      <>
        {lead}
        <section id={summaryId} className="scroll-mt-16">
          <div className={eyebrow}>{S.rail.summary}</div>
          <Empty>
            {S.project.noDescription} {emptyHint}
          </Empty>
        </section>
        {afterSummary}
        {tail}
      </>
    );
  }
  const all = description.statements;
  const shown = shownStatements(all, query, kinds);
  const names = areaNames(all);
  const narrowed = query.trim() !== "" || kinds !== undefined;
  return (
    <>
      {lead}
      <section id={summaryId} className="scroll-mt-16">
        <div className={eyebrow}>{S.rail.summary}</div>
        <Summary text={description.summary} query={query} layout />
      </section>
      {afterSummary}
      {all.length === 0 ? (
        <Empty>{S.areas.none}</Empty>
      ) : (
        narrowed && shown.length === 0 && <Empty>{S.rail.noHits}</Empty>
      )}
      {areaGroups(all, shown).map((g) => (
        <section
          key={g.area}
          // The rail's anchor.
          id={areaId(names.indexOf(g.area))}
          className="scroll-mt-16"
        >
          <h3 className={areaHeading}>
            <Inline text={areaTitle(g.area, names)} query={query} />
          </h3>
          {g.statements.map((s) => (
            <StatementBlock
              key={s.id}
              statement={s}
              pageRepo={pageRepo}
              query={query}
              anchor={tab === undefined ? undefined : anchorHash(tab, s.id)}
              targeted={s.id === target}
              onAnchor={onTarget}
            />
          ))}
        </section>
      ))}
      {tail}
    </>
  );
}

/** AnchorLink is a statement's own address: a link a reader can copy or
 *  open in a new tab as any, and a plain click on it picks the statement
 *  (onPick: the page writes the address in place, as a tab switch does,
 *  so back leaves the page) and copies the address, saying so for a
 *  moment. Always there, quiet: a touch screen has no hover to show it
 *  on. Named after its statement, so a screen reader's list of links
 *  tells them apart. */
function AnchorLink({
  hash,
  name,
  onPick,
}: {
  hash: string;
  name: string;
  onPick: () => void;
}) {
  const [done, copy] = useCopied();
  return (
    <a
      href={hash}
      aria-label={S.copy.statementLink(plain(name))}
      title={S.copy.statementLinkHint}
      onClick={(e) => {
        if (!plainClick(e)) return;
        e.preventDefault();
        onPick();
        const { origin, pathname, search } = window.location;
        copy(origin + pathname + search + hash);
      }}
      className="float-right px-1 font-mono text-[11px] text-faint hover:text-ink"
    >
      {done ? S.copy.done : S.copy.statementAnchor}
    </a>
  );
}

/** areaHeading is an area's chapter title: under the page's h2, over its
 *  statements' h4 names; a reading path's step title too. */
export const areaHeading =
  "mt-9 mb-1 font-serif text-[21px] font-medium leading-tight tracking-tight text-ink";

/** areaGroups is the shown statements in their areas, each area once,
 *  where its first statement stands: as the backend lays a page out
 *  (baseline.Arrange), so a page not laid out yet (an area, another, the
 *  first again) reads as one that is, and an area has one chapter for the
 *  rail to point at. Inside an area the stored order, the outline's:
 *  overview before detail, whatever the kind. An area the search leaves
 *  without a hit is not shown. */
export function areaGroups(
  all: Statement[],
  shown: Statement[],
): { area: string; statements: Statement[] }[] {
  // In the order of all the page's areas, as the rail lists them, never
  // the order the search's hits happen to give.
  return areaNames(all).flatMap((area) => {
    const statements = shown.filter((s) => areaOf(s) === area);
    return statements.length === 0 ? [] : [{ area, statements }];
  });
}

/** sourceKey names a source once, as the backend does: a PR by its key, a
 *  baseline by its repository and commit. A source without a kind is a
 *  PR. */
function sourceKey(src: Source): string {
  return src.kind === "baseline"
    ? `b:${src.repo ?? ""}@${src.commit}`
    : `p:${src.pr_key}`;
}

/** SourceChip is a statement's source: a PR links to its note; a baseline
 *  run's commit has no page and is a quiet chip, naming its repository
 *  when that is not the page's own. */
export function SourceChip({
  source: src,
  pageRepo,
}: {
  source: Source;
  pageRepo?: string;
}) {
  if (src.kind === "baseline") {
    const sha = shortSHA(src.commit ?? "");
    const repo = src.repo !== pageRepo ? src.repo : undefined;
    return (
      <Chip
        to={null}
        quiet
        title={S.baseline.chipTitle(
          sha,
          src.committed_at ? day(src.committed_at) : "",
        )}
      >
        {S.baseline.chip(sha, repo)}
      </Chip>
    );
  }
  const key = src.pr_key ?? "";
  return (
    <Chip
      to={notePathFor(key)}
      title={`${src.ticket_key ?? ""} ${src.merged_at ? day(src.merged_at) : ""}`.trim()}
    >
      {key}
    </Chip>
  );
}

/** RevisionSources is what wrote a revision: its run's commit, its note's
 *  PR, or every PR a call of several notes took, a chip each. */
export function RevisionSources({
  kind,
  prKey,
  prKeys,
  commit,
}: {
  kind?: "pr" | "baseline";
  prKey: string;
  prKeys?: string[];
  commit?: string;
}) {
  if (kind !== "baseline" && prKeys && prKeys.length > 1) {
    return (
      <span className="inline-flex flex-wrap gap-1">
        {prKeys.map((k) => (
          <SourceChip key={k} source={{ kind: "pr", pr_key: k }} />
        ))}
      </span>
    );
  }
  return <SourceChip source={{ kind, pr_key: prKey, commit }} />;
}

/** checkedNews reports whether a statement's checked commit says more than
 *  its sources: a run that wrote the statement at that commit already
 *  shows it as a Basis chip. */
function checkedNews(s: Statement): boolean {
  if (s.checked === undefined || s.checked === "") return false;
  return !s.sources.some(
    (src) => src.kind === "baseline" && src.commit === s.checked,
  );
}

/** StatementBlock is one statement: documentation, not a line. Its id is
 *  the statement's, the deep link's anchor; anchor is that link, shown as
 *  a control beside the kind that makes the statement the target
 *  (onAnchor, the page writing the address) and copies the link;
 *  targeted sets the block off as the one the address names. */
export function StatementBlock({
  statement: s,
  pageRepo,
  query = "",
  anchor,
  targeted = false,
  onAnchor,
}: {
  statement: Statement;
  pageRepo?: string;
  query?: string;
  anchor?: string;
  targeted?: boolean;
  onAnchor?: (id: string) => void;
}) {
  return (
    <article
      id={s.id}
      data-targeted={targeted || undefined}
      className={
        "scroll-mt-16 border-t border-border-soft py-3 first-of-type:border-t-0 " +
        (targeted ? "-mx-3 rounded-ui bg-active px-3" : "")
      }
    >
      {anchor !== undefined && (
        <AnchorLink
          hash={anchor}
          name={s.name}
          onPick={() => onAnchor?.(s.id)}
        />
      )}
      <div className={kindLabelStyle}>
        {kindLabel(s.kind, pageRepo === undefined)}
      </div>
      <h4 className="font-semibold text-ink">
        <Inline text={s.name} query={query} />
      </h4>
      <Doc
        text={s.text}
        query={query}
        className="mt-1 mb-1.5 font-serif text-[15.5px] leading-[1.55] text-ink-dim"
      />
      {s.diagram && (
        <Diagram ir={s.diagram} title={S.diagram.titles[s.diagram.kind]} />
      )}
      <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-muted">
        {s.files.map((f, i) => (
          <span key={f}>
            <span className={mono}>
              <Marked text={f} query={query} />
            </span>
            {i < s.files.length - 1 && ","}
          </span>
        ))}
        {s.files.length > 0 && s.sources.length > 0 && (
          <span aria-hidden className="mx-1 text-faint">
            {S.common.sep}
          </span>
        )}
        {s.sources.map((src) => (
          <SourceChip key={sourceKey(src)} source={src} pageRepo={pageRepo} />
        ))}
        {checkedNews(s) && (
          <span
            className={mono + " text-faint"}
            title={S.baseline.checkedTitle(shortSHA(s.checked ?? ""))}
          >
            {S.baseline.checked(shortSHA(s.checked ?? ""))}
          </span>
        )}
      </div>
    </article>
  );
}
