import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Dependencies, RepoDetail } from "./api";
import {
  DepsPage,
  depGraph,
  drawnGraph,
  edgeLabel,
  sharedRows,
  oddVersions,
  repoNamer,
  shortVersion,
} from "./DepsPage";
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

const ref = (project: string, slug: string) => ({ project, slug });

const graph: Dependencies = {
  repos: [
    ref("tools", "llmwire"),
    ref("tools", "loom"),
    ref("tools", "rongo"),
    ref("tools", "webfetch"),
  ],
  edges: [
    {
      from: ref("tools", "loom"),
      to: ref("tools", "llmwire"),
      version: "v0.1.8",
      coords: ["go:example.invalid/llmwire"],
      scope: "main",
    },
    {
      from: ref("tools", "loom"),
      to: ref("tools", "webfetch"),
      version: "v0.3.0",
      coords: ["go:example.invalid/webfetch"],
      scope: "main",
    },
    {
      from: ref("tools", "rongo"),
      to: ref("tools", "llmwire"),
      version: "v0.1.6",
      coords: ["go:example.invalid/llmwire"],
      scope: "main",
    },
  ],
  ambiguous: [
    {
      coord: "npm:shared",
      providers: [ref("tools", "a"), ref("tools", "b")],
      cloned_twice: true,
    },
  ],
};

const repo: RepoDetail = {
  project_key: "tools",
  slug: "rongo",
  description: null,
  notes_total: 0,
  last_merge: null,
  revisions: [],
  notes: [],
  map: null,
  map_declined: false,
  uses: [
    {
      project: "tools",
      slug: "llmwire",
      version: "v0.1.6",
      also: [{ project: "tools", slug: "loom", version: "v0.1.8" }],
    },
  ],
  used_by: [{ project: "apps", slug: "chat", version: "" }],
  messaging: { topics: [], schemas: [] },
  interfaces: [],
};

describe("dependency graph", () => {
  it("draws users left, shared repositories right, one edge per use", () => {
    const ir = depGraph(graph);
    expect(ir.direction).toBe("LR");
    const zone = Object.fromEntries(ir.nodes.map((n) => [n.label, n.zone]));
    expect(zone).toEqual({
      loom: "users",
      llmwire: "shared",
      webfetch: "shared",
      rongo: "users",
    });
    expect(ir.edges.map((e) => e.label)).toEqual([
      "v0.1.8",
      "v0.3.0",
      "v0.1.6",
    ]);
  });

  it("dashes a use that is not at runtime and says what it is for", () => {
    const withTest: Dependencies = {
      ...graph,
      edges: [
        ...graph.edges,
        {
          from: ref("tools", "rongo"),
          to: ref("tools", "webfetch"),
          version: "",
          coords: ["go:example.invalid/webfetch"],
          scope: "test",
        },
      ],
    };
    const ir = depGraph(withTest);
    expect(ir.edges.map((e) => [e.label, e.dashed])).toEqual([
      ["v0.1.8", false],
      ["v0.3.0", false],
      ["v0.1.6", false],
      ["Test", true],
    ]);
    expect(depGraph(withTest, true).edges).toHaveLength(3);
    expect(
      edgeLabel({ ...withTest.edges[0], scope: "managed", version: "2.0" }),
    ).toBe("Versionen · 2.0");
    expect(drawnGraph(withTest)).toMatchObject({ draw: true, mainOnly: false });
  });

  it("draws the uses at runtime alone when all would not fit", () => {
    const many: Dependencies = {
      ...graph,
      edges: [
        ...graph.edges,
        ...Array.from({ length: 20 }, (_, i) => ({
          from: ref("tools", `t${i}`),
          to: ref("tools", "llmwire"),
          version: "",
          coords: ["go:example.invalid/llmwire"],
          scope: "test" as const,
        })),
      ],
    };
    const g = drawnGraph(many);
    expect(g).toMatchObject({ draw: true, mainOnly: true });
    expect(g.ir.edges.every((e) => !e.dashed)).toBe(true);
  });

  it("names a repository with its project only across projects", () => {
    expect(repoNamer(graph)(ref("tools", "loom"))).toBe("loom");
    const mixed = { ...graph, repos: [...graph.repos, ref("apps", "chat")] };
    expect(repoNamer(mixed)(ref("tools", "loom"))).toBe("tools/loom");
  });

  it("marks the versions the other users do not name", () => {
    const rows = sharedRows(graph);
    expect(rows.map((r) => r.lib.slug)).toEqual(["llmwire", "webfetch"]);
    expect(rows[0].users.map((u) => [u.ref.slug, u.odd])).toEqual([
      ["loom", true],
      ["rongo", true],
    ]);
    expect(rows[1].users[0].odd).toBe(false);
  });

  it("marks only the versions apart from the most common one", () => {
    expect(oddVersions(["v1", "v1", "v0"])).toEqual([false, false, true]);
    expect(oddVersions(["v1", "v0"])).toEqual([true, true]);
    expect(oddVersions(["v1", "v1"])).toEqual([false, false]);
    expect(oddVersions([])).toEqual([]);
    expect(shortVersion("v0.1.8")).toBe("v0.1.8");
    expect(shortVersion("git+https://host.example/x.git#a1b2c3")).toBe(
      "git+https://hos…",
    );
    // No version written is no disagreement.
    expect(oddVersions(["1.4.0", ""])).toEqual([false, false]);
    expect(oddVersions(["v1", "v1", "v0", ""])).toEqual([
      false,
      false,
      true,
      false,
    ]);
  });
});

describe("DepsPage", () => {
  it("lists the shared repositories with their users and the ambiguous ones", async () => {
    serve({
      dependencies: graph,
      sources: { enabled: true, sources: [], default_host: "", max_calls: 16 },
    });
    render(<DepsPage />);
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(4);
    expect(within(table).getByText("v0.1.6").getAttribute("title")).toMatch(
      /Andere Version/,
    );
    expect(
      within(table)
        .getAllByText("llmwire")[0]
        .closest("a")
        ?.getAttribute("href"),
    ).toBe("/projects/tools/repos/llmwire");
    expect(screen.getByText("npm:shared")).toBeInTheDocument();
    expect(screen.getByText(/von zwei Quellen geclont/)).toBeInTheDocument();
  });

  it("says what a dashed edge is, and the scope beside its version", async () => {
    const withTest: Dependencies = {
      ...graph,
      edges: [
        ...graph.edges,
        {
          from: ref("tools", "rongo"),
          to: ref("tools", "webfetch"),
          version: "v0.2.0",
          coords: ["go:example.invalid/webfetch"],
          scope: "test",
        },
      ],
    };
    serve({
      dependencies: withTest,
      sources: { enabled: true, sources: [], default_host: "" },
    });
    render(<DepsPage />);
    const table = await screen.findByRole("table");
    expect(screen.getByText(/^Ocker gestrichelt:/)).toBeInTheDocument();
    const row = within(table).getByText("v0.2.0").closest("tr")!;
    // A pill of its own beside the version, not part of its text.
    expect(within(row).getByText("Test")).not.toBe(
      within(row).getByText("v0.2.0"),
    );
  });

  it("says so when the sources cannot be read, not loading for ever", async () => {
    serve({ dependencies: graph });
    render(<DepsPage />);
    expect(
      await screen.findByText("Die Dependencies konnten nicht geladen werden."),
    ).toBeInTheDocument();
  });

  it("says when there is nothing to show, and when the sources are off", async () => {
    serve({
      dependencies: { repos: [], edges: [], ambiguous: [] },
      sources: { enabled: true, sources: [], default_host: "", max_calls: 16 },
    });
    const { unmount } = render(<DepsPage />);
    expect(
      await screen.findByText(/Noch keine Dependency/),
    ).toBeInTheDocument();
    unmount();
    serve({
      dependencies: { repos: [], edges: [], ambiguous: [] },
      sources: { enabled: false, sources: [], default_host: "", max_calls: 0 },
    });
    render(<DepsPage />);
    expect(await screen.findByText(/BACKEND_REPOS_DIR/)).toBeInTheDocument();
  });
});

describe("RepoPage dependencies", () => {
  it("lists what it uses, who else does, and who uses it, in the column", async () => {
    serve({ "projects/tools/repos/rongo": repo });
    render(<RepoPage projectKey="tools" slug="rongo" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    const link = within(desc).getByRole("link", { name: "llmwire" });
    expect(link.getAttribute("href")).toBe("/projects/tools/repos/llmwire");
    const row = link.closest("tr")!;
    expect(within(row).getByText("auch loom")).toBeInTheDocument();
    const version = within(row).getByText("v0.1.6");
    expect(version.className).toMatch(/text-ochre/);
    expect(version.getAttribute("title")).toMatch(/Andere Version/);
    const chat = within(desc).getByRole("link", { name: "apps/chat" });
    expect(chat.getAttribute("href")).toBe("/projects/apps/repos/chat");
    expect(within(chat.closest("tr")!).getByText("–")).toBeInTheDocument();
    // The rail points at the section, uses · used by; it lists none.
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    const entry = within(rail).getByText("Dependencies").closest("a")!;
    expect(entry.getAttribute("href")).toBe("#dependencies");
    expect(entry.textContent).toContain("1 · 1");
    expect(within(rail).queryByText("llmwire")).toBeNull();
  });

  it("has no section for a repository without edges", async () => {
    serve({
      "projects/tools/repos/rongo": { ...repo, uses: [], used_by: [] },
    });
    render(<RepoPage projectKey="tools" slug="rongo" />);
    await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(screen.queryByText("Dependencies")).toBeNull();
  });
});
