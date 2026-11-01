import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import type { Me, SourceDetail, Sources } from "./api";
import { dayTime } from "./format";
import { S } from "./strings";

/** serve answers GET paths from a table and records every write. */
function serve(table: Record<string, unknown>) {
  const writes: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const path = url.replace("/api/", "");
      const method = init?.method ?? "GET";
      if (method !== "GET") {
        writes.push({
          method,
          path,
          body: init?.body ? JSON.parse(String(init.body)) : undefined,
        });
        if (path === "sources" && method === "POST") {
          return {
            ok: true,
            status: 201,
            text: async () => JSON.stringify({ ...detail, project_key: "neu" }),
          };
        }
        if (path.endsWith("/repos") && method === "POST") {
          return {
            ok: false,
            status: 422,
            text: async () =>
              JSON.stringify({
                error: "clone_url: an https URL without credentials",
              }),
          };
        }
        return {
          ok: true,
          status: 202,
          text: async () => `{"status":"queued","repos":1}`,
        };
      }
      if (!(path in table)) {
        return { ok: false, status: 404, json: async () => ({}) };
      }
      return { ok: true, status: 200, json: async () => table[path] };
    }),
  );
  return writes;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

const when = new Date(2026, 9, 30, 14, 2).toISOString();
const synced = new Date(2026, 9, 31, 9, 5).toISOString();

const sources: Sources = {
  enabled: true,
  default_host: "",
  sources: [
    {
      project_key: "SHOP",
      kind: "bitbucket",
      host: "https://forge.example.invalid",
      has_token: true,
      token_auth: "bearer",
      token_user: "",
      repos: 7,
      ready: 7,
      active: 0,
      failed: 0,
      missing: 0,
      last_listed_at: when,
      last_synced_at: when,
      last_error: "",
      created_at: when,
      updated_at: when,
    },
    {
      project_key: "werkzeuge",
      kind: "static",
      host: "",
      has_token: false,
      token_auth: "basic",
      token_user: "",
      repos: 3,
      ready: 1,
      active: 1,
      failed: 1,
      missing: 0,
      last_listed_at: null,
      last_synced_at: synced,
      last_error: "",
      created_at: when,
      updated_at: when,
    },
  ],
};

const detail: SourceDetail = {
  project_key: "werkzeuge",
  kind: "static",
  host: "",
  has_token: false,
  token_auth: "basic",
  token_user: "",
  dir: "/app/repos/p3",
  disk_bytes: 1_400_000_000,
  last_listed_at: null,
  last_error: "",
  created_at: when,
  updated_at: when,
  repos: [
    {
      slug: "acme-cli",
      clone_url: "https://github.com/acme/acme-cli.git",
      origin: "manual",
      branch: "main",
      head_commit: "7e1d9a0abcdef",
      state: "ready",
      last_synced_at: when,
      last_error: "",
      created_at: when,
      dir: "/app/repos/p3/r11",
      baseline: {
        run_id: 7,
        state: "done",
        map_step: false,
        mode: "full",
        trigger: "first_ready",
        commit: "7e1d9a0abcdef",
        step: 3,
        steps: 4,
        skipped: 0,
        skipped_parts: [],
        cost_usd: "2.418",
        error: "",
        queued_at: when,
        started_at: when,
        finished_at: when,
        documented_commit: "5c3b2a1abcdef",
        documented_at: when,
        left_parts: false,
        auto_retry: false,
      },
    },
    {
      slug: "acme-legacy",
      clone_url: "https://github.com/acme/acme-legacy.git",
      origin: "manual",
      branch: "develop",
      head_commit: "",
      state: "failed",
      last_synced_at: null,
      last_error: 'branch "develop": configured branch not found on the remote',
      created_at: when,
      dir: "/app/repos/p3/r12",
      baseline: null,
    },
  ],
};

describe("SourcesPage", () => {
  it("lists sources with their token and state pills", async () => {
    serve({ sources });
    render(<SourcesPage />);
    expect(await screen.findByText("SHOP")).toBeInTheDocument();
    expect(screen.getByText("forge.example.invalid")).toBeInTheDocument();
    expect(screen.getByText("Hinterlegt")).toBeInTheDocument();
    expect(screen.getByText("Öffentlich")).toBeInTheDocument();
    expect(screen.getByText("7 bereit")).toBeInTheDocument();
    expect(screen.getByText("1 läuft")).toBeInTheDocument();
    expect(screen.getByText("1 fehlgeschlagen")).toBeInTheDocument();
    // Last run: SHOP's listing, the static source's newest repo sync.
    expect(screen.getByText(dayTime(when))).toBeInTheDocument();
    expect(screen.getByText(dayTime(synced))).toBeInTheDocument();
    expect(screen.getByText("SHOP").closest("a")).toHaveAttribute(
      "href",
      "/sources/SHOP",
    );
  });

  it("says when the feature is off", async () => {
    serve({ sources: { enabled: false, default_host: "", sources: [] } });
    render(<SourcesPage />);
    expect(await screen.findByText(/BACKEND_REPOS_DIR/)).toBeInTheDocument();
    expect(screen.queryByText("Neue Quelle")).not.toBeInTheDocument();
  });

  it("creates a source: the token is optional, the auth switch appears with it", async () => {
    const writes = serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    expect(screen.queryByText("Token senden als")).not.toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText("SHOP"), {
      target: { value: "neu" },
    });
    fireEvent.change(
      screen.getByPlaceholderText("https://bitbucket.example.invalid"),
      { target: { value: "https://forge.example.invalid" } },
    );
    fireEvent.change(
      screen.getByPlaceholderText("Leer lassen für öffentliche Repositories"),
      { target: { value: "pat" } },
    );
    expect(screen.getByText("Token senden als")).toBeInTheDocument();
    expect(
      screen.getByText(/listet das Projekt über die API/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByText("Anlegen und synchronisieren"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({
      method: "POST",
      path: "sources",
      body: {
        project_key: "neu",
        kind: "bitbucket",
        host: "https://forge.example.invalid",
        token: "pat",
        token_auth: "bearer",
        token_user: "",
      },
    });
    await waitFor(() => expect(window.location.pathname).toBe("/sources/neu"));
  });

  it("gives the token the selected kind's hint", async () => {
    serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    // A single repository is the default, the first option.
    expect(screen.getByText(/«Contents: read»/)).toBeInTheDocument();
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    expect(screen.getByText(/privates Bitbucket-Projekt/)).toBeInTheDocument();
    expect(screen.queryByText(/«Contents: read»/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByText(/^Einzelnes Repository/));
    expect(screen.getByText(/«Contents: read»/)).toBeInTheDocument();
    expect(
      screen.queryByText(/privates Bitbucket-Projekt/),
    ).not.toBeInTheDocument();
  });

  it("never carries a repository's name into a Bitbucket project key", async () => {
    serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-api.git"),
      { target: { value: "https://forge.example.invalid/x/shop-api.git" } },
    );
    expect(screen.getByPlaceholderText("acme-api")).toHaveValue("shop-api");
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    expect(screen.getByPlaceholderText("SHOP")).toHaveValue("");
    fireEvent.change(screen.getByPlaceholderText("SHOP"), {
      target: { value: "SHOP" },
    });
    // A click on the selected kind keeps what was typed.
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    expect(screen.getByPlaceholderText("SHOP")).toHaveValue("SHOP");
    // Back to the single repository: its slug as it was, typed by hand too.
    fireEvent.click(screen.getByText(/^Einzelnes Repository/));
    const slug = screen.getByPlaceholderText("acme-api");
    expect(slug).toHaveValue("shop-api");
    fireEvent.change(slug, { target: { value: "shop" } });
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    expect(screen.getByPlaceholderText("SHOP")).toHaveValue("SHOP");
    fireEvent.click(screen.getByText(/^Einzelnes Repository/));
    expect(screen.getByPlaceholderText("acme-api")).toHaveValue("shop");
  });

  it("creates a source on the configured host", async () => {
    const writes = serve({
      sources: { ...sources, default_host: "https://forge.example.invalid" },
    });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    fireEvent.click(screen.getByText(/^Ganzes Projekt/));
    fireEvent.change(screen.getByPlaceholderText("SHOP"), {
      target: { value: "neu" },
    });
    expect(
      screen.getByPlaceholderText("https://bitbucket.example.invalid"),
    ).toHaveValue("https://forge.example.invalid");
    fireEvent.click(screen.getByText("Anlegen und synchronisieren"));
    await waitFor(() => expect(writes).toHaveLength(1));
    const body = writes[0]?.body as { host: string };
    expect(body.host).toBe("https://forge.example.invalid");
    // A Bitbucket project takes the server's rule: no system sent.
    expect(body).not.toHaveProperty("system");
  });

  it("creates a project with its first repository, the slug from the URL", async () => {
    const writes = serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    // No system switch: one repository alone is a project of one.
    expect(screen.queryByRole("switch")).toBeNull();
    const create = screen.getByText("Anlegen und synchronisieren");
    expect(create).toBeDisabled();
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-api.git"),
      { target: { value: "https://github.com/acme/acme-gateway.git" } },
    );
    const slug = screen.getByPlaceholderText("acme-api") as HTMLInputElement;
    expect(slug.value).toBe("acme-gateway");
    // Typed by hand, the slug stays when the URL changes.
    fireEvent.change(slug, { target: { value: "gateway" } });
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-api.git"),
      { target: { value: "https://github.com/acme/acme-gateway" } },
    );
    expect(slug.value).toBe("gateway");
    fireEvent.click(create);
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]?.body).toMatchObject({
      project_key: "gateway",
      kind: "static",
      clone_url: "https://github.com/acme/acme-gateway",
      slug: "gateway",
    });
  });
});

describe("SourcePage baseline", () => {
  it("shows a repository's description state and queues runs", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    expect(await screen.findByText("Beschrieben")).toBeInTheDocument();
    expect(screen.getByText(/^Bei 5c3b2a1 · \$2\.42 ·/)).toBeInTheDocument();
    expect(screen.getByText("Noch nicht")).toBeInTheDocument();
    expect(screen.getByText(/1 beschrieben/)).toBeInTheDocument();
    // The clone without a head cannot be described yet.
    expect(screen.getByText("Beschreiben")).toBeDisabled();

    fireEvent.click(screen.getByText("Aktualisieren"));
    // The row is locked while its request runs: no second POST.
    expect(screen.getByText("Neu beschreiben")).toBeDisabled();
    await waitFor(() => expect(writes).toHaveLength(1));
    await waitFor(() =>
      expect(screen.getByText("Neu beschreiben")).toBeEnabled(),
    );
    fireEvent.click(screen.getByText("Neu beschreiben"));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes).toEqual([
      {
        method: "POST",
        path: "sources/werkzeuge/repos/acme-cli/describe",
        body: { mode: "reconcile" },
      },
      {
        method: "POST",
        path: "sources/werkzeuge/repos/acme-cli/describe",
        body: { mode: "full" },
      },
    ]);
  });

  it("shows a run under way, a partial and a failed one", async () => {
    const run = detail.repos[0]?.baseline;
    if (!run) throw new Error("fixture");
    const repos = [
      {
        ...detail.repos[0],
        slug: "a",
        baseline: {
          ...run,
          state: "describing" as const,
          step: 1,
          steps: 4,
          documented_commit: "",
          cost_usd: "0.812",
        },
      },
      {
        ...detail.repos[0],
        slug: "b",
        baseline: {
          ...run,
          skipped: 8,
          left_parts: true,
          auto_retry: true,
          skipped_parts: [
            { part: "overview" as const, name: "", reason: "bytes" as const },
            {
              part: "subject" as const,
              name: "Auth",
              reason: "bytes" as const,
            },
            ...["B", "C", "D"].map((name) => ({
              part: "subject" as const,
              name,
              reason: "bytes" as const,
            })),
            { part: "project" as const, name: "", reason: "failed" as const },
            // A code this bundle does not know yet.
            { part: "subject" as const, name: "E", reason: "timeout" as never },
          ],
        },
      },
      {
        ...detail.repos[0],
        slug: "c",
        baseline: {
          ...run,
          state: "failed" as const,
          error: "the catalogue failed: unparseable",
        },
      },
    ];
    serve({ "sources/werkzeuge": { ...detail, repos } });
    render(<SourcePage projectKey="werkzeuge" />);
    expect(await screen.findByText("Beschreibt 2/3")).toBeInTheDocument();
    expect(screen.getByText("Bei 7e1d9a0 · $0.81 bisher")).toBeInTheDocument();
    expect(screen.getByText("Teilweise")).toBeInTheDocument();
    // Each part once: the run counted 8 skips over 7 parts.
    expect(screen.getByText(/7 Teile übersprungen/)).toBeInTheDocument();
    expect(screen.getByText("Grund nicht erfasst: E")).toBeInTheDocument();
    // The skipped parts by reason, one line each, the run's order kept.
    expect(
      screen.getByText(
        "Lesebudget des Laufs überschritten: Überblick, Auth, B und 2 weitere",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Fehler: Modellaufruf fehlgeschlagen: Projekt"),
    ).toBeInTheDocument();
    // When the skipped parts are read again.
    expect(
      screen.getByText("Wird von selbst erneut gelesen."),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Beschreibung fehlgeschlagen: the catalogue failed/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Seite aktualisiert alle 3 s/)).toBeInTheDocument();
    // A run under way blocks its row's button; the documented rows keep
    // theirs.
    expect(screen.getByText("Beschreiben")).toBeDisabled();
    expect(screen.getAllByText("Aktualisieren")[0]).toBeEnabled();
  });

  it("names the project step and the repository map apart", async () => {
    const run = detail.repos[0]!.baseline!;
    const repos = [
      {
        ...detail.repos[0],
        slug: "a",
        baseline: { ...run, state: "project" as const, map_step: false },
      },
      {
        ...detail.repos[0],
        slug: "b",
        baseline: { ...run, state: "project" as const, map_step: true },
      },
    ];
    serve({ "sources/werkzeuge": { ...detail, repos } });
    render(<SourcePage projectKey="werkzeuge" />);
    expect(await screen.findByText("Projekt")).toBeInTheDocument();
    expect(screen.getByText("Aufbau")).toBeInTheDocument();
  });

  it("offers a reconcile of a documented head only while parts were skipped", async () => {
    const run = detail.repos[0]!.baseline!;
    const head = detail.repos[0]!.head_commit;
    const skipped = [
      { part: "subject" as const, name: "Auth", reason: "refused" as const },
    ];
    const gone = [
      { part: "subject" as const, name: "Alt", reason: "gone" as const },
    ];
    const repos = [
      {
        ...detail.repos[0],
        slug: "done",
        baseline: { ...run, documented_commit: head },
      },
      {
        ...detail.repos[0],
        slug: "left",
        baseline: {
          ...run,
          documented_commit: head,
          skipped: 1,
          skipped_parts: skipped,
          trigger: "retry" as const,
          left_parts: true,
        },
      },
      {
        ...detail.repos[0],
        slug: "gone",
        baseline: {
          ...run,
          documented_commit: head,
          skipped: 1,
          skipped_parts: gone,
          // The server's rule: a subject gone is nothing to read again.
          left_parts: false,
        },
      },
    ];
    serve({ "sources/werkzeuge": { ...detail, repos } });
    render(<SourcePage projectKey="werkzeuge" />);
    // Rows alphabetical: done, gone, left.
    const [done, goneBtn, left] = await screen.findAllByText("Aktualisieren");
    expect(done).toBeDisabled();
    expect(left).toBeEnabled();
    expect(goneBtn).toBeDisabled();
    expect(
      screen.getByText("Fehler: Antwort regelwidrig, verworfen: Auth"),
    ).toBeInTheDocument();
    // No retry on its own (auto_retry false): the next run or the button,
    // said once, not under the gone subject.
    expect(
      screen.getAllByText(
        "Der nächste Lauf liest sie erneut, Aktualisieren sofort.",
      ),
    ).toHaveLength(1);
  });

  it("saves the access form without a switch for the runs", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    fireEvent.click(await screen.findByText("Token"));
    // A hand-filled project's token hint: one token, the GitHub scopes.
    expect(
      screen.getByText(/gilt für jedes Repository darin/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/listet das Projekt/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Automatisch/)).toBeNull();
    fireEvent.click(screen.getByText("Speichern"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]?.body).toEqual({
      token_auth: "basic",
      token_user: "",
    });
  });

  it("sends a token typed in the access form", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    fireEvent.click(await screen.findByText("Token"));
    fireEvent.change(
      screen.getByPlaceholderText("Leer lassen für öffentliche Repositories"),
      { target: { value: "ghp" } },
    );
    fireEvent.click(screen.getByText("Speichern"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]?.body).toEqual({
      token: "ghp",
      token_auth: "basic",
      token_user: "",
    });
  });
});

describe("SourcePage", () => {
  it("shows the repos with states, the error line and the add form's rejection", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    expect(await screen.findByText("acme-cli")).toBeInTheDocument();
    expect(screen.getByText("7e1d9a0")).toBeInTheDocument();
    // "Bereit" is a tile label and a state pill.
    expect(screen.getAllByText("Bereit")).toHaveLength(2);
    expect(screen.getByText("Fehlgeschlagen")).toBeInTheDocument();
    expect(screen.getByText(/configured branch not found/)).toBeInTheDocument();
    expect(screen.getByText("1.4 GB")).toBeInTheDocument();
    expect(screen.getByText("Erneut")).toBeInTheDocument();

    fireEvent.click(screen.getByText("Repository hinzufügen"));
    // The slug follows the URL until typed by hand.
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-cli.git"),
      { target: { value: "https://github.com/acme/tool.git" } },
    );
    expect(
      (screen.getByPlaceholderText("acme-cli") as HTMLInputElement).value,
    ).toBe("tool");
    fireEvent.change(screen.getByPlaceholderText("acme-cli"), {
      target: { value: "neu" },
    });
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-cli.git"),
      { target: { value: "http://x" } },
    );
    fireEvent.click(screen.getByText("Hinzufügen und clonen"));
    expect(
      await screen.findByText("clone_url: an https URL without credentials"),
    ).toBeInTheDocument();
    expect(writes[0]?.body).toEqual({
      slug: "neu",
      clone_url: "http://x",
      branch: "",
    });

    // The primary button and the ready row's action share the word; the
    // primary one comes first in the document.
    fireEvent.click(screen.getAllByText("Synchronisieren")[0]);
    await waitFor(() =>
      expect(writes.some((w) => w.path === "sources/werkzeuge/sync")).toBe(
        true,
      ),
    );
  });

  describe("the source's sync button", () => {
    const [cli, legacy] = detail.repos;
    if (!cli?.baseline || !legacy) throw new Error("fixture");
    const run = cli.baseline;

    /** sourceSync renders the page with the given repos and returns the
     *  source's button: the ready row's own one shares its name. */
    const sourceSync = async (repos: SourceDetail["repos"]) => {
      serve({ "sources/werkzeuge": { ...detail, repos } });
      render(<SourcePage projectKey="werkzeuge" />);
      await screen.findByText("acme-cli");
      const top = screen
        .getAllByRole("button", { name: "Synchronisieren" })
        .filter((b) => b.closest("table") === null);
      expect(top).toHaveLength(1);
      return top[0];
    };

    it("is held while a repo syncs", async () => {
      const b = await sourceSync([cli, { ...legacy, state: "syncing" }]);
      expect(b).toBeDisabled();
      expect(b).toHaveAttribute("title", S.sources.actions.syncRunning);
    });

    it("stays free while a repo only waits", async () => {
      const b = await sourceSync([cli, { ...legacy, state: "pending" }]);
      expect(b).toBeEnabled();
      expect(b).not.toHaveAttribute("title");
      // The tile still says something runs.
      expect(screen.getByText(S.sources.tiles.running)).toBeInTheDocument();
    });

    it("stays free while a run describes", async () => {
      const b = await sourceSync([
        { ...cli, baseline: { ...run, state: "describing" } },
        legacy,
      ]);
      expect(b).toBeEnabled();
      expect(b).not.toHaveAttribute("title");
    });
  });

  it("renames the source, a change of case included, and moves the address", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    window.history.replaceState({}, "", "/sources/werkzeuge");
    render(<SourcePage projectKey="werkzeuge" />);
    await screen.findByText("acme-cli");
    const [open] = screen
      .getAllByRole("button", { name: S.sources.actions.rename })
      .filter((b) => b.closest("table") === null);
    fireEvent.click(open);
    fireEvent.change(screen.getByDisplayValue("werkzeuge"), {
      target: { value: "Werkzeuge" },
    });
    // The form's button shares the action's word and comes after it.
    const save = screen
      .getAllByRole("button", { name: S.sources.renameForm.save })
      .filter((b) => b.closest("table") === null)
      .at(-1);
    if (!save || save === open) throw new Error("no save button");
    fireEvent.click(save);
    await waitFor(() =>
      expect(writes).toEqual([
        {
          method: "PATCH",
          path: "sources/werkzeuge",
          body: { project_key: "Werkzeuge" },
        },
      ]),
    );
    await waitFor(() =>
      expect(window.location.pathname).toBe("/sources/Werkzeuge"),
    );
  });

  it("renames a hand-entered repository", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    await screen.findByText("acme-cli");
    const [row] = screen
      .getAllByRole("button", { name: S.sources.repoActions.rename })
      .filter((b) => b.closest("table") !== null);
    fireEvent.click(row);
    expect(
      screen.getByText(S.sources.renameRepoForm.title("acme-cli")),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByDisplayValue("acme-cli"), {
      target: { value: "acme-tool" },
    });
    const save = screen
      .getAllByRole("button", { name: S.sources.renameRepoForm.save })
      .filter((b) => b.closest("table") === null)
      .at(-1);
    if (!save) throw new Error("no save button");
    fireEvent.click(save);
    await waitFor(() =>
      expect(writes).toEqual([
        {
          method: "PATCH",
          path: "sources/werkzeuge/repos/acme-cli",
          body: { slug: "acme-tool", clone_url: "" },
        },
      ]),
    );
  });

  it("shows the current key in the address when opened under an old one", async () => {
    serve({ "sources/alt": detail });
    window.history.replaceState({}, "", "/sources/alt");
    render(<SourcePage projectKey="alt" />);
    await screen.findByText("acme-cli");
    await waitFor(() =>
      expect(window.location.pathname).toBe("/sources/werkzeuge"),
    );
  });

  it("confirms before removing", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    fireEvent.click(await screen.findByText("Quelle entfernen"));
    expect(
      screen.getByText(/Quelle werkzeuge entfernen\?/),
    ).toBeInTheDocument();
    expect(writes).toHaveLength(0);
    fireEvent.click(screen.getByText("Ja, entfernen"));
    await waitFor(() =>
      expect(writes).toEqual([
        { method: "DELETE", path: "sources/werkzeuge", body: undefined },
      ]),
    );
  });
});

const me: Me = { mode: "none", user: "" };

describe("App with sources", () => {
  it("shows the Quellen entry only when the instance has the feature", async () => {
    serve({ me, sources, projects: { projects: [] } });
    window.history.replaceState({}, "", "/");
    const { unmount } = render(<App />);
    expect(await screen.findByText("Quellen")).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();
    serve({
      me,
      sources: { enabled: false, default_host: "", sources: [] },
      projects: { projects: [] },
    });
    render(<App />);
    // The nav entry and the page title both read "Projekte".
    await screen.findAllByText("Projekte");
    await waitFor(() =>
      expect(screen.queryByText("Quellen")).not.toBeInTheDocument(),
    );
  });
});
