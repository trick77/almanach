// Copied from rongo (ui/src/diagramExport.test.ts), kept in step by hand.
// The mermaid half (toMermaid, mermaidize) stays in rongo.
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import {
  toSvgFile,
  fileName,
  withGround,
  svgSize,
  pngScale,
  toPng,
  toXml,
  phoneCanvas,
} from "./diagramExport";

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

describe("svgSize", () => {
  it("reads the pixel size the engine wrote", () => {
    expect(
      svgSize('<svg width="120px" height="80px" viewBox="0 0 240 160">'),
    ).toEqual({ w: 120, h: 80 });
  });

  it("falls back to the viewBox, and knows when there is neither", () => {
    expect(svgSize('<svg viewBox="-5 -5 240 160">')).toEqual({
      w: 240,
      h: 160,
    });
    expect(svgSize("<svg>")).toBeNull();
  });
});

describe("pngScale", () => {
  it("draws at twice the size where the canvas allows it", () => {
    expect(pngScale(600, 400)).toBe(2);
  });

  it("stays inside what a browser canvas holds", () => {
    // The file that first overflowed the engine's own cap.
    const s = pngScale(4788, 13531);
    expect(13531 * s).toBeLessThanOrEqual(16384);
    expect(s).toBeGreaterThan(1);
    expect(40000 * pngScale(40000, 100)).toBeLessThanOrEqual(16384);
  });

  it("fits a phone's smaller canvas when asked to", () => {
    const s = pngScale(2500, 2000, phoneCanvas);
    expect(2500 * s * 2000 * s).toBeLessThanOrEqual(phoneCanvas.area);
  });
});

describe("toXml", () => {
  it("writes the drawing as XML, which a file must be", () => {
    // What DOMPurify hands back is HTML: a no-break space comes out as
    // &nbsp;, an entity no SVG viewer knows.
    const html =
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><text>a&nbsp;b</text><br></svg>';
    const out = toXml(html);
    expect(out).not.toContain("&nbsp;");
    const doc = new DOMParser().parseFromString(out, "image/svg+xml");
    expect(doc.querySelector("parsererror")).toBeNull();
    expect(doc.querySelector("text")?.textContent).toBe("a b");
    expect(out).toContain('viewBox="0 0 4 4"');
  });
});

describe("toPng", () => {
  let blob: Blob | null;
  let fail: boolean;
  const ctx = { fillStyle: "", fillRect: vi.fn(), drawImage: vi.fn() };

  beforeEach(() => {
    blob = new Blob(["png"], { type: "image/png" });
    fail = false;
    ctx.fillStyle = "";
    ctx.fillRect.mockReset();
    ctx.drawImage.mockReset();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      ctx as unknown as CanvasRenderingContext2D,
    );
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(
      (cb: BlobCallback) => cb(blob),
    );
    // jsdom loads no images: this one answers as soon as it has a source.
    vi.stubGlobal(
      "Image",
      class {
        onload: (() => void) | null = null;
        onerror: (() => void) | null = null;
        set src(_v: string) {
          setTimeout(() => (fail ? this.onerror?.() : this.onload?.()), 0);
        }
      },
    );
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("paints the drawing on white at the scale the canvas allows", async () => {
    expect(await toPng('<svg width="100px" height="50px"></svg>')).toBe(blob);
    expect(ctx.fillStyle).toBe("#FFFFFF");
    expect(ctx.fillRect).toHaveBeenCalledWith(0, 0, 200, 100);
    expect(ctx.drawImage).toHaveBeenCalledWith(
      expect.anything(),
      0,
      0,
      200,
      100,
    );
  });

  it("fails when the picture has no size, will not load, or will not encode", async () => {
    await expect(toPng("<svg></svg>")).rejects.toThrow();
    fail = true;
    await expect(toPng('<svg width="10" height="10"></svg>')).rejects.toThrow();
    fail = false;
    blob = null;
    await expect(toPng('<svg width="10" height="10"></svg>')).rejects.toThrow();
  });

  it("fails rather than hangs when the canvas throws", async () => {
    ctx.drawImage.mockImplementation(() => {
      throw new Error("SecurityError");
    });
    await expect(toPng('<svg width="10" height="10"></svg>')).rejects.toThrow(
      "SecurityError",
    );
  });

  it("draws again at a phone's size when the large canvas is refused", async () => {
    vi.mocked(HTMLCanvasElement.prototype.getContext)
      .mockReturnValueOnce(null)
      .mockReturnValue(ctx as unknown as CanvasRenderingContext2D);
    expect(await toPng('<svg width="3000" height="3000"></svg>')).toBe(blob);
    const [, , w, h] = ctx.fillRect.mock.calls[0];
    expect(w * h).toBeLessThanOrEqual(phoneCanvas.area);
    expect(w).toBeGreaterThan(3000);
  });
});
