import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import type { Me, SourceDetail, Sources } from "./api";

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

const sources: Sources = {
  enabled: true,
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
      last_error: "",
      auto_describe: true,
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
      last_error: "",
      auto_describe: true,
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
  dir: "/app/repos/werkzeuge",
  disk_bytes: 1_400_000_000,
  last_listed_at: null,
  last_error: "",
  auto_describe: true,
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
      baseline: {
        run_id: 7,
        state: "done",
        mode: "full",
        trigger: "first_ready",
        commit: "7e1d9a0abcdef",
        step: 3,
        steps: 4,
        skipped: 0,
        cost_usd: "2.418",
        error: "",
        queued_at: when,
        started_at: when,
        finished_at: when,
        documented_commit: "5c3b2a1abcdef",
        documented_at: when,
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
    expect(screen.getByText("SHOP").closest("a")).toHaveAttribute(
      "href",
      "/quellen/SHOP",
    );
  });

  it("says when the feature is off", async () => {
    serve({ sources: { enabled: false, sources: [] } });
    render(<SourcesPage />);
    expect(await screen.findByText(/ALMANACH_REPOS_DIR/)).toBeInTheDocument();
    expect(screen.queryByText("Neue Quelle")).not.toBeInTheDocument();
  });

  it("creates a source: the token is optional, the auth switch appears with it", async () => {
    const writes = serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
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
        auto_describe: true,
      },
    });
    await waitFor(() => expect(window.location.pathname).toBe("/quellen/neu"));
  });

  it("creates a source that describes nothing on its own", async () => {
    const writes = serve({ sources });
    render(<SourcesPage />);
    fireEvent.click(await screen.findByText("Neue Quelle"));
    fireEvent.change(screen.getByPlaceholderText("SHOP"), {
      target: { value: "neu" },
    });
    fireEvent.change(
      screen.getByPlaceholderText("https://bitbucket.example.invalid"),
      { target: { value: "https://forge.example.invalid" } },
    );
    const auto = screen.getByLabelText(/Automatisch beschreiben/);
    expect(auto).toBeChecked();
    fireEvent.click(auto);
    fireEvent.click(screen.getByText("Anlegen und synchronisieren"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect((writes[0]?.body as { auto_describe: boolean }).auto_describe).toBe(
      false,
    );
  });
});

describe("SourcePage baseline", () => {
  it("shows a repository's description state and queues runs", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    expect(await screen.findByText("Beschrieben")).toBeInTheDocument();
    expect(screen.getByText(/^Bei 5c3b2a1 · \$2\.418 · /)).toBeInTheDocument();
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
      { ...detail.repos[0], slug: "b", baseline: { ...run, skipped: 2 } },
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
    expect(screen.getByText("Bei 7e1d9a0 · $0.812 bisher")).toBeInTheDocument();
    expect(screen.getByText("Teilweise")).toBeInTheDocument();
    expect(screen.getByText(/2 Themen übersprungen/)).toBeInTheDocument();
    expect(
      screen.getByText(/Beschreibung fehlgeschlagen: the catalogue failed/),
    ).toBeInTheDocument();
    expect(screen.getByText(/Seite aktualisiert alle 3 s/)).toBeInTheDocument();
    // A run under way blocks its row's button; the documented rows keep
    // theirs.
    expect(screen.getByText("Beschreiben")).toBeDisabled();
    expect(screen.getAllByText("Aktualisieren")[0]).toBeEnabled();
  });

  it("turns the automatic runs off in the access form", async () => {
    const writes = serve({ "sources/werkzeuge": detail });
    render(<SourcePage projectKey="werkzeuge" />);
    fireEvent.click(await screen.findByText("Token"));
    fireEvent.click(screen.getByLabelText(/Automatisch beschreiben/));
    fireEvent.click(screen.getByText("Speichern"));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]?.body).toEqual({
      token_auth: "basic",
      token_user: "",
      auto_describe: false,
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
    fireEvent.change(screen.getByPlaceholderText("acme-cli"), {
      target: { value: "neu" },
    });
    fireEvent.change(
      screen.getByPlaceholderText("https://github.com/acme/acme-cli.git"),
      { target: { value: "http://x" } },
    );
    fireEvent.click(screen.getByText("Hinzufügen und klonen"));
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
      sources: { enabled: false, sources: [] },
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
