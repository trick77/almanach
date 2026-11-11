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
  shownStatements,
  statementMatches,
} from "./Description";
import { Rail, shortcutFree, type RailItem } from "./Rail";
import { day } from "./format";
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
      shape: null,
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
      shape: null,
      repo: null,
    },
  ],
};

const project: ProjectDetail = {
  key: "PAY",
  team: "platform",
  single: false,
  stale: null,
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
  interfaces: [],
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
  stale: null,
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
  interfaces: [],
  parts: { units: [], edges: [], graph: { nodes: [], edges: [] } },
};

/** stepText is a path step's title as a text query matches it: the DOM's
 *  text normalized, its en spaces plain. */
const stepText = (n: number, title: string) =>
  S.path.step(n, title).replace(/\s+/g, " ");

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
  it("lists projects as cards, no figures on them, a project without a description says so", async () => {
    serve({ projects });
    render(<ProjectsPage />);
    const pay = (await screen.findByRole("heading", { name: "PAY" })).closest(
      "a",
    )!;
    expect(pay.getAttribute("href")).toBe("/projects/PAY");
    // A project of several shows its own page's summary.
    expect(
      within(pay).getByText("Der Zahlungsverkehr über alle Kanäle."),
    ).toBeInTheDocument();
    // Team and repositories, no revision, notes or last merge: those are
    // the table's.
    expect(pay).toHaveTextContent("Team platform·4 Repositories");
    expect(pay.textContent).not.toMatch(/Revision|Notiz|Merge/);
    const mob = screen.getByRole("heading", { name: "MOB" }).closest("a")!;
    expect(
      within(mob).getByText("noch keine Beschreibung"),
    ).toBeInTheDocument();
    // No system: one group, no titles over it.
    expect(screen.queryByText(S.projects.systems)).toBeNull();
    expect(screen.getByText(S.projects.lede)).toBeInTheDocument();
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
            shape: null,
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
            coords: [],
            scope: "main",
          },
          {
            from: { project: "loom", slug: "loom" },
            to: { project: "testkit", slug: "testkit" },
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
    // Its repository has a description: none missing in the meta line.
    expect(within(tile).queryByText("noch keine Beschreibung")).toBeNull();
    expect(within(tile).getByText("llmwire")).toBeInTheDocument();
    // A test use is no use of the project's.
    expect(within(tile).queryByText("testkit")).toBeNull();
    // No headings splitting projects: one grid.
    expect(screen.queryByText("Einzelne Repositories")).toBeNull();
  });

  /** home is a list with a system, a collection, a project of one and a
   *  project not judged yet. */
  const home: Projects = {
    projects: [
      { ...projects.projects[0]!, shape: "system" },
      {
        ...projects.projects[0]!,
        key: "CORE",
        team: "",
        repos: 6,
        notes: 0,
        revision: 2,
        summary: "Geteilte Libraries.",
        last_merge: null,
        shape: "collection",
      },
      {
        ...projects.projects[1]!,
        key: "TOOLS",
        repos: 1,
        notes: 9,
        revision: 0,
        summary: "Ein CLI.",
        last_merge: "2026-11-02T10:00:00Z",
        repo: {
          slug: "tools-cli",
          revision: 4,
          summary: "Ein CLI.",
          notes: 9,
          last_merge: "2026-11-02T10:00:00Z",
        },
      },
      projects.projects[1]!,
    ],
  };

  it("puts the systems first, the rest compact under them", async () => {
    // Projects of one whose repository is a spelling of the key: other case,
    // other separator.
    const tools = home.projects[2]!;
    const of = (key: string, slug: string) => ({
      ...tools,
      key,
      repo: { ...tools.repo!, slug },
    });
    serve({
      projects: {
        projects: [...home.projects, of("CLI", "cli"), of("MY_APP", "my-app")],
      },
    });
    render(<ProjectsPage />);
    const systems = (await screen.findByText(S.projects.systems)).closest(
      "section",
    )!;
    // Each group is a titled region; its tiles' keys a level below.
    expect(screen.getByRole("region", { name: S.projects.systems })).toBe(
      systems,
    );
    expect(
      within(systems).getByRole("heading", { level: 4, name: "PAY" }),
    ).toBeInTheDocument();
    expect(
      within(systems).getByRole("heading", { name: "PAY" }),
    ).toBeInTheDocument();
    const others = screen.getByText(S.projects.others).closest("section")!;
    const row = (key: string) =>
      within(others).getByText(key, { exact: true }).closest("a")!;
    // Form and size on the right: a collection, the rest their count. A
    // project of one neither: its repository only where the slug is no
    // repeat of the key.
    expect(row("CORE")).toHaveTextContent("Sammlung·6 Repositories");
    expect(row("CORE").getAttribute("href")).toBe("/projects/CORE");
    expect(row("TOOLS").textContent).toBe("TOOLSEin CLI.tools-cli");
    expect(within(row("TOOLS")).getByText("tools-cli")).not.toHaveClass(
      "first-letter:uppercase",
    );
    expect(row("CLI").textContent).toBe("CLIEin CLI.");
    expect(row("MY_APP").textContent).toBe("MY_APPEin CLI.");
    expect(row("MOB")).toHaveTextContent("1 Repository");
    expect(row("MOB").textContent).not.toMatch(/Sammlung|System/);
    // A row's teaser on one line.
    expect(within(row("CORE")).getByText("Geteilte Libraries.")).toHaveClass(
      "truncate",
    );
  });

  it("shows the figures in a table, kept in the address, sortable", async () => {
    serve({ projects: home });
    window.history.replaceState({}, "", "/");
    render(<ProjectsPage />);
    const view = await screen.findByRole("group", { name: S.projects.view });
    expect(
      within(view).getByRole("button", { name: S.projects.tiles }),
    ).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(
      within(view).getByRole("button", { name: S.projects.table }),
    );
    expect(window.location.hash).toBe("#table");
    const table = screen.getByRole("table");
    const heads = within(table).getAllByRole("columnheader");
    expect(heads.map((h) => h.textContent)).toEqual(
      Object.values(S.projects.headers),
    );
    // A cell that opens on a lowercase word shows it as a capital.
    expect(
      within(table).getByText("CORE").closest("tr")!.lastElementChild!
        .className,
    ).toContain("first-letter:uppercase");
    const keys = () =>
      within(table)
        .getAllByRole("row")
        .slice(1)
        .map((r) => r.querySelector("td")!.textContent);
    // By project, ascending, to start.
    expect(heads[0]).toHaveAttribute("aria-sort", "ascending");
    expect(keys()).toEqual(["CORE", "MOB", "PAY", "TOOLS"]);
    // A project of one reads as its repository: revision 4.
    const tools = within(table).getByText("TOOLS").closest("tr")!;
    expect(tools.textContent).toContain(S.projects.oneRepo);
    expect(within(tools).getByText("4")).toBeInTheDocument();
    // The last merge, newest first on a first click, oldest on a second;
    // "nie" stays last.
    const merge = heads[6]!;
    fireEvent.click(within(merge).getByRole("button"));
    expect(merge).toHaveAttribute("aria-sort", "descending");
    expect(heads[0]).not.toHaveAttribute("aria-sort");
    expect(keys()).toEqual(["TOOLS", "PAY", "CORE", "MOB"]);
    fireEvent.click(within(merge).getByRole("button"));
    expect(merge).toHaveAttribute("aria-sort", "ascending");
    expect(keys()).toEqual(["PAY", "TOOLS", "CORE", "MOB"]);
    // The notes: a number column, the most first.
    fireEvent.click(within(heads[4]!).getByRole("button"));
    expect(keys()).toEqual(["PAY", "TOOLS", "MOB", "CORE"]);
    // The kind: one not judged yet last.
    fireEvent.click(within(heads[1]!).getByRole("button"));
    expect(keys().at(-1)).toBe("MOB");
    // Back to the tiles: the address without a hash.
    fireEvent.click(
      within(view).getByRole("button", { name: S.projects.tiles }),
    );
    expect(window.location.hash).toBe("");
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("opens on the table the address names", async () => {
    serve({ projects: home });
    window.history.replaceState({}, "", "/#table");
    render(<ProjectsPage />);
    expect(await screen.findByRole("table")).toBeInTheDocument();
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
  it("opens on the start tab: the reading path in order, the tables a tab away", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const start = await screen.findByRole("tabpanel", { name: S.tabs.start });
    // No map promised: the steps there number on without it.
    expect(
      within(start)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toEqual([
      S.path.step(1, S.path.projectWhat),
      S.path.step(2, S.path.repos),
      S.path.step(3, S.path.guard),
      S.path.step(4, S.path.hurt),
    ]);
    // What it does: the summary's opening, here all of it.
    expect(
      within(start).getByText(
        "Das Projekt Payments nimmt Zahlungsaufträge an.",
      ),
    ).toBeInTheDocument();
    expect(within(start).queryByText(S.path.wholeSummary)).toBeNull();
    // The repositories, each a link; where it hurt, the problem areas.
    expect(
      within(start).getByRole("link", { name: "pay-lib" }).getAttribute("href"),
    ).toBe("/projects/PAY/repos/pay-lib");
    expect(within(start).getByText("Clearing-Adapter")).toBeInTheDocument();
    // The statement that must not break links to its place.
    expect(
      within(start)
        .getByRole("link", { name: "Grenze aus limit()" })
        .getAttribute("href"),
    ).toBe("/projects/PAY#description/s2");
    // The notes and the interfaces are not on the path.
    expect(
      within(start).queryByText("Tageslimit und Kontolimit gemeinsam prüfen"),
    ).toBeNull();
    // Next: the first repository.
    const next = within(start).getByRole("link", { name: /Weiter/ });
    expect(next.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    // The figures in the quiet line, none in the meta line.
    expect(
      screen.getByRole("button", { name: /^Stand: Beschreibung Revision 7/ }),
    ).toBeInTheDocument();
    const metaLine = screen.getByRole("heading", { level: 2 })
      .nextElementSibling!.textContent;
    expect(metaLine).toContain("2 Repositories");
    expect(metaLine).not.toMatch(/Notiz|Revision|Merge/);
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
    await openTab(/Nachschlagen/);
    const reference = screen.getByRole("tabpanel", { name: S.tabs.reference });
    // After the description, under the facts read from the code.
    expect(within(reference).getByText(S.facts.title)).toBeInTheDocument();
    const row = within(reference).getByText("pay.booked.v1").closest("tr")!;
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
    await openTab(/Nachschlagen/);
    expect(screen.queryByText("Messaging")).toBeNull();
    // Neither interfaces nor topics: no facts heading either.
    expect(screen.queryByText(S.facts.title)).toBeNull();
  });

  it("lists the project page's revisions on the history tab, each a link", async () => {
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
    await openTab(/Verlauf/);
    const overview = screen.getByRole("tabpanel", { name: S.tabs.history });
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Revisionen").closest("a")?.textContent).toBe(
      "Revisionen2",
    );
    // Each repository's figures head the history.
    const stats = within(overview).getByText("pay-api").closest("tr")!;
    expect(stats.textContent).toBe("pay-api511" + day(mergedAt));
    expect(
      within(within(overview).getByText("pay-lib").closest("tr")!).getByText(
        S.projects.noDescription,
      ),
    ).toBeInTheDocument();
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
    const start = await screen.findByRole("tabpanel", { name: S.tabs.start });
    const overview = within(start)
      .getByRole("link", { name: "pay-lib" })
      .closest("table")!;
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
    await openTab(/Verlauf/);
    const overview = screen.getByRole("tabpanel", { name: S.tabs.history });
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
    await openTab(/Nachschlagen/);
    const desc = screen.getByRole("tabpanel", { name: S.tabs.reference });
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
    await openTab(/Nachschlagen/);
    const desc = screen.getByRole("tabpanel", { name: S.tabs.reference });
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
      await screen.findByRole("tabpanel", { name: S.tabs.reference }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: /Einstieg/ }));
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/projects/PAY");
    fireEvent.click(screen.getByRole("tab", { name: /Verlauf/ }));
    expect(window.location.hash).toBe("#history");
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
    const desc = await screen.findByRole("tabpanel", {
      name: S.tabs.reference,
    });
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
    fireEvent.click(screen.getByRole("tab", { name: /Einstieg/ }));
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
    const desc = await screen.findByRole("tabpanel", {
      name: S.tabs.reference,
    });
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
    // Said who the two links are for.
    expect(open.parentElement!.firstElementChild!.textContent).toBe(
      S.copy.forAgents,
    );
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
    await screen.findByRole("tab", { name: /Einstieg/ });
    // A search hit's link from the header, on the start tab: the tab opens
    // and the statement is scrolled to.
    act(() => {
      window.history.pushState({}, "", "/projects/PAY#description/s1");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    const desc = await screen.findByRole("tabpanel", {
      name: S.tabs.reference,
    });
    await waitFor(() =>
      expect(scrolled.mock.contexts).toContain(document.getElementById("s1")),
    );
    expect(within(desc).getByText("Auftragsannahme")).toBeInTheDocument();
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });

  it("opens the start tab on a hash it does not know, a broken one too", async () => {
    window.history.replaceState({}, "", "/projects/PAY#%E0");
    serve({ "projects/PAY": project });
    const { unmount } = render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByRole("tabpanel", { name: S.tabs.start }),
    ).toBeInTheDocument();
    unmount();
    // The overview tab of old is gone: its link opens the start tab.
    window.history.replaceState({}, "", "/projects/PAY#overview");
    render(<ProjectPage projectKey="PAY" />);
    expect(
      await screen.findByRole("tabpanel", { name: S.tabs.start }),
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

  it("lists the three tabs' sections in the rail and jumps across", async () => {
    // jsdom lays nothing out and has no scrollIntoView.
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const rail = await screen.findByRole("navigation", { name: "Inhalt" });
    for (const title of [S.path.label, S.tabs.reference, S.tabs.history])
      expect(within(rail).getByText(title)).toBeInTheDocument();
    // The path's steps, numbered as on the page.
    expect(
      within(rail).getByText(stepText(2, S.path.repos)),
    ).toBeInTheDocument();
    // The description's sections are its areas, no kind among them.
    expect(within(rail).getByText("Aufträge").closest("a")?.textContent).toBe(
      "Aufträge1",
    );
    expect(within(rail).queryByText("Regeln")).toBeNull();
    fireEvent.click(within(rail).getByText("Grenzwerte"));
    expect(
      screen.getByRole("tabpanel", { name: S.tabs.reference }),
    ).toBeInTheDocument();
    // A section link scrolls, once the other tab has rendered; it never
    // writes the address, which holds the tab.
    expect(scrolled.mock.contexts[0]).toBe(document.getElementById("area-1"));
    expect(window.location.hash).toBe("#description");
    // And on to the history.
    fireEvent.click(within(rail).getByText(S.revision.list));
    expect(
      screen.getByRole("tabpanel", { name: S.tabs.history }),
    ).toBeInTheDocument();
    expect(scrolled.mock.contexts.at(-1)).toBe(
      document.getElementById("revisions"),
    );
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });

  it("searches the description: typing on the start tab goes there", async () => {
    serve({ "projects/PAY": project });
    render(<ProjectPage projectKey="PAY" />);
    const search = await screen.findByRole("searchbox", {
      name: "Beschreibung durchsuchen",
    });
    fireEvent.change(search, { target: { value: "mandant" } });
    const desc = screen.getByRole("tabpanel", { name: S.tabs.reference });
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
    // The start tab, no next repository: there is none.
    expect(
      screen.getByRole("tabpanel", { name: S.tabs.start }),
    ).toBeInTheDocument();
    expect(screen.queryByText(S.path.next)).toBeNull();
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
    const { unmount } = render(<ProjectPage projectKey="PAY" />);
    // The path's first step says so; nothing to guard, nowhere it hurt:
    // those steps are not there.
    const start = await screen.findByRole("tabpanel", { name: S.tabs.start });
    const what = within(start)
      .getByText(stepText(1, S.path.projectWhat))
      .closest("section")!;
    expect(what).toHaveTextContent(
      `${S.project.noDescription} ${S.project.noDescriptionHint}`,
    );
    expect(
      within(start)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toEqual([
      S.path.step(1, S.path.projectWhat),
      S.path.step(2, S.path.repos),
    ]);
    expect(within(start).getByText(S.project.noRepos)).toBeInTheDocument();
    // No repository, no next card; the quiet line says there is no page yet.
    expect(within(start).queryByText(S.path.next)).toBeNull();
    expect(
      screen.getByRole("button", { name: /^Stand: noch keine Beschreibung/ }),
    ).toBeInTheDocument();
    await openTab(/Nachschlagen/);
    expect(screen.getByText(/Noch keine Beschreibung\./)).toBeInTheDocument();
    // No statements, no question chips.
    expect(screen.queryByRole("group", { name: S.ask.label })).toBeNull();
    // The rail points only at what the page shows: no kind sections.
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    expect(within(rail).getByText("Überblick")).toBeInTheDocument();
    expect(within(rail).queryByText("Regeln")).toBeNull();
    unmount();
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
    // Its own step, above the repositories.
    expect(drawing.closest("section")?.querySelector("h3")?.textContent).toBe(
      S.path.step(2, S.path.how),
    );
    const repos = within(
      screen.getByRole("tabpanel", { name: S.tabs.start }),
    ).getByRole("link", { name: "pay-lib" });
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
    await openTab(/Nachschlagen/);
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
    // A collection's path says what it holds.
    expect(
      screen.getByRole("heading", {
        level: 3,
        name: S.path.step(1, S.path.collectionWhat),
      }),
    ).toBeInTheDocument();
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

  describe("reading path", () => {
    const st = (
      id: string,
      kind: Statement["kind"],
      sources: number,
    ): Statement => ({
      id,
      kind,
      name: `Aussage ${id}`,
      text: `Text ${id}.`,
      files: [],
      sources: Array.from({ length: sources }, (_, i) => ({
        pr_key: `PAY/pay-api#${i + 1}`,
      })),
      area: "Aufträge",
    });
    const many: ProjectDetail = {
      ...project,
      description: {
        ...project.description!,
        summary:
          "Nimmt Zahlungen an.\n\n#### Ablauf\n\nAnnahme, Buchung, Meldung.",
        statements: [
          st("g1", "rule", 0),
          st("g2", "invariant", 4),
          st("g3", "rule", 2),
          st("g4", "invariant", 1),
          st("p1", "pitfall", 0),
          st("p2", "pitfall", 3),
          st("d1", "decision", 1),
        ],
      },
      problems: [],
    };

    it("shows the three most cited of a kind, the rest a chip away", async () => {
      serve({ "projects/PAY": many });
      render(<ProjectPage projectKey="PAY" />);
      const start = await screen.findByRole("tabpanel", {
        name: S.tabs.start,
      });
      const guard = within(start)
        .getByText(stepText(3, S.path.guard))
        .closest("section")!;
      expect(
        within(guard)
          .getAllByRole("link")
          .map((a) => a.textContent),
      ).toEqual(["Aussage g2", "Aussage g3", "Aussage g4"]);
      // Two pitfalls, no problem areas: the step stands, no more link.
      const hurt = within(start)
        .getByText(stepText(4, S.path.hurt))
        .closest("section")!;
      expect(
        within(hurt)
          .getAllByRole("link")
          .map((a) => a.textContent),
      ).toEqual(["Aussage p2", "Aussage p1"]);
      expect(within(hurt).queryByRole("button")).toBeNull();
      // The summary's opening, the whole of it a link away.
      expect(
        within(start).getByText("Nimmt Zahlungen an."),
      ).toBeInTheDocument();
      expect(within(start).queryByText("Ablauf")).toBeNull();
      expect(
        within(start)
          .getByRole("link", { name: /Ganzer Überblick/ })
          .getAttribute("href"),
      ).toBe("/projects/PAY#description/summary");
      // All four: the reference tab, its chip pressed, the others hidden.
      fireEvent.click(
        within(guard).getByRole("button", { name: /Alle 4 Invarianten/ }),
      );
      const reference = screen.getByRole("tabpanel", {
        name: S.tabs.reference,
      });
      expect(
        within(reference).getByRole("button", { name: /Was darf nicht/ }),
      ).toHaveAttribute("aria-pressed", "true");
      expect(
        [...reference.querySelectorAll("h4")].map((h) => h.textContent),
      ).toEqual(["Aussage g1", "Aussage g2", "Aussage g3", "Aussage g4"]);
      // The summary stays; the rail counts what the chip keeps.
      expect(
        within(reference).getByText("Nimmt Zahlungen an."),
      ).toBeInTheDocument();
      const rail = screen.getByRole("navigation", { name: "Inhalt" });
      expect(within(rail).getByText("Aufträge").closest("a")?.textContent).toBe(
        "Aufträge4",
      );
      // A switch by the tab bar lets the chip go.
      fireEvent.click(screen.getByRole("tab", { name: /Einstieg/ }));
      fireEvent.click(screen.getByRole("tab", { name: /Nachschlagen/ }));
      expect(
        screen.getByRole("button", { name: new RegExp(`^${S.ask.all}`) }),
      ).toHaveAttribute("aria-pressed", "true");
    });

    it("lets the chip go when the address lands on a statement it hides", async () => {
      const scrolled = vi.fn();
      Element.prototype.scrollIntoView = scrolled;
      serve({ "projects/PAY": many });
      render(<ProjectPage projectKey="PAY" />);
      await openTab(/Nachschlagen/);
      fireEvent.click(
        screen.getByRole("button", { name: /Was ging schon schief/ }),
      );
      expect(
        [...document.querySelectorAll("article h4")].map((h) => h.textContent),
      ).toEqual(["Aussage p1", "Aussage p2"]);
      act(() => {
        window.history.pushState({}, "", "/projects/PAY#description/d1");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      await waitFor(() =>
        expect(scrolled.mock.contexts).toContain(document.getElementById("d1")),
      );
      expect(
        screen.getByRole("button", { name: new RegExp(`^${S.ask.all}`) }),
      ).toHaveAttribute("aria-pressed", "true");
      // The same link again, the chip pressed meanwhile: it lets go again,
      // though the address names the target it named before.
      fireEvent.click(
        screen.getByRole("button", { name: /Was ging schon schief/ }),
      );
      expect(document.getElementById("d1")).toBeNull();
      act(() => {
        window.history.pushState({}, "", "/projects/PAY#description/d1");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      await waitFor(() => expect(document.getElementById("d1")).not.toBeNull());
      // A statement the chip keeps: the chip stays.
      fireEvent.click(
        screen.getByRole("button", { name: /Was ging schon schief/ }),
      );
      act(() => {
        window.history.pushState({}, "", "/projects/PAY#description/p1");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });
      expect(
        screen.getByRole("button", { name: /Was ging schon schief/ }),
      ).toHaveAttribute("aria-pressed", "true");
      // A click on the tab open already is no switch: the chip stays.
      fireEvent.click(screen.getByRole("tab", { name: /Nachschlagen/ }));
      expect(
        screen.getByRole("button", { name: /Was ging schon schief/ }),
      ).toHaveAttribute("aria-pressed", "true");
    });

    it("lays a summary that opens on a heading under the step, one level down", async () => {
      serve({
        "projects/PAY": {
          ...many,
          description: {
            ...many.description!,
            summary: "#### Zweck\n\nDer Kern.\n\n#### Rollen\n\n- a",
          },
        },
      });
      render(<ProjectPage projectKey="PAY" />);
      const start = await screen.findByRole("tabpanel", {
        name: S.tabs.start,
      });
      expect(within(start).getByText("Zweck").tagName).toBe("H4");
      expect(within(start).getByText("Rollen").tagName).toBe("H4");
      // Problem areas only with problems: no empty table in this step.
      expect(within(start).queryByText(S.project.noProblems)).toBeNull();
      expect(within(start).queryByText(S.project.problems)).toBeNull();
    });

    it("opens the history from the quiet line", async () => {
      serve({ "projects/PAY": project });
      render(<ProjectPage projectKey="PAY" />);
      fireEvent.click(await screen.findByRole("button", { name: /^Stand:/ }));
      expect(
        screen.getByRole("tabpanel", { name: S.tabs.history }),
      ).toBeInTheDocument();
      expect(window.location.hash).toBe("#history");
    });

    it("shows no chips for a collection, whose page holds a summary alone", async () => {
      serve({
        "projects/PAY": {
          ...project,
          shape: "collection",
          description: { ...project.description!, statements: [] },
        },
      });
      render(<ProjectPage projectKey="PAY" />);
      expect(
        await screen.findByRole("heading", {
          level: 3,
          name: S.path.step(1, S.path.collectionWhat),
        }),
      ).toBeInTheDocument();
      await openTab(/Nachschlagen/);
      expect(screen.queryByRole("group", { name: S.ask.label })).toBeNull();
    });
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
    await openTab(/Nachschlagen/);
    const desc = screen.getByRole("tabpanel", { name: S.tabs.reference });
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
    await openTab(/Nachschlagen/);
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

  describe("reading path", () => {
    const st = (
      id: string,
      kind: Statement["kind"],
      sources = 0,
    ): Statement => ({
      id,
      kind,
      name: `Aussage ${id}`,
      text: `Text ${id}.`,
      files: [],
      sources: Array.from({ length: sources }, (_, i) => ({
        pr_key: `PAY/pay-clearing#${i + 1}`,
      })),
      area: "Abrechnung",
    });
    const full: RepoDetail = {
      ...repo,
      map: repoMap,
      description: {
        ...project.description!,
        summary: "Gleicht Zahlungen ab.\n\n#### Teile\n\nExport, Abgleich.",
        statements: [
          st("r1", "rule", 1),
          st("x1", "extension", 0),
          st("x2", "extension", 2),
          st("x3", "extension", 1),
          st("x4", "extension", 3),
          st("f1", "pitfall", 1),
          st("c1", "component"),
        ],
      },
    };
    // The project lists pay-clearing second of three: pay-lib comes next.
    const listing: ProjectDetail = {
      ...project,
      repos: [
        project.repos[0]!,
        { ...project.repos[1]!, slug: "pay-clearing" },
        { ...project.repos[1]!, summary: "Teilt Code." },
      ],
    };

    it("leads through what, map, guard, change and pitfalls, then the next repository", async () => {
      serve({
        "projects/PAY/repos/pay-clearing": full,
        "projects/PAY": listing,
      });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const start = await screen.findByRole("tabpanel", {
        name: S.tabs.start,
      });
      await within(start).findByText(stepText(5, S.path.pitfalls));
      expect(
        within(start)
          .getAllByRole("heading", { level: 3 })
          .map((h) => h.textContent),
      ).toEqual([
        S.path.step(1, S.path.repoWhat),
        S.path.step(2, S.repo.map),
        S.path.step(3, S.path.guard),
        S.path.step(4, S.path.change),
        S.path.step(5, S.path.pitfalls),
      ]);
      // The map: zones titled from strings.ts, parts not pages.
      const drawing = within(start).getByRole("img", { name: "Aufbau" });
      expect(drawing.textContent).toContain("Eingang");
      expect(within(drawing.parentElement!).queryByRole("link")).toBeNull();
      // The summary's opening, the whole of it a link away.
      expect(
        within(start).getByText("Gleicht Zahlungen ab."),
      ).toBeInTheDocument();
      expect(
        within(start)
          .getByRole("link", { name: /Ganzer Überblick/ })
          .getAttribute("href"),
      ).toBe("/projects/PAY/repos/pay-clearing#description/summary");
      // Where to change what: the most cited three, the rest a chip away.
      const change = within(start)
        .getByText(stepText(4, S.path.change))
        .closest("section")!;
      expect(
        within(change)
          .getAllByRole("link")
          .map((a) => [a.textContent, a.getAttribute("href")]),
      ).toEqual([
        ["Aussage x4", "/projects/PAY/repos/pay-clearing#description/x4"],
        ["Aussage x2", "/projects/PAY/repos/pay-clearing#description/x2"],
        ["Aussage x3", "/projects/PAY/repos/pay-clearing#description/x3"],
      ]);
      // A repository's statement is labelled as one, not a shared one.
      expect(within(start).getAllByText("Erweiterung").length).toBe(3);
      // Pitfalls, then this repository's problem areas, without its column.
      const pitfalls = within(start)
        .getByText(stepText(5, S.path.pitfalls))
        .closest("section")!;
      const pitfall = within(pitfalls).getByRole("link", {
        name: "Aussage f1",
      });
      const area = await within(pitfalls).findByText("Clearing-Adapter");
      expect(
        pitfall.compareDocumentPosition(area) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(within(pitfalls).queryByText("Repository")).toBeNull();
      // Next: the repository after this one in the project's list.
      const next = within(start).getByRole("link", { name: /Weiter/ });
      expect(next.getAttribute("href")).toBe("/projects/PAY/repos/pay-lib");
      expect(next).toHaveTextContent("Teilt Code.");
      // The rail: the path, the reference, the history.
      const rail = screen.getByRole("navigation", { name: "Inhalt" });
      for (const title of [S.path.label, S.tabs.reference, S.tabs.history])
        expect(within(rail).getByText(title)).toBeInTheDocument();
      expect(within(rail).queryByText("Problemzonen")).toBeNull();
      // All four extensions: the reference tab, its chip pressed.
      fireEvent.click(
        within(change).getByRole("button", { name: /Alle 4 Erweiterungen/ }),
      );
      const reference = screen.getByRole("tabpanel", {
        name: S.tabs.reference,
      });
      expect(
        within(reference).getByRole("button", { name: /Wo ändere ich was/ }),
      ).toHaveAttribute("aria-pressed", "true");
      expect(
        [...reference.querySelectorAll("article h4")].map((h) => h.textContent),
      ).toEqual(["Aussage x1", "Aussage x2", "Aussage x3", "Aussage x4"]);
      // The problem areas are the path's, not the reference's.
      expect(within(reference).queryByText("Clearing-Adapter")).toBeNull();
    });

    it("leaves out the steps it has nothing for, the last repository no next", async () => {
      serve({
        "projects/PAY/repos/pay-clearing": {
          ...repo,
          description: {
            ...project.description!,
            statements: [st("c1", "component")],
          },
        },
        "projects/PAY": {
          ...project,
          problems: [],
          repos: [
            ...project.repos,
            { ...project.repos[1]!, slug: "pay-clearing" },
          ],
        },
      });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const start = await screen.findByRole("tabpanel", {
        name: S.tabs.start,
      });
      // Wait for the project: the switch in the rail.
      await screen.findAllByRole("link", { name: /Projekt PAY/ });
      expect(
        within(start)
          .getAllByRole("heading", { level: 3 })
          .map((h) => h.textContent),
      ).toEqual([S.path.step(1, S.path.repoWhat)]);
      expect(within(start).queryByText(S.path.next)).toBeNull();
    });

    it("says so without a description, the quiet line too", async () => {
      serve({
        "projects/PAY/repos/pay-clearing": repo,
        "projects/PAY": listing,
      });
      render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
      const start = await screen.findByRole("tabpanel", {
        name: S.tabs.start,
      });
      expect(start).toHaveTextContent(
        `${S.project.noDescription} ${S.projects.firstDescription}`,
      );
      expect(
        screen.getByRole("button", {
          name: /^Stand: noch keine Beschreibung\W*250 Notizen/,
        }),
      ).toBeInTheDocument();
      // The meta line names the project, no figures.
      const metaLine = screen.getByRole("heading", { level: 2 })
        .nextElementSibling!.textContent;
      expect(metaLine).toBe("Projekt PAY");
      // The history tab counts the notes.
      expect(screen.getByRole("tab", { name: /Verlauf/ })).toHaveTextContent(
        "250",
      );
    });
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
    expect(within(rail).getByText(stepText(2, S.repo.map))).toBeInTheDocument();
  });

  it("goes without the project's parts when the project fails", async () => {
    serve({ "projects/PAY/repos/pay-clearing": repo });
    render(<RepoPage projectKey="PAY" slug="pay-clearing" />);
    expect(
      await screen.findByRole("tabpanel", { name: S.tabs.start }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: "Aufbau" })).toBeNull();
    expect(screen.queryByText("Problemzonen")).toBeNull();
    expect(screen.queryByText("pay-api")).toBeNull();
    // No project, no next repository.
    expect(screen.queryByText(S.path.next)).toBeNull();
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
    await openTab(/Nachschlagen/);
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
    expect(
      await screen.findByText("Der Zahlungsverkehr über alle Kanäle."),
    ).toBeInTheDocument();
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

  it("keeps operations in a menu that closes on Escape, a press outside and a pick", async () => {
    serve({
      me: anonymous,
      projects,
      sources: { enabled: true, default_host: "", max_calls: 0, sources: [] },
    });
    window.history.replaceState({}, "", "/");
    render(<App />);
    const opener = await screen.findByRole("button", { name: S.nav.ops });
    expect(opener).toHaveAttribute("aria-haspopup", "menu");
    expect(opener).toHaveAttribute("aria-expanded", "false");
    // The knowledge stays in the nav; operations are not in it.
    const nav = screen.getByRole("navigation", { name: S.nav.label });
    expect(within(nav).queryByText(S.nav.queue)).toBeNull();
    await screen.findByText(S.nav.deps);

    fireEvent.click(opener);
    const menu = screen.getByRole("menu", { name: S.nav.opsMenu(true) });
    expect(opener).toHaveAttribute("aria-expanded", "true");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((a) => a.textContent),
    ).toEqual([S.nav.sources, S.nav.queue, S.nav.costs]);
    // The first entry takes the focus; the arrows walk the entries.
    const items = within(menu).getAllByRole("menuitem");
    expect(items[0]).toHaveFocus();
    fireEvent.keyDown(items[0]!, { key: "ArrowDown" });
    expect(items[1]).toHaveFocus();
    fireEvent.keyDown(items[1]!, { key: "ArrowUp" });
    fireEvent.keyDown(items[0]!, { key: "ArrowUp" });
    expect(items[2]).toHaveFocus();
    // Out of the tab order: Tab leaves the menu, the arrows walk it.
    expect(items.every((a) => a.getAttribute("tabindex") === "-1")).toBe(true);

    // Escape closes, gives the focus back to the button and is the
    // menu's alone: no other Escape listener hears it.
    const elsewhere = vi.fn();
    document.addEventListener("keydown", elsewhere);
    fireEvent.keyDown(items[2]!, { key: "Escape" });
    document.removeEventListener("keydown", elsewhere);
    expect(elsewhere).not.toHaveBeenCalled();
    expect(screen.queryByRole("menu")).toBeNull();
    expect(opener).toHaveFocus();
    expect(opener).toHaveAttribute("aria-expanded", "false");

    // The focus leaving by keyboard closes; the page's shortcuts hold off
    // while the menu stands.
    fireEvent.click(opener);
    const first = within(screen.getByRole("menu")).getAllByRole("menuitem")[0]!;
    const slash = () => new KeyboardEvent("keydown", { key: "/" });
    expect(shortcutFree(slash())).toBe(false);
    fireEvent.blur(first, {
      relatedTarget: screen.getByRole("searchbox", { name: S.search.box }),
    });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(shortcutFree(slash())).toBe(true);

    // A press outside closes; one on the button toggles.
    fireEvent.click(opener);
    expect(screen.getByRole("menu")).toBeInTheDocument();
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
    fireEvent.click(opener);
    fireEvent.click(opener);
    expect(screen.queryByRole("menu")).toBeNull();

    // A pick opens its page and closes the menu; the button is current.
    fireEvent.click(opener);
    fireEvent.click(screen.getByRole("menuitem", { name: S.nav.costs }));
    await waitFor(() => expect(window.location.pathname).toBe("/costs"));
    expect(screen.queryByRole("menu")).toBeNull();
    // A pick that navigates leaves the focus to the new page.
    expect(opener).not.toHaveFocus();
    expect(opener.className).toContain("bg-active text-ink");
    expect(
      within(nav).getByText(S.nav.projects).getAttribute("aria-current"),
    ).toBeNull();

    // The page's own entry goes nowhere: the focus back on the button.
    fireEvent.click(opener);
    fireEvent.click(screen.getByRole("menuitem", { name: S.nav.costs }));
    expect(screen.queryByRole("menu")).toBeNull();
    expect(opener).toHaveFocus();
  });

  it("counts and marks the queue on the operations button, no Quellen without sources", async () => {
    serve({
      me: anonymous,
      projects,
      "queue/count": { count: 3, paused: true },
    });
    window.history.replaceState({}, "", "/");
    render(<App />);
    const opener = await screen.findByRole("button", {
      name: S.nav.opsLabel(3, true),
    });
    expect(opener).toHaveAccessibleName("Admin, 3 in der Queue, Pausiert");
    expect(S.nav.opsMenu(true)).toBe("Admin: Quellen, Queue, Kosten");
    expect(within(opener).getByTestId("queue-count")).toHaveTextContent("3");
    expect(within(opener).getByTestId("queue-paused")).toHaveTextContent(
      S.queue.pausedBadge,
    );
    expect(opener.className).not.toContain("bg-active text-ink");
    fireEvent.click(opener);
    const menu = screen.getByRole("menu", { name: S.nav.opsMenu(false) });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((a) => a.firstElementChild?.textContent),
    ).toEqual([S.nav.queue, S.nav.costs]);
    // The queue's entry carries its count and pause too, without the ids.
    expect(
      within(menu).getByRole("menuitem", { name: /Queue/ }),
    ).toHaveTextContent(`3${S.queue.pausedBadge}`);
    expect(screen.getAllByTestId("queue-count")).toHaveLength(1);
  });

  it("marks the operations button on the queue and closes on a route change", async () => {
    serve({ me: anonymous, projects, "queue/count": { count: 0 } });
    window.history.replaceState({}, "", "/queue");
    render(<App />);
    const opener = await screen.findByRole("button", { name: S.nav.ops });
    expect(opener.className).toContain("bg-active text-ink");
    fireEvent.click(opener);
    expect(screen.getByRole("menu")).toBeInTheDocument();
    act(() => {
      window.history.pushState({}, "", "/");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(opener.className).not.toContain("bg-active text-ink");
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
  const loc = { href: "/", pathname, search, hash: "", reload: vi.fn() };
  vi.stubGlobal("location", loc);
  return loc;
}

describe("session gate", () => {
  afterEach(() => {
    window.sessionStorage.clear();
    armSignIn(false);
  });

  it("renders the wiki without a name or Logout in none mode", async () => {
    answer({ status: 200, body: anonymous });
    render(<App />);
    expect(await screen.findByText("PAY")).toBeInTheDocument();
    expect(screen.queryByText("Logout")).not.toBeInTheDocument();
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
    fireEvent.click(screen.getByText("Logout"));
    await waitFor(() => expect(loc.href).toBe("/?signed_out=1"));
    expect(calls).toContainEqual({ url: "/api/auth/logout", method: "POST" });
  });

  it("goes to the login on 401, by a full navigation", async () => {
    const loc = stubLocation();
    answer({ status: 401, body: { error: "sign-in required" } });
    render(<App />);
    await waitFor(() => expect(loc.href).toBe("/api/auth/login"));
    expect(screen.getByText("Weiterleitung zum Login …")).toBeInTheDocument();
    expect(screen.queryByText("PAY")).not.toBeInTheDocument();
  });

  it.each([
    ["?auth_error=oidc_callback_failed", "Der Login konnte nicht"],
    ["?signed_out=1", "Ausgeloggt."],
  ])(
    "halts on %s with a button instead of redirecting",
    async (search, text) => {
      const loc = stubLocation(search);
      const calls = answer({ status: 401 });
      render(<App />);
      expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
      expect(screen.getByText("Login").closest("a")).toHaveAttribute(
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
      await screen.findByText(/hat gerade eben keine Session ergeben/),
    ).toBeInTheDocument();
    expect(loc.href).toBe("/");
  });

  it("halts when the session cannot be checked", async () => {
    answer({ status: 500 });
    render(<App />);
    expect(
      await screen.findByText(
        "Die Session konnte nicht geprüft werden (HTTP 500).",
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
      await screen.findByText(/der Identity Provider antwortet nicht/),
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
    fireEvent.click(screen.getByText("Logout"));
    expect(
      await screen.findByText("Logout fehlgeschlagen, bitte erneut versuchen."),
    ).toBeInTheDocument();
    expect(loc.href).toBe("/");
    expect(loc.reload).not.toHaveBeenCalled();
    expect(screen.getByText("sub-1")).toBeInTheDocument();
  });

  it("passes the page along on the halted page's sign-in link", async () => {
    stubLocation("?x=1&auth_error=oidc_callback_failed", "/sources/werkzeuge");
    answer({ status: 401 });
    render(<App />);
    const link = await screen.findByText("Login");
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
      await screen.findByText(/Der Login ist falsch eingerichtet/),
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
      st("s3", "component", "Gradle-Build", "Entwicklung und Deployment"),
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
      ["Entwicklung und Deployment", ["Gradle-Build"]],
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
      ["Entwicklung und Deployment", ["Gradle-Build"]],
    ]);
    expect(statementMatches(description.statements[0], "abfrage")).toBe(true);
    // The chapter keeps its anchor, the area's place among all of them.
    expect(
      document.getElementById("area-2")?.querySelector("h3")?.textContent,
    ).toBe("Entwicklung und Deployment");
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
      ["area-2", "Entwicklung und Deployment", 1, false],
    ]);
    expect(rail("Gradle")).toEqual([
      ["summary", S.rail.summary, undefined, undefined],
      ["area-2", "Entwicklung und Deployment", 1, true],
    ]);
  });

  it("reads an area named as the summary as no area: one Überblick in the rail", () => {
    const named = {
      ...description,
      statements: [
        st("o1", "entity", "Auftrag", "Überblick"),
        st("o2", "flow", "Ablauf", " überblick "),
        st("a1", "rule", "Regel", "Abfrage"),
      ],
    };
    const labels = descriptionRail(named, "", "description").items.map(
      (i) => i.label,
    );
    expect(labels.filter((l) => l === S.rail.summary)).toHaveLength(1);
    expect(labels).toEqual([S.rail.summary, S.areas.otherArea, "Abfrage"]);
    render(<DescriptionView description={named} emptyHint="" pageRepo="svc" />);
    expect(chapters()).toEqual([
      [S.areas.otherArea, ["Auftrag", "Ablauf"]],
      ["Abfrage", ["Regel"]],
    ]);
  });

  it("shows only the kinds asked, in their chapters, the search on top", () => {
    const rules: readonly Statement["kind"][] = ["rule", "invariant"];
    expect(
      shownStatements(description.statements, "", rules).map((s) => s.id),
    ).toEqual(["s4"]);
    expect(shownStatements(description.statements, "Gradle", rules)).toEqual(
      [],
    );
    expect(
      shownStatements(description.statements, "Alt").map((s) => s.id),
    ).toEqual(["s2"]);
    const { rerender } = render(
      <DescriptionView
        description={description}
        emptyHint=""
        pageRepo="svc"
        kinds={rules}
        tail={<p>Fakten</p>}
      />,
    );
    expect(chapters()).toEqual([["Abfrage", ["Regel"]]]);
    // The summary stays, the tail comes after the chapters.
    expect(screen.getByText("Ein Dienst.")).toBeInTheDocument();
    expect(screen.getByText("Fakten")).toBeInTheDocument();
    // Asked for kinds the page has none of: said, as a search with none.
    rerender(
      <DescriptionView
        description={description}
        emptyHint=""
        pageRepo="svc"
        kinds={["pitfall"]}
        tail={<p>Fakten</p>}
      />,
    );
    expect(chapters()).toEqual([]);
    expect(screen.getByText(S.rail.noHits)).toBeInTheDocument();
    // The rail counts what the kinds keep, as hits.
    expect(
      descriptionRail(description, "", "description", rules).items.map((c) => [
        c.label,
        c.count,
        c.hit,
      ]),
    ).toEqual([
      [S.rail.summary, undefined, undefined],
      ["Abfrage", 1, true],
    ]);
    // The search's line counts what the chip keeps of the hits.
    expect(
      descriptionRail(description, "Regel", "description", rules).hitLine,
    ).toBe("1 von 1 Eintrag · 1 Kapitel");
    expect(
      descriptionRail(description, "Gradle", "description", rules).hitLine,
    ).toBe(S.rail.noHits);
    // No description: the tail still stands.
    rerender(
      <DescriptionView description={null} emptyHint="" tail={<p>Fakten</p>} />,
    );
    expect(screen.getByText("Fakten")).toBeInTheDocument();
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
