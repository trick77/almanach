import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { FaqPage } from "./FaqPage";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { RepoPage } from "./RepoPage";
import { armSignIn } from "./signin";
import type {
  Me,
  NoteDetail,
  ProjectDetail,
  Projects,
  RepoDetail,
} from "./api";
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
    expect(screen.getByText("Entscheidungen")).toBeInTheDocument();
    expect(screen.getByText("Fallstricke")).toBeInTheDocument();
    // An extension is a repository's: the project page has no such section.
    expect(screen.queryByText("Erweiterungen")).toBeNull();
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

  it("names the kinds for agents in German, in the export's order", async () => {
    const st = (id: string, kind: "decision" | "extension" | "pitfall") => ({
      id,
      kind,
      name: `Aussage ${id}`,
      text: "Text.",
      files: ["a.go"],
      sources: [],
    });
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        description: {
          ...project.description!,
          statements: [
            st("s1", "extension"),
            st("s2", "pitfall"),
            st("s3", "decision"),
          ],
        },
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    const decisions = await screen.findByText("Entscheidungen");
    const pitfalls = screen.getByText("Fallstricke");
    const extensions = screen.getByText("Erweiterungen");
    // Decisions, then pitfalls, then extensions.
    expect(
      decisions.compareDocumentPosition(pitfalls) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      pitfalls.compareDocumentPosition(extensions) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByText("Aussage s1")).toBeInTheDocument();
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
    serve({ me: anonymous, projects, "projects/PAY": project });
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

const anonymous: Me = { mode: "none", user: "" };

/** answer stubs fetch with one response for /api/me and the table's for
 *  the rest, recording every call. */
function answer(
  me: { status: number; body?: unknown; unreadable?: boolean } | Error,
  projectsStatus = 200,
) {
  const calls: { url: string; method: string }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? "GET" });
      if (url === "/api/me") {
        if (me instanceof Error) throw me;
        return {
          ok: me.status < 300,
          status: me.status,
          json: async () => {
            if (me.unreadable) throw new SyntaxError("Unexpected token <");
            return me.body;
          },
        };
      }
      if (url === "/api/projects" && projectsStatus !== 200) {
        return {
          ok: false,
          status: projectsStatus,
          json: async () => ({ error: "sign-in required" }),
        };
      }
      if (url === "/api/auth/logout") {
        return {
          ok: true,
          status: 200,
          json: async () => ({ redirect_url: "/?signed_out=1" }),
        };
      }
      if (url === "/api/projects") {
        return { ok: true, status: 200, json: async () => projects };
      }
      return { ok: false, status: 404, json: async () => ({}) };
    }),
  );
  return calls;
}

/** stubLocation swaps window.location for a plain object, so a test can
 *  see where the gate sends the browser; jsdom does not navigate. */
function stubLocation(search = "", pathname = "/") {
  const loc = { href: "/", pathname, search, reload: vi.fn() };
  vi.stubGlobal("location", loc);
  return loc;
}

describe("session gate", () => {
  afterEach(() => {
    window.sessionStorage.clear();
    armSignIn(false);
  });

  it("renders the wiki without a name or Abmelden in none mode", async () => {
    answer({ status: 200, body: anonymous });
    render(<App />);
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    expect(screen.queryByText("Abmelden")).not.toBeInTheDocument();
    expect(
      document.querySelector('[data-slot="reader"]'),
    ).toBeEmptyDOMElement();
  });

  it("names the reader and signs out in oidc mode", async () => {
    const calls = answer({
      status: 200,
      body: { mode: "oidc", user: "jan@example.com" },
    });
    render(<App />);
    expect(await screen.findByText("jan@example.com")).toBeInTheDocument();
    const loc = stubLocation();
    fireEvent.click(screen.getByText("Abmelden"));
    await waitFor(() => expect(loc.href).toBe("/?signed_out=1"));
    expect(calls).toContainEqual({ url: "/api/auth/logout", method: "POST" });
  });

  it("goes to the login on 401, by a full navigation", async () => {
    const loc = stubLocation();
    answer({ status: 401, body: { error: "sign-in required" } });
    render(<App />);
    await waitFor(() => expect(loc.href).toBe("/api/auth/login"));
    expect(
      screen.getByText("Weiterleitung zur Anmeldung …"),
    ).toBeInTheDocument();
    expect(screen.queryByText("PAY")).not.toBeInTheDocument();
  });

  it.each([
    ["?auth_error=oidc_callback_failed", "Die Anmeldung konnte nicht"],
    ["?signed_out=1", "Abgemeldet."],
  ])(
    "halts on %s with a button instead of redirecting",
    async (search, text) => {
      const loc = stubLocation(search);
      const calls = answer({ status: 401 });
      render(<App />);
      expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
      expect(screen.getByText("Anmelden").closest("a")).toHaveAttribute(
        "href",
        "/api/auth/login",
      );
      // /api/me first: the markers count only without a session.
      expect(calls).toEqual([{ url: "/api/me", method: "GET" }]);
      expect(loc.href).toBe("/");
    },
  );

  it.each([
    ["none mode", anonymous],
    ["a session", { mode: "oidc", user: "jan" }],
  ])("ignores and strips the markers with %s", async (_, me) => {
    window.history.replaceState({}, "", "/?signed_out=1&x=2");
    answer({ status: 200, body: me });
    render(<App />);
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    expect(window.location.search).toBe("?x=2");
  });

  it("halts with a retry when /api/me answers no JSON", async () => {
    answer({ status: 200, unreadable: true });
    render(<App />);
    expect(
      await screen.findByText(/die Antwort des Servers war unlesbar/),
    ).toBeInTheDocument();
    const loc = stubLocation();
    fireEvent.click(screen.getByText("Erneut versuchen"));
    expect(loc.reload).toHaveBeenCalled();
  });

  it("signs in again on a later 401 in oidc mode", async () => {
    const loc = stubLocation();
    answer({ status: 200, body: { mode: "oidc", user: "jan" } }, 401);
    render(<App />);
    await waitFor(() => expect(loc.href).toBe("/api/auth/login"));
  });

  it("does not on a later 401 in none mode", async () => {
    const loc = stubLocation();
    answer({ status: 200, body: anonymous }, 401);
    render(<App />);
    expect(
      await screen.findByText("die Projekte konnte nicht geladen werden."),
    ).toBeInTheDocument();
    expect(loc.href).toBe("/");
  });

  it("halts instead of looping when a sign-in just happened", async () => {
    window.sessionStorage.setItem("almanach.signin", String(Date.now()));
    const loc = stubLocation();
    answer({ status: 401 });
    render(<App />);
    expect(
      await screen.findByText(/hat gerade eben keine Sitzung ergeben/),
    ).toBeInTheDocument();
    expect(loc.href).toBe("/");
  });

  it("halts when the session cannot be checked", async () => {
    answer({ status: 500 });
    render(<App />);
    expect(
      await screen.findByText(
        "Die Sitzung konnte nicht geprüft werden (HTTP 500).",
      ),
    ).toBeInTheDocument();
  });

  it("halts with a retry when the check fails on the network", async () => {
    answer(new TypeError("offline"));
    render(<App />);
    expect(
      await screen.findByText(/der Server ist nicht erreichbar/),
    ).toBeInTheDocument();
    // No mode assumed: the wiki stays unrendered.
    expect(screen.queryByText("PAY")).not.toBeInTheDocument();
    const loc = stubLocation();
    fireEvent.click(screen.getByText("Erneut versuchen"));
    expect(loc.reload).toHaveBeenCalled();
  });

  it("takes the deep link along to the login", async () => {
    const loc = stubLocation("?x=1&signed_in=no", "/projekte/PAY");
    answer({ status: 401 });
    render(<App />);
    await waitFor(() =>
      expect(loc.href).toBe(
        "/api/auth/login?next=" +
          encodeURIComponent("/projekte/PAY?x=1&signed_in=no"),
      ),
    );
  });

  it("halts while the provider is unavailable, with a way to try again", async () => {
    const loc = stubLocation("?auth_error=provider_unavailable");
    answer({ status: 401 });
    render(<App />);
    expect(
      await screen.findByText(/der Identitätsanbieter antwortet nicht/),
    ).toBeInTheDocument();
    expect(screen.getByText("Erneut versuchen").closest("a")).toHaveAttribute(
      "href",
      "/api/auth/login",
    );
    expect(loc.href).toBe("/");
  });

  it.each([
    ["the network fails", null],
    ["the server refuses", 403],
  ])("stays signed in and says so when %s on sign-out", async (_, status) => {
    answer({ status: 200, body: { mode: "oidc", user: "sub-1" } });
    render(<App />);
    await screen.findByText("sub-1");
    const loc = stubLocation();
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        if (status === null) throw new TypeError("offline");
        return {
          ok: false,
          status,
          json: async () => ({ error: "cross-site request refused" }),
        };
      }),
    );
    fireEvent.click(screen.getByText("Abmelden"));
    expect(
      await screen.findByText(
        "Abmelden fehlgeschlagen, bitte erneut versuchen.",
      ),
    ).toBeInTheDocument();
    expect(loc.href).toBe("/");
    expect(loc.reload).not.toHaveBeenCalled();
    expect(screen.getByText("sub-1")).toBeInTheDocument();
  });

  it("passes the page along on the halted page's sign-in link", async () => {
    stubLocation("?x=1&auth_error=oidc_callback_failed", "/quellen/werkzeuge");
    answer({ status: 401 });
    render(<App />);
    const link = await screen.findByText("Anmelden");
    expect(link.closest("a")).toHaveAttribute(
      "href",
      "/api/auth/login?next=" + encodeURIComponent("/quellen/werkzeuge?x=1"),
    );
  });

  it("halts on a misconfigured provider", async () => {
    stubLocation("?auth_error=provider_misconfigured");
    answer({ status: 401 });
    render(<App />);
    expect(
      await screen.findByText(/Die Anmeldung ist falsch eingerichtet/),
    ).toBeInTheDocument();
  });
});
