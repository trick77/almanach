import { describe, expect, it } from "vitest";
import { bandOrder, drawIR } from "./Diagram";
import {
  drawFlow,
  engine,
  layoutElk,
  palette,
  type ERIR,
  type FlowIR,
  type SwimlaneLayout,
} from "@trick77/diagram";
import { checkIR } from "@trick77/diagram/check";

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
    // An arrow's text is a note in capitals.
    expect(svg).toContain("HOLDS");
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

  it("lays a map out in bands down the page, by role whatever order the model wrote", async () => {
    const step = (id: string, zone: string) => ({
      id,
      label: id,
      shape: "step" as const,
      zone,
    });
    const edge = (from: string, to: string, label: string) => ({
      from,
      to,
      label,
      dashed: false,
      head: "end" as const,
    });
    const map: FlowIR = {
      kind: "flow",
      direction: "TB",
      // Written outside in: the bands still run who calls, where it
      // enters, the repositories, the stores, what is outside.
      zones: [
        { id: "external", title: "Externe Systeme" },
        { id: "stores", title: "Datenspeicher" },
        { id: "repositories", title: "Repositories" },
        { id: "clients", title: "Clients" },
      ],
      nodes: [
        step("psp", "external"),
        step("pg", "stores"),
        step("shop-api", "repositories"),
        step("shop-web", "repositories"),
        step("browser", "clients"),
      ],
      edges: [
        edge("browser", "shop-web", "HTTP"),
        edge("shop-web", "shop-api", "HTTP"),
        edge("shop-api", "pg", "DB"),
        edge("shop-api", "psp", "HTTP"),
      ],
    };
    const { svg, layout, laid } = await drawIR(map, "t", true);
    expect(svg).toContain("shop-api");
    expect(layout).not.toBeNull();
    expect((laid as SwimlaneLayout).lanes.map((l) => l.id)).toEqual([
      "clients",
      "repositories",
      "stores",
      "external",
    ]);
    // Every box is in the layout the node links are laid over.
    expect(layout!.nodes.map((n) => n.id).sort()).toEqual([
      "browser",
      "pg",
      "psp",
      "shop-api",
      "shop-web",
    ]);
    expect(
      await checkIR(map, { lanes: true, bandOrder, layout: laid }),
    ).toEqual([]);
    // Bands stacked down the page, whichever way the model wrote the map:
    // one written left to right is no swimlane of columns.
    const across = await drawIR({ ...map, direction: "LR" }, "t", true);
    const lanes = (across.laid as SwimlaneLayout).lanes;
    expect(lanes.map((l) => l.id)).toEqual([
      "clients",
      "repositories",
      "stores",
      "external",
    ]);
    lanes.slice(1).forEach((l, i) => {
      expect(l.x).toBe(lanes[i].x);
      expect(l.y).toBeGreaterThan(lanes[i].y);
    });
    expect(across.svg).toBe(svg);
  });
});
