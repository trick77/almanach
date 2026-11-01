// Dates, money and tones, the way the pages print them.

import { S } from "./strings";

/** day is a Swiss date: 28.10.2026. */
export function day(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${pad(d.getDate())}.${pad(d.getMonth() + 1)}.${d.getFullYear()}`;
}

/** dayTime is a Swiss date with the time: 28.10.2026 14:03. */
export function dayTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${day(iso)} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** clock is the time of day alone: 14:03, for a tile whose note has the
 *  date. */
export function clock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** shortDay is the day without the year, for a dense table: 24.10. */
export function shortDay(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return `${pad(d.getDate())}.${pad(d.getMonth() + 1)}.`;
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** money shows a USD string as the API sends it (3 decimals) at 2,
 *  rounded half up on the digits, never through a float, with the sign in
 *  front; absent means the gateway did not price the call. */
export function money(usd: string | undefined | null): string {
  if (usd === undefined || usd === null || usd === "") return S.note.unpriced;
  const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(usd);
  if (!m) return `$${usd}`;
  const [, sign, whole, frac = ""] = m;
  // The amount as an integer of its smallest unit, then of cents: digit
  // strings stay exact well past any bill.
  const scale = Math.max(frac.length, 2);
  const units = Number(whole + frac.padEnd(scale, "0"));
  const per = 10 ** (scale - 2);
  const cents = Math.floor((units + Math.floor(per / 2)) / per);
  const digits = String(cents).padStart(3, "0");
  const shown = `${digits.slice(0, -2)}.${digits.slice(-2)}`;
  return `${cents === 0 ? "" : sign}$${shown}`;
}

/** share is a percentage of a whole, rounded, or null without a whole. */
export function share(
  part: number | null,
  whole: number | null,
): number | null {
  if (part === null || whole === null || whole <= 0) return null;
  return Math.round((part / whole) * 100);
}

/** paragraphs splits a text on blank lines and reads nothing else: a
 *  note's text, which noergler wrote and no normalizer laid out, where a
 *  line that happens to start with a dash or a hash must not turn into a
 *  list or a heading. */
export function paragraphs(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p.length > 0);
}

/** A block of laid-out text: a paragraph, a sub-heading, or a flat list. */
export type Block =
  | { kind: "p"; text: string }
  | { kind: "h"; text: string }
  | { kind: "ul"; items: string[] }
  | { kind: "ol"; start: number; items: string[] };

/** The one sub-heading level statement text holds (markup.Heading). */
const headingMark = "#### ";

/** listItem is a line as a list item in the stored form markup.Normalize
 *  writes ("- text", "12. text"), or null. */
function listItem(
  line: string,
): { kind: "ul" | "ol"; n: number; text: string } | null {
  // Exactly the stored form, as markup.StoredItem: one blank after the
  // marker, the text starting right after it; a rule ("- - -") is none.
  if (/^([-*_])(\s*\1){2,}\s*$/.test(line)) return null;
  const u = /^- (\S.*)$/.exec(line);
  if (u) return { kind: "ul", n: 0, text: u[1] };
  const m = /^(\d{1,9})\. (\S.*)$/.exec(line);
  return m ? { kind: "ol", n: Number(m[1]), text: m[2] } : null;
}

/** blocks reads Almanach's laid-out text, a statement's or a summary's,
 *  as backend/internal/markup stores it: blocks a blank line apart, a "- "
 *  or "N. " line a list item (a line under one without a marker
 *  continues it), a "#### " line a sub-heading where headings is set (a
 *  statement's text), every other line paragraph text. Nothing else is
 *  markup: a quote, a table or a "* " line stays as written. */
export function blocks(text: string, headings: boolean): Block[] {
  const out: Block[] = [];
  let para: string[] = [];
  let list: Extract<Block, { kind: "ul" | "ol" }> | null = null;
  const close = () => {
    if (para.length > 0) out.push({ kind: "p", text: para.join("\n") });
    para = [];
    list = null;
  };
  // A blank line in a list keeps it open for a next item, as
  // markup.Normalize and CommonMark read it; any other line closes it.
  let gap = false;
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (line === "") {
      if (list === null) close();
      else gap = true;
      continue;
    }
    if (gap) {
      gap = false;
      if (listItem(line) === null) close();
    }
    if (
      headings &&
      line.startsWith(headingMark) &&
      line.slice(headingMark.length).trim() !== ""
    ) {
      close();
      out.push({ kind: "h", text: line.slice(headingMark.length).trim() });
      continue;
    }
    const item = listItem(line);
    if (item !== null) {
      if (list === null || list.kind !== item.kind) {
        close();
        list =
          item.kind === "ul"
            ? { kind: "ul", items: [] }
            : { kind: "ol", start: item.n, items: [] };
        out.push(list);
      }
      list.items.push(item.text);
      continue;
    }
    if (list !== null) {
      list.items[list.items.length - 1] += " " + line;
      continue;
    }
    para.push(line);
  }
  close();
  return out;
}

/** plain is laid-out text as Doc shows it, what a search reads, so a
 *  statement kept is one with a mark: read by blocks itself (an item's
 *  continuation joined to it, a #### only where headings is set), each
 *  block's text as runs leaves it (the ** that pair and the code spans'
 *  backticks gone), one block a line. */
export function plain(text: string, headings = true): string {
  return blocks(text, headings)
    .flatMap((b) => (b.kind === "p" || b.kind === "h" ? [b.text] : b.items))
    .map((t) =>
      runs(t)
        .map((r) => r.text)
        .join(""),
    )
    .join("\n");
}

/** A run of a line of laid-out text: prose or code, bold or not. */
export type Run = { text: string; code: boolean; bold: boolean };

/** runs cuts a paragraph into its code spans (codeSpans) and the **bold**
 *  around them: a ** pairs with the next one on its line, outside code,
 *  with something between; an unpaired ** stays as written. Bold may hold
 *  a code span ("**`strict`** gilt"). */
export function runs(p: string): Run[] {
  type Item = { mark: true } | { mark: false; text: string; code: boolean };
  const items: Item[] = [];
  for (const s of codeSpans(p)) {
    if (s.code) {
      items.push({ mark: false, text: s.text, code: true });
      continue;
    }
    s.text.split("**").forEach((t, i) => {
      if (i > 0) items.push({ mark: true });
      if (t !== "") items.push({ mark: false, text: t, code: false });
    });
  }
  const out: Run[] = [];
  const push = (text: string, code: boolean, bold: boolean) => {
    const prev = out[out.length - 1];
    if (prev !== undefined && !code && !prev.code && prev.bold === bold)
      prev.text += text;
    else out.push({ text, code, bold });
  };
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (!it.mark) {
      push(it.text, it.code, false);
      continue;
    }
    const j = items.findIndex((x, k) => k > i && x.mark);
    const inner = items.slice(i + 1, j < 0 ? i + 1 : j);
    const pairs =
      j > i + 1 && inner.every((x) => !x.mark && !x.text.includes("\n"));
    if (!pairs) {
      push("**", false, false);
      continue;
    }
    for (const x of inner) if (!x.mark) push(x.text, x.code, true);
    i = j;
  }
  return out;
}

/** A run of a paragraph: prose, or an identifier the model quoted in
 *  backticks. */
export type Span = { text: string; code: boolean };

/** codeSpans cuts a paragraph into prose and code, the only markup the
 *  wiki takes: the way lang.SwissSS (swiss.go, swissLine) reads it, so
 *  what it left unconverted is what shows as code. Line by line, a run of
 *  backticks opens a span the next same run on that line closes; a run
 *  never closed leaves the rest of its line text, itself included. */
export function codeSpans(p: string): Span[] {
  const out: Span[] = [];
  const text = (t: string) => {
    if (t === "") return;
    const prev = out[out.length - 1];
    if (prev !== undefined && !prev.code) prev.text += t;
    else out.push({ text: t, code: false });
  };
  for (const [n, line] of p.split("\n").entries()) {
    if (n > 0) text("\n");
    let i = 0;
    while (i < line.length) {
      const open = line.indexOf("`", i);
      if (open < 0) {
        text(line.slice(i));
        break;
      }
      text(line.slice(i, open));
      let j = open;
      while (line[j] === "`") j++;
      const run = line.slice(open, j);
      const close = line.indexOf(run, j);
      if (close < 0) {
        text(line.slice(open));
        break;
      }
      // CommonMark's padding: one blank either side, as markup.CodeSpan
      // writes around code holding a backtick, is not the code's.
      const code = line.slice(j, close);
      const padded =
        code.length > 2 &&
        code.startsWith(" ") &&
        code.endsWith(" ") &&
        code.trim() !== "";
      out.push({ text: padded ? code.slice(1, -1) : code, code: true });
      i = close + run.length;
    }
  }
  return out;
}

export type Tone = "ok" | "warn" | "fail" | "none" | "quiet";

/** changeTone colours a change kind: a bugfix stands out, the rest is
 *  furniture. */
export function changeTone(kind: string): Tone {
  return kind === "bugfix" ? "fail" : "none";
}

/** changeLabel is the German word for a change kind. */
export function changeLabel(kind: string): string {
  const table: Record<string, string> = S.changeKind;
  return table[kind] ?? (kind === "" ? S.changeKind.unknown : kind);
}

/** causeLabel is the German word for a bugfix's cause class. */
export function causeLabel(kind: string): string {
  const table: Record<string, string> = S.causeKind;
  return table[kind] ?? kind;
}

/** problemTone grades a bugfix count: one is furniture, two is ochre,
 *  three and more is red. */
export function problemTone(n: number): Tone {
  return n >= 3 ? "fail" : n === 2 ? "warn" : "none";
}

/** stateTone colours a clone's state: ready is green, a run is ochre, a
 *  failure red, a vanished repository quiet. */
export function stateTone(state: string): Tone {
  switch (state) {
    case "ready":
      return "ok";
    case "pending":
    case "syncing":
      return "warn";
    case "failed":
      return "fail";
    default:
      return "quiet";
  }
}

/** stateLabel is the German word for a clone's state. */
export function stateLabel(state: string): string {
  const table: Record<string, string> = S.sources.state;
  return table[state] ?? state;
}

/** bytes renders a size the way a disk is talked about: 1.4 GB, 320 MB,
 *  12 kB; one decimal from a megabyte up. */
export function bytes(n: number): string {
  if (n < 1000) return `${n} B`;
  if (n < 1_000_000) return `${Math.round(n / 1000)} kB`;
  if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(1)} MB`;
  return `${(n / 1_000_000_000).toFixed(1)} GB`;
}

/** shortSHA is the seven-character form of a commit. */
export function shortSHA(sha: string): string {
  return sha.length > 7 ? sha.slice(0, 7) : sha;
}

/** slugFromURL is the repository's name in a clone URL: its last path
 *  segment without a trailing slash or ".git", as the backend derives it
 *  (store.SlugFromURL); "" when the URL has none. */
export function slugFromURL(url: string): string {
  let path: string;
  try {
    path = new URL(url.trim()).pathname;
  } catch {
    return "";
  }
  path = path
    .replace(/\/+$/, "")
    .replace(/\.git$/, "")
    .replace(/\/+$/, "");
  return path.slice(path.lastIndexOf("/") + 1);
}
