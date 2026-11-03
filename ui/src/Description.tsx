import type { ReactNode } from "react";
import type { Description, Kind, Source, Statement } from "./api";
import { Diagram } from "./Diagram";
import { day, plain, shortSHA } from "./format";
import type { RailItem } from "./Rail";
import { notePathFor } from "./routing";
import { S } from "./strings";
import {
  Chip,
  Doc,
  Empty,
  Inline,
  Marked,
  Summary,
  eyebrow,
  matchesQuery,
  mono,
} from "./ui";

/** The kinds in the order the Markdown export has them: what must not
 *  break and why first (a public interface is what others rely on not
 *  breaking), the parts after. On the page it orders only the statements
 *  no area holds: an area's own are in the order its outline gave them,
 *  overview before detail, whatever their kind. */
const KINDS: Kind[] = [
  "invariant",
  "interface",
  "rule",
  "decision",
  "pitfall",
  "extension",
  "component",
  "entity",
];

/** kindLabel names one statement's kind, as a label over its name: the
 *  project page's entities are the shared ones and say so. */
export function kindLabel(kind: Kind, project: boolean): string {
  return (project ? S.projectKind[kind] : undefined) ?? S.kind[kind];
}

/** The ids of a description's sections, the rail's anchors. */
export const summaryId = "summary";
/** areaId is an area's chapter, by the area's place among the page's
 *  areas (areaNames), so a search leaves it where it was. */
export const areaId = (i: number) => `area-${i}`;

const areaOf = (s: Statement) => s.area ?? "";

/** areaNames is a page's areas, each once, in the page's order: none when
 *  its statements stand in one area or none, which have no heading. */
export function areaNames(all: Statement[]): string[] {
  const names = [...new Set(all.map(areaOf))];
  return names.length < 2 ? [] : names;
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

/** descriptionRail is the rail's part of a description: the summary and
 *  each area with its count, the search's hit count while one runs, and
 *  the line under the search. lead is the sections before the summary (a
 *  map), extra the ones after it (problem areas, dependencies). An area
 *  the search leaves without a hit has no chapter to point at and is left
 *  out. */
export function descriptionRail(
  description: Description | null,
  query: string,
  tab: string,
  lead: RailItem[] = [],
  extra: RailItem[] = [],
): { items: RailItem[]; hitLine: string } {
  const statements = description?.statements ?? [];
  const searching = query.trim() !== "";
  const hits = statements.filter((s) => statementMatches(s, query));
  const items: RailItem[] = [
    ...lead,
    { id: summaryId, label: S.rail.summary, tab },
    ...extra,
    ...areaNames(statements).flatMap((area, i): RailItem[] => {
      const n = hits.filter((s) => areaOf(s) === area).length;
      return n === 0
        ? []
        : [
            {
              id: areaId(i),
              label: area === "" ? S.areas.otherArea : plain(area),
              tab,
              count: n,
              hit: searching,
            },
          ];
    }),
  ];
  const hitLine = !searching
    ? ""
    : hits.length === 0
      ? S.rail.noHits
      : S.rail.hits(
          hits.length,
          statements.length,
          new Set(hits.map(areaOf)).size,
        );
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
 *  summary stays, marked. lead comes first (a map), afterSummary under
 *  the summary (a repository's problem areas). */
export function DescriptionView({
  description,
  emptyHint,
  pageRepo,
  query = "",
  lead,
  afterSummary,
}: {
  description: Description | null;
  emptyHint: string;
  pageRepo?: string;
  query?: string;
  lead?: ReactNode;
  afterSummary?: ReactNode;
}) {
  const project = pageRepo === undefined;
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
      </>
    );
  }
  const all = description.statements;
  const shown = all.filter((s) => statementMatches(s, query));
  const names = areaNames(all);
  return (
    <>
      {lead}
      <section id={summaryId} className="scroll-mt-16">
        <div className={eyebrow}>{S.rail.summary}</div>
        <Summary text={description.summary} query={query} layout />
      </section>
      {afterSummary}
      {query.trim() !== "" && shown.length === 0 && (
        <Empty>{S.rail.noHits}</Empty>
      )}
      {areaGroups(all, shown).map((g) => (
        <section
          key={g.area ?? ""}
          // The rail's anchor.
          id={g.area === null ? undefined : areaId(names.indexOf(g.area))}
          className="scroll-mt-16"
        >
          {g.area !== null && (
            <h3 className={areaHeading}>
              <Inline text={g.area || S.areas.otherArea} query={query} />
            </h3>
          )}
          {g.statements.map((s) => (
            <StatementBlock
              key={s.id}
              statement={s}
              pageRepo={pageRepo}
              project={project}
              query={query}
            />
          ))}
        </section>
      ))}
    </>
  );
}

/** areaHeading is an area's chapter title: under the page's h2, over its
 *  statements' h4 names. */
const areaHeading =
  "mt-9 mb-1 font-serif text-[21px] font-medium leading-tight tracking-tight text-ink";

/** byKind is statements in KINDS' order, stable: the order of statements
 *  no area holds, a page not laid out yet, which has no outline's order. */
const byKind = (list: Statement[]) =>
  [...list].sort((a, b) => KINDS.indexOf(a.kind) - KINDS.indexOf(b.kind));

/** areaGroups is a page's shown statements in their areas, each area
 *  once, where its first statement stands: as the backend lays a page out,
 *  so a page not laid out yet (an area, another, the first again) reads
 *  as one that is, and an area has one chapter for the rail to point at.
 *  An area's statements keep their order, the outline's: overview before
 *  detail. Those in no area go by kind. area is null for one group without
 *  a heading: a page whose statements, all of them and not only the
 *  search's hits, stand in one area or none. An area the search leaves
 *  without a hit is not shown. */
export function areaGroups(
  all: Statement[],
  shown: Statement[],
): { area: string | null; statements: Statement[] }[] {
  if (areaNames(all).length === 0) {
    if (shown.length === 0) return [];
    return [
      {
        area: null,
        statements: all.every((s) => areaOf(s) === "") ? byKind(shown) : shown,
      },
    ];
  }
  const groups = new Map<string, Statement[]>();
  for (const s of shown) {
    const group = groups.get(areaOf(s));
    if (group) group.push(s);
    else groups.set(areaOf(s), [s]);
  }
  return [...groups].map(([area, statements]) => ({
    area,
    statements: area === "" ? byKind(statements) : statements,
  }));
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

/** checkedNews reports whether a statement's checked commit says more than
 *  its sources: a run that wrote the statement at that commit already
 *  shows it as a Basis chip. */
function checkedNews(s: Statement): boolean {
  if (s.checked === undefined || s.checked === "") return false;
  return !s.sources.some(
    (src) => src.kind === "baseline" && src.commit === s.checked,
  );
}

/** StatementBlock is one statement: documentation, not a line. */
export function StatementBlock({
  statement: s,
  pageRepo,
  project = false,
  query = "",
}: {
  statement: Statement;
  pageRepo?: string;
  project?: boolean;
  query?: string;
}) {
  return (
    <article className="border-t border-border-soft py-3 first-of-type:border-t-0">
      <div className="text-[11px] font-medium tracking-[.1em] text-faint uppercase">
        {kindLabel(s.kind, project)}
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
