import { useEffect, useState, type ReactNode } from "react";
import {
  drawER,
  drawFlow,
  drawSequence,
  drawSwimlane,
  engine,
  fileName,
  layoutER,
  layoutBands,
  layoutFlow,
  layoutFlowChecked,
  layoutSequence,
  margin,
  palette,
  type ERIR,
  type FlowIR,
  type FlowLayout,
  type Rendered,
  type SequenceIR,
  type SwimlaneLayout,
} from "@trick77/diagram";
import {
  DiagramCard,
  DiagramView,
  classes,
  type Classes,
  type Icons,
} from "@trick77/diagram/react";
import { downward, fits } from "./diagramIR";
import OverlayShell from "./OverlayShell";
import { DownloadIcon, ExpandIcon } from "./icons";
import { Link } from "./ui";
import { S } from "./strings";

/**
 * One diagram on a page: the backend's validated IR drawn by
 * @trick77/diagram, with its two ways out, the full view and the file. The
 * IR arrives as JSON, there is nothing to parse.
 *
 * links names the page a flow node opens, null for none: the system map's
 * repository nodes. The drawing is a string without ids on its nodes, so a
 * link is an anchor laid over the node's box from the layout, in percent of
 * the drawing (the card's overlay).
 */

/** The kinds the backend stores (internal/diagram): the package draws
 * more, which almanach neither gets nor bundles. */
export type DiagramIR = FlowIR | SequenceIR | ERIR;

/** The app's name on a downloaded file: almanach-flowchart-diagram.svg. */
export const app = "almanach";

/** The card's and the full view's icons, almanach's own set. */
export const icons: Icons = {
  expand: <ExpandIcon />,
  download: <DownloadIcon />,
};

/** The card's own box carries no spacing: the figure around it does, as a
 * figure is what a page's picture is. */
const cardClasses: Classes = { ...classes, card: "" };

/** A map's bands, top to bottom, by zone id: who calls, where it enters,
 * the repositories or a repository's parts, the repositories of other
 * projects, the stores, what is outside. A zone not named comes after
 * them, as written. */
export const bandOrder = [
  "clients",
  "entry",
  "repositories",
  "parts",
  "shared",
  "stores",
  "external",
];

/** layoutMap lays a map, the system map or a repository's, out in the
 * package's bands: its zones stacked down the page in bandOrder, the boxes
 * of no zone in a last band. Always bands, never the zoned flow the
 * package's lane reading falls back to for a map of one zone, of no arrow,
 * or of arrows within one zone only: clean over compact, a wide map is
 * scaled into the column and the full view shows it large (user,
 * 2026-11-22). An arrow from a box to itself or to one the map does not
 * hold says nothing and is left out (the backend drops them too); the
 * bands refuse it. A map of no box is an empty canvas; one the bands
 * cannot lay out at all is the engine's zoned flow. */
export async function layoutMap(ir: FlowIR): Promise<FlowLayout> {
  const down = downward(ir);
  if (down.nodes.length === 0)
    return {
      width: 2 * margin,
      height: 2 * margin,
      nodes: [],
      edges: [],
      zones: [],
    };
  const held = new Set(down.nodes.map((n) => n.id));
  const edges = down.edges.filter(
    (e) => e.from !== e.to && held.has(e.from) && held.has(e.to),
  );
  const bands = await layoutBands(
    { ...down, edges },
    await engine(),
    bandOrder,
  );
  return bands ?? layoutFlowChecked(down, engine, { tree: false });
}

/** drawIR lays a diagram out and writes it as SVG. ids prefixes the marker
 * ids: two drawings on one page must not share one. A map is laid out in
 * bands (layoutMap). A tree-shaped flow stays the engine's: the package's
 * org chart is for answers that ask for one, a statement's flow is a
 * process. Its own dispatch over the three kinds rather than the package's
 * drawIR, which brings every kind's layout into the bundle. */
export async function drawIR(
  ir: DiagramIR,
  ids: string,
  map = false,
): Promise<Rendered> {
  const pal = palette();
  if (ir.kind === "sequence") {
    const laid = layoutSequence(ir);
    return { svg: drawSequence(laid, pal, ids), layout: null, laid };
  }
  if (ir.kind === "er") {
    const laid = await layoutER(ir, await engine());
    return { svg: drawER(laid, pal), layout: null, laid };
  }
  const layout = map
    ? await layoutMap(ir)
    : await layoutFlow(ir, engine, { tree: false });
  const svg =
    "lanes" in layout
      ? drawSwimlane(layout as SwimlaneLayout, pal, ids)
      : drawFlow(layout, pal, ids);
  return { svg, layout, laid: layout };
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

type State = { key: string; drawn: Rendered | null; failed: boolean };

export function Diagram({
  ir,
  title,
  links,
  map = false,
}: {
  ir: DiagramIR;
  title: string;
  links?: (id: string) => string | null;
  /** A map, the system map or a repository's: its caps and its layout. */
  map?: boolean;
}): ReactNode {
  const [ids] = useState(() => `almanach-diagram-${++seq}`);
  const key = JSON.stringify(ir);
  const [state, setState] = useState<State>({
    key: "",
    drawn: null,
    failed: false,
  });
  // Every diagram the backend stores fits: its caps are below these. One
  // that does not is a defect, said rather than drawn crowded.
  const ok = fits(ir, map);
  const names = new Map(
    ir.kind === "flow" ? ir.nodes.map((n) => [n.id, n.label]) : [],
  );

  useEffect(() => {
    if (!ok) return;
    let live = true;
    drawIR(JSON.parse(key) as DiagramIR, ids, map).then(
      (drawn) => live && setState({ key, drawn, failed: false }),
      () => live && setState({ key, drawn: null, failed: true }),
    );
    return () => {
      live = false;
    };
  }, [key, ids, ok, map]);

  if (!ok || (state.key === key && state.failed))
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.failed}</p>;
  if (state.key !== key || state.drawn === null)
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.pending}</p>;
  const { svg, layout } = state.drawn;

  return (
    <figure className="my-3">
      <DiagramCard
        svg={svg}
        title={title}
        fileName={fileName(ir.kind, app)}
        own
        shell={OverlayShell}
        icons={icons}
        strings={S.diagram}
        classes={cardClasses}
        overlay={
          layout !== null && links
            ? ({ full, close }) => (
                <NodeLinks
                  layout={layout}
                  links={links}
                  names={names}
                  onFollow={full ? close : undefined}
                />
              )
            : undefined
        }
      />
    </figure>
  );
}

/** FullDiagram is a drawing seen whole, its linked nodes linked there too;
 *  a followed link closes it. The compact map's full view: its card draws
 *  no picture. */
export function FullDiagram({
  title,
  kind,
  drawn,
  links,
  names,
  onClose,
}: {
  title: string;
  kind: DiagramIR["kind"];
  /** The drawing and, for links, the flow layout it was drawn from. */
  drawn: Pick<Rendered, "svg" | "layout">;
  links?: (id: string) => string | null;
  names: Map<string, string>;
  onClose: () => void;
}) {
  return (
    <DiagramView
      svg={drawn.svg}
      title={title}
      fileName={fileName(kind, app)}
      onClose={onClose}
      shell={OverlayShell}
      icons={icons}
      strings={S.diagram}
      overlay={
        drawn.layout !== null && links ? (
          <NodeLinks
            layout={drawn.layout}
            links={links}
            names={names}
            onFollow={onClose}
          />
        ) : undefined
      }
    />
  );
}

/** NodeLinks lays an anchor over every node links names a page for, placed
 * by the layout's own box so it scales with the drawing. onFollow closes
 * the full view a link is followed from. */
function NodeLinks({
  layout,
  links,
  names,
  onFollow,
}: {
  layout: FlowLayout;
  links: (id: string) => string | null;
  /** names are the nodes' labels by id: a link is named by what it shows. */
  names: Map<string, string>;
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
            title={S.diagram.repoLink(names.get(n.id) ?? n.id)}
            className="absolute rounded-[6px] hover:bg-accent/10 hover:outline-2 hover:outline-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong"
            style={{
              left: pct(n.x, layout.width),
              top: pct(n.y, layout.height),
              width: pct(n.w, layout.width),
              height: pct(n.h, layout.height),
            }}
          >
            <span className="sr-only">
              {S.diagram.repoLink(names.get(n.id) ?? n.id)}
            </span>
          </Link>
        );
      })}
    </span>
  );
}
