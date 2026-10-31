// Copied from rongo (ui/src/diagramExport.test.ts), kept in step by hand.
// The SVG file only; the PNG and mermaid halves stay in rongo.
import { describe, it, expect, vi, afterEach } from "vitest";
import { toSvgFile, fileName, withGround } from "./diagramExport";

describe("toSvgFile", () => {
  afterEach(() => {
    document.head
      .querySelectorAll("style[data-test]")
      .forEach((s) => s.remove());
  });

  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="-8 -25 240 90"><style>.x{}</style><rect width="10" height="10"/></svg>';

  function card(): HTMLElement {
    const style = document.createElement("style");
    style.setAttribute("data-test", "1");
    style.textContent = ".card { background-color: #1b1b1a; }";
    document.head.appendChild(style);
    const host = document.createElement("div");
    host.className = "card";
    document.body.appendChild(host);
    return host;
  }

  it("carries the card's ground, so the pale labels are not lost on white", () => {
    const out = toSvgFile({ svg }, card());
    // Over the viewBox, which starts above and left of the origin.
    expect(out).toContain(
      '<rect x="-8" y="-25" width="240" height="90" fill="rgb(27, 27, 26)"/>',
    );
    // First, so it sits behind the drawing rather than over it.
    expect(out.indexOf('fill="rgb(27, 27, 26)"')).toBeLessThan(
      out.indexOf('width="10"'),
    );
  });

  it("leaves the renderer's file alone otherwise", () => {
    expect(toSvgFile({ svg }, null)).toBe(svg);
    const out = toSvgFile({ svg }, card());
    expect(out).toContain('viewBox="-8 -25 240 90"');
    expect(out).toContain("<style>.x{}</style>");
  });
});

describe("fileName", () => {
  it("names the file after the kind of picture", () => {
    expect(fileName("sequence")).toBe("almanach-sequence-diagram.svg");
    expect(fileName("flow")).toBe("almanach-flowchart-diagram.svg");
    expect(fileName("er")).toBe("almanach-er-diagram.svg");
    expect(fileName("")).toBe("almanach-diagram.svg");
  });
});

describe("download", () => {
  it("hands the browser a blob and lets it go again", async () => {
    const { download } = await import("./diagramExport");
    const create = vi.fn(() => "blob:x");
    const revoke = vi.fn();
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    Object.defineProperty(URL, "createObjectURL", {
      value: create,
      configurable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      value: revoke,
      configurable: true,
    });
    download("d.svg", "<svg/>");
    expect(create).toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
    expect(revoke).toHaveBeenCalledWith("blob:x");
    click.mockRestore();
  });

  it("hands over a blob it was given as it is", async () => {
    const { download } = await import("./diagramExport");
    const create = vi.fn((_b: Blob) => "blob:y");
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(() => {});
    Object.defineProperty(URL, "createObjectURL", {
      value: create,
      configurable: true,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      value: vi.fn(),
      configurable: true,
    });
    const png = new Blob(["x"], { type: "image/png" });
    download("d.png", png);
    expect(create).toHaveBeenCalledWith(png);
    click.mockRestore();
  });
});

describe("withGround", () => {
  it("lays the colour over the viewBox, behind the drawing", () => {
    expect(
      withGround('<svg viewBox="0 0 30 20"><rect width="4"/></svg>', "#FFFFFF"),
    ).toBe(
      '<svg viewBox="0 0 30 20"><rect x="0" y="0" width="30" height="20" fill="#FFFFFF"/><rect width="4"/></svg>',
    );
  });
});
