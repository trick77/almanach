import type { Description, Kind, Source, Statement } from "./api";
import { Diagram } from "./Diagram";
import { day, shortSHA } from "./format";
import { notePathFor } from "./routing";
import { S } from "./strings";
import { Chip, Empty, Prose, Summary, eyebrow, mono } from "./ui";

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

/** DescriptionView is a page's description: the summary as prose, then the
 *  statements grouped by kind, each a block with its name, its text as
 *  paragraphs, its files and its sources as citation chips. pageRepo is
 *  the repository a repository page shows, whose slug its chips leave out;
 *  without it the page is a project's, which holds no extension. */
export function DescriptionView({
  description,
  emptyHint,
  pageRepo,
}: {
  description: Description | null;
  emptyHint: string;
  pageRepo?: string;
}) {
  const kinds =
    pageRepo === undefined ? KINDS.filter((k) => k !== "extension") : KINDS;
  if (description === null) {
    return (
      <>
        <div className={eyebrow}>{S.project.description}</div>
        <Empty>
          {S.project.noDescription} {emptyHint}
        </Empty>
      </>
    );
  }
  return (
    <>
      <div className={eyebrow}>{S.project.description}</div>
      <Summary text={description.summary} />
      {kinds.map((kind) => {
        const list = description.statements.filter((s) => s.kind === kind);
        return (
          <section key={kind}>
            <div className={eyebrow}>
              {S.kinds[kind]}
              {list.length === 0 && (
                <span className="ml-2 font-normal tracking-normal normal-case text-faint">
                  {S.note.noneOfKind}
                </span>
              )}
            </div>
            {list.map((s) => (
              <StatementBlock key={s.id} statement={s} pageRepo={pageRepo} />
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
}: {
  statement: Statement;
  pageRepo?: string;
}) {
  return (
    <article className="border-t border-border-soft py-3 first:border-t-0">
      <h4 className="font-semibold text-ink">{s.name}</h4>
      <Prose
        text={s.text}
        className="mt-1 mb-1.5 font-serif text-[15.5px] leading-[1.55] text-ink-dim"
      />
      {s.diagram && (
        <Diagram ir={s.diagram} title={S.diagram.titles[s.diagram.kind]} />
      )}
      <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-muted">
        {s.files.map((f, i) => (
          <span key={f}>
            <span className={mono}>{f}</span>
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
