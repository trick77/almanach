import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { SourcePage } from "./SourcePage";
import { SourcesPage } from "./SourcesPage";
import type { SourceDetail, Sources } from "./api";

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
      },
    });
    await waitFor(() => expect(window.location.pathname).toBe("/quellen/neu"));
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

describe("App with sources", () => {
  it("shows the Quellen entry only when the instance has the feature", async () => {
    serve({ sources, projects: { projects: [] } });
    window.history.replaceState({}, "", "/");
    const { unmount } = render(<App />);
    expect(await screen.findByText("Quellen")).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();
    serve({
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
