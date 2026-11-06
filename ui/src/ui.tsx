// The small shared vocabulary, after ../noergler's ui.tsx: class strings
// hoisted to named consts where a component would add nothing, components
// where there is behaviour or a shape worth a name.
import {
  Fragment,
  useCallback,
  useEffect,
  useState,
  type ComponentPropsWithoutRef,
  type CSSProperties,
  type MouseEvent,
  type ReactNode,
} from "react";
import {
  blocks,
  codeSpans,
  lines,
  paragraphs,
  runs,
  type Tone,
} from "./format";
import { S } from "./strings";
import { navigateTo, onNav } from "./routing";

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
  ...rest
}: {
  to: string;
  className?: string;
  title?: string;
  style?: CSSProperties;
  children: ReactNode;
} & Omit<
  ComponentPropsWithoutRef<"a">,
  "href" | "onClick" | "className" | "title" | "style" | "children"
>) {
  return (
    <a
      {...rest}
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
  row = false,
  title,
  children,
}: {
  to: string | null;
  quiet?: boolean;
  /** row: the chip names its row's page (rowOpens), lit by the row's
   *  hover rather than its own. */
  row?: boolean;
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
      className={
        cls +
        (row
          ? " group-hover:border-accent group-hover:text-ink"
          : " hover:border-accent hover:text-ink")
      }
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
 *  backtick-quoted identifier in the mono face (codeSpans) and nothing
 *  else read: a note's text. Almanach's own laid-out text is
 *  Doc's. Held at measure, in the font size the caller's className sets;
 *  a caller sets no max-w of its own. query sets off a search's matches. */
export function Prose({
  text,
  className = "",
  query = "",
}: {
  text: string;
  className?: string;
  query?: string;
}) {
  return (
    <div className={measure + " " + className}>
      {paragraphs(text).map((p, i) => (
        <p key={i} className="mb-3.5 last:mb-0">
          <Inline text={p} query={query} />
        </p>
      ))}
    </div>
  );
}

/** codePill is a backtick-quoted identifier, every one in the app the
 *  same: mono on a slight pill, a soft border that still reads on a row's
 *  hover (bg-active); cloned per line when one wraps. */
const codePill =
  "rounded-[5px] border border-elevated-border/75 bg-active px-[0.4em] py-[0.05em] font-mono text-[0.84em] text-ink box-decoration-clone";

/** teaserLines is what a Teaser shows of a summary; none when it holds
 *  only sub-headings, so the caller says it has no description. */
export function teaserLines(text: string): string[] {
  return lines(text, { headings: false, markers: true });
}

/** Teaser is a summary read on in a list, a table cell's or a tile's
 *  (teaserLines): paragraphs and items run together, each item with its
 *  marker, code spans and bold as Doc sets them. */
export function Teaser({ lines }: { lines: string[] }) {
  return (
    <>
      {lines.map((t, i) => (
        <Fragment key={i}>
          {i > 0 && " "}
          <Inline text={t} bold />
        </Fragment>
      ))}
    </>
  );
}

/** Inline is a line of model text with its backtick-quoted identifiers in
 *  the mono face (codeSpans): a paragraph of Prose, a statement's name.
 *  bold reads **bold** as well (runs), for Doc's laid-out text only; its
 *  search marks are found on the runs' text as one (plain's, which the
 *  search matched), so a match across a bold or code edge is set off in
 *  each run it covers. query sets off a search's matches, in the code
 *  spans too. */
export function Inline({
  text,
  query = "",
  bold = false,
}: {
  text: string;
  query?: string;
  bold?: boolean;
}) {
  const parts = bold
    ? runs(text)
    : codeSpans(text).map((s) => ({ ...s, bold: false }));
  const hits = bold
    ? matchRanges(parts.map((p) => p.text).join(""), query)
    : [];
  // Each run's offset in the runs' text as one; bold's alone needs them.
  const starts: number[] = [];
  if (bold)
    for (let j = 0, at = 0; j < parts.length; at += parts[j].text.length, j++)
      starts.push(at);
  return (
    <>
      {parts.map((s, j) => {
        const body = bold ? (
          <Ranged text={s.text} from={starts[j]} hits={hits} />
        ) : (
          <Marked text={s.text} query={query} />
        );
        const run = s.code ? <code className={codePill}>{body}</code> : body;
        return s.bold ? (
          <strong key={j} className="font-semibold text-ink">
            {run}
          </strong>
        ) : (
          <Fragment key={j}>{run}</Fragment>
        );
      })}
    </>
  );
}

/** Doc renders Almanach's laid-out text, a statement's or a summary's
 *  (blocks): paragraphs, flat lists, **bold**, code spans and #### sub-
 *  headings; anything else as written. Held at measure, in the font size
 *  the caller's className sets. query sets off a search's matches in every
 *  block. heading is the sub-headings' level, one under the text's own: a
 *  statement's h5 under its h4 name, a summary's h3 under the page's h2. */
export function Doc({
  text,
  className = "",
  query = "",
  heading: H = "h5",
}: {
  text: string;
  className?: string;
  query?: string;
  heading?: "h3" | "h5";
}) {
  const block = "mb-3.5 last:mb-0";
  return (
    <div className={measure + " " + className}>
      {blocks(text).map((b, i) => {
        switch (b.kind) {
          case "h":
            return (
              <H
                key={i}
                className="mt-5 mb-1.5 font-sans text-[13.5px] font-semibold tracking-[.01em] text-ink first:mt-0"
              >
                <Inline text={b.text} query={query} bold />
              </H>
            );
          case "ul":
          case "ol": {
            const items = b.items.map((it, k) => (
              <li key={k} className="mb-1 pl-1 last:mb-0">
                <Inline text={it} query={query} bold />
              </li>
            ));
            return b.kind === "ul" ? (
              <ul
                key={i}
                className={block + " list-disc pl-5 marker:text-faint"}
              >
                {items}
              </ul>
            ) : (
              <ol
                key={i}
                start={b.start}
                className={block + " list-decimal pl-6 marker:text-muted"}
              >
                {items}
              </ol>
            );
          }
          default:
            return (
              <p key={i} className={block + " whitespace-pre-line"}>
                <Inline text={b.text} query={query} bold />
              </p>
            );
        }
      })}
    </div>
  );
}

/** Summary is a description's or a note's prose in the serif, ../rongo's
 *  answer column. layout reads a description's summary as Doc does a
 *  statement's, sub-headings and all; a note's stays Prose. */
export function Summary({
  text,
  query,
  layout = false,
}: {
  text: string;
  query?: string;
  layout?: boolean;
}) {
  const className = "font-serif text-[17px] leading-[1.6] text-ink-prose";
  return layout ? (
    <Doc text={text} query={query} className={className} heading="h3" />
  ) : (
    <Prose text={text} query={query} className={className} />
  );
}

/** Marked is text with every match of a search set off, case-insensitive;
 *  the text itself unchanged, never markup from it. */
export function Marked({ text, query }: { text: string; query: string }) {
  return <Ranged text={text} from={0} hits={matchRanges(text, query)} />;
}

/** matchRanges is every case-insensitive match of a search in a text, as
 *  [start, end) offsets: the one search pattern, Marked's and Inline's
 *  marks that cross runs. */
function matchRanges(text: string, query: string): [number, number][] {
  const q = query.trim();
  if (q === "") return [];
  const re = new RegExp(escapeRegExp(q), "gi");
  return [...text.matchAll(re)].map((m) => [m.index, m.index + m[0].length]);
}

/** Ranged is a run of text that starts at offset from of its line, with
 *  the parts the line's hits cover set off as Marked sets them off. */
function Ranged({
  text,
  from,
  hits,
}: {
  text: string;
  from: number;
  hits: [number, number][];
}) {
  const out: ReactNode[] = [];
  let i = 0;
  for (const [s, e] of hits) {
    const a = Math.max(s - from, i);
    const b = Math.min(e - from, text.length);
    if (b <= a) continue;
    if (a > i) out.push(text.slice(i, a));
    out.push(
      <mark key={a} className="rounded-[2px] bg-ochre-wash px-px text-ochre">
        {text.slice(a, b)}
      </mark>,
    );
    i = b;
  }
  if (i < text.length) out.push(text.slice(i));
  return <>{out}</>;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** matchesQuery is a search's test, the same one Marked sets off: every
 *  text given, case-insensitive. */
export function matchesQuery(query: string, ...texts: string[]): boolean {
  const q = query.trim();
  if (q === "") return true;
  // Marked's own pattern, so a text kept is a text with a mark.
  const re = new RegExp(escapeRegExp(q), "i");
  return texts.some((t) => re.test(t));
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
/** rowOpens makes a <tr> open its subject's page from anywhere on it: a
 *  click navigates, a middle or ⌘/Ctrl click opens a tab. The name stays a
 *  real Link (rowLink), what the keyboard and a copied address use; a
 *  click on it is its own. A drag that selected text opens nothing, so a
 *  cell's text still copies. */
export function rowOpens(to: string) {
  const own = (e: MouseEvent<HTMLElement>) =>
    e.target instanceof Element && e.target.closest("a, button") !== null;
  return {
    className: "group cursor-pointer hover:bg-active",
    onClick: (e: MouseEvent<HTMLElement>) => {
      if (own(e) || !(window.getSelection()?.isCollapsed ?? true)) return;
      if (e.metaKey || e.ctrlKey) window.open(to, "_blank", "noopener");
      else navigateTo(to);
    },
    onAuxClick: (e: MouseEvent<HTMLElement>) => {
      if (e.button === 1 && !own(e)) window.open(to, "_blank", "noopener");
    },
  };
}
export const rowLink = "text-ink-dim group-hover:text-ink";

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
/** A field's error in the hint's place, with role="alert" where used. */
export const fieldError = "mt-1 block text-[12px] text-danger-ink";
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

/** useCopied is a copy control's feedback: copied() puts text on the
 *  clipboard and says so for a moment. A browser without the clipboard
 *  (no secure context) copies nothing and says nothing. */
export function useCopied(): [boolean, (text: string) => void] {
  // A count, not a flag: every copy restarts the moment.
  const [copies, setCopies] = useState(0);
  useEffect(() => {
    if (copies === 0) return;
    const id = window.setTimeout(() => setCopies(0), 1500);
    return () => window.clearTimeout(id);
  }, [copies]);
  const copy = useCallback((text: string) => {
    const clip = navigator.clipboard;
    if (!clip) return;
    clip.writeText(text).then(
      () => setCopies((n) => n + 1),
      () => {},
    );
  }, []);
  return [copies > 0, copy];
}

/** CopyButton puts text on the clipboard and says "Kopiert" for a
 *  moment: a plain text control, the page's tone. */
export function CopyButton({
  text,
  label,
  className = "",
}: {
  text: string;
  label: string;
  className?: string;
}) {
  const [done, copy] = useCopied();
  return (
    <button
      type="button"
      onClick={() => copy(text)}
      className={"hover:text-ink " + className}
    >
      {done ? S.copy.done : label}
    </button>
  );
}

/** ExportLine is a page's way out to its Markdown export, the agent's
 *  interface: open it, or copy its address for one. */
export function ExportLine({ href }: { href: string }) {
  const url = window.location.origin + href;
  return (
    <p className="mt-1 flex flex-wrap gap-x-3 text-[12.5px] text-muted">
      <a href={href} target="_blank" rel="noopener" className="hover:text-ink">
        {S.copy.openMarkdown}
      </a>
      <CopyButton text={url} label={S.copy.url} />
    </p>
  );
}

/** Empty is what a section says when there is nothing, a real state here. */
export function Empty({ children }: { children: ReactNode }) {
  return (
    <p className={"py-1.5 text-[13.5px] text-faint " + measure}>{children}</p>
  );
}

/** Failed shows a page's whole failure sentence: the verb agrees with
 *  its subject, so strings.ts writes each one out. */
export function Failed({ message }: { message: string }) {
  return (
    <div
      className={
        "rounded-ui border border-border bg-panel px-3.5 py-3 text-[13.5px] text-muted " +
        boxWidth
      }
    >
      {message}
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
