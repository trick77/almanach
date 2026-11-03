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
 *  breaking), the parts after. */
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

/** kindsOf is a page's kinds: a project's hold no extension. */
export function kindsOf(project: boolean): Kind[] {
  return project ? KINDS.filter((k) => k !== "extension") : KINDS;
}

/** kindLabel names a kind as its page does: the project page's entities
 *  are the shared ones and say so. */
export function kindLabel(kind: Kind, project: boolean): string {
  return (project ? S.projectKinds[kind] : undefined) ?? S.kinds[kind];
}

/** The ids of a description's sections, the rail's anchors. */
export const summaryId = "summary";
export const kindId = (k: Kind) => `kind-${k}`;
/** areaId is an area's heading inside its kind, by the area's place among
 *  the kind's areas (areaNames), so a search leaves it where it was. */
export const areaId = (k: Kind, i: number) => `${kindId(k)}-area-${i}`;

const areaOf = (s: Statement) => s.area ?? "";

/** areaNames is a kind's areas, each once, in the page's order: none when
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
 *  each kind with its count, its areas under it when it heads them, the
 *  search's hit count while one runs, and the line under the search.
 *  lead is the sections before the summary (a map), extra the ones after
 *  it (problem areas, dependencies). An area the search leaves without
 *  a hit has no heading to point at and is left out. */
export function descriptionRail(
  description: Description | null,
  project: boolean,
  query: string,
  tab: string,
  lead: RailItem[] = [],
  extra: RailItem[] = [],
): { items: RailItem[]; hitLine: string } {
  const statements = description?.statements ?? [];
  const searching = query.trim() !== "";
  const hits = statements.filter((s) => statementMatches(s, query));
  // Without a description the page shows the summary's place alone: no
  // kind section to point at.
  const kinds = description === null ? [] : kindsOf(project);
  const items: RailItem[] = [
    ...lead,
    { id: summaryId, label: S.rail.summary, tab },
    ...extra,
    ...kinds.map((kind): RailItem => {
      const ofKind = statements.filter((s) => s.kind === kind);
      const all = ofKind.length;
      const kindHits = hits.filter((s) => s.kind === kind);
      const hit = kindHits.length;
      const children = areaNames(ofKind).flatMap((area, i) => {
        const n = kindHits.filter((s) => areaOf(s) === area).length;
        return n === 0
          ? []
          : [
              {
                id: areaId(kind, i),
                label: area === "" ? S.areas.otherArea : plain(area),
                tab,
                count: n,
                hit: searching,
              },
            ];
      });
      return {
        id: kindId(kind),
        label: kindLabel(kind, project),
        tab,
        count: searching ? hit : all === 0 ? "–" : all,
        dim: all === 0 || (searching && hit === 0),
        hit: searching,
        children: children.length > 0 ? children : undefined,
      };
    }),
  ];
  const hitLine = !searching
    ? ""
    : hits.length === 0
      ? S.rail.noHits
      : S.rail.hits(
          hits.length,
          statements.length,
          new Set(hits.map((s) => s.kind)).size,
        );
  return { items, hitLine };
}

/** DescriptionView is a page's description: the summary laid out as a
 *  statement's text is (sub-headings, lists, bold), then the
 *  statements grouped by kind, each a block with its name, its text laid
 *  out (Doc), its files and its sources as citation chips. pageRepo is
 *  the repository a repository page shows, whose slug its chips leave out;
 *  without it the page is a project's, which holds no extension. query
 *  keeps the statements it matches and sets the matches off; the summary
 *  stays, marked. lead comes first (a map), afterSummary under the
 *  summary (a repository's problem areas). */
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
  const searching = query.trim() !== "";
  return (
    <>
      {lead}
      <section id={summaryId} className="scroll-mt-16">
        <div className={eyebrow}>{S.rail.summary}</div>
        <Summary text={description.summary} query={query} layout />
      </section>
      {afterSummary}
      {kindsOf(project).map((kind) => {
        const all = description.statements.filter((s) => s.kind === kind);
        const list = all.filter((s) => statementMatches(s, query));
        const names = areaNames(all);
        return (
          <section key={kind} id={kindId(kind)} className="scroll-mt-16">
            <div className={eyebrow}>
              {kindLabel(kind, project)}
              {(all.length === 0 || searching) && (
                <span className="ml-2 font-normal tracking-normal normal-case text-faint">
                  {all.length === 0
                    ? S.note.noneOfKind
                    : list.length === 0
                      ? S.rail.noHits
                      : `${list.length} / ${all.length}`}
                </span>
              )}
            </div>
            {areaGroups(all, list).map((g, i) => (
              <div key={i}>
                {g.area !== null && (
                  <h3
                    // The rail's anchor.
                    id={areaId(kind, names.indexOf(g.area))}
                    className={
                      (i === 0 ? "mt-1 " : "mt-6 ") +
                      "scroll-mt-16 " +
                      areaHeading
                    }
                  >
                    <Inline text={g.area || S.areas.otherArea} query={query} />
                  </h3>
                )}
                {g.statements.map((s) => (
                  <StatementBlock
                    key={s.id}
                    statement={s}
                    pageRepo={pageRepo}
                    query={query}
                  />
                ))}
              </div>
            ))}
          </section>
        );
      })}
    </>
  );
}

/** areaHeading sets an area off inside its kind: under the kind's
 *  eyebrow, over its statements' h4 names. Its top margin is the caller's
 *  by the group's place: each heading leads a wrapper of its own, so
 *  `first:` would match every one. */
const areaHeading = "mb-1 font-serif text-[17px] font-medium text-ink";

/** areaGroups is a kind's shown statements in their areas, each area
 *  once, where its first statement stands: as the backend lays a page out,
 *  so a page not laid out yet (an area, another, the first again) reads
 *  as one that is, and an area has one heading for the rail to point at.
 *  area is null for one group without a heading: a kind whose statements,
 *  all of them and not only the search's hits, stand in one area or none.
 *  An area the search leaves without a hit is not shown. */
export function areaGroups(
  all: Statement[],
  shown: Statement[],
): { area: string | null; statements: Statement[] }[] {
  if (areaNames(all).length === 0) {
    return shown.length === 0 ? [] : [{ area: null, statements: shown }];
  }
  const groups = new Map<string, Statement[]>();
  for (const s of shown) {
    const group = groups.get(areaOf(s));
    if (group) group.push(s);
    else groups.set(areaOf(s), [s]);
  }
  return [...groups].map(([area, statements]) => ({ area, statements }));
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
  query = "",
}: {
  statement: Statement;
  pageRepo?: string;
  query?: string;
}) {
  return (
    <article className="border-t border-border-soft py-3 first:border-t-0">
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
