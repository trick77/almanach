import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SearchResult } from "./api";
import { SearchPage } from "./SearchPage";
import { S } from "./strings";

/** serve answers each API path from a table, recording the calls. */
function serve(table: Record<string, unknown>): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      calls.push(url);
      const path = url.replace("/api/", "");
      if (!(path in table)) {
        return { ok: false, status: 404, json: async () => ({}) };
      }
      return { ok: true, status: 200, json: async () => table[path] };
    }),
  );
  return calls;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

const result: SearchResult = {
  query: "auftrag",
  more: 0,
  pages: [
    {
      scope: "project",
      project_key: "PAY",
      single: false,
      summary: "Das Projekt Payments. Ein Auftrag durchläuft Zustände.",
      statements: [
        {
          id: "s1",
          kind: "flow",
          name: "Upload",
          area: "Aufträge",
          snippet: "Vom Auftrag zur Datei.",
        },
      ],
    },
    {
      scope: "repository",
      project_key: "PAY",
      repo_slug: "pay-api",
      single: false,
      statements: [
        {
          id: "s1",
          kind: "component",
          name: "Auftragsannahme",
          area: "",
          snippet: "",
        },
        {
          id: "s3",
          kind: "rule",
          name: "Grenze",
          area: "",
          snippet: "",
          file: "internal/auftrag/limit.go",
        },
        {
          id: "s2",
          kind: "entity",
          name: "Auftrag",
          area: "",
          snippet: "… ein Auftrag im Eingang …",
        },
      ],
    },
  ],
};

describe("SearchPage", () => {
  it("lists the hits by page, each a deep link, the phrase marked", async () => {
    const calls = serve({ "search?q=auftrag": result });
    render(<SearchPage q=" auftrag " />);
    expect(
      await screen.findByText("5 Treffer auf 2 Seiten"),
    ).toBeInTheDocument();
    expect(calls).toEqual(["/api/search?q=auftrag"]);
    expect(document.title).toContain("Suche");
    // The page's name heads its hits and links to it.
    const project = screen.getByText("Projekt PAY").closest("a");
    expect(project?.getAttribute("href")).toBe("/projects/PAY");
    const repo = screen.getByText("PAY/pay-api").closest("a");
    expect(repo?.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    // The summary's hit first, then the statements, each by its anchor.
    const links = screen
      .getAllByRole("link")
      .map((a) => a.getAttribute("href"));
    expect(links).toEqual([
      "/projects/PAY",
      "/projects/PAY#description/summary",
      "/projects/PAY#description/s1",
      "/projects/PAY/repos/pay-api",
      "/projects/PAY/repos/pay-api#description/s1",
      "/projects/PAY/repos/pay-api#description/s3",
      "/projects/PAY/repos/pay-api#description/s2",
    ]);
    // The phrase is marked in the snippet, in the name and in the area; a
    // hit by name alone shows no snippet line.
    const marks = screen.getAllByText("Auftrag", { selector: "mark" });
    expect(marks.length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText("Ablauf")).toBeInTheDocument();
    expect(screen.getByText("Komponente")).toBeInTheDocument();
    // A repository page's entity is its own, not a shared one.
    expect(screen.getByText("Entität")).toBeInTheDocument();
    const row = screen.getByText("sannahme").closest("li");
    expect(within(row!).queryByRole("paragraph")).toBeNull();
    // A hit by file alone shows the file, the phrase marked in it.
    const byFile = screen.getByText("Grenze").closest("li");
    expect(within(byFile!).getByText(/limit\.go/)).toBeInTheDocument();
    expect(
      within(byFile!).getByText("auftrag", { selector: "mark" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/weitere Seite/)).toBeNull();
  });

  it("puts a project of one at the project's path, by its key", async () => {
    serve({
      "search?q=auftrag": {
        ...result,
        pages: [{ ...result.pages[1], single: true }],
        more: 2,
      },
    });
    render(<SearchPage q="auftrag" />);
    expect(
      await screen.findByText("3 Treffer auf einer Seite"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Projekt PAY").closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY");
    expect(
      screen.getByText("sannahme").closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY#description/s1");
    expect(screen.getByText(S.search.more(2))).toBeInTheDocument();
  });

  it("marks a phrase typed with two spaces, as the server matched it", async () => {
    const calls = serve({
      "search?q=Auftrag%20zur": { ...result, query: "Auftrag zur" },
    });
    render(<SearchPage q="Auftrag  zur" />);
    expect(
      await screen.findByText("Auftrag zur", { selector: "mark" }),
    ).toBeInTheDocument();
    expect(calls).toEqual(["/api/search?q=Auftrag%20zur"]);
  });

  it("says when nothing matched, when the phrase is too short, and when the search failed", async () => {
    const calls = serve({ "search?q=nirgends": { ...result, pages: [] } });
    const { unmount } = render(<SearchPage q="nirgends" />);
    expect(
      await screen.findByText("Keine Treffer für «nirgends»."),
    ).toBeInTheDocument();
    unmount();
    render(<SearchPage q="a" />);
    expect(screen.getByText(S.search.tooShort)).toBeInTheDocument();
    expect(calls).toEqual(["/api/search?q=nirgends"]);
    unmount();
    render(<SearchPage q="boom" />);
    expect(await screen.findByText(S.search.failed)).toBeInTheDocument();
  });
});
