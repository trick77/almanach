// Copied from rongo (ui/src/diagramIR.ts), kept in step by hand.
// The IR types and fits only: rongo reads them out of the mermaid parse,
// Almanach gets them as JSON the backend validated (baseline/diagram.go).
// Almanach: a sequence's rows count whole, frames included, as the
// backend counts them. The limits below are the backend's DrawMaxNodes,
// DrawMaxEdges, DrawMaxActors, DrawMaxRows, DrawMaxEntities, DrawMaxRels
// and DrawMaxAttrs (backend/internal/diagram); change both together.

/** start and stop are a state diagram's `[*]`: a dot where it begins, a
 * ringed dot where it ends. They carry no text. */
export type FlowShape = "pill" | "step" | "decision" | "start" | "stop";
export type FlowNodeIR = {
  id: string;
  label: string;
  shape: FlowShape;
  zone?: string;
  /** Tagged `:::focus` by the model: the part the question is about. */
  focus?: boolean;
};
export type FlowEdgeIR = {
  from: string;
  to: string;
  label: string;
  dashed: boolean;
  /** Which end carries the arrowhead. */
  head: "end" | "both" | "none";
};
export type FlowIR = {
  kind: "flow";
  direction: "TB" | "LR";
  nodes: FlowNodeIR[];
  edges: FlowEdgeIR[];
  zones: { id: string; title: string }[];
};
/** How a message is drawn: its line, and what its arrow ends in. A dashed
 * line is a reply, never a boundary, so it takes no colour. */
export type SeqLine = "solid" | "dashed";
export type SeqHead = "filled" | "open" | "none" | "cross" | "both";
export type SeqRow =
  | {
      type: "message";
      from: string;
      to: string;
      label: string;
      line: SeqLine;
      head: SeqHead;
    }
  | {
      type: "note";
      /** The lifelines it sits over, or the one it sits beside. */
      from: string;
      to: string;
      side: "over" | "left" | "right";
      label: string;
    }
  /** A frame opens (`alt`, `loop`, `par` ...), is split by a divider
   * (`else`, `and`, `option`) and closes at `end`. */
  | { type: "frame"; keyword: string; label: string }
  | { type: "divider"; keyword: string; label: string }
  | { type: "end" };
export type SequenceIR = {
  kind: "sequence";
  actors: { id: string; label: string }[];
  /** Top to bottom, in the order the source wrote them. */
  rows: SeqRow[];
};
/** How many of an entity sit at one end of a relationship. */
export type Card = "one" | "zeroOrOne" | "oneOrMore" | "zeroOrMore";
export type EntityIR = {
  id: string;
  label: string;
  attrs: { type: string; name: string; keys: string[]; comment: string }[];
  focus?: boolean;
};
export type RelIR = {
  from: string;
  to: string;
  label: string;
  /** The cardinality drawn at each end: fromCard at from's box. */
  fromCard: Card;
  toCard: Card;
  /** Non-identifying, written `..`: dashed, never coloured. */
  dashed: boolean;
};
export type ERIR = {
  kind: "er";
  direction: "TB" | "LR";
  entities: EntityIR[];
  rels: RelIR[];
};
export type DiagramIR = FlowIR | SequenceIR | ERIR;

/** The largest diagram drawn editorially. The prompt asks for at most 12
 * nodes; past this the layout stops reading as composed, and the renderer's
 * own drawing is no worse than a crowded one of ours. */
const maxNodes = 16;
const maxEdges = 24;
/** The prompt asks for at most 5 participants and 12 messages; the same
 * slack over it as a flow gets. Wider than this a sequence is too small to
 * read inline in either drawing, and the renderer's is the wider one. */
const maxActors = 8;
const maxRows = 20;

/** Almanach: a map, the system map or a repository's, holds more: every
 * external system it names is its own box (user, 2026-11-01), and its
 * layout wraps rows rather than widening (mapLayout.ts). The backend's
 * MapMaxNodes and MapMaxEdges. */
export const mapMaxNodes = 22;
export const mapMaxEdges = 33;

export function fits(ir: DiagramIR, map = false): boolean {
  if (ir.kind === "flow" && map)
    return ir.nodes.length <= mapMaxNodes && ir.edges.length <= mapMaxEdges;
  if (ir.kind === "sequence")
    return ir.actors.length <= maxActors && ir.rows.length <= maxRows;
  if (ir.kind === "er")
    return (
      ir.entities.length <= maxEntities &&
      ir.rels.length <= maxRels &&
      ir.entities.every((e) => e.attrs.length <= maxAttrs)
    );
  return ir.nodes.length <= maxNodes && ir.edges.length <= maxEdges;
}

/** An entity model's ceiling: tables are larger than steps, so fewer of
 * them fit the answer's width before every field is too small to read. */
const maxEntities = 8;
const maxRels = 12;
const maxAttrs = 12;
