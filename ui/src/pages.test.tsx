import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { FaqPage } from "./FaqPage";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { RepoPage } from "./RepoPage";
import type { NoteDetail, ProjectDetail, Projects, RepoDetail } from "./api";
import type { FlowIR } from "./diagramIR";

/** serve answers each API path from a table, so a page under test fetches
 *  exactly what it would in the browser. */
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

const projects: Projects = {
  projects: [
    {
      key: "PAY",
      team: "platform",
      repos: 4,
      notes: 23,
      revision: 7,
      last_merge: mergedAt,
    },
    {
      key: "MOB",
      team: "mobile",
      repos: 1,
      notes: 2,
      revision: 0,
      last_merge: null,
    },
  ],
};

const project: ProjectDetail = {
  key: "PAY",
  team: "platform",
  description: {
    revision: 7,
    summary:
      "Das Projekt Payments nimmt Zahlungsaufträge an.\n\nEin Auftrag durchläuft Zustände.",
    statements: [
      {
        id: "s1",
        kind: "component",
        name: "Auftragsannahme",
        text: "Nimmt Aufträge an.\n\nDer Mandant kommt aus der Kopfzeile.",
        files: ["pay-api/internal/orders/intake.go"],
        sources: [
          {
            pr_key: "PAY/pay-api#212",
            ticket_key: "PAY-9",
            merged_at: mergedAt,
          },
        ],
      },
      {
        id: "s2",
        kind: "rule",
        name: "Kleinere Grenze gilt",
        text: "Die kleinere Grenze gilt.",
        files: [],
        sources: [],
      },
    ],
    source_pr_key: "PAY/pay-api#231",
    updated_at: mergedAt,
  },
  map: null,
  map_expected: false,
  map_declined: false,
  repos: [
    {
      slug: "pay-api",
      revision: 5,
      summary: "Nimmt Zahlungsaufträge an.",
      notes: 11,
      last_merge: mergedAt,
    },
    { slug: "pay-lib", revision: 0, summary: "", notes: 2, last_merge: null },
  ],
  problems: [
    {
      repo_slug: "pay-clearing",
      area: "internal/files",
      component: "Clearing-Adapter",
      bugfixes: 4,
      top_cause: "logic",
      top_cause_count: 3,
      issues_posted: 7,
      suggestions_posted: 3,
      last_fix_pr_key: "PAY/pay-clearing#77",
      last_fix_at: mergedAt,
    },
  ],
  problem_window_days: 90,
  recent_notes: [
    {
      pr_id: 231,
      pr_key: "PAY/pay-api#231",
      pr_title: "Tageslimit und Kontolimit gemeinsam prüfen",
      repo_slug: "pay-api",
      ticket_key: "PAY-412",
      change_kind: "bugfix",
      merged_at: mergedAt,
      items: 3,
      issues_posted: 2,
      suggestions_posted: 1,
    },
  ],
};

const repo: RepoDetail = {
  project_key: "PAY",
  slug: "pay-clearing",
  description: null,
  notes_total: 250,
  last_merge: mergedAt,
  revisions: [
    {
      revision: 2,
      source_pr_key: "PAY/pay-clearing#77",
      statements: 7,
      chars: 1912,
      created_at: mergedAt,
    },
    {
      revision: 1,
      source_pr_key: "PAY/pay-clearing#51",
      statements: 2,
      chars: 520,
      created_at: mergedAt,
    },
  ],
  notes: project.recent_notes,
};

const note: NoteDetail = {
  ...project.recent_notes[0],
  project_key: "PAY",
  jira_issue_type: "Bug",
  reviewed_commit: "abc1234abc1234",
  source_commit: "def5678def5678",
  merge_commit: "0123456789abcdef",
  run_id: 77,
  model_label: "gpt-5.5-high",
  bugfix: {
    symptom: "Upload bricht ab.",
    cause: "Retry ohne Backoff.",
    cause_kind: "logic",
  },
  note: {
    summary: "Der Export wiederholte ohne Grenze.",
    change_kind: "bugfix",
    components: [
      {
        name: "Sammeldatei-Export",
        text: "Bündelt Aufträge.\n\nLädt per SFTP hoch.",
        files: ["internal/files/export.go"],
      },
    ],
    rules: [],
    invariants: null,
    entities: [],
  },
  received_at: mergedAt,
  consolidated_at: null,
  note_call: {
    outcome: "ok",
    note_status: "written",
    model: "gpt-5.5",
    input_tokens: 100000,
    cache_read_tokens: 90000,
    output_tokens: 800,
    cost_usd: "0.012",
    rounds: 3,
    rounds_input_tokens: 300000,
    rounds_output_tokens: 2400,
    rounds_cost_usd: "0.040",
  },
};

describe("ProjectsPage", () => {
  it("lists projects as cards, a project without a description says so", async () => {
    serve({ projects });
    render(<ProjectsPage />);
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    expect(screen.getByText(/Revision 7/)).toBeInTheDocument();
    expect(screen.getByText("noch keine Beschreibung")).toBeInTheDocument();
    expect(screen.getByText(/letzter Merge nie/)).toBeInTheDocument();
    expect(screen.getByText("PAY").closest("a")?.getAttribute("href")).toBe(
      "/projekte/PAY",
    );
  });
  it("leaves out the team of a project known only as a source", async () => {
    serve({
      projects: {
        projects: [{ ...projects.projects[0]!, key: "tools", team: "" }],
      },
    });
    render(<ProjectsPage />);
    expect(await screen.findByText("tools")).toBeInTheDocument();
    expect(screen.queryByText(/Team/)).not.toBeInTheDocument();
  });
  it("shows the empty and the failed state", async () => {
    serve({ projects: { projects: [] } });
    render(<ProjectsPage />);
    expect(await screen.findByText(/Noch kein Projekt/)).toBeInTheDocument();
    serve({});
    render(<ProjectsPage />);
    expect(
      await screen.findByText(/konnte nicht geladen werden/),
    ).toBeInTheDocument();
  });
});

describe("ProjectPage", () => {
  it("renders the description as paragraphs, the statements with chips, the tables", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByText(
        "Das Projekt Payments nimmt Zahlungsaufträge an.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Ein Auftrag durchläuft Zustände."),
    ).toBeInTheDocument();
    expect(screen.getByText("Auftragsannahme")).toBeInTheDocument();
    expect(
      screen.getByText("Der Mandant kommt aus der Kopfzeile."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("pay-api/internal/orders/intake.go"),
    ).toBeInTheDocument();
    const chip = screen.getByText("PAY/pay-api#212");
    expect(chip.closest("a")?.getAttribute("href")).toBe(
      "/projekte/PAY/repos/pay-api/notizen/212",
    );
    expect(screen.getByText("Komponenten")).toBeInTheDocument();
    expect(screen.getByText("Invarianten")).toBeInTheDocument();
    expect(screen.getByText("Clearing-Adapter")).toBeInTheDocument();
    expect(screen.getByText("Logik")).toBeInTheDocument();
    expect(
      screen.getByText("Tageslimit und Kontolimit gemeinsam prüfen"),
    ).toBeInTheDocument();
    expect(screen.getByText("pay-lib").closest("a")?.getAttribute("href")).toBe(
      "/projekte/PAY/repos/pay-lib",
    );
    expect(screen.getByText(/Beschreibung Revision 7/)).toBeInTheDocument();
    expect(document.title).toContain("PAY");
  });
  it("says so without a description and fails cleanly", async () => {
    serve({
      "projects/PAY": {
        ...project,
        description: null,
        problems: [],
        recent_notes: [],
        repos: [],
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByText(/Noch keine Beschreibung\./),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Keine Bugfixes im Zeitfenster."),
    ).toBeInTheDocument();
    serve({});
    render(<ProjectPage projectKey="NOPE" />);
    expect(
      await screen.findByText(/das Projekt konnte nicht geladen werden/),
    ).toBeInTheDocument();
  });

  const map: FlowIR = {
    kind: "flow",
    direction: "LR",
    zones: [
      { id: "clients", title: "Clients" },
      { id: "repositories", title: "Repos" },
      { id: "stores", title: "Stores" },
    ],
    nodes: [
      { id: "browser", label: "Browser", shape: "pill", zone: "clients" },
      { id: "pay-api", label: "pay-api", shape: "step", zone: "repositories" },
      {
        id: "pay-gone",
        label: "pay-gone",
        shape: "step",
        zone: "repositories",
      },
      { id: "pg", label: "PostgreSQL", shape: "step", zone: "stores" },
    ],
    edges: [
      {
        from: "browser",
        to: "pay-api",
        label: "HTTP",
        dashed: false,
        head: "end",
      },
      { from: "pay-api", to: "pg", label: "DB", dashed: false, head: "end" },
      {
        from: "pay-gone",
        to: "pay-api",
        label: "event",
        dashed: true,
        head: "end",
      },
    ],
  };

  it("draws the system map above the summary, repository nodes linked", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const drawing = await screen.findByRole("img", { name: "Systemkarte" });
    expect(drawing.querySelector("svg")).toBeTruthy();
    // The zones are titled from strings.ts, not by the model.
    expect(drawing.textContent).toContain("Datenspeicher");
    expect(drawing.textContent).not.toContain("Stores");
    const link = screen.getByRole("link", {
      name: "Repository pay-api öffnen",
    });
    expect(link.getAttribute("href")).toBe("/projekte/PAY/repos/pay-api");
    expect(link.style.left).toMatch(/%$/);
    // A node outside the repositories zone, or a slug the project no longer
    // lists, links nowhere.
    expect(
      screen.queryByRole("link", { name: /PostgreSQL|Browser/ }),
    ).toBeNull();
    expect(screen.queryByRole("link", { name: /pay-gone/ })).toBeNull();
    // Above the summary.
    const summary = screen.getByText(
      "Das Projekt Payments nimmt Zahlungsaufträge an.",
    );
    expect(
      drawing.compareDocumentPosition(summary) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("opens the map whole", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Systemkarte: Vollansicht" }),
    );
    expect(screen.getByRole("dialog").getAttribute("aria-label")).toBe(
      "Systemkarte",
    );
  });

  it("draws a statement's diagram under its paragraphs", async () => {
    const statements = [
      {
        ...project.description!.statements[0],
        diagram: {
          kind: "sequence" as const,
          actors: [
            { id: "api", label: "pay-api" },
            { id: "lib", label: "pay-lib" },
          ],
          rows: [
            {
              type: "message" as const,
              from: "api",
              to: "lib",
              label: "Limit prüfen",
              line: "solid" as const,
              head: "filled" as const,
            },
          ],
        },
      },
      project.description!.statements[1],
    ];
    serve({
      "projects/PAY": {
        ...project,
        map,
        description: { ...project.description!, statements },
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    const drawing = await screen.findByRole("img", { name: "Sequenzdiagramm" });
    expect(drawing.textContent).toContain("Limit prüfen");
    const text = screen.getByText("Der Mandant kommt aus der Kopfzeile.");
    expect(
      text.compareDocumentPosition(drawing) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("draws nothing without a diagram, and says the map is still missing", async () => {
    serve({ "projects/PAY": { ...project, map_expected: true } });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByText(/Noch keine Systemkarte/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText(/wird gezeichnet/)).toBeNull();
  });

  it("promises no map again once a run asked and none was accepted", async () => {
    serve({
      "projects/PAY": { ...project, map_expected: true, map_declined: true },
    });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByText(
        "Das Projekt Payments nimmt Zahlungsaufträge an.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Noch keine Systemkarte/)).toBeNull();
    expect(screen.queryByText("Systemkarte")).toBeNull();
  });

  it("promises no map where no run will draw one", async () => {
    serve({ "projects/PAY": { ...project, map_expected: false } });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByText(
        "Das Projekt Payments nimmt Zahlungsaufträge an.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Noch keine Systemkarte/)).toBeNull();
    expect(screen.queryByText("Systemkarte")).toBeNull();
    // The page asks for nothing beyond its project.
    expect(vi.mocked(fetch)).not.toHaveBeenCalledWith(
      expect.stringContaining("sources"),
      expect.anything(),
    );
  });
});

describe("RepoPage", () => {
  it("lists revisions with the current one marked", async () => {
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        description: { ...project.description!, revision: 2 },
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    expect(await screen.findByText("aktuell")).toBeInTheDocument();
    expect(screen.getByText(/7 Aussagen/)).toBeInTheDocument();
    expect(screen.getByText(/1 912 Zeichen/)).toBeInTheDocument();
    expect(
      screen
        .getByText("PAY/pay-clearing#51")
        .closest("a")
        ?.getAttribute("href"),
    ).toBe("/projekte/PAY/repos/pay-clearing/notizen/51");
  });

  it("shows a baseline as a commit, a quiet chip that links nowhere", async () => {
    const base = {
      kind: "baseline" as const,
      commit: "4e1f0c2abcdef",
      committed_at: mergedAt,
    };
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        revisions: [
          {
            revision: 3,
            source_kind: "baseline",
            source_pr_key: "",
            source_commit: "4e1f0c2abcdef",
            statements: 1,
            chars: 30,
            created_at: mergedAt,
          },
          ...repo.revisions,
        ],
        description: {
          ...project.description!,
          statements: [
            {
              id: "s1",
              kind: "component",
              name: "Abrechnungslauf",
              text: "Rechnet ab.",
              files: ["billing/run.go"],
              // A PR source of before baselines has no kind.
              sources: [
                { pr_key: "PAY/pay-clearing#77", merged_at: mergedAt },
                base,
              ],
              checked: "9a8b7c6abcdef",
            },
          ],
        },
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    const chips = await screen.findAllByText("Basis 4e1f0c2");
    expect(chips).toHaveLength(2);
    for (const chip of chips) {
      expect(chip.closest("a")).toBeNull();
    }
    // The statement's chip carries the commit's date; the revision row
    // shows its own date beside it.
    expect(chips[0]?.getAttribute("title")).toMatch(
      /^Basis bei Commit 4e1f0c2, /,
    );
    expect(
      screen
        .getAllByText("PAY/pay-clearing#77")[0]
        ?.closest("a")
        ?.getAttribute("href"),
    ).toBe("/projekte/PAY/repos/pay-clearing/notizen/77");
    expect(screen.getByText("Geprüft 9a8b7c6")).toBeInTheDocument();
  });

  it("names the repository of a baseline source when it carries one", async () => {
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        description: {
          ...project.description!,
          statements: [
            {
              id: "s1",
              kind: "entity",
              name: "Auftrag",
              text: "Ein Auftrag.",
              files: [],
              sources: [
                { kind: "baseline", commit: "4e1f0c2abcdef", repo: "pay-api" },
              ],
            },
          ],
        },
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    expect(
      await screen.findByText("Basis pay-api 4e1f0c2"),
    ).toBeInTheDocument();
  });
});

describe("NotePage", () => {
  it("shows the bugfix, the items as paragraphs and the call tiles", async () => {
    serve({ "projects/PAY/repos/pay-api/notes/231": note });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect(await screen.findByText("Upload bricht ab.")).toBeInTheDocument();
    expect(screen.getByText("Retry ohne Backoff.")).toBeInTheDocument();
    expect(screen.getByText("Logik")).toBeInTheDocument();
    expect(screen.getByText("Lädt per SFTP hoch.")).toBeInTheDocument();
    expect(screen.getByText("90 %")).toBeInTheDocument();
    expect(screen.getByText("$0.012")).toBeInTheDocument();
    expect(screen.getByText("$0.040")).toBeInTheDocument();
    expect(screen.getByText("noch nicht")).toBeInTheDocument();
    expect(screen.getByText(/\(Bug\)/)).toBeInTheDocument();
  });
  it("says when noergler reported no call", async () => {
    serve({
      "projects/PAY/repos/pay-api/notes/231": {
        ...note,
        note_call: null,
        note: null,
        bugfix: null,
      },
    });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect(
      await screen.findByText(/keinen Aufruf gemeldet/),
    ).toBeInTheDocument();
    expect(screen.getByText(/keine Zusammenfassung/)).toBeInTheDocument();
  });
});

describe("App", () => {
  it("routes by the address and navigates on a click", async () => {
    serve({ projects, "projects/PAY": project });
    window.history.replaceState({}, "", "/");
    render(<App />);
    expect(await screen.findByText(/Revision 7/)).toBeInTheDocument();
    screen.getByText("PAY").closest("a")!.click();
    await waitFor(() => expect(window.location.pathname).toBe("/projekte/PAY"));
    expect(await screen.findByText("Auftragsannahme")).toBeInTheDocument();
    screen.getByText("FAQ").click();
    expect(await screen.findByText("Was ist Almanach?")).toBeInTheDocument();
    expect(document.title).toContain("FAQ");
  });
  it("renders the FAQ", () => {
    render(<FaqPage />);
    expect(screen.getByText("Wer schreibt hier?")).toBeInTheDocument();
  });
});
