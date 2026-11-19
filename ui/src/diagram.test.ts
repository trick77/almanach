import { describe, expect, it } from "vitest";
import { drawIR } from "./Diagram";
import type { ERIR } from "./diagramIR";

// An entity model goes through the layout engine, loaded for it on demand.
describe("drawIR", () => {
  it("draws an entity model with the engine", async () => {
    const er: ERIR = {
      kind: "er",
      direction: "TB",
      entities: [
        { id: "THREAD", label: "THREAD", attrs: [] },
        { id: "MESSAGE", label: "MESSAGE", attrs: [] },
      ],
      rels: [
        {
          from: "THREAD",
          to: "MESSAGE",
          label: "holds",
          fromCard: "one",
          toCard: "zeroOrMore",
          dashed: false,
        },
      ],
    };
    const { svg, layout } = await drawIR(er, "t");
    expect(layout).toBeNull();
    expect(svg).toContain("THREAD");
    expect(svg).toContain("holds");
  });
});
