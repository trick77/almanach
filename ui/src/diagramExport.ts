// Copied from rongo (ui/src/diagramExport.ts), kept in step by hand.
// The SVG and PNG files only: the mermaid half (toMermaid, mermaidize) stays
// in rongo, and fileName takes the IR's kind instead of a mermaid source.

/**
 * Taking a diagram out of Almanach: as a standalone .svg file.
 *
 * The file is the drawing's own SVG with a ground put under it.
 */

// ---- SVG file ----

/** toSvgFile turns a rendered diagram into a file that stands on its own.
 * The renderer already inlines its stylesheet and a viewBox, so the string
 * lacks only its ground: on the page the dark panel is the card's, not the
 * SVG's, and in a file a viewer paints white behind it and the warm off-white
 * labels all but disappear. The ground is read off the card rather than
 * named here, so there is one copy of the token. */
export function toSvgFile(
  drawn: { svg: string },
  card: Element | null,
): string {
  const ground = card ? groundOf(card) : null;
  if (ground === null) return drawn.svg;
  return withGround(drawn.svg, ground);
}

/** withGround paints a colour under the whole drawing, as its first child so
 * it sits behind everything else. */
export function withGround(svg: string, ground: string): string {
  return svg.replace(/<svg\b[^>]*>/, (open) => {
    // The ground covers the viewBox, not the user-space origin: the
    // renderer's viewBox starts left of and above 0 (a sequence at y -25,
    // a flowchart at minus its padding), and a rect at 0,0 would leave a
    // bare band along the top and left edge of the file.
    const vb = /viewBox="([^"]*)"/.exec(open);
    const [x, y, w, h] = vb
      ? vb[1].trim().split(/[\s,]+/)
      : ["0", "0", "100%", "100%"];
    return `${open}<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="${ground}"/>`;
  });
}

/** groundOf is the first real background behind the drawing, walked up from
 * the card. */
function groundOf(el: Element): string | null {
  for (let node: Element | null = el; node; node = node.parentElement) {
    const bg = getComputedStyle(node).backgroundColor;
    if (bg !== "" && bg !== "transparent" && !/^rgba\(.*,\s*0\)$/.test(bg))
      return bg;
  }
  return null;
}

/** download hands the browser a file: an SVG as its text, anything else as
 * the blob it already is. */
export function download(name: string, data: string | Blob): void {
  const blob =
    typeof data === "string"
      ? new Blob([data], { type: "image/svg+xml;charset=utf-8" })
      : data;
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/** fileName is what the download is called: the kind of picture it is, so a
 * folder of them stays sortable. */
export function fileName(kind: "flow" | "sequence" | "er" | ""): string {
  if (kind === "") return "almanach-diagram.svg";
  return `almanach-${kind === "flow" ? "flowchart" : kind}-diagram.svg`;
}

// ---- PNG file ----

/** svgSize is the size a drawing asks for: the width and height its root
 * element states, else its viewBox. */
export function svgSize(svg: string): { w: number; h: number } | null {
  const open = /<svg\b[^>]*>/.exec(svg)?.[0] ?? "";
  const w = parseFloat(/\swidth="([\d.]+)(?:px)?"/.exec(open)?.[1] ?? "");
  const h = parseFloat(/\sheight="([\d.]+)(?:px)?"/.exec(open)?.[1] ?? "");
  if (w > 0 && h > 0) return { w, h };
  const vb = /viewBox="([^"]*)"/
    .exec(open)?.[1]
    .trim()
    .split(/[\s,]+/)
    .map(Number);
  if (vb && vb.length === 4 && vb[2] > 0 && vb[3] > 0)
    return { w: vb[2], h: vb[3] };
  return null;
}

/** toXml writes a drawing as XML. DOMPurify hands back HTML, which is what
 * the page draws, but a file is read as XML: a no-break space in a label
 * came out as &nbsp;, an entity no SVG viewer defines, and neither the file
 * nor the PNG made from it would open. */
export function toXml(svg: string): string {
  const el = new DOMParser()
    .parseFromString(svg, "text/html")
    .querySelector("svg");
  return el ? new XMLSerializer().serializeToString(el) : svg;
}

/** A canvas past its browser's limits draws nothing and encodes to nothing,
 * silently. Desktop browsers hold a 16384 side (the area kept at half of
 * what that side allows); iOS Safari stops at about 16.7M pixels, whatever
 * the side. */
type CanvasLimit = { side: number; area: number };
const desktopCanvas: CanvasLimit = { side: 16384, area: 16384 * 8192 };
export const phoneCanvas: CanvasLimit = { side: 4096, area: 4096 * 4096 };

/** pngScale is how much larger than 1:1 the PNG is drawn: twice, for a
 * picture that stays sharp on a dense screen, less where that would not fit
 * the canvas. A diagram too large even at 1:1 is drawn smaller rather than
 * not at all. */
export function pngScale(
  w: number,
  h: number,
  limit: CanvasLimit = desktopCanvas,
): number {
  return Math.min(
    2,
    limit.side / w,
    limit.side / h,
    Math.sqrt(limit.area / (w * h)),
  );
}

/** toPng paints a drawing on white, the paper it was drawn for, and encodes
 * it. Through a data: URL, not a blob: URL, which some browsers count as
 * foreign content and then refuse to read back off the canvas. The desktop
 * size is tried first, a phone's second: which one a browser holds is only
 * known by asking it. */
export async function toPng(svg: string): Promise<Blob> {
  const size = svgSize(svg);
  if (size === null) throw new Error("the drawing states no size");
  const img = await new Promise<HTMLImageElement>((resolve, reject) => {
    const i = new Image();
    i.onload = () => resolve(i);
    i.onerror = () => reject(new Error("the drawing could not be read"));
    i.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  });
  let last: unknown;
  for (const limit of [desktopCanvas, phoneCanvas]) {
    const scale = pngScale(size.w, size.h, limit);
    try {
      return await paint(
        img,
        Math.max(1, Math.round(size.w * scale)),
        Math.max(1, Math.round(size.h * scale)),
      );
    } catch (e) {
      last = e;
    }
  }
  throw last;
}

/** paint draws one PNG at one size. Whatever the canvas throws (a refused
 * size, a tainted read) rejects, so the reader is told rather than left
 * clicking a button that does nothing. */
function paint(img: HTMLImageElement, w: number, h: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    try {
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) throw new Error("the browser gave no canvas");
      ctx.fillStyle = "#FFFFFF";
      ctx.fillRect(0, 0, w, h);
      ctx.drawImage(img, 0, 0, w, h);
      canvas.toBlob(
        (b) =>
          b
            ? resolve(b)
            : reject(new Error("the picture is too large for a PNG")),
        "image/png",
      );
    } catch (e) {
      reject(e);
    }
  });
}
