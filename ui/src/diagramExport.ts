// Copied from rongo (ui/src/diagramExport.ts), kept in step by hand.
// The SVG file only, what Almanach calls: the PNG and the mermaid halves
// stay in rongo, and fileName takes the IR's kind instead of a mermaid
// source.

/**
 * Taking a diagram out of Almanach: as a standalone .svg file, the
 * drawing's own SVG with a ground put under it.
 */

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
