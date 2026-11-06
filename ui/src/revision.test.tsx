import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RevisionDetail } from "./api";
import { RevisionPage } from "./RevisionPage";

/** serve answers each API path from a table. */
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
  window.history.replaceState({}, "", "/");
});

const mergedAt = new Date(2026, 9, 28, 12, 0).toISOString();

const revision: RevisionDetail = {
  scope: "repository",
  project_key: "PAY",
  repo_slug: "pay-api",
  revision: 2,
  current: 3,
  previous: 1,
  created_at: mergedAt,
  summary_changed: true,
  description: {
    revision: 2,
    summary: "Der Zahlungsdienst, neu gefasst.",
    source_kind: "pr",
    source_pr_key: "PAY/pay-api#231",
    source_commit: "",
    updated_at: mergedAt,
    statements: [
      {
        id: "s1",
        kind: "component",
        name: "Auftragsannahme",
        text: "Nimmt Aufträge an.",
        files: ["internal/orders/intake.go"],
        sources: [],
        area: "Aufträge",
      },
      {
        id: "s3",
        kind: "rule",
        name: "Grenze",
        text: "Die kleinere gilt.",
        files: [],
        sources: [],
        area: "Aufträge",
      },
    ],
  },
  added: [{ id: "s3", kind: "rule", name: "Grenze" }],
  changed: [{ id: "s1", kind: "component", name: "Auftragsannahme" }],
  dropped: [{ id: "s2", kind: "entity", name: "Beleg" }],
};

describe("RevisionPage", () => {
  it("shows what the revision changed, then the description as it stood", async () => {
    serve({ "projects/PAY/repos/pay-api/revisions/2": revision });
    window.history.replaceState(
      {},
      "",
      "/projects/PAY/repos/pay-api/revisions/2",
    );
    render(<RevisionPage projectKey="PAY" slug="pay-api" n={2} />);
    expect(await screen.findByText("Revision 2 von 3")).toBeInTheDocument();
    expect(document.title).toContain("Revision 2");
    // The crumbs lead back; previous, next and the current page a step away.
    const links = (name: string) =>
      screen.getByText(name).closest("a")?.getAttribute("href");
    expect(links("‹ Vorherige")).toBe(
      "/projects/PAY/repos/pay-api/revisions/1",
    );
    expect(links("Nächste ›")).toBe("/projects/PAY/repos/pay-api/revisions/3");
    expect(links("Aktuelle Seite")).toBe("/projects/PAY/repos/pay-api");
    expect(screen.getByText("PAY/pay-api#231")).toBeInTheDocument();
    expect(screen.getByText("gegenüber Revision 1")).toBeInTheDocument();
    // The diff by kind of change, a dropped statement struck, unlinked.
    expect(screen.getByText("Überblick geändert")).toBeInTheDocument();
    expect(screen.getByText("Neu")).toBeInTheDocument();
    const added = screen.getByText("Grenze", { selector: "dd span" });
    expect(added.closest("li")).not.toHaveClass("line-through");
    const dropped = screen.getByText("Beleg");
    expect(dropped.closest("li")).toHaveClass("line-through");
    // The description as it stood, under its own heading.
    expect(
      screen.getByText("Der Zahlungsdienst, neu gefasst."),
    ).toBeInTheDocument();
    expect(screen.getByText("Nimmt Aufträge an.")).toBeInTheDocument();
    expect(
      screen.getByText("Beschreibung in dieser Revision"),
    ).toBeInTheDocument();
  });

  it("says when a later revision changed nothing", async () => {
    serve({
      "projects/PAY/repos/pay-api/revisions/3": {
        ...revision,
        revision: 3,
        current: 3,
        previous: 2,
        summary_changed: false,
        added: [],
        changed: [],
        dropped: [],
      },
    });
    render(<RevisionPage projectKey="PAY" slug="pay-api" n={3} />);
    expect(
      await screen.findByText("Revision 3, die aktuelle"),
    ).toBeInTheDocument();
    expect(screen.getByText("gegenüber Revision 2")).toBeInTheDocument();
    expect(screen.getByText(/Keine Aussage hinzugekommen/)).toBeInTheDocument();
    expect(screen.queryByText("Nächste ›")).toBeNull();
  });

  it("reads the first revision as all new, the summary with it, the current one so", async () => {
    serve({
      "projects/PAY/revisions/1": {
        ...revision,
        scope: "project",
        repo_slug: undefined,
        revision: 1,
        current: 1,
        previous: 0,
        summary_changed: true,
        added: [{ id: "s1", kind: "entity", name: "Auftrag" }],
        changed: [],
        dropped: [],
      },
    });
    render(<RevisionPage projectKey="PAY" slug="" n={1} />);
    expect(
      await screen.findByText("Revision 1, die aktuelle"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("die erste Revision: alles neu"),
    ).toBeInTheDocument();
    expect(screen.getByText("Überblick geändert")).toBeInTheDocument();
    // A project page's entity reads as a shared one.
    expect(screen.getByText("Geteilte Entität")).toBeInTheDocument();
    expect(screen.queryByText("‹ Vorherige")).toBeNull();
    expect(screen.queryByText("Nächste ›")).toBeNull();
    expect(
      screen.getByText("Aktuelle Seite").closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY");
    // The crumbs end at the project.
    const crumbs = screen.getByRole("navigation", { name: "Pfad" });
    expect(
      within(crumbs).getByText("PAY").closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY");
    expect(within(crumbs).getByText("Revision 1")).toBeInTheDocument();
  });

  it("fails cleanly", async () => {
    serve({});
    render(<RevisionPage projectKey="PAY" slug="pay-api" n={9} />);
    expect(
      await screen.findByText("Die Revision konnte nicht geladen werden."),
    ).toBeInTheDocument();
  });
});
