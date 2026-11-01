// The small shared vocabulary, after ../noergler's ui.tsx: class strings
// hoisted to named consts where a component would add nothing, components
// where there is behaviour or a shape worth a name.
import { Fragment, type CSSProperties, type ReactNode } from "react";
import { codeSpans, paragraphs, type Tone } from "./format";
import { S } from "./strings";
import { onNav } from "./routing";

export const page = "h-full overflow-auto";
/** The page column: 1400px, centred, ../noergler's width. Tables, source
 *  rows and diagrams take it; running text keeps measure, label/value rows
 *  and forms boxWidth. */
export const column = "mx-auto max-w-[1400px] px-4 py-7 sm:px-6 lg:px-10";
/** measure is every running text's line: 75ch of its own font size, the
 *  one reading width in the UI (prose, lede, empty). A meta line is chips
 *  and dates, not text: it keeps the column. */
export const measure = "max-w-[75ch]";
/** boxWidth holds every bordered box (forms, confirms, failures) and the
 *  label/value rows at the width they had in the 900px column: in the
 *  1400px one a label sat 1300px from its value. */
export const boxWidth = "max-w-[820px]";
export const h2 =
  "font-serif text-[22px] font-medium leading-tight tracking-tight text-ink sm:text-[28px]";
export const meta = "mt-1.5 text-[14.5px] text-muted";
export const lede = "mt-1 mb-6 text-[14.5px] text-muted " + measure;
export const eyebrow =
  "mt-7 mb-2.5 text-[11px] font-medium tracking-[.12em] text-faint uppercase";
export const mono = "font-mono text-[12.5px]";

/** Sep is the dot between the clauses of a meta line. An element with
 *  margins, not a string with spaces: a Unicode space is a fraction of the
 *  font size and reads jammed. aria-hidden: it is punctuation. */
export function Sep() {
  return (
    <span aria-hidden className="mx-[max(7px,0.32em)] text-faint">
      {S.common.sep}
    </span>
  );
}

/** Link is an in-app link: a real <a> with the path, so a middle click or a
 *  copied address works, and a plain click stays in the SPA. */
export function Link({
  to,
  className = "",
  title,
  style,
  children,
}: {
  to: string;
  className?: string;
  title?: string;
  style?: CSSProperties;
  children: ReactNode;
}) {
  return (
    <a
      href={to}
      onClick={onNav(to)}
      className={className}
      title={title}
      style={style}
    >
      {children}
    </a>
  );
}

/** Chip is a citation: a PR key in a small accent-bordered box, ../rongo's
 *  citation marker. It links to the note when the key has the API's shape.
 *  quiet is a citation with nothing to link to, a baseline's commit. */
export function Chip({
  to,
  quiet = false,
  title,
  children,
}: {
  to: string | null;
  quiet?: boolean;
  title?: string;
  children: ReactNode;
}) {
  const cls = quiet
    ? "inline-flex items-center gap-1 rounded-[6px] border border-elevated-border px-[7px] py-px font-mono text-[11px] whitespace-nowrap text-muted"
    : "inline-flex items-center gap-1 rounded-[6px] border border-accent-dim bg-accent/10 px-[7px] py-px font-mono text-[11px] whitespace-nowrap text-accent-strong";
  if (to === null)
    return (
      <span className={cls} title={title}>
        {children}
      </span>
    );
  return (
    <Link
      to={to}
      title={title}
      className={cls + " hover:border-accent hover:text-ink"}
    >
      {children}
    </Link>
  );
}

const toneClass: Record<Tone, string> = {
  ok: "bg-online/15 text-[#82c491]",
  warn: "bg-ochre-wash text-ochre",
  fail: "bg-danger/15 text-danger-ink",
  none: "bg-elevated text-ink-dim",
  quiet: "text-muted ring-1 ring-inset ring-elevated-border",
};
const dotClass: Record<Tone, string> = {
  ok: "bg-online",
  warn: "bg-ochre",
  fail: "bg-danger",
  none: "bg-muted",
  quiet: "bg-faint",
};

/** Pill always carries its word; the dot is a second cue, never the only
 *  one. */
export function Pill({
  tone,
  dot = false,
  children,
}: {
  tone: Tone;
  dot?: boolean;
  children: ReactNode;
}) {
  return (
    <span
      className={
        "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium whitespace-nowrap " +
        toneClass[tone]
      }
    >
      {dot && (
        <span
          aria-hidden
          className={"inline-block h-1.5 w-1.5 rounded-full " + dotClass[tone]}
        />
      )}
      {children}
    </span>
  );
}

/** Prose renders a text as paragraphs, split on blank lines, with a
 *  backtick-quoted identifier in the mono face (codeSpans): the wiki is
 *  prose, no other markdown. Held at measure, in the font size the
 *  caller's className sets; a caller sets no max-w of its own. */
export function Prose({
  text,
  className = "",
}: {
  text: string;
  className?: string;
}) {
  return (
    <div className={measure + " " + className}>
      {paragraphs(text).map((p, i) => (
        <p key={i} className="mb-3.5 last:mb-0">
          <Inline text={p} />
        </p>
      ))}
    </div>
  );
}

/** Inline is a line of model text with its backtick-quoted identifiers in
 *  the mono face (codeSpans): a paragraph of Prose, a statement's name. */
export function Inline({ text }: { text: string }) {
  return (
    <>
      {codeSpans(text).map((s, j) =>
        s.code ? (
          <code
            key={j}
            className="rounded-sm bg-active px-[0.3em] font-mono text-[0.84em] text-ink"
          >
            {s.text}
          </code>
        ) : (
          <Fragment key={j}>{s.text}</Fragment>
        ),
      )}
    </>
  );
}

/** Summary is a description's or a note's prose in the serif, ../rongo's
 *  answer column. */
export function Summary({ text }: { text: string }) {
  return (
    <Prose
      text={text}
      className="font-serif text-[17px] leading-[1.6] text-ink-prose"
    />
  );
}

/** Tables scroll inside their box on a narrow screen rather than widening
 *  the page. */
export const tableBox = "overflow-x-auto";
export const table =
  "w-full min-w-[560px] border-separate border-spacing-0 text-[13.5px]";
export const th =
  "border-b border-border pb-2 pr-2.5 text-left text-[11px] font-medium tracking-[.12em] text-faint uppercase whitespace-nowrap";
export const thNum = th + " text-right";
export const td =
  "border-b border-border-soft py-[7px] pr-2.5 align-top text-ink-dim";
export const tdNum = td + " text-right tabular-nums whitespace-nowrap";
export const tdMono = td + " " + mono + " whitespace-nowrap";

/** Buttons, the Quellen pages' only ones: a quiet default, one primary
 *  per row, a dangerous one that only reddens on hover. */
export const btn =
  "inline-flex items-center gap-2 rounded-ui-sm border border-border bg-panel px-3 py-1.5 text-[13.5px] text-ink-dim hover:border-elevated-border hover:bg-active hover:text-ink disabled:cursor-default disabled:opacity-50";
export const btnPrimary =
  "inline-flex items-center gap-2 rounded-ui-sm border border-accent-fill bg-accent-fill px-3 py-1.5 text-[13.5px] text-white hover:border-accent-strong hover:bg-accent-strong disabled:cursor-default disabled:opacity-50";
export const btnDanger = btn + " hover:border-danger hover:text-danger-ink";
/** Row actions: text buttons in a table cell. */
export const rowBtn =
  "rounded-[6px] px-2 py-0.5 text-[12.5px] text-muted hover:bg-active hover:text-ink disabled:cursor-default disabled:opacity-50";

/** Form fields: a label over an input, a hint under it. */
export const fieldLabel = "mb-1 block text-[12px] text-muted";
export const fieldHint = "mt-1 block text-[12px] text-faint";
export const input =
  "w-full rounded-ui-sm border border-border bg-bg px-2.5 py-[7px] text-[14px] text-ink placeholder:text-faint focus:border-accent focus:outline-none";

/** Segmented is a two-or-more way switch, the form's radio. */
export function Segmented<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <div className="inline-flex overflow-hidden rounded-ui-sm border border-border">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={o.value === value}
          onClick={() => onChange(o.value)}
          className={
            "px-3 py-1.5 text-[13.5px] " +
            (o.value === value ? "bg-active text-ink" : "text-muted")
          }
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Panel is an inline form or confirm box, opened in place, never a modal. */
export function Panel({
  tone = "plain",
  children,
}: {
  tone?: "plain" | "danger";
  children: ReactNode;
}) {
  return (
    <div
      className={
        // wrap-break-word: a mono error (a URL, a JSON fragment) breaks
        // inside the box instead of running past its border.
        "mt-3 rounded-ui border px-4 py-3.5 text-[13.5px] wrap-break-word " +
        boxWidth +
        " " +
        (tone === "danger"
          ? "border-danger bg-danger/8 text-ink-dim"
          : "border-border bg-panel")
      }
    >
      {children}
    </div>
  );
}

/** Empty is what a section says when there is nothing, a real state here. */
export function Empty({ children }: { children: ReactNode }) {
  return (
    <p className={"py-1.5 text-[13.5px] text-faint " + measure}>{children}</p>
  );
}

/** Failed takes `what` in the case the sentence needs ("das Projekt"). */
export function Failed({ what }: { what: string }) {
  return (
    <div
      className={
        "rounded-ui border border-border bg-panel px-3.5 py-3 text-[13.5px] text-muted " +
        boxWidth
      }
    >
      {S.common.failed(what)}
    </div>
  );
}

/** Skeleton is the page while it loads: three fading bars. */
export function Skeleton() {
  return (
    <div aria-busy className="space-y-3 py-2">
      <div className="skeleton h-7 w-1/3 rounded-ui-sm" />
      <div className="skeleton h-4 w-2/3 rounded-ui-sm" />
      <div className="skeleton h-4 w-1/2 rounded-ui-sm" />
    </div>
  );
}

/** Crumbs is the breadcrumb: links, then the current name. */
export function Crumbs({ items }: { items: { label: string; to?: string }[] }) {
  return (
    <nav aria-label="Pfad" className="mb-2.5 text-[13px] text-faint">
      {items.map((it, i) => (
        <span key={i}>
          {i > 0 && <span className="mx-1.5">/</span>}
          {it.to ? (
            <Link to={it.to} className="text-muted hover:text-ink-dim">
              {it.label}
            </Link>
          ) : (
            <span className="text-ink-dim">{it.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}

/** Tiles is a row of stat tiles, four across on a wide screen. */
export function Tiles({ children }: { children: ReactNode }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">{children}</div>
  );
}

export function Tile({
  label,
  value,
  note,
}: {
  label: string;
  value: ReactNode;
  note?: string;
}) {
  return (
    <div className="rounded-ui border border-border bg-panel px-3.5 py-3">
      <div className="text-[11px] font-medium tracking-[.12em] text-faint uppercase">
        {label}
      </div>
      <div className="mt-0.5 text-[22px] font-medium tabular-nums text-ink">
        {value}
      </div>
      {note && (
        <div className="text-[12px] text-muted wrap-break-word">{note}</div>
      )}
    </div>
  );
}
