import { describe, expect, it } from "vitest";
import {
  causeLabel,
  changeLabel,
  changeTone,
  codeSpans,
  day,
  dayTime,
  money,
  paragraphs,
  problemTone,
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
  it("shows the API's string with a sign, or unpriced", () => {
    expect(money("0.012")).toBe("$0.012");
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
