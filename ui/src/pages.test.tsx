import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { NotePage } from "./NotePage";
import { ProjectPage } from "./ProjectPage";
import { ProjectsPage } from "./ProjectsPage";
import { RepoPage } from "./RepoPage";
import {
  DescriptionView,
  StatementBlock,
  areaGroups,
  areaTitle,
  kindLabel,
  descriptionRail,
  statementMatches,
} from "./Description";
import { Rail, type RailItem } from "./Rail";
import { breakable } from "./RepoSwitcher";
import { useRef } from "react";
import { S } from "./strings";
import { armSignIn } from "./signin";
import { Doc, Marked, matchesQuery } from "./ui";
import type {
  Description,
  Me,
  NoteDetail,
  ProjectDetail,
  Projects,
  RepoDetail,
  Statement,
} from "./api";
import type { FlowIR } from "./diagramIR";
import * as diagram from "./Diagram";

// The map's drawing, made to throw where a test asks: no stored map does.
const drawing = vi.hoisted(() => ({ throws: false }));
vi.mock("./Diagram", async (actual) => {
  const real = await actual<typeof diagram>();
  return {
    ...real,
    drawMap: (...a: Parameters<typeof real.drawMap>) => {
      if (drawing.throws) throw new Error("layout");
      return real.drawMap(...a);
    },
  };
});

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
  // jsdom has no scrollIntoView; a test that stubs one leaves none.
  delete (Element.prototype as Partial<Element>).scrollIntoView;
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
      summary: "Der Zahlungsverkehr über alle Kanäle.",
      last_merge: mergedAt,
      repo: null,
    },
    {
      key: "MOB",
      team: "mobile",
      repos: 1,
      notes: 2,
      revision: 0,
      summary: "",
      last_merge: null,
      repo: null,
    },
  ],
};

const project: ProjectDetail = {
  key: "PAY",
  team: "platform",
  single: false,
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
        area: "Aufträge",
      },
      {
        id: "s2",
        kind: "rule",
        name: "Grenze aus `limit()`",
        text: "Die kleinere Grenze aus `max_size` gilt.",
        files: [],
        sources: [],
        area: "Grenzwerte",
      },
    ],
    source_pr_key: "PAY/pay-api#231",
    updated_at: mergedAt,
  },
  map: null,
  map_expected: false,
  map_declined: false,
  shape: null,
  shared: [],
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
  revisions: [],
  topics: [],
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
  map: null,
  map_declined: false,
  uses: [],
  used_by: [],
  // A repository's messages: messaging.test.tsx.
  messaging: { topics: [], schemas: [] },
};

/** openTab switches a page to one of its tabs, as a reader does. */
async function openTab(name: RegExp) {
  fireEvent.click(await screen.findByRole("tab", { name }));
}

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
  taught: [],
  single: false,
};

describe("ProjectsPage", () => {
  it("lists projects as cards, a project without a description says so", async () => {
    serve({ projects });
    render(<ProjectsPage />);
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    expect(screen.getByText(/Revision 7/)).toBeInTheDocument();
    // A project of several shows its own page's summary.
    expect(
      screen.getByText("Der Zahlungsverkehr über alle Kanäle."),
    ).toBeInTheDocument();
    expect(screen.getByText("noch keine Beschreibung")).toBeInTheDocument();
    expect(screen.getByText(/letzter Merge nie/)).toHaveClass(
      "first-letter:uppercase",
    );
    expect(screen.getByText("PAY").closest("a")?.getAttribute("href")).toBe(
      "/projects/PAY",
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
  it("shows a project of one as its repository, with what it shares", async () => {
    serve({
      projects: {
        projects: [
          projects.projects[0]!,
          {
            key: "loom",
            team: "",
            repos: 1,
            notes: 0,
            revision: 0,
            summary: "#### Zweck\n\nEin Recherche-Agent, `loom`.",
            last_merge: null,
            repo: {
              slug: "loom",
              revision: 3,
              summary: "#### Zweck\n\nEin Recherche-Agent, `loom`.",
              notes: 0,
              last_merge: null,
            },
          },
        ],
      },
      dependencies: {
        repos: [],
        edges: [
          {
            from: { project: "loom", slug: "loom" },
            to: { project: "llmwire", slug: "llmwire" },
            version: "v0.1.8",
            coords: [],
            scope: "main",
          },
          {
            from: { project: "loom", slug: "loom" },
            to: { project: "testkit", slug: "testkit" },
            version: "v1",
            coords: [],
            scope: "test",
          },
        ],
        ambiguous: [],
      },
    });
    render(<ProjectsPage />);
    const tile = (await screen.findByRole("heading", { name: "loom" })).closest(
      "a",
    )!;
    expect(tile.getAttribute("href")).toBe("/projects/loom");
    expect(within(tile).getByText(/Ein Recherche-Agent,/)).toBeInTheDocument();
    // Code spans as the repository table sets them, no part's title.
    expect(
      within(tile).getByText("loom", { selector: "code" }),
    ).toBeInTheDocument();
    expect(within(tile).queryByText(/Zweck/)).toBeNull();
    expect(within(tile).getByText(/Revision 3/)).toBeInTheDocument();
    expect(within(tile).getByText("llmwire")).toBeInTheDocument();
    // A test use is no use of the project's.
    expect(within(tile).queryByText("testkit")).toBeNull();
    // No headings splitting projects: one grid.
    expect(screen.queryByText("Einzelne Repositories")).toBeNull();
  });
  it("shows the empty and the failed state", async () => {
    serve({ projects: { projects: [] } });
    render(<ProjectsPage />);
    expect(await screen.findByText(/Noch kein Projekt/)).toBeInTheDocument();
    serve({});
    render(<ProjectsPage />);
    expect(
      await screen.findByText("Die Projekte konnten nicht geladen werden."),
    ).toBeInTheDocument();
  });
});

describe("ProjectPage", () => {
  it("opens on the overview: the tables first, the description a tab away", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const overview = await screen.findByRole("tabpanel", { name: "Übersicht" });
    expect(within(overview).getByText("Clearing-Adapter")).toBeInTheDocument();
    expect(within(overview).getByText("Logik")).toBeInTheDocument();
    expect(
      within(overview).getByText("Tageslimit und Kontolimit gemeinsam prüfen"),
    ).toBeInTheDocument();
    expect(
      within(overview).getByText("pay-lib").closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY/repos/pay-lib");
    expect(
      screen.queryByText("Das Projekt Payments nimmt Zahlungsaufträge an."),
    ).toBeNull();
    expect(screen.getByText(/Beschreibung Revision 7/)).toBeInTheDocument();
    expect(document.title).toContain("PAY");
    // The start tab has no hash.
    expect(window.location.hash).toBe("");
  });

  it("lists the project's topics with who sends and who receives, another project's too", async () => {
    const topics: ProjectDetail["topics"] = [
      {
        name: "pay.booked.v1",
        producers: [{ project: "PAY", slug: "pay-clearing" }],
        consumers: [
          { project: "PAY", slug: "pay-api" },
          { project: "LEDGER", slug: "ledger-core" },
        ],
        declared: [],
      },
    ];
    serve({ "projects/PAY": { ...project, topics } });
    render(<ProjectPage projectKey="PAY" />);
    const overview = await screen.findByRole("tabpanel", { name: "Übersicht" });
    const row = within(overview).getByText("pay.booked.v1").closest("tr")!;
    expect(
      within(row)
        .getByRole("link", { name: "pay-clearing" })
        .getAttribute("href"),
    ).toBe("/projects/PAY/repos/pay-clearing");
    expect(
      within(row)
        .getByRole("link", { name: "LEDGER/ledger-core" })
        .getAttribute("href"),
    ).toBe("/projects/LEDGER/repos/ledger-core");
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(
      within(rail).getByText("Messaging").closest("a")?.getAttribute("href"),
    ).toBe("#messages");
  });

  it("has no messages section for a project whose code names no topic", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    await screen.findByRole("tabpanel", { name: "Übersicht" });
    expect(screen.queryByText("Messaging")).toBeNull();
  });

  it("lists the project page's revisions on the overview, each a link", async () => {
    serve({
      "projects/PAY": {
        ...project,
        revisions: [
          {
            revision: 7,
            source_pr_key: "PAY/pay-api#231",
            statements: 2,
            chars: 300,
            created_at: mergedAt,
          },
          {
            revision: 6,
            source_kind: "baseline",
            source_pr_key: "",
            source_commit: "abc1234abc1234",
            statements: 1,
            chars: 120,
            created_at: mergedAt,
          },
        ],
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    const overview = await screen.findByRole("tabpanel", { name: "Übersicht" });
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Revisionen").closest("a")?.textContent).toBe(
      "Revisionen2",
    );
    expect(within(overview).getByText("aktuell")).toBeInTheDocument();
    expect(
      within(overview).getByText("7", { selector: "a" }).getAttribute("href"),
    ).toBe("/projects/PAY/revisions/7");
    expect(
      within(overview).getByText("6", { selector: "a" }).getAttribute("href"),
    ).toBe("/projects/PAY/revisions/6");
  });

  it("reads a repository's summary as the tile does: code spans, no heading", async () => {
    serve({
      "projects/PAY": {
        ...project,
        repos: [
          {
            slug: "pay-api",
            revision: 5,
            summary:
              "#### Zweck\n\n`pay-api` nimmt Aufträge an.\n\n1. prüft `limit`\n2. bucht",
            notes: 11,
            last_merge: mergedAt,
          },
          // Sub-headings alone: no teaser, so no description.
          {
            slug: "pay-lib",
            revision: 1,
            summary: "#### Zweck",
            notes: 0,
            last_merge: null,
          },
        ],
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    const overview = await screen.findByRole("tabpanel", { name: "Übersicht" });
    const code = within(overview).getByText("limit");
    expect(code.closest("code")).not.toBeNull();
    const row = code.closest("tr")!;
    expect(within(row).queryByText(/Zweck/)).toBeNull();
    expect(row.textContent).not.toContain("`");
    // An item keeps its marker: the list's order reads on.
    expect(row.textContent).toContain("an. 1. prüft limit 2. bucht");
    const lib = within(overview).getByText("pay-lib").closest("tr")!;
    expect(
      within(lib).getByText("Noch keine Beschreibung."),
    ).toBeInTheDocument();
    // The name stays the row's one link; a click anywhere on the row opens it.
    const links = within(row).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    fireEvent.click(code);
    expect(window.location.pathname).toBe("/projects/PAY/repos/pay-api");
  });

  it("opens a row's page from anywhere on it, but not after a selection", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const overview = await screen.findByRole("tabpanel", { name: "Übersicht" });
    const open = vi.spyOn(window, "open").mockReturnValue(null);
    const here = window.location.pathname;
    const note = within(overview).getByText(
      "Tageslimit und Kontolimit gemeinsam prüfen",
    );
    // Text selected in a cell: the drag copies, it opens nothing.
    const sel = vi
      .spyOn(window, "getSelection")
      .mockReturnValue({ isCollapsed: false } as Selection);
    fireEvent.click(note);
    expect(window.location.pathname).toBe(here);
    sel.mockRestore();
    // A middle or ⌘ click opens a tab, a plain one navigates.
    fireEvent(note, new MouseEvent("auxclick", { bubbles: true, button: 1 }));
    fireEvent.click(note, { metaKey: true });
    const to = note.closest("tr")!.querySelector("a")!.getAttribute("href")!;
    expect(open).toHaveBeenCalledTimes(2);
    expect(open).toHaveBeenCalledWith(to, "_blank", "noopener");
    expect(window.location.pathname).toBe(here);
    fireEvent.click(note);
    expect(window.location.pathname).toBe(to);
    open.mockRestore();
  });

  it("renders the description as paragraphs, the statements with chips", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    await openTab(/Beschreibung/);
    const desc = screen.getByRole("tabpanel", { name: "Beschreibung" });
    expect(
      within(desc).getByText("Das Projekt Payments nimmt Zahlungsaufträge an."),
    ).toBeInTheDocument();
    expect(
      within(desc).getByText("Ein Auftrag durchläuft Zustände."),
    ).toBeInTheDocument();
    expect(within(desc).getByText("Auftragsannahme")).toBeInTheDocument();
    // A backtick-quoted identifier is code, in the name and in the text.
    expect(within(desc).getByText("limit()").tagName).toBe("CODE");
    expect(within(desc).getByText("max_size").tagName).toBe("CODE");
    expect(
      within(desc).getByText("Der Mandant kommt aus der Kopfzeile."),
    ).toBeInTheDocument();
    expect(
      within(desc).getByText("pay-api/internal/orders/intake.go"),
    ).toBeInTheDocument();
    const chip = within(desc).getByText("PAY/pay-api#212");
    expect(chip.closest("a")?.getAttribute("href")).toBe(
      "/projects/PAY/repos/pay-api/notes/212",
    );
    // Each statement says its kind over its name; no section per kind.
    expect(within(desc).getByText("Komponente")).toBeInTheDocument();
    expect(within(desc).getByText("Regel")).toBeInTheDocument();
    expect(within(desc).queryByText("Komponenten")).toBeNull();
    expect(within(desc).queryByText("Invarianten")).toBeNull();
    expect(window.location.hash).toBe("#description");
  });

  it("lays out a statement's text and the summary, nothing beyond the subset", async () => {
    const d = project.description!;
    serve({
      "projects/PAY": {
        ...project,
        description: {
          ...d,
          summary:
            "#### Zweck\n\nDer **Kern** der Zahlungen.\n\n#### Rollen\n\n- Annahme\n- Abrechnung",
          statements: [
            {
              ...d.statements[0],
              text: "Nimmt Aufträge an.\n\n#### Ablauf\n\n1. Prüfen\n2. Speichern mit **`tx`**\n\n> kein Zitat",
            },
            ...d.statements.slice(1),
          ],
        },
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    await openTab(/Beschreibung/);
    const desc = screen.getByRole("tabpanel", { name: "Beschreibung" });
    // A summary's parts sit under the page's h2, a statement's under its h4.
    expect(within(desc).getByText("Zweck").tagName).toBe("H3");
    expect(within(desc).getByText("Rollen").tagName).toBe("H3");
    expect(within(desc).getByText("Kern").tagName).toBe("STRONG");
    expect(within(desc).getByText("Abrechnung").tagName).toBe("LI");
    expect(within(desc).getByText("Ablauf").tagName).toBe("H5");
    const step = within(desc).getByText("Prüfen");
    expect(step.tagName).toBe("LI");
    expect(step.closest("ol")).not.toBeNull();
    const tx = within(desc).getByText("tx");
    expect(tx.tagName).toBe("CODE");
    expect(tx.closest("strong")).not.toBeNull();
    expect(within(desc).getByText("> kein Zitat").tagName).toBe("P");
  });

  it("finds and marks a statement's words across its bold and code edges", () => {
    const s = {
      ...project.description!.statements[0],
      text: "Der **Mandant** kommt aus `X-Tenant` mit.",
    };
    expect(statementMatches(s, "Mandant kommt")).toBe(true);
    expect(statementMatches(s, "aus X-Ten")).toBe(true);
    const { container } = render(<Doc text={s.text} query="Mandant kommt" />);
    const marks = [...container.querySelectorAll("mark")];
    expect(marks.map((m) => m.textContent)).toEqual(["Mandant", " kommt"]);
    expect(marks[0].closest("strong")).not.toBeNull();
  });

  it("opens the tab the address names", async () => {
    window.history.replaceState({}, "", "/projects/PAY#description");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByRole("tabpanel", { name: "Beschreibung" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: /Übersicht/ }));
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/projects/PAY");
  });

  it("opens the statement the address names, scrolled and set off, and copies its link", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    const written: string[] = [];
    vi.stubGlobal("navigator", {
      ...navigator,
      clipboard: { writeText: async (t: string) => void written.push(t) },
    });
    window.history.replaceState({}, "", "/projects/PAY#description/s2");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    const s2 = within(desc).getByText("Grenze aus").closest("article");
    expect(s2?.id).toBe("s2");
    expect(s2?.dataset.targeted).toBe("true");
    expect(scrolled.mock.contexts[0]).toBe(s2);
    expect(
      within(desc).getByText("Auftragsannahme").closest("article")?.dataset
        .targeted,
    ).toBeUndefined();
    // Each statement carries its own address; a click writes it in place
    // and copies it, the mark moves along.
    const links = within(desc).getAllByRole("link", {
      name: /^Link zur Aussage /,
    });
    expect(links[0]).toHaveAccessibleName("Link zur Aussage Auftragsannahme");
    expect(links.map((a) => a.getAttribute("href"))).toEqual([
      "#description/s1",
      "#description/s2",
    ]);
    fireEvent.click(links[0]);
    expect(window.location.hash).toBe("#description/s1");
    expect(written).toEqual([
      "http://localhost:3000/projects/PAY#description/s1",
    ]);
    await waitFor(() => expect(links[0].textContent).toBe("Kopiert"));
    expect(
      within(desc).getByText("Auftragsannahme").closest("article")?.dataset
        .targeted,
    ).toBe("true");
    // The page did not move under the click: only the address's own
    // target is scrolled to.
    expect(scrolled).toHaveBeenCalledTimes(1);
    // A tab switch drops the target; the start tab has no hash.
    fireEvent.click(screen.getByRole("tab", { name: /Übersicht/ }));
    expect(window.location.hash).toBe("");
  });

  it("jumps again when the project's parts land above a repository's statements", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    window.history.replaceState(
      {},
      "",
      "/projects/PAY/repos/pay-clearing#description/s1",
    );
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        description: project.description,
      },
      "projects/PAY": project,
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    await screen.findByText("Auftragsannahme");
    // Once on the statements, again when the project detail (the problem
    // areas above them) has landed and changed the page.
    await waitFor(() =>
      expect(scrolled.mock.calls.length).toBeGreaterThanOrEqual(2),
    );
    const s1 = document.getElementById("s1");
    expect(scrolled.mock.contexts.every((c) => c === s1)).toBe(true);
    // The reader scrolls: the page is theirs, a later change moves nothing.
    const before = scrolled.mock.calls.length;
    fireEvent.wheel(window);
    document.body.appendChild(document.createElement("p"));
    await new Promise((r) => setTimeout(r, 20));
    expect(scrolled.mock.calls.length).toBe(before);
  });

  it("hands the page to the reader on a click while it settles", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    window.history.replaceState({}, "", "/projects/PAY#description/s1");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    await screen.findByText("Auftragsannahme");
    await waitFor(() => expect(scrolled).toHaveBeenCalled());
    // A click (a rail entry, the scrollbar) is the reader's: a change of
    // the page after it, a rail's aria-current among them, moves nothing.
    const before = scrolled.mock.calls.length;
    fireEvent.pointerDown(window);
    document.body.setAttribute("data-x", "1");
    document.body.appendChild(document.createElement("p"));
    await new Promise((r) => setTimeout(r, 20));
    expect(scrolled.mock.calls.length).toBe(before);
    document.body.removeAttribute("data-x");
  });

  it("opens the description on a target it does not have, the page whole", async () => {
    window.history.replaceState({}, "", "/projects/PAY#description/s99");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(within(desc).getByText("Auftragsannahme")).toBeInTheDocument();
    expect(desc.querySelector("[data-targeted]")).toBeNull();
  });

  it("offers the Markdown export and copies its address", async () => {
    const written: string[] = [];
    vi.stubGlobal("navigator", {
      ...navigator,
      clipboard: { writeText: async (t: string) => void written.push(t) },
    });
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const open = await screen.findByRole("link", {
      name: "Als Markdown öffnen",
    });
    expect(open.getAttribute("href")).toBe("/api/projects/PAY.md");
    expect(open.getAttribute("target")).toBe("_blank");
    fireEvent.click(screen.getByRole("button", { name: "URL kopieren" }));
    expect(written).toEqual(["http://localhost:3000/api/projects/PAY.md"]);
    expect(
      await screen.findByRole("button", { name: "Kopiert" }),
    ).toBeInTheDocument();
    vi.unstubAllGlobals();
  });

  it("opens a statement's link followed while the page is open", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    window.history.replaceState({}, "", "/projects/PAY");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    await screen.findByRole("tab", { name: /Übersicht/ });
    // A search hit's link from the header, on the overview: the tab opens
    // and the statement is scrolled to.
    act(() => {
      window.history.pushState({}, "", "/projects/PAY#description/s1");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    await waitFor(() =>
      expect(scrolled.mock.contexts).toContain(document.getElementById("s1")),
    );
    expect(within(desc).getByText("Auftragsannahme")).toBeInTheDocument();
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });

  it("opens the start tab on a hash it does not know, a broken one too", async () => {
    window.history.replaceState({}, "", "/projects/PAY#%E0");
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByRole("tabpanel", { name: "Übersicht" }),
    ).toBeInTheDocument();
  });

  it("keeps what it marks: the filter and the marks agree on case", () => {
    expect(matchesQuery("MANDANT", "der Mandant")).toBe(true);
    expect(matchesQuery("", "x")).toBe(true);
    expect(matchesQuery("a.b", "axb")).toBe(false);
    render(
      <p>
        <Marked text="der Mandant und mandant" query="MANDANT" />
      </p>,
    );
    expect(screen.getAllByText(/mandant/i).map((m) => m.tagName)).toEqual([
      "MARK",
      "MARK",
    ]);
  });

  it("lists both tabs' sections in the rail and jumps across", async () => {
    // jsdom lays nothing out and has no scrollIntoView.
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const rail = await screen.findByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Repositories")).toBeInTheDocument();
    // The description's sections are its areas, no kind among them.
    expect(within(rail).getByText("Aufträge").closest("a")?.textContent).toBe(
      "Aufträge1",
    );
    expect(within(rail).queryByText("Regeln")).toBeNull();
    fireEvent.click(within(rail).getByText("Grenzwerte"));
    expect(
      screen.getByRole("tabpanel", { name: "Beschreibung" }),
    ).toBeInTheDocument();
    // A section link scrolls, once the other tab has rendered; it never
    // writes the address, which holds the tab.
    expect(scrolled.mock.contexts[0]).toBe(document.getElementById("area-1"));
    expect(window.location.hash).toBe("#description");
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });

  it("searches the description: typing on the overview goes there", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const search = await screen.findByRole("searchbox", {
      name: "Beschreibung durchsuchen",
    });
    fireEvent.change(search, { target: { value: "mandant" } });
    const desc = screen.getByRole("tabpanel", { name: "Beschreibung" });
    // The statement that matches stays, its match set off; the other goes.
    expect(within(desc).getByText("Auftragsannahme")).toBeInTheDocument();
    expect(within(desc).getByText("Mandant").tagName).toBe("MARK");
    expect(within(desc).queryByText("max_size")).toBeNull();
    expect(
      screen.getByText("1 von 2 Einträgen · 1 Kapitel"),
    ).toBeInTheDocument();
    fireEvent.change(search, { target: { value: "nirgends" } });
    expect(within(desc).getByText("keine Treffer")).toBeInTheDocument();
    fireEvent.keyDown(search, { key: "Escape" });
    expect(within(desc).getByText("max_size")).toBeInTheDocument();
  });

  it("shows a project of one as its repository's page, at the project's path", async () => {
    serve({
      "projects/loom": {
        ...project,
        key: "loom",
        single: true,
        description: null,
        map: null,
        map_expected: false,
        repos: [
          {
            slug: "loom",
            revision: 0,
            summary: "",
            notes: 0,
            last_merge: null,
          },
        ],
      },
      "projects/loom/repos/loom": {
        ...repo,
        project_key: "loom",
        slug: "loom",
      },
    });
    window.history.replaceState({}, "", "/projects/loom");
    render(<ProjectPage projectKey="loom" />);
    expect(
      await screen.findByText(/Projekt aus einem Repository/),
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("loom");
    // No rail head: no other repository, no project page of its own.
    expect(screen.queryByText("Projekt loom")).toBeNull();
    // Its own path, not the repository's.
    expect(window.location.pathname).toBe("/projects/loom");
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
      await screen.findByText("Keine Bugfixes im Zeitfenster."),
    ).toBeInTheDocument();
    await openTab(/Beschreibung/);
    expect(screen.getByText(/Noch keine Beschreibung\./)).toBeInTheDocument();
    // The rail points only at what the page shows: no kind sections.
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Überblick")).toBeInTheDocument();
    expect(within(rail).queryByText("Regeln")).toBeNull();
    serve({});
    render(<ProjectPage projectKey="NOPE" />);
    expect(
      await screen.findByText("Das Projekt konnte nicht geladen werden."),
    ).toBeInTheDocument();
  });

  it("starts a tab a search opens at its top", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const search = await screen.findByRole("searchbox", {
      name: "Beschreibung durchsuchen",
    });
    const scroller = search.closest(".overflow-auto") as HTMLElement;
    scroller.scrollTop = 500;
    fireEvent.change(search, { target: { value: "mandant" } });
    expect(scroller.scrollTop).toBe(0);
  });

  it("finds with / but not while a dialog holds the page", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const search = await screen.findByRole("searchbox", {
      name: "Beschreibung durchsuchen",
    });
    fireEvent.keyDown(document.body, { key: "/" });
    expect(document.activeElement).toBe(search);
    search.blur();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Systemübersicht: Vollansicht",
      }),
    );
    fireEvent.keyDown(document.body, { key: "/" });
    expect(document.activeElement).not.toBe(search);
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

  it("shows the system map compact above the repositories, repository boxes linked", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const drawing = await screen.findByRole("figure", {
      name: "Systemübersicht",
    });
    // Compact: names in bands, no drawing on the page.
    expect(within(drawing).queryByRole("img")).toBeNull();
    // The zones are titled from strings.ts, not by the model, in the
    // drawing's band order.
    expect(
      within(drawing)
        .getAllByRole("group")
        .map((g) => g.getAttribute("aria-label")),
    ).toEqual(["Clients", "Repositories", "Datenspeicher"]);
    expect(drawing.textContent).not.toContain("Stores");
    const link = screen.getByRole("link", {
      name: "Repository pay-api öffnen",
    });
    expect(link.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    // A repository box carries its summary's teaser.
    expect(link.textContent).toContain(
      project.repos.find((r) => r.slug === "pay-api")!.summary.split("\n")[0],
    );
    // A node outside the repositories zone, or a slug the project no longer
    // lists, links nowhere.
    expect(
      screen.queryByRole("link", { name: /PostgreSQL|Browser/ }),
    ).toBeNull();
    expect(screen.queryByRole("link", { name: /pay-gone/ })).toBeNull();
    // Above the repositories.
    const repos = within(
      screen.getByRole("tabpanel", { name: "Übersicht" }),
    ).getByText("pay-lib");
    expect(
      drawing.compareDocumentPosition(repos) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("lights what a box is connected to, seen from it, and puts it out", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const api = await screen.findByRole("link", {
      name: "Repository pay-api öffnen",
    });
    const figure = screen.getByRole("figure", { name: "Systemübersicht" });
    const node = (name: string) =>
      within(figure).getByText(name).closest("button, a")!;
    fireEvent.mouseEnter(api);
    expect(node("PostgreSQL").textContent).toBe("PostgreSQL→ DB");
    expect(node("Browser").textContent).toBe("Browser← HTTP");
    // A dashed arrow, a boundary, stays dashed.
    expect(node("pay-gone").textContent).toContain("⇠ event");
    expect(node("pay-gone").className).toContain("border-accent-strong");
    // A screen reader hears it.
    expect(figure.querySelector("[aria-live]")!.textContent).toBe(
      "pay-api: verbunden mit Browser (← HTTP), PostgreSQL (→ DB), pay-gone (⇠ event)",
    );
    // An eye reads the ways whole in the line under the map, a legend on
    // a node's border cut to its width; the legend is said once, there.
    const line = within(figure)
      .getAllByText(/^pay-api: verbunden mit/)
      .find((p) => !p.hasAttribute("aria-live"))!;
    expect(line).toHaveAttribute("aria-hidden", "true");
    expect(line.textContent).toContain("PostgreSQL (→ DB)");
    const legend = within(node("PostgreSQL") as HTMLElement).getByText("→ DB");
    expect(legend).toHaveAttribute("aria-hidden", "true");
    expect(legend.className).toContain("absolute");
    fireEvent.mouseLeave(api);
    expect(node("PostgreSQL").textContent).toBe("PostgreSQL");
    expect(node("PostgreSQL").className).not.toContain("opacity-40");
    expect(figure.querySelector("[aria-live]")!.textContent).toBe("");
    expect(
      within(figure).getByText(/^Zeigen, fokussieren/),
    ).not.toHaveAttribute("aria-hidden");

    // A node without a page is a button: the keyboard reaches it, and a
    // pointer leaving another node does not put out what focus lit.
    fireEvent.focus(node("PostgreSQL"));
    fireEvent.mouseEnter(node("Browser"));
    fireEvent.mouseLeave(node("Browser"));
    expect(node("pay-api").textContent).toContain("← DB");
    fireEvent.blur(node("PostgreSQL"));
    expect(node("pay-api").textContent).not.toContain("← DB");
  });

  it("lights a tapped link first and opens it on the second tap", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const api = await screen.findByRole("link", {
      name: "Repository pay-api öffnen",
    });
    const before = window.location.pathname;
    fireEvent.touchStart(api);
    fireEvent.click(api);
    expect(window.location.pathname).toBe(before);
    expect(screen.getByText("PostgreSQL").textContent).toBe("PostgreSQL→ DB");
    fireEvent.touchStart(api);
    fireEvent.click(api);
    expect(window.location.pathname).toBe("/projects/PAY/repos/pay-api");
  });

  it("pins a clicked node until a click elsewhere or Escape", async () => {
    serve({
      "projects/PAY": {
        ...project,
        map: {
          ...map,
          // An edge without a head or a label says nothing, not "()".
          edges: [
            ...map.edges,
            {
              from: "browser",
              to: "pg",
              label: "",
              dashed: false,
              head: "none",
            },
          ],
        },
      },
    });
    render(<ProjectPage projectKey="PAY" />);
    const figure = await screen.findByRole("figure", {
      name: "Systemübersicht",
    });
    const pg = within(figure).getByRole("button", { name: "PostgreSQL" });
    const live = () => figure.querySelector("[aria-live]")!.textContent;
    fireEvent.mouseEnter(pg);
    fireEvent.click(pg);
    fireEvent.mouseLeave(pg);
    expect(live()).toBe("PostgreSQL: verbunden mit pay-api (← DB), Browser");
    fireEvent.pointerDown(pg);
    expect(live()).not.toBe("");
    fireEvent.pointerDown(document.body);
    expect(live()).toBe("");
    fireEvent.click(pg);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(live()).toBe("");
  });

  it("keeps the page when the drawing cannot be laid out", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    const open = await screen.findByRole("button", {
      name: "Systemübersicht: Vollansicht",
    });
    drawing.throws = true;
    try {
      fireEvent.click(open);
    } finally {
      drawing.throws = false;
    }
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      screen.getByText("Das Diagramm konnte nicht gezeichnet werden."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Repository pay-api öffnen" }),
    ).toBeInTheDocument();
  });

  it("opens the map whole, its repository nodes linked there too", async () => {
    serve({ "projects/PAY": { ...project, map } });
    render(<ProjectPage projectKey="PAY" />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Systemübersicht: Vollansicht",
      }),
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog.getAttribute("aria-label")).toBe("Systemübersicht");
    const link = within(dialog).getByRole("link", {
      name: "Repository pay-api öffnen",
    });
    expect(link.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    // Followed, the link closes the full view.
    fireEvent.click(link);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(window.location.pathname).toBe("/projects/PAY/repos/pay-api");
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
    await openTab(/Beschreibung/);
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
      await screen.findByText(/Noch keine Systemübersicht/),
    ).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.queryByText(/wird gezeichnet/)).toBeNull();
  });

  it("promises no map again once a run asked and none was accepted", async () => {
    serve({
      "projects/PAY": { ...project, map_expected: true, map_declined: true },
    });
    render(<ProjectPage projectKey="PAY" />);
    expect(await screen.findByText("Clearing-Adapter")).toBeInTheDocument();
    expect(screen.queryByText(/Noch keine Systemübersicht/)).toBeNull();
    expect(screen.queryByText("Systemübersicht")).toBeNull();
  });

  it("says what Almanach judged the project to be, nothing before it judged", async () => {
    serve({ "projects/PAY": { ...project, shape: "collection" } });
    const { unmount } = render(<ProjectPage projectKey="PAY" />);
    const label = await screen.findByText("Sammlung");
    expect(label).toHaveAttribute(
      "title",
      expect.stringContaining("kurze Zusammenfassung"),
    );
    unmount();
    serve({ "projects/PAY": { ...project, shape: "system" } });
    const again = render(<ProjectPage projectKey="PAY" />);
    expect(await screen.findByText("System")).toBeInTheDocument();
    again.unmount();
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    expect(await screen.findByText("Clearing-Adapter")).toBeInTheDocument();
    expect(screen.queryByText("Sammlung")).toBeNull();
    expect(screen.queryByText("System")).toBeNull();
  });

  it("promises no map where no run will draw one", async () => {
    serve({ "projects/PAY": { ...project, map_expected: false } });
    render(<ProjectPage projectKey="PAY" />);
    expect(await screen.findByText("Clearing-Adapter")).toBeInTheDocument();
    expect(screen.queryByText(/Noch keine Systemübersicht/)).toBeNull();
    expect(screen.queryByText("Systemübersicht")).toBeNull();
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
    // The revisions are the history, the second tab.
    await openTab(/Verlauf/);
    expect(await screen.findByText("aktuell")).toBeInTheDocument();
    expect(window.location.hash).toBe("#history");
    expect(screen.getByText(/7 Aussagen/)).toBeInTheDocument();
    expect(screen.getByText(/1 912 Zeichen/)).toBeInTheDocument();
    expect(
      screen
        .getByText("PAY/pay-clearing#51")
        .closest("a")
        ?.getAttribute("href"),
    ).toBe("/projects/PAY/repos/pay-clearing/notes/51");
    // Each revision's number opens its view.
    expect(screen.getByText("2", { selector: "a" }).getAttribute("href")).toBe(
      "/projects/PAY/repos/pay-clearing/revisions/2",
    );
  });

  it("labels each statement with its kind, a page in no area one chapter", async () => {
    const st = (
      id: string,
      kind: "interface" | "decision" | "extension" | "pitfall",
    ) => ({
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
            st("s4", "interface"),
          ],
        },
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    // A page not laid out yet is one chapter, in the stored order; an
    // unknown kind shows as stored.
    expect(
      within(desc)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toContain(S.areas.all);
    expect(
      within(desc)
        .getAllByRole("heading", { level: 4 })
        .map((h) => [h.previousElementSibling?.textContent, h.textContent]),
    ).toEqual([
      ["Erweiterung", "Aussage s1"],
      ["Fallstrick", "Aussage s2"],
      ["Entscheidung", "Aussage s3"],
      ["Schnittstelle", "Aussage s4"],
    ]);
    expect(kindLabel("future" as Statement["kind"], false)).toBe("future");
  });

  it("labels a repository's entity as one, a project's as a shared one", () => {
    const entity: Statement = {
      id: "e1",
      kind: "entity",
      name: "Zahlung",
      text: "Eine Zahlung.",
      files: [],
      sources: [],
    };
    const { unmount } = render(
      <StatementBlock statement={entity} pageRepo="svc" />,
    );
    expect(screen.getByText("Entität")).toBeInTheDocument();
    unmount();
    render(<StatementBlock statement={entity} />);
    expect(screen.getByText("Geteilte Entität")).toBeInTheDocument();
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
    // The statement's chip carries the commit's date.
    const chip = await screen.findByText("Basis 4e1f0c2");
    expect(chip.closest("a")).toBeNull();
    expect(chip.getAttribute("title")).toMatch(/^Basis bei Commit 4e1f0c2, /);
    expect(
      screen
        .getByText("PAY/pay-clearing#77")
        .closest("a")
        ?.getAttribute("href"),
    ).toBe("/projects/PAY/repos/pay-clearing/notes/77");
    expect(screen.getByText("Geprüft 9a8b7c6")).toBeInTheDocument();
    // The revision row shows the same commit, a quiet chip too.
    await openTab(/Verlauf/);
    const row = screen.getByText("Basis 4e1f0c2");
    expect(row.closest("a")).toBeNull();
  });

  const repoMap: FlowIR = {
    kind: "flow",
    direction: "LR",
    zones: [
      { id: "entry", title: "Entry" },
      { id: "parts", title: "Parts" },
    ],
    nodes: [
      { id: "api", label: "HTTP-API", shape: "step", zone: "entry" },
      { id: "files", label: "Sammeldateien", shape: "step", zone: "parts" },
    ],
    edges: [
      { from: "api", to: "files", label: "call", dashed: false, head: "end" },
    ],
  };

  it("leads with its map, then the summary and its own problem areas", async () => {
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        map: repoMap,
        description: project.description,
      },
      "projects/PAY": {
        ...project,
        repos: [
          ...project.repos,
          { ...project.repos[1]!, slug: "pay-clearing" },
        ],
      },
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    const drawing = await screen.findByRole("img", { name: "Aufbau" });
    // The zones are titled from strings.ts, not by the model.
    expect(drawing.textContent).toContain("Eingang");
    expect(drawing.textContent).not.toContain("Entry");
    // Its nodes are parts, not pages.
    expect(within(drawing.parentElement!).queryByRole("link")).toBeNull();
    const desc = screen.getByRole("tabpanel", { name: "Beschreibung" });
    const summary = within(desc).getByText(
      "Das Projekt Payments nimmt Zahlungsaufträge an.",
    );
    expect(
      drawing.compareDocumentPosition(summary) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    // This repository's problem areas, from the project, without the
    // repository column; under the summary.
    const area = await within(desc).findByText("Clearing-Adapter");
    expect(
      summary.compareDocumentPosition(area) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(within(desc).queryByText("Repository")).toBeNull();
    // The rail lists none of the project's repositories: its head is the
    // project and one switch.
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).queryByText("pay-api")).toBeNull();
    expect(within(rail).getByText("Aufbau")).toBeInTheDocument();
  });

  describe("rail head", () => {
    const three: ProjectDetail = {
      ...project,
      repos: [
        ...project.repos,
        {
          slug: "pay-clearing",
          revision: 2,
          summary: "Gleicht **Zahlungen** ab.",
          notes: 4,
          last_merge: null,
        },
      ],
    };

    it("names the project and steps through its repositories, round the end", async () => {
      serve({ "projects/PAY/repos/pay-clearing": repo, "projects/PAY": three });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      const projectLink = await within(rail).findAllByRole("link", {
        name: /Projekt PAY/,
      });
      expect(projectLink[0]!.getAttribute("href")).toBe("/projects/PAY");
      expect(within(rail).getByText("3 / 3")).toBeInTheDocument();
      expect(
        within(rail)
          .getByRole("link", { name: "Vorheriges Repository: pay-lib" })
          .getAttribute("href"),
      ).toBe("/projects/PAY/repos/pay-lib");
      expect(
        within(rail)
          .getByRole("link", { name: "Nächstes Repository: pay-api" })
          .getAttribute("href"),
      ).toBe("/projects/PAY/repos/pay-api");
      // "]" steps as › does; not while typing.
      fireEvent.keyDown(
        within(rail).getByRole("searchbox", {
          name: "Beschreibung durchsuchen",
        }),
        { key: "]" },
      );
      expect(window.location.pathname).toBe("/projects/PAY/repos/pay-clearing");
      fireEvent.keyDown(document.body, { key: "]" });
      expect(window.location.pathname).toBe("/projects/PAY/repos/pay-api");
    });

    it("steps on a Swiss layout's Option key, once per press, never on Cmd", async () => {
      serve({ "projects/PAY/repos/pay-clearing": repo, "projects/PAY": three });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      await within(rail).findByText("3 / 3");
      fireEvent.keyDown(document.body, { key: "[", metaKey: true });
      fireEvent.keyDown(document.body, { key: "[", repeat: true });
      expect(window.location.pathname).toBe("/projects/PAY/repos/pay-clearing");
      // Option+5 types "[" on a Swiss Mac.
      fireEvent.keyDown(document.body, { key: "[", altKey: true });
      expect(window.location.pathname).toBe("/projects/PAY/repos/pay-lib");
    });

    it("opens the list without steps for a repository the project does not list", async () => {
      serve({
        "projects/PAY/repos/pay-clearing": repo,
        "projects/PAY": project,
      });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      const [button] = await within(rail).findAllByRole("button", {
        name: "pay-clearing",
      });
      expect(within(rail).queryByText(/ \/ 2/)).toBeNull();
      expect(
        within(rail).queryByRole("link", { name: /Repository:/ }),
      ).toBeNull();
      fireEvent.click(button!);
      const list = screen.getByRole("dialog", { name: "Repository wechseln" });
      expect(within(list).getAllByRole("link")).toHaveLength(2);
    });

    it("closes on its own repository, on focus leaving it; follows a scroll", async () => {
      serve({ "projects/PAY/repos/pay-clearing": repo, "projects/PAY": three });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      const [button] = await within(rail).findAllByRole("button", {
        name: "pay-clearing",
      });
      const open = () => {
        fireEvent.click(button!);
        return screen.getByRole("dialog", { name: "Repository wechseln" });
      };
      // Its own repository: closed, the focus back on the switch.
      fireEvent.click(
        within(open()).getByRole("link", { name: /pay-clearing/ }),
      );
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(document.activeElement).toBe(button);
      const filter = within(open()).getByRole("searchbox");
      fireEvent.change(filter, { target: { value: "clearing" } });
      fireEvent.keyDown(filter, { key: "Enter" });
      expect(screen.queryByRole("dialog")).toBeNull();
      // Tab past its end: the focus is in the page, the list goes.
      const list = open();
      fireEvent.blur(within(list).getByRole("searchbox"), {
        relatedTarget: rail.querySelector("input"),
      });
      expect(screen.queryByRole("dialog")).toBeNull();
      // A scroll outside it, a phone's keyboard opening say, moves it with
      // the switch; inside it, nothing.
      const shown = vi
        .spyOn(HTMLElement.prototype, "getClientRects")
        .mockReturnValue([{}] as unknown as DOMRectList);
      const at = vi
        .spyOn(button!, "getBoundingClientRect")
        .mockReturnValue({ left: 20, bottom: 100 } as DOMRect);
      const again = open();
      expect(again.style.top).toBe("106px");
      fireEvent.scroll(again.querySelector("ul")!);
      at.mockReturnValue({ left: 20, bottom: 40 } as DOMRect);
      fireEvent.scroll(rail);
      expect(screen.getByRole("dialog").style.top).toBe("46px");
      // Once the switch is hidden (the other width's), it closes.
      shown.mockReturnValue([] as unknown as DOMRectList);
      fireEvent(window, new Event("resize"));
      expect(screen.queryByRole("dialog")).toBeNull();
      shown.mockRestore();
      at.mockRestore();
      // A press on the switch toggles, it does not close and reopen.
      open();
      fireEvent.pointerDown(button!);
      fireEvent.click(button!);
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    it("breaks a slug only after its separators", () => {
      const { container } = render(<p>{breakable("a-b.c_d/e")}</p>);
      expect(container.querySelectorAll("wbr")).toHaveLength(4);
      expect(container.textContent).toBe("a-b.c_d/e");
      const { container: one } = render(<p>{breakable("plain")}</p>);
      expect(one.querySelectorAll("wbr")).toHaveLength(0);
    });

    it("opens the project's repositories with their teasers, filterable", async () => {
      serve({ "projects/PAY/repos/pay-clearing": repo, "projects/PAY": three });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      const [button] = await within(rail).findAllByRole("button", {
        name: "pay-clearing",
      });
      expect(button!.getAttribute("aria-expanded")).toBe("false");
      fireEvent.click(button!);
      expect(button!.getAttribute("aria-expanded")).toBe("true");
      const list = screen.getByRole("dialog", { name: "Repository wechseln" });
      expect(within(list).getAllByRole("link")).toHaveLength(3);
      expect(
        within(list).getByText("Nimmt Zahlungsaufträge an."),
      ).toBeInTheDocument();
      expect(
        within(list)
          .getByRole("link", { name: /pay-clearing/ })
          .getAttribute("aria-current"),
      ).toBe("page");
      const filter = within(list).getByRole("searchbox", {
        name: "Repository suchen",
      });
      fireEvent.change(filter, { target: { value: "zzz" } });
      expect(within(list).getByText("Kein Repository")).toBeInTheDocument();
      fireEvent.change(filter, { target: { value: "lib" } });
      expect(within(list).getAllByRole("link")).toHaveLength(1);
      // The summary as the teaser reads, not its markup.
      fireEvent.change(filter, { target: { value: "gleicht zahlungen" } });
      expect(within(list).getAllByRole("link")).toHaveLength(1);
      fireEvent.change(filter, { target: { value: "**" } });
      expect(within(list).getByText("Kein Repository")).toBeInTheDocument();
      // Escape closes it and gives the focus back to the switch.
      fireEvent.keyDown(filter, { key: "Escape" });
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(document.activeElement).toBe(button);
      // Enter in the filter opens the first match.
      fireEvent.click(button!);
      const again = screen.getByRole("searchbox", {
        name: "Repository suchen",
      });
      fireEvent.change(again, { target: { value: "api" } });
      fireEvent.keyDown(again, { key: "Enter" });
      expect(window.location.pathname).toBe("/projects/PAY/repos/pay-api");
    });

    it("has no switch where the project lists no other repository", async () => {
      serve({
        "projects/PAY/repos/pay-clearing": repo,
        "projects/PAY": { ...three, repos: [three.repos[2]!] },
      });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const rail = await screen.findByRole("navigation", { name: "Inhalt" });
      await within(rail).findAllByRole("link", { name: /Projekt PAY/ });
      expect(within(rail).queryByRole("button")).toBeNull();
      expect(within(rail).queryByText("1 / 1")).toBeNull();
    });
  });

  it("says why it has no map when every draft was refused", async () => {
    serve({
      "projects/PAY/repos/pay-clearing": {
        ...repo,
        map_declined: true,
        description: project.description,
      },
      "projects/PAY": project,
    });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    expect(await screen.findByText(/^Kein Aufbau: /)).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "Aufbau" })).toBeNull();
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Aufbau")).toBeInTheDocument();
  });

  it("goes without the project's parts when the project fails", async () => {
    serve({ "projects/PAY/repos/pay-clearing": repo });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    expect(
      await screen.findByRole("tabpanel", { name: "Beschreibung" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "Aufbau" })).toBeNull();
    expect(screen.queryByText("Problemzonen")).toBeNull();
    expect(screen.queryByText("pay-api")).toBeNull();
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
    expect(screen.getByText("$0.01")).toBeInTheDocument();
    expect(screen.getByText("$0.04")).toBeInTheDocument();
    expect(screen.getByText("noch nicht")).toBeInTheDocument();
    expect(screen.getByText(/\(Bug\)/)).toBeInTheDocument();
  });
  it("reads a note's text as paragraphs alone: noergler's is not laid out", async () => {
    serve({
      "projects/PAY/repos/pay-api/notes/231": {
        ...note,
        note: { ...note.note!, summary: "- kein **Punkt**" },
      },
    });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect((await screen.findByText("- kein **Punkt**")).tagName).toBe("P");
  });
  it("links the statements the note taught, by page and id", async () => {
    serve({
      "projects/PAY/repos/pay-api/notes/231": {
        ...note,
        consolidated_at: mergedAt,
        taught: [
          {
            scope: "repository",
            project_key: "PAY",
            repo_slug: "pay-api",
            id: "s3",
            kind: "rule",
            name: "Grenze aus `limit()`",
          },
          {
            scope: "project",
            project_key: "PAY",
            id: "s1",
            kind: "entity",
            name: "Auftrag",
          },
        ],
      },
    });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect(await screen.findByText("Eingearbeitet in")).toBeInTheDocument();
    // The name's inline markup renders as on the page: code, no backticks.
    const code = screen.getByText("limit()");
    expect(code.tagName).toBe("CODE");
    expect(screen.queryByText(/`limit\(\)`/)).toBeNull();
    expect(code.closest("a")?.getAttribute("href")).toBe(
      "/projects/PAY/repos/pay-api#description/s3",
    );
    expect(screen.getByText("Regel")).toBeInTheDocument();
    expect(screen.getByText("Auftrag").closest("a")?.getAttribute("href")).toBe(
      "/projects/PAY#description/s1",
    );
    // A project page's entity is a shared one, labelled so.
    expect(screen.getByText("Geteilte Entität")).toBeInTheDocument();
    expect(screen.getByText("Projekt PAY")).toBeInTheDocument();
    expect(
      screen.getByText("pay-api", { selector: "li span" }),
    ).toBeInTheDocument();
  });

  it("links a project of one's statements to the project's path, unnamed", async () => {
    serve({
      "projects/PAY/repos/pay-api/notes/231": {
        ...note,
        consolidated_at: mergedAt,
        single: true,
        taught: [
          {
            scope: "repository",
            project_key: "PAY",
            repo_slug: "pay-api",
            id: "s3",
            kind: "rule",
            name: "Grenze",
          },
        ],
      },
    });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect(
      (await screen.findByText("Grenze")).closest("a")?.getAttribute("href"),
    ).toBe("/projects/PAY#description/s3");
    expect(screen.queryByText("pay-api", { selector: "li span" })).toBeNull();
  });

  it("says why nothing is taught: not folded yet, or nothing left", async () => {
    serve({ "projects/PAY/repos/pay-api/notes/231": note });
    const { unmount } = render(
      <NotePage projectKey="PAY" slug="pay-api" pr={231} />,
    );
    expect(
      await screen.findByText("Die Notiz ist noch nicht eingearbeitet."),
    ).toBeInTheDocument();
    unmount();
    serve({
      "projects/PAY/repos/pay-api/notes/231": {
        ...note,
        consolidated_at: mergedAt,
      },
    });
    render(<NotePage projectKey="PAY" slug="pay-api" pr={231} />);
    expect(
      await screen.findByText(/Keine aktuelle Aussage nennt/),
    ).toBeInTheDocument();
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
    await waitFor(() => expect(window.location.pathname).toBe("/projects/PAY"));
    expect(await screen.findByText("Clearing-Adapter")).toBeInTheDocument();
    expect(document.title).toContain("PAY");
    screen.getByText("Almanach").closest("a")!.click();
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    await waitFor(() => expect(document.title).toContain("Projekte"));
  });

  it("searches the wiki from the header, the phrase in the address", async () => {
    serve({
      me: anonymous,
      projects,
      "search?q=Geb%C3%BChren": { query: "Gebühren", pages: [], more: 0 },
    });
    window.history.replaceState({}, "", "/");
    render(<App />);
    const box = await screen.findByRole("searchbox", {
      name: "Wiki durchsuchen",
    });
    fireEvent.change(box, { target: { value: " Gebühren " } });
    fireEvent.submit(box.closest("form")!);
    await waitFor(() =>
      expect(window.location.pathname + window.location.search).toBe(
        "/search?q=Geb%C3%BChren",
      ),
    );
    expect(
      await screen.findByText("Keine Treffer für «Gebühren»."),
    ).toBeInTheDocument();
    // The search is no nav entry's page: Projekte is not current.
    const nav = screen.getByRole("navigation", { name: S.nav.label });
    expect(
      within(nav).getByText(S.nav.projects).getAttribute("aria-current"),
    ).toBeNull();
    // The box shows the phrase searched; an empty one submits nothing.
    expect(
      screen.getByRole("searchbox", { name: "Wiki durchsuchen" }),
    ).toHaveValue("Gebühren");
    fireEvent.change(box, { target: { value: "  " } });
    fireEvent.submit(box.closest("form")!);
    expect(window.location.search).toBe("?q=Geb%C3%BChren");
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
      await screen.findByText("Die Projekte konnten nicht geladen werden."),
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
    const loc = stubLocation("?x=1&signed_in=no", "/projects/PAY");
    answer({ status: 401 });
    render(<App />);
    await waitFor(() =>
      expect(loc.href).toBe(
        "/api/auth/login?next=" +
          encodeURIComponent("/projects/PAY?x=1&signed_in=no"),
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
    stubLocation("?x=1&auth_error=oidc_callback_failed", "/sources/werkzeuge");
    answer({ status: 401 });
    render(<App />);
    const link = await screen.findByText("Anmelden");
    expect(link.closest("a")).toHaveAttribute(
      "href",
      "/api/auth/login?next=" + encodeURIComponent("/sources/werkzeuge?x=1"),
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

describe("DescriptionView areas", () => {
  const st = (
    id: string,
    kind: Statement["kind"],
    name: string,
    area?: string,
  ): Statement => ({
    id,
    kind,
    name,
    text: name + ".",
    files: [],
    sources: [],
    area,
  });
  const description: Description = {
    revision: 1,
    summary: "Ein Dienst.",
    statements: [
      st("s1", "component", "Abfrage-Teil", "Abfrage"),
      st("s2", "component", "Alt"),
      st("s3", "component", "Gradle-Build", "Entwicklung und Auslieferung"),
      st("s4", "rule", "Regel", "Abfrage"),
    ],
    source_pr_key: "",
    updated_at: mergedAt,
  };
  const chapters = () =>
    [...document.querySelectorAll("section h3")].map((h) => [
      h.textContent,
      [...h.parentElement!.querySelectorAll("h4")].map((n) => n.textContent),
    ]);

  it("makes each area a chapter of its statements, whatever their kind", () => {
    render(
      <DescriptionView description={description} emptyHint="" pageRepo="svc" />,
    );
    expect(chapters()).toEqual([
      ["Abfrage", ["Abfrage-Teil", "Regel"]],
      [S.areas.otherArea, ["Alt"]],
      ["Entwicklung und Auslieferung", ["Gradle-Build"]],
    ]);
  });

  it("keeps only the areas a search hits, and finds an area by its name", () => {
    render(
      <DescriptionView
        description={description}
        emptyHint=""
        pageRepo="svc"
        query="Gradle"
      />,
    );
    expect(chapters()).toEqual([
      ["Entwicklung und Auslieferung", ["Gradle-Build"]],
    ]);
    expect(statementMatches(description.statements[0], "abfrage")).toBe(true);
    // The chapter keeps its anchor, the area's place among all of them.
    expect(
      document.getElementById("area-2")?.querySelector("h3")?.textContent,
    ).toBe("Entwicklung und Auslieferung");
  });

  it("heads an area once, where it first stands, on a page not laid out yet", () => {
    const twice = [
      st("a1", "rule", "Eins", "A"),
      st("b1", "rule", "Zwei", "B"),
      st("a2", "rule", "Drei", "A"),
    ];
    expect(
      areaGroups(twice, twice).map((g) => [
        g.area,
        g.statements.map((s) => s.id),
      ]),
    ).toEqual([
      ["A", ["a1", "a2"]],
      ["B", ["b1"]],
    ]);
    // A search's hits keep the page's order of areas, the rail's.
    expect(areaGroups(twice, twice.slice(1)).map((g) => g.area)).toEqual([
      "A",
      "B",
    ]);
  });

  it("says so on a page without statements", () => {
    render(
      <DescriptionView
        description={{ ...description, statements: [] }}
        emptyHint=""
        pageRepo="svc"
      />,
    );
    expect(screen.getByText(S.areas.none)).toBeInTheDocument();
  });

  it("titles every chapter: an area, the rest, or a page without areas", () => {
    expect(areaTitle("Abfrage", ["Abfrage", ""])).toBe("Abfrage");
    expect(areaTitle("", ["Abfrage", ""])).toBe(S.areas.otherArea);
    expect(areaTitle("", [""])).toBe(S.areas.all);
    // A page in one area keeps its heading and its rail entry.
    const one = { ...description, statements: [st("c1", "rule", "Teil", "A")] };
    expect(
      descriptionRail(one, "", "description").items.map((i) => i.label),
    ).toEqual([S.rail.summary, "A"]);
    expect(descriptionRail(one, "Teil", "description").hitLine).toBe(
      "1 von 1 Eintrag · 1 Kapitel",
    );
  });

  it("gives the rail the page's areas, the ones a search hits only", () => {
    const rail = (q: string) =>
      descriptionRail(description, q, "description").items.map((c) => [
        c.id,
        c.label,
        c.count,
        c.hit,
      ]);
    expect(rail("")).toEqual([
      ["summary", S.rail.summary, undefined, undefined],
      ["area-0", "Abfrage", 2, false],
      ["area-1", S.areas.otherArea, 1, false],
      ["area-2", "Entwicklung und Auslieferung", 1, false],
    ]);
    expect(rail("Gradle")).toEqual([
      ["summary", S.rail.summary, undefined, undefined],
      ["area-2", "Entwicklung und Auslieferung", 1, true],
    ]);
  });
});

describe("Rail", () => {
  // jsdom lays nothing out: an element with an id at or before the one
  // being read stands above the reading line, every other one under it.
  function readAt(id: string) {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
      function (this: Element) {
        const target = document.getElementById(id)!;
        const above =
          this.id !== "" &&
          (this === target ||
            (this.compareDocumentPosition(target) &
              Node.DOCUMENT_POSITION_FOLLOWING) !==
              0);
        return { top: this.id === "" ? 0 : above ? -10 : 10 } as DOMRect;
      },
    );
  }

  function Page() {
    const scroller = useRef<HTMLDivElement>(null);
    const items: RailItem[] = [
      { id: "a", label: "Erstes", tab: "t" },
      { id: "k", label: "Abfrage", tab: "t" },
      { id: "z", label: "Letztes", tab: "t" },
    ];
    return (
      <div ref={scroller}>
        <Rail
          groups={[{ title: "T", items }]}
          tab="t"
          onTab={() => {}}
          query=""
          onQuery={() => {}}
          hitLine=""
          scroller={scroller}
        />
        {["a", "k", "z"].map((id) => (
          <section key={id} id={id} />
        ))}
      </div>
    );
  }

  afterEach(() => vi.restoreAllMocks());

  it("marks the section being read", () => {
    const current = (label: string) =>
      within(screen.getByRole("navigation", { name: "Inhalt" }))
        .getByText(label)
        .closest("a")
        ?.getAttribute("aria-current");
    readAt("a");
    render(<Page />);
    expect(current("Erstes")).toBe("location");
    readAt("k");
    fireEvent.scroll(
      screen.getByRole("navigation", { name: "Inhalt" }).parentElement!,
    );
    expect(current("Abfrage")).toBe("location");
    expect(current("Erstes")).toBeNull();
  });
});
