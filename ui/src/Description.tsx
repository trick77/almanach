import type { Description, Kind, Statement } from "./api";
import { day } from "./format";
import { notePathFor } from "./routing";
import { S } from "./strings";
import { Chip, Empty, Prose, Summary, eyebrow, mono } from "./ui";

const KINDS: Kind[] = ["component", "rule", "invariant", "entity"];

/** DescriptionView is a page's description: the summary as prose, then the
 *  statements grouped by kind, each a block with its name, its text as
 *  paragraphs, its files and its sources as citation chips. */
export function DescriptionView({
  description,
  emptyHint,
}: {
  description: Description | null;
  emptyHint: string;
}) {
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
      {KINDS.map((kind) => {
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
              <StatementBlock key={s.id} statement={s} />
            ))}
          </section>
        );
      })}
    </>
  );
}

/** StatementBlock is one statement: documentation, not a line. */
export function StatementBlock({ statement: s }: { statement: Statement }) {
  return (
    <article className="border-t border-border-soft py-3 first:border-t-0">
      <h4 className="font-semibold text-ink">{s.name}</h4>
      <Prose
        text={s.text}
        className="mt-1 mb-1.5 font-serif text-[15.5px] leading-[1.55] text-ink-dim"
      />
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
          <span
            key={src.pr_key}
            title={`${src.ticket_key ?? ""} ${day(src.merged_at)}`.trim()}
          >
            <Chip to={notePathFor(src.pr_key)}>{src.pr_key}</Chip>
          </span>
        ))}
      </div>
    </article>
  );
}
