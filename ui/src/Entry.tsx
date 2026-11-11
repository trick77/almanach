// The start tab's reading path and the reference tab's question chips:
// built from the page's own data by code, never by a model call.
import type { ReactNode } from "react";
import type { Kind, Statement } from "./api";
import { areaHeading, kindLabel } from "./Description";
import { day } from "./format";
import { S } from "./strings";
import {
  Inline,
  Link,
  Sep,
  Teaser,
  kindLabelStyle,
  measure,
  teaserLines,
} from "./ui";

/** Ask is a question chip: all statements, or the kinds one question
 *  reads. */
export type Ask = "all" | "guard" | "why" | "hurt" | "where";

/** askKinds is the kinds each question reads. */
export const askKinds: Record<Exclude<Ask, "all">, readonly Kind[]> = {
  guard: ["invariant", "rule"],
  why: ["decision"],
  hurt: ["pitfall"],
  where: ["extension"],
};

/** askOrder is the chips' order, all first. */
const askOrder: Exclude<Ask, "all">[] = ["guard", "why", "hurt", "where"];

/** ofKinds is the statements of kinds, in the page's order: what a step
 *  counts for its "Alle n", a chip for its number, pick picks from. */
export function ofKinds(
  statements: Statement[],
  kinds: readonly Kind[],
): Statement[] {
  return statements.filter((s) => kinds.includes(s.kind));
}

/** pick is a step's statements: those of kinds, the most cited first (the
 *  number of sources), the page's order on a tie, at most n. */
export function pick(
  statements: Statement[],
  kinds: readonly Kind[],
  n: number,
): Statement[] {
  return ofKinds(statements, kinds)
    .map((s, i) => ({ s, i }))
    .sort((a, b) => b.s.sources.length - a.s.sources.length || a.i - b.i)
    .slice(0, n)
    .map(({ s }) => s);
}

/** Step is one step of the reading path: its section id, its title, what
 *  it shows, and more, its way to all of its kind (a question chip). */
export interface Step {
  id: string;
  title: string;
  body: ReactNode;
  more?: { label: string; ask: Ask };
}

/** EntryPath is the reading path: the steps given, numbered in turn, so a
 *  step left out for want of content leaves no gap. onAsk follows a
 *  step's more. */
export function EntryPath({
  steps,
  onAsk,
}: {
  steps: Step[];
  onAsk: (ask: Ask) => void;
}) {
  return (
    <>
      {steps.map((step, i) => (
        <section key={step.id} id={step.id} className="scroll-mt-16">
          <h3 className={areaHeading + " mb-3"}>
            {S.path.step(i + 1, step.title)}
          </h3>
          {step.body}
          {step.more && (
            <EntryMore
              label={step.more.label}
              onClick={() => onAsk(step.more!.ask)}
            />
          )}
        </section>
      ))}
    </>
  );
}

/** EntryStatement is a statement on the path: its kind, its name linking
 *  to its place on the reference tab (to), and its text's opening. */
export function EntryStatement({
  statement: s,
  to,
  project,
}: {
  statement: Statement;
  to: string;
  project: boolean;
}) {
  return (
    <article className="border-t border-border-soft py-3 first:border-t-0">
      <div className={kindLabelStyle}>{kindLabel(s.kind, project)}</div>
      <Link to={to} className="font-semibold text-ink hover:underline">
        <Inline text={s.name} />
      </Link>
      <p
        className={
          "mt-1 line-clamp-3 font-serif text-[15.5px] leading-[1.55] text-ink-dim " +
          measure
        }
      >
        <Teaser lines={teaserLines(s.text)} />
      </p>
    </article>
  );
}

/** EntryMore is the way from a step to all of its kind on the reference
 *  tab: a button, as it switches the tab and presses a chip. */
export function EntryMore({
  label,
  onClick,
}: {
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="mt-2 text-[14px] text-accent-strong hover:text-ink"
    >
      {label} <span aria-hidden>→</span>
    </button>
  );
}

/** NextCard is where to read on after the path: the next repository's
 *  page, its teaser beside it. */
export function NextCard({
  to,
  slug,
  teaser,
}: {
  to: string;
  slug: string;
  teaser: string[];
}) {
  return (
    <Link
      to={to}
      className="mt-10 flex flex-wrap items-baseline gap-x-3 gap-y-1 rounded-ui border border-elevated-border bg-panel px-5 py-4 text-ink-dim hover:border-accent hover:text-ink"
    >
      <span className="text-[13px] text-muted">{S.path.next}</span>
      <span className="font-mono text-[14.5px] text-ink">{slug}</span>
      {teaser.length > 0 && (
        <span className="min-w-0 flex-1 basis-[240px] text-[14px] text-muted">
          <Teaser lines={teaser} />
        </span>
      )}
      <span className="ml-auto text-[14px] whitespace-nowrap text-accent-strong">
        {S.path.readEntry} <span aria-hidden>→</span>
      </span>
    </Link>
  );
}

/** AskChips is the reference tab's questions: each a pressed-or-not chip
 *  with its count, all first; a question with no statement is left out,
 *  and with none left but all the row is not there. */
export function AskChips({
  statements,
  value,
  onChange,
}: {
  statements: Statement[];
  value: Ask;
  onChange: (ask: Ask) => void;
}) {
  const asks = askOrder
    .map((a) => ({
      a,
      n: ofKinds(statements, askKinds[a]).length,
    }))
    .filter(({ n }) => n > 0);
  if (asks.length === 0) return null;
  const chips: { a: Ask; n: number }[] = [
    { a: "all", n: statements.length },
    ...asks,
  ];
  return (
    <div
      role="group"
      aria-label={S.ask.label}
      className="mt-5 flex flex-wrap gap-1.5"
    >
      {chips.map(({ a, n }) => (
        <button
          key={a}
          type="button"
          aria-pressed={a === value}
          onClick={() => onChange(a)}
          className={
            "rounded-full border px-3 py-1 text-[13px] " +
            (a === value
              ? "border-accent bg-active text-ink"
              : "border-border text-muted hover:border-elevated-border hover:text-ink")
          }
        >
          {S.ask[a]}
          <span className="ml-1.5 text-[12px] text-faint tabular-nums">
            {n}
          </span>
        </button>
      ))}
    </div>
  );
}

/** StandLine is the page's figures, quiet under its head: the
 *  description's revision, the notes, the last merge; the whole line
 *  opens the history tab. */
export function StandLine({
  revision,
  notes,
  lastMerge,
  onHistory,
}: {
  revision: number | null;
  notes: number;
  lastMerge: string | null;
  onHistory: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onHistory}
      className="mt-1 text-left text-[12.5px] text-muted hover:text-ink"
    >
      {S.stand.label}{" "}
      {revision === null
        ? S.projects.noDescription
        : S.project.descriptionRevision(revision)}
      <Sep />
      {S.projects.notes(notes)}
      <Sep />
      {S.projects.lastMerge}{" "}
      {lastMerge === null ? S.common.never : day(lastMerge)}
      <Sep />
      {S.stand.toHistory} <span aria-hidden>→</span>
    </button>
  );
}
