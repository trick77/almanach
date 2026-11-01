import { describe, expect, it } from "vitest";
import type { FlowIR } from "./diagramIR";
import { withShared } from "./ProjectPage";

const map: FlowIR = {
  kind: "flow",
  direction: "LR",
  zones: [
    { id: "clients", title: "Clients" },
    { id: "repositories", title: "Repositories" },
    { id: "external", title: "Externe Systeme" },
  ],
  nodes: [
    { id: "browser", label: "Browser", shape: "pill", zone: "clients" },
    { id: "loom", label: "loom", shape: "step", zone: "repositories" },
    { id: "rongo", label: "rongo", shape: "step", zone: "repositories" },
    { id: "wire", label: "llmwire", shape: "pill", zone: "external" },
  ],
  edges: [
    { from: "browser", to: "loom", label: "HTTP", dashed: false, head: "end" },
    { from: "loom", to: "wire", label: "library", dashed: false, head: "end" },
  ],
};

const llmwire = {
  project: "tools",
  slug: "llmwire",
  used_by: [
    { project: "apps", slug: "loom" },
    { project: "apps", slug: "rongo" },
  ],
};

describe("withShared", () => {
  it("leaves a map without libraries as stored", () => {
    expect(withShared(map, [])).toBe(map);
  });

  it("draws each library in its own zone, an edge from each user", () => {
    const ir = withShared(map, [llmwire]);
    const lib = ir.nodes.find((n) => n.zone === "shared");
    expect(lib).toEqual({
      id: "shared:tools/llmwire",
      label: "tools/llmwire",
      shape: "pill",
      zone: "shared",
    });
    expect(ir.edges.filter((e) => e.to === lib!.id).map((e) => e.from)).toEqual(
      ["loom", "rongo"],
    );
    expect(ir.zones.map((z) => z.id)).toEqual([
      "clients",
      "repositories",
      "shared",
    ]);
  });

  it("puts the library in place of the node the model drew for it", () => {
    const ir = withShared(map, [llmwire]);
    expect(ir.nodes.some((n) => n.id === "wire")).toBe(false);
    // loom's edge moved over, not doubled.
    expect(
      ir.edges.filter((e) => e.from === "loom" && e.to.startsWith("shared")),
    ).toHaveLength(1);
  });

  it("draws as many libraries as one drawing holds", () => {
    const many = Array.from({ length: 20 }, (_, i) => ({
      ...llmwire,
      slug: `lib${i}`,
    }));
    // 2 edges stored, 2 per library within a map's 33: fifteen.
    const ir = withShared(map, many);
    expect(ir.nodes.filter((n) => n.zone === "shared")).toHaveLength(15);
    expect(ir.nodes.some((n) => n.id === "shared:tools/lib14")).toBe(true);
  });

  it("leaves a node alone that is no library use", () => {
    // An API gateway called over HTTP, a library slug "gateway" aside.
    const gw: FlowIR = {
      ...map,
      nodes: [
        ...map.nodes,
        { id: "gateway", label: "gateway", shape: "pill", zone: "external" },
      ],
      edges: [
        ...map.edges,
        {
          from: "rongo",
          to: "gateway",
          label: "HTTP",
          dashed: false,
          head: "end",
        },
      ],
    };
    const ir = withShared(gw, [{ ...llmwire, slug: "gateway" }]);
    expect(ir.nodes.some((n) => n.id === "gateway")).toBe(true);
    expect(ir.edges.some((e) => e.to === "gateway")).toBe(true);
  });

  it("matches a slug two libraries share only by project and slug", () => {
    const two = [llmwire, { ...llmwire, project: "other" }];
    const ir = withShared(map, two);
    // "llmwire" alone names neither: the model's node stays.
    expect(ir.nodes.some((n) => n.id === "wire")).toBe(true);
  });

  it("keeps a shared slug ambiguous when one of the two was cut", () => {
    const many = [
      llmwire,
      ...Array.from({ length: 20 }, (_, i) => ({ ...llmwire, slug: `x${i}` })),
      { ...llmwire, project: "other" },
    ];
    const ir = withShared(map, many);
    expect(ir.nodes.some((n) => n.id === "shared:other/llmwire")).toBe(false);
    expect(ir.nodes.some((n) => n.id === "wire")).toBe(true);
  });

  it("draws a library edge the model drew backwards once", () => {
    const back: FlowIR = {
      ...map,
      edges: [
        map.edges[0],
        {
          from: "wire",
          to: "loom",
          label: "library",
          dashed: false,
          head: "end",
        },
      ],
    };
    const ir = withShared(back, [llmwire]);
    const lib = "shared:tools/llmwire";
    expect(
      ir.edges.filter(
        (e) =>
          (e.from === lib && e.to === "loom") ||
          (e.from === "loom" && e.to === lib),
      ),
    ).toHaveLength(1);
  });

  it("leaves out a library whose users the map does not show", () => {
    const ir = withShared(map, [
      { ...llmwire, used_by: [{ project: "apps", slug: "newer" }] },
    ]);
    expect(ir).toBe(map);
  });
});
