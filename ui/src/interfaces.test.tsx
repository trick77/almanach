import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Endpoint, Interface, RepoDetail } from "./api";
import { ProjectInterfaces } from "./Interfaces";
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

const op = (method: string, path: string, summary = ""): Endpoint => ({
  kind: "http",
  api: "Orders API",
  method,
  path,
  summary,
  direction: "offers",
  files: ["api.yaml"],
});

const offered: Interface = {
  key: "7:http:Orders API",
  repo: { project: "shop", slug: "orders" },
  commit: "c0ffee",
  kind: "http",
  name: "Orders API",
  version: "2.1.0",
  base_path: "/v1",
  direction: "offers",
  files: ["api.yaml", "OrderController.java"],
  endpoints: [
    op("GET", "/v1/orders", "List orders"),
    op("GET", "/v1/orders/{id}"),
  ],
};

const declared: Interface = {
  ...offered,
  key: "7:http:Stock",
  name: "Stock",
  version: "",
  base_path: "",
  direction: "declares",
  files: ["specs/stock.yaml"],
  endpoints: [{ ...op("GET", "/stock"), api: "Stock", direction: "declares" }],
};

const routes: Interface = {
  ...offered,
  key: "7:http:",
  name: "",
  version: "",
  base_path: "",
  files: ["Health.java"],
  endpoints: Array.from({ length: 70 }, (_, i) => op("GET", `/r/${i}`)),
};

const repo: RepoDetail = {
  project_key: "shop",
  slug: "orders",
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
  messaging: { topics: [], schemas: [] },
  interfaces: [declared, offered, routes],
  parts: { units: [], edges: [], graph: { nodes: [], edges: [] } },
};

describe("RepoPage interfaces", () => {
  it("lists what its code offers, then what it only declares, each operation, and points the rail at them", async () => {
    serve({ "projects/shop/repos/orders": repo });
    render(<RepoPage projectKey="shop" slug="orders" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    const blocks = within(desc).getAllByTestId("interface");
    expect(blocks.map((b) => b.querySelector("summary")!.textContent)).toEqual([
      expect.stringContaining("Orders API"),
      expect.stringContaining("Routen im Code"),
      expect.stringContaining("Nur genannt"),
    ]);
    const first = blocks[0];
    expect(first.hasAttribute("open")).toBe(true);
    expect(within(first).getByText("Basis /v1")).toBeInTheDocument();
    expect(within(first).getByText("2 Operationen")).toBeInTheDocument();
    const row = within(first).getByText("/v1/orders").closest("tr")!;
    expect(within(row).getByText("GET")).toBeInTheDocument();
    expect(within(row).getByText("List orders")).toBeInTheDocument();
    // Seventy operations start folded, every one still there.
    expect(blocks[1].hasAttribute("open")).toBe(false);
    expect(within(blocks[1]).getAllByRole("row")).toHaveLength(71);
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    const entry = within(rail).getByText("Schnittstellen").closest("a")!;
    expect(entry.getAttribute("href")).toBe("#interfaces");
    expect(entry.textContent).toContain("2 · 1");
  });

  it("has no section for a repository without interfaces", async () => {
    serve({ "projects/shop/repos/orders": { ...repo, interfaces: [] } });
    render(<RepoPage projectKey="shop" slug="orders" />);
    await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(screen.queryByText("Schnittstellen")).toBeNull();
  });
});

describe("ProjectInterfaces", () => {
  it("names each interface's repository, linked", () => {
    render(<ProjectInterfaces apis={[offered]} />);
    const link = screen.getByRole("link", { name: "orders" });
    expect(link.getAttribute("href")).toBe("/projects/shop/repos/orders");
    expect(screen.getByText("List orders")).toBeInTheDocument();
  });
});
