import { fits as within, type DiagramIR, type Limits } from "@trick77/diagram";

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
 * system it names is its own box (user, 2026-11-01), and its layout wraps
 * rows rather than widening (mapLayout.ts). The backend's MapMaxNodes and
 * MapMaxEdges. */
export const mapMaxNodes = 22;
export const mapMaxEdges = 33;

/** fits says whether a diagram is drawn: within the backend's caps, a
 * map's for a map. */
export function fits(ir: DiagramIR, map = false): boolean {
  return within(
    ir,
    map ? { ...caps, nodes: mapMaxNodes, edges: mapMaxEdges } : caps,
  );
}
