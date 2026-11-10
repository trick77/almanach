import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import {
  duration,
  QueuePage,
  queueBusy,
  queueChanged,
  reasonText,
} from "./QueuePage";
import type { Me, Queue, QueueItem, QueueRun, RunStep } from "./api";
import { dayTime } from "./format";
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
    reason: null,
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
    paused: null,
    current: null,
    next: [],
    sync: {
      running: false,
      done: 0,
      total: 0,
      finished_at: null,
      next_at: iso(30),
    },
    spent: { cost_usd: "1.284", calls: 23, unpriced: 2 },
    recent: [],
    failed: [],
    poll_seconds: 20,
    max_attempts: 5,
    more: 0,
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
    expect(screen.getByText(S.queue.polling(3))).toBeInTheDocument();
    expect(screen.getByText("$1.28")).toBeInTheDocument();
    // The calls without a price are said beside the count, explained.
    const spentNote = screen.getByText("23 Aufrufe · 2 ohne Preis");
    expect(spentNote.getAttribute("title")).toBe(S.queue.unpricedHint);
  });

  it("names a departure run by its trigger alone, no mode", async () => {
    serve({
      queue: queue({
        next: [
          runItem(
            run({
              id: 43,
              state: "queued",
              trigger: "departure",
              started_at: null,
              cost_usd: undefined,
              steps: [],
            }),
          ),
        ],
      }),
    });
    render(<QueuePage />);
    const [item] = await screen.findAllByTestId("queue-item");
    expect(
      within(item).getByText(S.queue.trigger.departure),
    ).toBeInTheDocument();
    expect(within(item).queryByText(S.queue.mode.full)).not.toBeInTheDocument();
  });

  it("names a restyle by its mode and trigger, its steps the pages", async () => {
    const step = (kind: RunStep["kind"], state: RunStep["state"]): RunStep => ({
      kind,
      name: "",
      files: 0,
      bytes: 0,
      state,
      skip: "",
      calls: state === "done" ? 1 : 0,
      failed: 0,
      at: state === "done" ? iso(-1) : null,
    });
    serve({
      queue: queue({
        current: {
          ...runItem(
            run({
              id: 44,
              state: "project",
              mode: "restyle",
              trigger: "epoch",
              base_commit: "a1b2c3d4e5",
              calls: 1,
              steps: [
                step("restyle", "done"),
                step("project_restyle", "current"),
                step("repo_map", "todo"),
              ],
            }),
          ),
          since: iso(-1),
        },
      }),
    });
    render(<QueuePage />);
    const [item] = await screen.findAllByTestId("queue-item");
    expect(within(item).getByText(S.queue.mode.restyle)).toBeInTheDocument();
    expect(
      within(item).getByText(S.queue.trigger.epoch, { exact: false }),
    ).toBeInTheDocument();
    expect(within(item).getByText(S.queue.step.restyle)).toBeInTheDocument();
    expect(
      within(item).getAllByText(S.queue.stepSub.project_restyle, {
        exact: false,
      }).length,
    ).toBeGreaterThan(0);
  });

  it("says why a run was queued, under its meta line", async () => {
    serve({
      queue: queue({
        current: {
          ...runItem(
            run({
              reason: {
                kind: "retry",
                attempt: 2,
                of: 3,
                last_error: "the call failed: too_long",
              },
            }),
          ),
          since: iso(-1),
        },
      }),
    });
    render(<QueuePage />);
    expect(await screen.findByTestId("run-reason")).toHaveTextContent(
      `${S.queue.why} ${S.queue.reason.retry(2, 3, "the call failed: too_long")}`,
    );
  });

  it("words every reason", () => {
    const R = S.queue.reason;
    expect(reasonText({ kind: "first" })).toBe(R.first);
    expect(reasonText({ kind: "unfinished" })).toBe(R.unfinished);
    expect(reasonText({ kind: "retry", attempt: 1, of: 3 })).toBe(
      "Neuer Versuch 1 von 3.",
    );
    expect(
      reasonText({
        kind: "interval",
        from: "abcdef1234",
        to: "1234abcdef",
        days: 31,
      }),
    ).toBe(R.interval(31, "abcdef1", "1234abc"));
    expect(reasonText({ kind: "interval", days: 40 })).toBe(R.intervalFull(40));
    expect(reasonText({ kind: "restyle", project_epoch: 2 })).toBe(
      "Projektseite auf Regelversion 2 (Projektseite nur Verdrahtung, als System oder Sammlung beurteilt)",
    );
    expect(
      reasonText({ kind: "restyle", repo_epoch: 1, project_epoch: 9 }),
    ).toBe(
      "Repository-Seite auf Regelversion 1; Projektseite auf Regelversion 9",
    );
    expect(reasonText({ kind: "restyle", repo_epoch: 3 })).toBe(
      "Repository-Seite auf Regelversion 3 (IT-Begriffe englisch statt wörtlich übersetzt)",
    );
    expect(reasonText({ kind: "manual" })).toBe("Von Hand ausgelöst.");
    expect(
      reasonText({ kind: "returned", from: "abcdef1234", to: "1234abcdef" }),
    ).toBe("Wieder aktiviert; Abgleich von abcdef1 bis 1234abc.");
    expect(reasonText({ kind: "returned" })).toBe(R.returnedFull);
    expect(reasonText({ kind: "departure" })).toBe(R.departure);
    // A kind from a newer server: no line.
    expect(
      reasonText({ kind: "later" } as unknown as Parameters<
        typeof reasonText
      >[0]),
    ).toBe("");
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
            kind: "run",
            at: iso(-35),
            project: "shop",
            repo: "cart",
            run_id: 38,
            mode: "restyle",
            state: "done",
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
    expect(screen.queryByText(S.queue.polling(3))).not.toBeInTheDocument();
    // A restyle done reads as what it did, never "described".
    expect(screen.getByText(S.queue.runRestyled)).toBeInTheDocument();
    expect(
      screen.getByText(/Erneut automatisch frühestens/),
    ).toBeInTheDocument();
    expect(screen.getByText(S.queue.noteOutcome.ok)).toBeInTheDocument();
    expect(screen.getByText(S.queue.noteOutcome[""])).toBeInTheDocument();
  });

  it("lists the notes the call failed on, with the reason and the attempts", async () => {
    serve({
      queue: queue({
        failed: [
          {
            project: "pay",
            repo: "ledger",
            pr_key: "pay/ledger#9",
            title: "Gebühren",
            at: iso(-10),
            reason: "too_long: the answer was cut by the model's output limit",
            attempts: 1,
            given_up: false,
          },
          {
            project: "pay",
            repo: "ledger",
            pr_key: "pay/ledger#8",
            at: iso(-20),
            reason: "unparseable",
            attempts: 5,
            given_up: true,
          },
        ],
      }),
    });
    render(<QueuePage />);
    const box = await screen.findByTestId("failed-notes");
    expect(screen.getByText(S.queue.failedNotes)).toBeInTheDocument();
    expect(
      screen.getByText(`${S.queue.failedHint} ${S.queue.givenUpHint}`),
    ).toBeInTheDocument();
    expect(
      within(box).getByRole("link", { name: "pay/ledger#9" }),
    ).toHaveAttribute("href", "/projects/pay/repos/ledger/notes/9");
    expect(within(box).getByText("Gebühren")).toBeInTheDocument();
    expect(within(box).getByText(/output limit/)).toBeInTheDocument();
    expect(
      within(box).getByText(S.queue.failedAttempts(1, 5)),
    ).toBeInTheDocument();
    expect(within(box).getByText(S.queue.givenUp)).toBeInTheDocument();
  });

  it("shows no failed section while none failed", async () => {
    serve({ queue: queue() });
    render(<QueuePage />);
    await screen.findByText(/^Nichts wartet/);
    expect(screen.queryByText(S.queue.failedNotes)).not.toBeInTheDocument();
  });

  it("shows a repository's PROJECT.md in flight", async () => {
    serve({
      queue: queue({
        current: {
          kind: "brief",
          project: "pay",
          repo: "billing",
          attempts: 0,
          since: iso(-1),
        },
        next: [note(812)],
      }),
    });
    render(<QueuePage />);
    const items = await screen.findAllByTestId("queue-item");
    expect(within(items[0]).getByText(S.queue.brief)).toBeInTheDocument();
    expect(within(items[0]).getByText("billing")).toBeInTheDocument();
    expect(
      within(items[0]).getByText(/^erzeugt oder pusht seit/),
    ).toBeInTheDocument();
  });

  it("shows a fold of several notes, when a waiting repository folds and a shared cost", async () => {
    const due = iso(5 * 60);
    serve({
      queue: queue({
        current: { ...note(812), since: iso(-1), batch: 3 },
        next: [note(900, { repo: "billing", due_at: due })],
        recent: [
          {
            kind: "note",
            at: iso(-30),
            project: "pay",
            repo: "web",
            pr_key: "pay/web#4",
            outcome: "ok",
            cost_usd: "0.120",
            batch: 3,
          },
        ],
      }),
    });
    render(<QueuePage />);
    const items = await screen.findAllByTestId("queue-item");
    const inLine = (s: string) =>
      new RegExp(s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
    expect(
      within(items[0]).getByText(inLine(S.queue.batch(3))),
    ).toBeInTheDocument();
    expect(
      within(items[1]).getByText(inLine(S.queue.dueAt(dayTime(due)))),
    ).toBeInTheDocument();
    expect(screen.getByText(S.queue.shared(3))).toBeInTheDocument();
  });

  it("counts what it does not list", async () => {
    serve({ queue: queue({ next: [note(812)], more: 5 }) });
    render(<QueuePage />);
    expect(await screen.findByText(S.queue.more(5))).toBeInTheDocument();
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
  it("pauses and resumes the queue, says since when, tells the nav", async () => {
    let paused: Queue["paused"] = null;
    const posts: string[] = [];
    let told = 0;
    const tell = () => told++;
    window.addEventListener(queueChanged, tell);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        const path = url.replace("/api/", "");
        if (init?.method === "POST") {
          posts.push(path);
          paused = path === "queue/pause" ? { at: iso(-2) } : null;
          return { ok: true, status: 204, text: async () => "" };
        }
        return { ok: true, status: 200, json: async () => queue({ paused }) };
      }),
    );
    render(<QueuePage />);
    (await screen.findByRole("button", { name: S.queue.pause })).click();
    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent(S.queue.paused(dayTime(paused!.at)));
    expect(banner).toHaveTextContent(S.queue.pausedHint);
    within(banner).getByRole("button", { name: S.queue.resume }).click();
    expect(
      await screen.findByRole("button", { name: S.queue.pause }),
    ).toBeInTheDocument();
    expect(posts).toEqual(["queue/pause", "queue/resume"]);
    expect(told).toBe(2);
    window.removeEventListener(queueChanged, tell);
  });

  it("is idle while paused with only waiting items", () => {
    const waiting = queue({ next: [note(812)] });
    expect(queueBusy(waiting)).toBe(true);
    expect(queueBusy({ ...waiting, paused: { at: iso(-1) } })).toBe(false);
    expect(
      queueBusy({
        ...waiting,
        paused: { at: iso(-1) },
        current: { ...runItem(run()), since: iso(-1) },
      }),
    ).toBe(true);
  });

  it("says when a pause cannot be set", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) =>
        init?.method === "POST"
          ? { ok: false, status: 500, text: async () => '{"error":"x"}' }
          : { ok: true, status: 200, json: async () => queue() },
      ),
    );
    render(<QueuePage />);
    (await screen.findByRole("button", { name: S.queue.pause })).click();
    expect(await screen.findByText(S.queue.pauseFailed)).toBeInTheDocument();
  });

  it("marks the nav entry while paused", async () => {
    window.history.replaceState({}, "", "/queue");
    const me: Me = { mode: "none", user: "" };
    serve({
      me,
      "auth/me": me,
      projects: { projects: [] },
      sources: { enabled: false, default_host: "", max_calls: 0, sources: [] },
      queue: queue({ paused: { at: iso(-5) } }),
      "queue/count": { count: 0, paused: true },
    });
    render(<App />);
    expect(await screen.findByTestId("queue-paused")).toHaveTextContent(
      S.queue.pausedBadge,
    );
  });

  it("counts the work in the nav and opens the page", async () => {
    window.history.replaceState({}, "", "/queue");
    const me: Me = { mode: "none", user: "" };
    serve({
      me,
      "auth/me": me,
      projects: { projects: [] },
      sources: { enabled: false, default_host: "", max_calls: 0, sources: [] },
      queue: queue({
        current: { ...runItem(run()), since: iso(-1) },
        next: [note(812)],
      }),
      "queue/count": { count: 2, paused: false },
    });
    render(<App />);
    expect(await screen.findByTestId("queue-count")).toHaveTextContent("2");
    expect(
      await screen.findByRole("heading", { name: S.queue.title }),
    ).toBeInTheDocument();
  });
});
