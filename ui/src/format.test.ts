import { describe, expect, it } from "vitest";
import {
  blocks,
  causeLabel,
  changeLabel,
  changeTone,
  codeSpans,
  day,
  dayTime,
  money,
  paragraphs,
  plain,
  problemTone,
  runs,
  share,
  shortDay,
} from "./format";
import { fmtInt } from "./strings";

describe("dates", () => {
  it("prints the Swiss way", () => {
    const iso = new Date(2026, 9, 28, 14, 3).toISOString();
    expect(day(iso)).toBe("28.10.2026");
    expect(dayTime(iso)).toBe("28.10.2026 14:03");
    expect(shortDay(iso)).toBe("28.10.");
    expect(day("nonsense")).toBe("nonsense");
  });
});

describe("money and shares", () => {
  it("shows the API's string at 2 decimals, rounded half up, or unpriced", () => {
    expect(money("0.012")).toBe("$0.01");
    expect(money("0.015")).toBe("$0.02");
    expect(money("0.004")).toBe("$0.00");
    expect(money("0.995")).toBe("$1.00");
    expect(money("9.995")).toBe("$10.00");
    expect(money("1284.733")).toBe("$1284.73");
    expect(money("3")).toBe("$3.00");
    expect(money("0.5")).toBe("$0.50");
    expect(money("-0.004")).toBe("$0.00");
    expect(money("-1.236")).toBe("-$1.24");
    expect(money(undefined)).toBe("nicht bepreist");
    expect(money("")).toBe("nicht bepreist");
  });
  it("rounds a share and refuses one without a whole", () => {
    expect(share(90000, 100000)).toBe(90);
    expect(share(1, 3)).toBe(33);
    expect(share(null, 100)).toBeNull();
    expect(share(5, 0)).toBeNull();
  });
  it("groups digits with a thin space", () => {
    expect(fmtInt(1912)).toBe("1\u202f912");
    expect(fmtInt(42)).toBe("42");
    expect(fmtInt(1234567)).toBe("1\u202f234\u202f567");
    expect(fmtInt(-1000)).toBe("-1\u202f000");
  });
});

describe("paragraphs", () => {
  it("splits on blank lines only, never on markup", () => {
    expect(paragraphs("Eins.\n\nZwei.\n \n- drei")).toEqual([
      "Eins.",
      "Zwei.",
      "- drei",
    ]);
    expect(paragraphs("  \n\n")).toEqual([]);
  });
});

describe("blocks", () => {
  it("reads paragraphs, flat lists and sub-headings", () => {
    expect(
      blocks(
        "Erst.\nZeile.\n\n#### Ablauf\n\n- eins\nweiter\n- zwei\n\n3. drei\n4. vier\n\nDanach.",
      ),
    ).toEqual([
      { kind: "p", text: "Erst.\nZeile." },
      { kind: "h", text: "Ablauf" },
      { kind: "ul", items: ["eins weiter", "zwei"] },
      { kind: "ol", start: 3, items: ["drei", "vier"] },
      { kind: "p", text: "Danach." },
    ]);
  });
  it("keeps one list over a blank line between items, as markup.Normalize", () => {
    expect(blocks("- a\n\n- b\n\nDanach.")).toEqual([
      { kind: "ul", items: ["a", "b"] },
      { kind: "p", text: "Danach." },
    ]);
  });
  it("starts a list under a paragraph line, a new one on a change of kind", () => {
    expect(blocks("Fälle:\n- a\n1. b")).toEqual([
      { kind: "p", text: "Fälle:" },
      { kind: "ul", items: ["a"] },
      { kind: "ol", start: 1, items: ["b"] },
    ]);
  });
  it("takes nothing outside the subset", () => {
    expect(
      blocks("## Zwei\n> zitiert\n* Stern\n1) Klammer\n-5 Grad\n| a |"),
    ).toEqual([
      {
        kind: "p",
        text: "## Zwei\n> zitiert\n* Stern\n1) Klammer\n-5 Grad\n| a |",
      },
    ]);
    expect(blocks("  \n\n")).toEqual([]);
  });
});

describe("runs", () => {
  it("pairs ** on a line, around code too", () => {
    expect(runs("Die **eine** Regel: **`strict`** gilt.")).toEqual([
      { text: "Die ", code: false, bold: false },
      { text: "eine", code: false, bold: true },
      { text: " Regel: ", code: false, bold: false },
      { text: "strict", code: true, bold: true },
      { text: " gilt.", code: false, bold: false },
    ]);
  });
  it("leaves an unpaired, an empty or a line-crossing ** and code's ** as written", () => {
    expect(runs("a ** b")).toEqual([
      { text: "a ** b", code: false, bold: false },
    ]);
    expect(runs("leer **** hier")).toEqual([
      { text: "leer **** hier", code: false, bold: false },
    ]);
    expect(runs("**a\nb**")).toEqual([
      { text: "**a\nb**", code: false, bold: false },
    ]);
    expect(runs("`a**b**c`")).toEqual([
      { text: "a**b**c", code: true, bold: false },
    ]);
  });
});

// The cases of markup_test.go TestPlain: the same marks read, the code
// spans' backticks dropped here (Doc draws them as mono, never as `).
describe("plain", () => {
  it("drops the layout's marks, code's text kept", () => {
    expect(plain("#### Ablauf\n\n- **eins**\n12. zwei `a**b`\n\nText.")).toBe(
      "Ablauf\neins\nzwei a**b\nText.",
    );
    expect(plain("## Zwei\n* Stern\n1) Klammer")).toBe(
      "## Zwei\n* Stern\n1) Klammer",
    );
    expect(plain("a ** b\nleer **** hier\n**a\nb**")).toBe(
      "a ** b\nleer **** hier\n**a\nb**",
    );
    expect(plain("**`strict`** gilt, **x")).toBe("strict gilt, **x");
  });
  it("reads as Doc renders: an item with its continuation", () => {
    expect(plain("- Bilder\nund Texte")).toBe("Bilder und Texte");
  });
  it("reads only the stored item form, as markup.StoredItem", () => {
    expect(blocks("-  zwei Blanks\n1.  auch")).toEqual([
      { kind: "p", text: "-  zwei Blanks\n1.  auch" },
    ]);
    // A rule is no item.
    expect(blocks("- - -")).toEqual([{ kind: "p", text: "- - -" }]);
  });
});

describe("codeSpans", () => {
  it("cuts backtick pairs out as code, the rest stays text", () => {
    expect(codeSpans("Der Prozess `backend/` rendert `ui/`.")).toEqual([
      { text: "Der Prozess ", code: false },
      { text: "backend/", code: true },
      { text: " rendert ", code: false },
      { text: "ui/", code: true },
      { text: ".", code: false },
    ]);
  });
  it("leaves a lone, an empty or a line-crossing backtick as itself", () => {
    expect(codeSpans("ein ` allein")).toEqual([
      { text: "ein ` allein", code: false },
    ]);
    expect(codeSpans("leer `` hier")).toEqual([
      { text: "leer `` hier", code: false },
    ]);
    expect(codeSpans("`a\nb`")).toEqual([{ text: "`a\nb`", code: false }]);
    expect(codeSpans("")).toEqual([]);
  });
  it("closes a run with the same run, as lang.SwissSS does", () => {
    expect(codeSpans("Die Option ``a`b`` setzt")).toEqual([
      { text: "Die Option ", code: false },
      { text: "a`b", code: true },
      { text: " setzt", code: false },
    ]);
    // An unclosed run leaves the rest of its line text, a later pair too.
    expect(codeSpans("x `` y `z`\n`w`")).toEqual([
      { text: "x `` y `z`\n", code: false },
      { text: "w", code: true },
    ]);
  });
  it("drops CommonMark's one-blank padding, as markup.CodeSpan writes it", () => {
    expect(codeSpans("`` make `x` ``")).toEqual([
      { text: "make `x`", code: true },
    ]);
    expect(codeSpans("`  `")).toEqual([{ text: "  ", code: true }]);
  });
});

describe("labels and tones", () => {
  it("names kinds in German and tones a bugfix", () => {
    expect(changeLabel("bugfix")).toBe("Bugfix");
    expect(changeLabel("")).toBe("unbekannt");
    expect(changeLabel("hotfix")).toBe("hotfix");
    expect(changeTone("bugfix")).toBe("fail");
    expect(changeTone("feature")).toBe("none");
    expect(causeLabel("concurrency")).toBe("Nebenläufigkeit");
    expect(causeLabel("x")).toBe("x");
    expect(problemTone(1)).toBe("none");
    expect(problemTone(2)).toBe("warn");
    expect(problemTone(4)).toBe("fail");
  });
});
