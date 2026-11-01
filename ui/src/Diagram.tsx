import { useEffect, useRef, useState, type ReactNode } from "react";
import type { DiagramIR, FlowIR } from "./diagramIR";
import { fits } from "./diagramIR";
import { layoutFlow, type Elk, type FlowLayout } from "./diagramLayout";
import { drawER, drawFlow, drawSequence } from "./diagramDraw";
import { layoutER } from "./diagramER";
import { layoutSequence } from "./diagramSequence";
import { palette } from "./diagramTheme";
import { download, fileName, toSvgFile } from "./diagramExport";
import DiagramView, { DiagramSvg } from "./DiagramView";
import { DownloadIcon, ExpandIcon } from "./icons";
import { Link } from "./ui";
import { S } from "./strings";

/**
 * One diagram on a page: the backend's validated IR drawn by rongo's code
 * (layout, drawing, theme), with rongo's two ways out of it, the full view
 * and the file. The glue is rongo's editorial() without the mermaid parse:
 * the IR arrives as JSON, there is nothing to parse.
 *
 * links names the page a flow node opens, null for none: the system map's
 * repository nodes. The drawing is a string without ids on its nodes, so a
 * link is an anchor laid over the node's box from the layout, in percent of
 * the drawing, and the copied drawing stays as rongo wrote it.
 */

type Drawn = { svg: string; layout: FlowLayout | null };

/** The layout engine is its own chunk, loaded on the first flow or entity
 * model: it is large, and a sequence never uses it. */
let elk: Promise<Elk> | null = null;
function engine(): Promise<Elk> {
  elk ??= import("elkjs/lib/elk.bundled.js").then(
    (m) => new m.default(),
    (e: unknown) => {
      // A chunk gone after a redeploy is tried again by the next diagram.
      elk = null;
      throw e;
    },
  );
  return elk;
}

/** drawIR lays a diagram out and writes it as SVG. ids prefixes the marker
 * ids: two drawings on one page must not share one. */
export async function drawIR(ir: DiagramIR, ids: string): Promise<Drawn> {
  if (ir.kind === "sequence")
    return {
      svg: drawSequence(layoutSequence(ir), palette(), ids),
      layout: null,
    };
  const e = await engine();
  if (ir.kind === "er")
    return { svg: drawER(await layoutER(ir, e), palette()), layout: null };
  const layout = await layoutFlow(ir, e);
  return { svg: drawFlow(layout, palette(), ids), layout };
}

/** titledZones is a map with its zones titled by id from strings.ts, not
 *  by the model: the system map's and a repository's, each its own set. */
export function titledZones(
  map: FlowIR,
  titles: Record<string, string>,
): FlowIR {
  return {
    ...map,
    zones: map.zones.map((z) => ({ ...z, title: titles[z.id] ?? z.title })),
  };
}

let seq = 0;

type State = { key: string; drawn: Drawn | null; failed: boolean };

export function Diagram({
  ir,
  title,
  links,
}: {
  ir: DiagramIR;
  title: string;
  links?: (id: string) => string | null;
}): ReactNode {
  const [ids] = useState(() => `almanach-diagram-${++seq}`);
  const key = JSON.stringify(ir);
  const [state, setState] = useState<State>({
    key: "",
    drawn: null,
    failed: false,
  });
  const [full, setFull] = useState(false);
  const card = useRef<HTMLDivElement>(null);
  // Every diagram the backend stores fits: its caps are below these. One
  // that does not is a defect, said rather than drawn crowded.
  const ok = fits(ir);

  useEffect(() => {
    if (!ok) return;
    let live = true;
    drawIR(JSON.parse(key) as DiagramIR, ids).then(
      (drawn) => live && setState({ key, drawn, failed: false }),
      () => live && setState({ key, drawn: null, failed: true }),
    );
    return () => {
      live = false;
    };
  }, [key, ids, ok]);

  if (!ok || (state.key === key && state.failed))
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.failed}</p>;
  if (state.key !== key || state.drawn === null)
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.pending}</p>;
  const { svg, layout } = state.drawn;

  function save() {
    download(fileName(ir.kind), toSvgFile({ svg }, card.current));
  }

  // The drawing brings its canvas, so the card is only a frame for the two
  // buttons, as wide as the drawing and never wider than the column.
  return (
    <figure className="my-3">
      <div ref={card} className="w-max max-w-full">
        <div className="mb-1 flex items-center justify-end gap-0.5">
          <button
            type="button"
            onClick={() => setFull(true)}
            title={S.diagram.full}
            aria-label={S.diagram.fullOf(title)}
            className="grid h-7 w-7 place-items-center rounded-ui-sm text-faint hover:bg-active hover:text-ink-dim"
          >
            <ExpandIcon />
          </button>
          <button
            type="button"
            onClick={save}
            title={S.diagram.download}
            aria-label={S.diagram.downloadOf(title)}
            className="grid h-7 w-7 place-items-center rounded-ui-sm text-faint hover:bg-active hover:text-ink-dim"
          >
            <DownloadIcon />
          </button>
        </div>
        <div className="relative">
          <DiagramSvg svg={svg} title={title} />
          {layout !== null && links && (
            <NodeLinks layout={layout} links={links} />
          )}
        </div>
        {full && (
          <DiagramView
            title={title}
            kind={ir.kind}
            svg={svg}
            onClose={() => setFull(false)}
            overlay={
              layout !== null && links ? (
                <NodeLinks
                  layout={layout}
                  links={links}
                  onFollow={() => setFull(false)}
                />
              ) : undefined
            }
          />
        )}
      </div>
    </figure>
  );
}

/** NodeLinks lays an anchor over every node links names a page for, placed
 * by the layout's own box so it scales with the drawing. onFollow closes
 * the full view a link is followed from. */
function NodeLinks({
  layout,
  links,
  onFollow,
}: {
  layout: FlowLayout;
  links: (id: string) => string | null;
  onFollow?: () => void;
}) {
  const pct = (v: number, of: number) => `${(v / of) * 100}%`;
  return (
    // contents: the anchors stay placed by the drawing's box. A plain click
    // is the one the app follows (its handler prevents the default); a new
    // tab leaves the full view open.
    <span
      className="contents"
      onClick={(e) => {
        if (e.defaultPrevented) onFollow?.();
      }}
    >
      {layout.nodes.map((n) => {
        const to = links(n.id);
        if (to === null) return null;
        return (
          <Link
            key={n.id}
            to={to}
            title={S.diagram.repoLink(n.id)}
            className="absolute rounded-[6px] hover:bg-accent/10 hover:outline-2 hover:outline-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong"
            style={{
              left: pct(n.x, layout.width),
              top: pct(n.y, layout.height),
              width: pct(n.w, layout.width),
              height: pct(n.h, layout.height),
            }}
          >
            <span className="sr-only">{S.diagram.repoLink(n.id)}</span>
          </Link>
        );
      })}
    </span>
  );
}
