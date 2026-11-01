import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Dependencies, RepoDetail } from "./api";
import {
  DepsPage,
  depGraph,
  libraryRows,
  oddVersions,
  repoNamer,
} from "./DepsPage";
import { RepoPage, depGroups } from "./RepoPage";

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
    },
    {
      from: ref("tools", "loom"),
      to: ref("tools", "webfetch"),
      version: "v0.3.0",
      coords: ["go:example.invalid/webfetch"],
    },
    {
      from: ref("tools", "rongo"),
      to: ref("tools", "llmwire"),
      version: "v0.1.6",
      coords: ["go:example.invalid/llmwire"],
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
  uses: [
    {
      project: "tools",
      slug: "llmwire",
      version: "v0.1.6",
      also: [{ project: "tools", slug: "loom", version: "v0.1.8" }],
    },
  ],
  used_by: [{ project: "apps", slug: "chat", version: "" }],
};

describe("dependency graph", () => {
  it("draws users left, libraries right, one edge per use", () => {
    const ir = depGraph(graph);
    expect(ir.direction).toBe("LR");
    const zone = Object.fromEntries(ir.nodes.map((n) => [n.label, n.zone]));
    expect(zone).toEqual({
      loom: "users",
      llmwire: "libraries",
      webfetch: "libraries",
      rongo: "users",
    });
    expect(ir.edges.map((e) => e.label)).toEqual([
      "v0.1.8",
      "v0.3.0",
      "v0.1.6",
    ]);
  });

  it("names a repository with its project only across projects", () => {
    expect(repoNamer(graph)(ref("tools", "loom"))).toBe("loom");
    const mixed = { ...graph, repos: [...graph.repos, ref("apps", "chat")] };
    expect(repoNamer(mixed)(ref("tools", "loom"))).toBe("tools/loom");
  });

  it("marks the versions the other users do not name", () => {
    const rows = libraryRows(graph);
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
  it("lists the libraries with their users and the ambiguous ones", async () => {
    serve({
      dependencies: graph,
      sources: { enabled: true, sources: [], default_host: "" },
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
    ).toBe("/projekte/tools/repos/llmwire");
    expect(screen.getByText("npm:shared")).toBeInTheDocument();
    expect(screen.getByText(/von zwei Quellen geclont/)).toBeInTheDocument();
  });

  it("says so when the sources cannot be read, not loading for ever", async () => {
    serve({ dependencies: graph });
    render(<DepsPage />);
    expect(
      await screen.findByText(/konnte nicht geladen werden/),
    ).toBeInTheDocument();
  });

  it("says when there is nothing to show, and when the sources are off", async () => {
    serve({
      dependencies: { repos: [], edges: [], ambiguous: [] },
      sources: { enabled: true, sources: [], default_host: "" },
    });
    const { unmount } = render(<DepsPage />);
    expect(
      await screen.findByText(/Noch keine Abhängigkeit/),
    ).toBeInTheDocument();
    unmount();
    serve({
      dependencies: { repos: [], edges: [], ambiguous: [] },
      sources: { enabled: false, sources: [], default_host: "" },
    });
    render(<DepsPage />);
    expect(await screen.findByText(/BACKEND_REPOS_DIR/)).toBeInTheDocument();
  });
});

describe("RepoPage dependencies", () => {
  it("lists what it uses, who else does, and who uses it", async () => {
    serve({ "projects/tools/repos/rongo": repo });
    render(<RepoPage projectKey="tools" slug="rongo" />);
    const link = await screen.findByRole("link", { name: /llmwire/ });
    expect(link.getAttribute("href")).toBe("/projekte/tools/repos/llmwire");
    expect(within(link).getByText("auch loom")).toBeInTheDocument();
    const version = within(link).getByText("v0.1.6");
    expect(version.className).toMatch(/text-ochre/);
    expect(
      screen.getByRole("link", { name: "apps/chat" }).getAttribute("href"),
    ).toBe("/projekte/apps/repos/chat");
  });

  it("has no lists for a repository without edges", () => {
    expect(depGroups({ ...repo, uses: [], used_by: [] })).toEqual([]);
  });
});
