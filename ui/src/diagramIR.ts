import {
  fits as within,
  type DiagramIR,
  type FlowIR,
  type Limits,
} from "@trick77/diagram";

/** The backend's caps (backend/internal/diagram), every one handed to the
 * package rather than left to its defaults, so a package release never
 * moves almanach's limits: DrawMaxNodes, DrawMaxEdges, DrawMaxActors,
 * DrawMaxRows, DrawMaxEntities, DrawMaxRels and DrawMaxAttrs. A sequence's
 * rows count whole, frames included, as the backend counts them. Change
 * both sides together. */
const caps: Pick<
  Limits,
  | "nodes"
  | "edges"
  | "actors"
  | "rows"
  | "countFrames"
  | "entities"
  | "rels"
  | "attrs"
> = {
  nodes: 16,
  edges: 24,
  actors: 8,
  rows: 20,
  countFrames: true,
  entities: 8,
  rels: 12,
  attrs: 12,
};

/** A map, the system map or a repository's, holds more: every external
 * system it names is its own box (user, 2026-11-01). The backend's
 * MapMaxNodes and MapMaxEdges. */
export const mapMaxNodes = 22;
export const mapMaxEdges = 33;
/** A map is drawn in bands, its zones counted as lanes: the system map's
 * five, the shared repositories code adds and a band of steps in no zone. */
const mapMaxLanes = 7;

/** downward is a map as it is drawn: top-down, whichever way the model
 * wrote it (the backend takes TB or LR). Left to right, the package draws
 * a zoned flow as a swimlane of columns in the order written, not in bands
 * in the maps' order. */
export function downward(map: FlowIR): FlowIR {
  return map.direction === "TB" ? map : { ...map, direction: "TB" };
}

/** fits says whether a diagram is drawn: within the backend's caps, a
 * map's for a map, counted as it is drawn, in bands. */
export function fits(ir: DiagramIR, map = false): boolean {
  if (!map) return within(ir, caps);
  return within(
    ir.kind === "flow" ? downward(ir) : ir,
    { ...caps, nodes: mapMaxNodes, edges: mapMaxEdges, lanes: mapMaxLanes },
    { lanes: true },
  );
}
