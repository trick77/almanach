import { describe, expect, it } from "vitest";
import { drawIR } from "./Diagram";
import {
  drawFlow,
  engine,
  layoutElk,
  palette,
  type ERIR,
  type FlowIR,
} from "@trick77/diagram";

// An entity model goes through the layout engine, loaded for it on demand;
// so does a flow neither a mapping nor a spine fits, a tree included.
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

  it("lays a tree-shaped flow out with the engine, never as an org chart", async () => {
    const step = (id: string) => ({ id, label: id, shape: "step" as const });
    const edge = (from: string, to: string) => ({
      from,
      to,
      label: "",
      dashed: false,
      head: "end" as const,
    });
    const tree: FlowIR = {
      kind: "flow",
      direction: "TB",
      zones: [],
      nodes: ["Anfrage", "Prüfung", "Speicher", "Antwort", "Log", "Metrik"].map(
        step,
      ),
      edges: [
        edge("Anfrage", "Prüfung"),
        edge("Anfrage", "Speicher"),
        edge("Anfrage", "Antwort"),
        edge("Speicher", "Log"),
        edge("Speicher", "Metrik"),
      ],
    };
    const { svg, layout } = await drawIR(tree, "t");
    const elk = await layoutElk(tree, await engine());
    expect(layout?.bus).toBeFalsy();
    expect(svg).toBe(drawFlow(elk, palette(), "t"));
  });
});
