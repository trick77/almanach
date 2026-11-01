import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { duration, QueuePage } from "./QueuePage";
import type { Me, Queue, QueueItem, QueueRun } from "./api";
import { S } from "./strings";

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

const iso = (minutes: number) =>
  new Date(Date.now() + minutes * 60_000).toISOString();

function run(over: Partial<QueueRun> = {}): QueueRun {
  return {
    id: 41,
    state: "describing",
    mode: "full",
    trigger: "first_ready",
    commit: "a1b2c3d4e5",
    base_commit: "",
    calls: 3,
    queued_at: iso(-20),
    started_at: iso(-9),
    cost_usd: "0.412",
    steps: [
      {
        kind: "catalogue",
        name: "",
        files: 0,
        bytes: 0,
        state: "done",
        skip: "",
        calls: 1,
        failed: 0,
        at: iso(-8),
        cost_usd: "0.031",
      },
      {
        kind: "subject",
        name: "Abgleich",
        files: 11,
        bytes: 61000,
        state: "done",
        skip: "",
        calls: 1,
        failed: 0,
        at: iso(-5),
        cost_usd: "0.089",
      },
      {
        kind: "subject",
        name: "Rechnungslauf",
        files: 2,
        bytes: 100,
        state: "skipped",
        skip: "bytes",
        calls: 0,
        failed: 0,
        at: null,
      },
      {
        kind: "subject",
        name: "Webhook-Empfang",
        files: 9,
        bytes: 61000,
        state: "current",
        skip: "",
        calls: 2,
        failed: 2,
        at: iso(-1),
      },
      {
        kind: "project",
        name: "",
        files: 0,
        bytes: 0,
        state: "todo",
        skip: "",
        calls: 0,
        failed: 0,
        at: null,
      },
      {
        kind: "repo_map",
        name: "",
        files: 0,
        bytes: 0,
        state: "todo",
        skip: "",
        calls: 0,
        failed: 0,
        at: null,
      },
    ],
    ...over,
  };
}

function note(pr: number, over: Partial<QueueItem> = {}): QueueItem {
  return {
    kind: "note",
    project: "pay",
    repo: "ledger",
    pr_key: `pay/ledger#${pr}`,
    title: `Titel ${pr}`,
    change_kind: "bugfix",
    received_at: iso(-3),
    attempts: 0,
    ...over,
  };
}

function queue(over: Partial<Queue> = {}): Queue {
  return {
    current: null,
    next: [],
    sync: {
      running: false,
      done: 0,
      total: 0,
      finished_at: null,
      next_at: iso(30),
    },
    spent: { cost_usd: "1.284", calls: 23 },
    recent: [],
    poll_seconds: 20,
    max_attempts: 5,
    ...over,
  };
}

const runItem = (r: QueueRun): QueueItem => ({
  kind: "run",
  project: "pay",
  repo: "billing",
  run: r,
  attempts: 0,
});

describe("QueuePage", () => {
  it("shows the run in flight with its steps, then the rest in order", async () => {
    serve({
      queue: queue({
        current: { ...runItem(run()), since: iso(-1) },
        next: [
          note(812),
          note(317, { repo: "billing", held_by: 41, attempts: 1 }),
          runItem(
            run({
              id: 42,
              state: "queued",
              started_at: null,
              cost_usd: undefined,
              steps: [],
            }),
          ),
        ],
      }),
    });
    render(<QueuePage />);
    const items = await screen.findAllByTestId("queue-item");
    expect(items).toHaveLength(4);
    expect(within(items[0]).getByText("Run #41")).toBeInTheDocument();
    expect(within(items[0]).getByText("$0.41 bisher")).toBeInTheDocument();
    const steps = within(items[0]).getByTestId("steps");
    expect(
      within(steps).getByText("Webhook-Empfang").closest("li"),
    ).toHaveAttribute("data-state", "current");
    expect(
      within(steps).getByText(
        new RegExp(S.queue.skipped(S.baseline.skipReason.bytes)),
      ),
    ).toBeInTheDocument();
    expect(
      within(steps).getByText(/2 Aufrufe ohne Antwort/),
    ).toBeInTheDocument();
    expect(within(steps).getByText("Schritt 4 von 6")).toBeInTheDocument();
    expect(within(items[1]).getByText("pay/ledger#812")).toBeInTheDocument();
    expect(within(items[2]).getByText(S.queue.heldBy(41))).toBeInTheDocument();
    // The attempt cap and the poll are the worker's, from the body.
    expect(within(items[2]).getByText("Versuch 2 von 5")).toBeInTheDocument();
    expect(within(items[3]).getByText("Run #42")).toBeInTheDocument();
    expect(
      within(screen.getByTestId("timeline")).getByText(S.queue.lane.now),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("timeline")).getByText(S.queue.lane.after),
    ).toBeInTheDocument();
    expect(screen.getByText(S.queue.polling)).toBeInTheDocument();
    expect(screen.getByText("$1.28")).toBeInTheDocument();
  });

  it("puts a hold on the next item, its cause and its end", async () => {
    serve({
      queue: queue({
        next: [
          { ...runItem(run()), wait: { reason: "rate_limit", until: iso(2) } },
          note(812),
        ],
      }),
    });
    render(<QueuePage />);
    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent(S.queue.wait.rate_limit);
    expect(status).toHaveTextContent(/in 1:5\d|in 2:00/);
    expect(
      within(screen.getByTestId("timeline")).getByText(S.queue.lane.next),
    ).toBeInTheDocument();
    // The waiting started run lists its steps; nothing is current.
    expect(screen.getByTestId("steps")).toBeInTheDocument();
    expect(
      within(screen.getByTestId("timeline")).queryByText(S.queue.lane.now),
    ).not.toBeInTheDocument();
  });

  it("says a step waiting for its clone is taken at the worker's next look", async () => {
    serve({
      queue: queue({
        next: [
          {
            ...runItem(run()),
            wait: { reason: "clone_busy", since: iso(-1) },
          },
        ],
      }),
    });
    render(<QueuePage />);
    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent("in ≤ 20 s");
    expect(status).toHaveTextContent(S.queue.wait.clone_busy);
    expect(status).toHaveTextContent(/seit 1:0\d/);
  });

  it("says when nothing waits, and lists what finished", async () => {
    serve({
      queue: queue({
        recent: [
          {
            kind: "run",
            at: iso(-30),
            project: "shop",
            repo: "search",
            run_id: 39,
            state: "failed",
            error: "aufgegeben",
            retry_at: iso(60 * 23),
            cost_usd: "0.204",
          },
          {
            kind: "note",
            at: iso(-40),
            project: "pay",
            repo: "ledger",
            pr_key: "pay/ledger#808",
            outcome: "ok",
            cost_usd: "0.012",
          },
          {
            kind: "note",
            at: iso(-50),
            project: "tools",
            repo: "x",
            pr_key: "tools/x#12",
          },
        ],
      }),
    });
    render(<QueuePage />);
    expect(
      await screen.findByText(/^Nichts wartet\. Nächster Sync/),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("timeline")).not.toBeInTheDocument();
    expect(screen.queryByText(S.queue.polling)).not.toBeInTheDocument();
    expect(
      screen.getByText(/Erneut automatisch frühestens/),
    ).toBeInTheDocument();
    expect(screen.getByText(S.queue.noteOutcome.ok)).toBeInTheDocument();
    expect(screen.getByText(S.queue.noteOutcome[""])).toBeInTheDocument();
  });

  it("names no next sync while one runs", async () => {
    serve({
      queue: queue({
        sync: {
          running: true,
          repo: "web",
          done: 0,
          total: 3,
          finished_at: null,
          next_at: iso(-1),
        },
      }),
    });
    render(<QueuePage />);
    expect(await screen.findByText(S.queue.empty)).toBeInTheDocument();
  });

  it("prints durations the way a wait is read", () => {
    expect(duration(42_000)).toBe("0:42");
    expect(duration(100_000)).toBe("1:40");
    expect(duration(-5)).toBe("0:00");
    expect(duration(12 * 60_000)).toBe("12 min");
    expect(duration(65 * 60_000)).toBe("1 h 5 min");
  });
});

describe("the queue's entry", () => {
  it("counts the work in the nav and opens the page", async () => {
    window.history.replaceState({}, "", "/warteschlange");
    const me: Me = { mode: "none", user: "" };
    serve({
      me,
      "auth/me": me,
      projects: { projects: [] },
      sources: { enabled: false, default_host: "", sources: [] },
      queue: queue({
        current: { ...runItem(run()), since: iso(-1) },
        next: [note(812)],
      }),
      "queue/count": { count: 2 },
    });
    render(<App />);
    expect(await screen.findByTestId("queue-count")).toHaveTextContent("2");
    expect(
      await screen.findByRole("heading", { name: S.queue.title }),
    ).toBeInTheDocument();
  });
});
