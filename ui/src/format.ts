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

/** money shows a USD string as the API sends it (3 decimals, never a
 *  float) with the sign in front; absent means the gateway did not price
 *  the call. */
export function money(usd: string | undefined | null): string {
  if (usd === undefined || usd === null || usd === "") return S.note.unpriced;
  return `$${usd}`;
}

/** share is a percentage of a whole, rounded, or null without a whole. */
export function share(
  part: number | null,
  whole: number | null,
): number | null {
  if (part === null || whole === null || whole <= 0) return null;
  return Math.round((part / whole) * 100);
}

/** paragraphs splits a statement's text on blank lines. Never markdown:
 *  the text is prose, and a line that happens to start with a dash or a
 *  hash must not turn into a list or a heading. */
export function paragraphs(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p.length > 0);
}

/** A run of a paragraph: prose, or an identifier the model quoted in
 *  backticks. */
export type Span = { text: string; code: boolean };

/** codeSpans cuts a paragraph into prose and code: a backtick pair on one
 *  line around something that is not a backtick, the only markup the wiki
 *  takes. The model quotes paths and names that way; anything else stays
 *  text, and a lone backtick shows as itself. */
export function codeSpans(p: string): Span[] {
  const out: Span[] = [];
  let last = 0;
  for (const m of p.matchAll(/`([^`\n]+)`/g)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ text: p.slice(last, at), code: false });
    out.push({ text: m[1], code: true });
    last = at + m[0].length;
  }
  if (last < p.length) out.push({ text: p.slice(last), code: false });
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
