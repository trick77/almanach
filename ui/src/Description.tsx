import type { ReactNode } from "react";
import type { Description, Kind, Source, Statement } from "./api";
import { Diagram } from "./Diagram";
import { day, shortSHA } from "./format";
import type { RailItem } from "./Rail";
import { notePathFor } from "./routing";
import { S } from "./strings";
import {
  Chip,
  Empty,
  Inline,
  Marked,
  Prose,
  Summary,
  eyebrow,
  matchesQuery,
  mono,
} from "./ui";

/** The kinds in the order the Markdown export has them: what must not
 *  break and why first, the parts after. */
const KINDS: Kind[] = [
  "invariant",
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
export const summaryId = "ueberblick";
export const kindId = (k: Kind) => `art-${k}`;

/** statementMatches is the search's test on a statement: its name, its
 *  text and its files. */
export function statementMatches(s: Statement, query: string): boolean {
  return matchesQuery(query, s.name, s.text, ...s.files);
}

/** descriptionRail is the rail's part of a description: the summary and
 *  each kind with its count, the search's hit count while one runs, and
 *  the line under the search. lead is a section before the summary (a
 *  map), extra one after it (problem areas). */
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
    ...kinds.map((kind) => {
      const all = statements.filter((s) => s.kind === kind).length;
      const hit = hits.filter((s) => s.kind === kind).length;
      return {
        id: kindId(kind),
        label: kindLabel(kind, project),
        tab,
        count: searching ? hit : all === 0 ? "–" : all,
        dim: all === 0 || (searching && hit === 0),
        hit: searching,
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

/** DescriptionView is a page's description: the summary as prose, then the
 *  statements grouped by kind, each a block with its name, its text as
 *  paragraphs, its files and its sources as citation chips. pageRepo is
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
        <Summary text={description.summary} query={query} />
      </section>
      {afterSummary}
      {kindsOf(project).map((kind) => {
        const all = description.statements.filter((s) => s.kind === kind);
        const list = all.filter((s) => statementMatches(s, query));
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
            {list.map((s) => (
              <StatementBlock
                key={s.id}
                statement={s}
                pageRepo={pageRepo}
                query={query}
              />
            ))}
          </section>
        );
      })}
    </>
  );
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
      <Prose
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
