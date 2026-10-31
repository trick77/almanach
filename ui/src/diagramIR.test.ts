import { describe, it, expect } from "vitest";
import { fits, type ERIR, type FlowIR, type SequenceIR } from "./diagramIR";

// rongo's diagramIR.test.ts reads the mermaid parse; only fits came along.

const node = (id: string) => ({ id, label: id, shape: "step" as const });

describe("fits", () => {
  it("takes a flow up to rongo's ceiling, not past it", () => {
    const flow = (n: number): FlowIR => ({
      kind: "flow",
      direction: "TB",
      nodes: Array.from({ length: n }, (_, i) => node(`n${i}`)),
      edges: [],
      zones: [],
    });
    expect(fits(flow(16))).toBe(true);
    expect(fits(flow(17))).toBe(false);
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
