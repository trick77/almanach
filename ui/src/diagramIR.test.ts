import { describe, it, expect, vi } from "vitest";
import * as diagram from "@trick77/diagram";
import type { ERIR, FlowIR, SequenceIR } from "@trick77/diagram";
import { fits } from "./diagramIR";

// The package's own limits are tested there; these are the backend's
// choices almanach hands it: every cap, frames counted, a map's larger
// ones.

vi.mock("@trick77/diagram", { spy: true });

const node = (id: string) => ({ id, label: id, shape: "step" as const });
const edge = (i: number) => ({
  from: `n${i}`,
  to: `n${i + 1}`,
  label: "",
  dashed: false,
  head: "end" as const,
});

const flow = (n: number, e = 0): FlowIR => ({
  kind: "flow",
  direction: "TB",
  nodes: Array.from({ length: n }, (_, i) => node(`n${i}`)),
  edges: Array.from({ length: e }, (_, i) => edge(i)),
  zones: [],
});

describe("fits", () => {
  it("hands the package every backend cap, never leaning on its defaults", () => {
    fits(flow(1));
    expect(vi.mocked(diagram.fits)).toHaveBeenLastCalledWith(flow(1), {
      nodes: 16,
      edges: 24,
      actors: 8,
      rows: 20,
      countFrames: true,
      entities: 8,
      rels: 12,
      attrs: 12,
    });
    fits(flow(1), true);
    expect(vi.mocked(diagram.fits)).toHaveBeenLastCalledWith(
      flow(1),
      expect.objectContaining({ nodes: 22, edges: 33, actors: 8 }),
    );
  });

  it("takes a flow up to the backend's DrawMax caps, not past them", () => {
    expect(fits(flow(16))).toBe(true);
    expect(fits(flow(17))).toBe(false);
    expect(fits(flow(16, 24))).toBe(true);
    expect(fits(flow(16, 25))).toBe(false);
  });

  it("takes a map up to the backend's MapMax caps, not past them", () => {
    expect(fits(flow(22), true)).toBe(true);
    expect(fits(flow(23), true)).toBe(false);
    expect(fits(flow(22, 33), true)).toBe(true);
    expect(fits(flow(22, 34), true)).toBe(false);
  });

  it("counts every row of a sequence, its frames included", () => {
    const seq: SequenceIR = {
      kind: "sequence",
      actors: [
        { id: "a", label: "A" },
        { id: "b", label: "B" },
      ],
      rows: [
        { type: "frame", keyword: "loop", label: "" },
        ...Array.from({ length: 18 }, () => ({
          type: "message" as const,
          from: "a",
          to: "b",
          label: "x",
          line: "solid" as const,
          head: "filled" as const,
        })),
        { type: "end" },
      ],
    };
    expect(fits(seq)).toBe(true);
    seq.rows.push({ type: "frame", keyword: "opt", label: "" });
    expect(fits(seq)).toBe(false);
  });

  it("holds an entity model to fewer tables and fields", () => {
    const er = (n: number, attrs: number): ERIR => ({
      kind: "er",
      direction: "LR",
      entities: Array.from({ length: n }, (_, i) => ({
        id: `e${i}`,
        label: `E${i}`,
        attrs: Array.from({ length: attrs }, (_, j) => ({
          type: "int",
          name: `f${j}`,
          keys: [],
          comment: "",
        })),
      })),
      rels: [],
    });
    expect(fits(er(8, 12))).toBe(true);
    expect(fits(er(9, 1))).toBe(false);
    expect(fits(er(1, 13))).toBe(false);
  });
});
