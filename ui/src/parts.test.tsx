import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Parts, RepoDetail } from "./api";
import { partLabel, partsGraph, reduced } from "./Parts";
import { RepoPage } from "./RepoPage";

function serve(table: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = url.replace("/api/", "");
      if (!(path in table)) {
        return { ok: false, status: 404, json: async () => ({}) };
      }
      return { ok: true, status: 200, json: async () => table[path] };
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const e = (from: string, to: string) => ({ from, to });

describe("reduced", () => {
  it("drops an edge another path already says", () => {
    expect(reduced([e("a", "b"), e("b", "c"), e("a", "c")])).toEqual([
      e("a", "b"),
      e("b", "c"),
    ]);
  });

  it("keeps a cycle whole and reduces across it", () => {
    // b and c use each other; a reaches c through b, d only through both.
    const got = reduced([
      e("a", "b"),
      e("b", "c"),
      e("c", "b"),
      e("a", "c"),
      e("c", "d"),
      e("a", "d"),
    ]);
    expect(got).toEqual([e("a", "b"), e("b", "c"), e("c", "b"), e("c", "d")]);
  });

  it("keeps edges no other path says", () => {
    const edges = [e("a", "b"), e("a", "c"), e("b", "d"), e("c", "d")];
    expect(reduced(edges)).toEqual(edges);
  });
});

const edges = [
  e("lib/render", "lib/core"),
  e("service/api", "lib/core"),
  e("service/api", "lib/render"),
  e("service/api", "tools/core"),
];
const parts: Parts = {
  units: [
    { dir: "lib/core", name: "core", kind: "lib" },
    { dir: "lib/render", name: "render", kind: "" },
    { dir: "service/api", name: "api", kind: "app" },
    { dir: "tools/core", name: "core", kind: "" },
  ],
  edges,
  graph: {
    nodes: ["lib/core", "lib/render", "service/api", "tools/core"],
    edges,
  },
};

describe("partsGraph", () => {
  it("puts the apps above the rest, reduced, a shared name by its folder", () => {
    const ir = partsGraph(parts);
    expect(ir.direction).toBe("TB");
    expect(ir.zones.map((z) => z.id)).toEqual(["apps", "parts"]);
    expect(ir.nodes.find((n) => n.id === "service/api")).toMatchObject({
      label: "api",
      shape: "step",
      zone: "apps",
    });
    expect(ir.nodes.find((n) => n.id === "lib/core")?.label).toBe("lib/core");
    expect(ir.edges.map((x) => [x.from, x.to])).toEqual([
      ["lib/render", "lib/core"],
      ["service/api", "lib/render"],
      ["service/api", "tools/core"],
    ]);
  });

  it("draws no zones when the build names no app", () => {
    const ir = partsGraph({
      units: [
        { dir: "a", name: "a", kind: "" },
        { dir: "b", name: "b", kind: "lib" },
      ],
      edges: [e("a", "b")],
      graph: { nodes: ["a", "b"], edges: [e("a", "b")] },
    });
    expect(ir.zones).toEqual([]);
    expect(ir.nodes.every((n) => n.zone === undefined)).toBe(true);
  });

  it("draws only what the graph holds: the table has the rest", () => {
    const ir = partsGraph({
      ...parts,
      graph: {
        nodes: ["service/api", "lib/core"],
        edges: [e("service/api", "lib/core")],
      },
    });
    expect(ir.nodes.map((n) => n.id)).toEqual(["service/api", "lib/core"]);
  });

  it("names a part by its name unless two share it", () => {
    const label = partLabel(parts.units);
    expect(parts.units.map(label)).toEqual([
      "lib/core",
      "render",
      "api",
      "tools/core",
    ]);
  });
});

const repo: RepoDetail = {
  project_key: "tools",
  slug: "mono",
  description: null,
  stale: null,
  notes_total: 0,
  last_merge: null,
  revisions: [],
  notes: [],
  map: null,
  map_declined: false,
  uses: [],
  used_by: [],
  parts,
  messaging: { topics: [], schemas: [] },
  interfaces: [],
};

describe("RepoPage parts", () => {
  it("lists every part with what it uses and what uses it", async () => {
    serve({ "projects/tools/repos/mono": repo });
    render(<RepoPage projectKey="tools" slug="mono" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    // The part's own row: its name in the first cell.
    const row = within(desc)
      .getAllByRole("row")
      .find((tr) => tr.firstElementChild?.textContent?.startsWith("api"))!;
    const cells = within(row).getAllByRole("cell");
    expect(cells[0].textContent).toContain("App");
    expect(cells[1].textContent).toBe("lib/core, render, tools/core");
    expect(cells[2].textContent).toBe("");
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    const entry = within(rail).getByText("Innere Abhängigkeiten").closest("a")!;
    expect(entry.getAttribute("href")).toBe("#parts");
    expect(entry.textContent).toContain("4");
  });

  it("has no section for a repository whose parts use none", async () => {
    serve({
      "projects/tools/repos/mono": {
        ...repo,
        parts: { units: [], edges: [], graph: { nodes: [], edges: [] } },
      },
    });
    render(<RepoPage projectKey="tools" slug="mono" />);
    await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(screen.queryByText("Innere Abhängigkeiten")).toBeNull();
  });
});
