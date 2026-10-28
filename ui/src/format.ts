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
