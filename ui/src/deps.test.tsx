import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Dependencies, RepoDetail } from "./api";
import {
  DepsPage,
  depGraph,
  drawnGraph,
  sharedRows,
  repoNamer,
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
      coords: ["go:example.invalid/llmwire"],
      scope: "main",
    },
    {
      from: ref("tools", "loom"),
      to: ref("tools", "webfetch"),
      coords: ["go:example.invalid/webfetch"],
      scope: "main",
    },
    {
      from: ref("tools", "rongo"),
      to: ref("tools", "llmwire"),
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
  stale: null,
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
      also: [{ project: "tools", slug: "loom" }],
    },
  ],
  used_by: [{ project: "apps", slug: "chat" }],
  messaging: { topics: [], schemas: [] },
  interfaces: [],
  parts: { units: [], edges: [], graph: { nodes: [], edges: [] } },
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
    // No version on an edge: the relation is what counts.
    expect(ir.edges.map((e) => e.label)).toEqual(["", "", ""]);
  });

  it("dashes a use that is not at runtime and says what it is for", () => {
    const withTest: Dependencies = {
      ...graph,
      edges: [
        ...graph.edges,
        {
          from: ref("tools", "rongo"),
          to: ref("tools", "webfetch"),
          coords: ["go:example.invalid/webfetch"],
          scope: "test",
        },
      ],
    };
    const ir = depGraph(withTest);
    expect(ir.edges.map((e) => [e.label, e.dashed])).toEqual([
      ["", false],
      ["", false],
      ["", false],
      ["Test", true],
    ]);
    expect(depGraph(withTest, true).edges).toHaveLength(3);
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

  it("groups the users under each shared repository", () => {
    const rows = sharedRows(graph);
    expect(rows.map((r) => r.lib.slug)).toEqual(["llmwire", "webfetch"]);
    expect(rows[0].users.map((u) => [u.ref.slug, u.scope])).toEqual([
      ["loom", "main"],
      ["rongo", "main"],
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
    expect(within(rows[0]).getAllByRole("columnheader")).toHaveLength(2);
    expect(within(table).queryByText(/v0\./)).toBeNull();
    expect(
      within(table)
        .getAllByText("llmwire")[0]
        .closest("a")
        ?.getAttribute("href"),
    ).toBe("/projects/tools/repos/llmwire#description/dependencies");
    expect(
      within(table).getByRole("link", { name: "rongo" }).getAttribute("href"),
    ).toBe("/projects/tools/repos/rongo#description/dependencies");
    expect(screen.getByText("npm:shared")).toBeInTheDocument();
    // An ambiguous provider too, its page holding the section or not.
    expect(screen.getByRole("link", { name: "a" }).getAttribute("href")).toBe(
      "/projects/tools/repos/a#description/dependencies",
    );
    expect(screen.getByText(/von zwei Quellen geclont/)).toBeInTheDocument();
  });

  it("says what a dashed edge is, and the scope beside its user", async () => {
    const withTest: Dependencies = {
      ...graph,
      edges: [
        ...graph.edges,
        {
          from: ref("tools", "rongo"),
          to: ref("tools", "webfetch"),
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
    const pill = within(table).getByText("Test");
    const cell = pill.closest("td")!;
    // A pill of its own beside the user's link, not part of its text.
    expect(within(cell).getByRole("link", { name: "rongo" })).not.toBe(pill);
  });

  it("says so when the sources cannot be read, not loading for ever", async () => {
    serve({ dependencies: graph });
    render(<DepsPage />);
    expect(
      await screen.findByText(
        "Die Abhängigkeiten konnten nicht geladen werden.",
      ),
    ).toBeInTheDocument();
  });

  it("says when there is nothing to show, and when the sources are off", async () => {
    serve({
      dependencies: { repos: [], edges: [], ambiguous: [] },
      sources: { enabled: true, sources: [], default_host: "", max_calls: 16 },
    });
    const { unmount } = render(<DepsPage />);
    expect(
      await screen.findByText(/Noch keine Abhängigkeit/),
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
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    const desc = screen.getByRole("tabpanel", { name: "Nachschlagen" });
    const link = within(desc).getByRole("link", { name: "llmwire" });
    expect(link.getAttribute("href")).toBe("/projects/tools/repos/llmwire");
    const row = link.closest("tr")!;
    expect(within(row).getByText("auch loom")).toBeInTheDocument();
    expect(within(row).getAllByRole("cell")).toHaveLength(1);
    const chat = within(desc).getByRole("link", { name: "apps/chat" });
    expect(chat.getAttribute("href")).toBe("/projects/apps/repos/chat");
    // The rail points at the section, uses · used by; it lists none.
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    const entry = within(rail).getByText("Abhängigkeiten").closest("a")!;
    expect(entry.getAttribute("href")).toBe("#dependencies");
    expect(entry.textContent).toContain("1 · 1");
    expect(within(rail).queryByText("llmwire")).toBeNull();
  });

  it("has no section for a repository without edges", async () => {
    serve({
      "projects/tools/repos/rongo": { ...repo, uses: [], used_by: [] },
    });
    render(<RepoPage projectKey="tools" slug="rongo" />);
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    expect(screen.queryByText("Abhängigkeiten")).toBeNull();
  });
});
