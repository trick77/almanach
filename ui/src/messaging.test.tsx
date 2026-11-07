import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RepoDetail, RepoTopic, TopicFlow } from "./api";
import { flowGraph, topicGraph } from "./Messaging";
import { RepoPage } from "./RepoPage";

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
});

const ref = (project: string, slug: string) => ({ project, slug });
const name = (r: { project: string; slug: string }) =>
  r.project === "shop" ? r.slug : `${r.project}/${r.slug}`;

const topics: RepoTopic[] = [
  {
    name: "order.placed",
    direction: "produces",
    files: ["a"],
    peers: [ref("shop", "billing"), ref("ops", "audit")],
  },
  {
    name: "payment.done",
    direction: "consumes",
    files: ["b"],
    peers: [ref("shop", "billing")],
  },
  { name: "ledger", direction: "declares", files: ["c"], peers: [] },
];

describe("topicGraph", () => {
  it("draws senders, received topics, the repository, sent topics, receivers", () => {
    const ir = topicGraph(ref("shop", "orders"), topics, name);
    const zone = Object.fromEntries(ir.nodes.map((n) => [n.id, n.zone]));
    expect(zone).toEqual({
      "repo:shop/orders": "self",
      "out:order.placed": "out",
      "to:shop/billing": "to",
      "to:ops/audit": "to",
      "in:payment.done": "in",
      "from:shop/billing": "from",
    });
    expect(ir.edges.map((e) => `${e.from}>${e.to}`)).toEqual([
      "repo:shop/orders>out:order.placed",
      "out:order.placed>to:shop/billing",
      "out:order.placed>to:ops/audit",
      "in:payment.done>repo:shop/orders",
      "from:shop/billing>in:payment.done",
    ]);
    // Zones titled for the reader; a named topic is not drawn.
    expect(ir.zones.find((z) => z.id === "in")?.title).toBe("Konsumiert");
    expect(ir.nodes.some((n) => n.label === "ledger")).toBe(false);
    expect(ir.nodes.find((n) => n.id === "to:ops/audit")?.label).toBe(
      "ops/audit",
    );
  });

  it("draws nothing for topics only named", () => {
    const ir = topicGraph(ref("shop", "orders"), [topics[2]], name);
    expect(ir.nodes).toEqual([]);
    expect(ir.edges).toEqual([]);
  });
});

describe("flowGraph", () => {
  it("draws each sender to its topic and the topic to its receivers", () => {
    const flows: TopicFlow[] = [
      {
        name: "order.placed",
        producers: [ref("shop", "orders")],
        consumers: [ref("shop", "billing")],
        declared: [],
      },
      {
        name: "ledger",
        producers: [],
        consumers: [],
        declared: [ref("shop", "billing")],
      },
    ];
    const ir = flowGraph(flows, name);
    expect(ir.nodes.map((n) => n.id)).toEqual([
      "topic:order.placed",
      "repo:shop/orders",
      "repo:shop/billing",
    ]);
    expect(ir.edges.map((e) => `${e.from}>${e.to}`)).toEqual([
      "repo:shop/orders>topic:order.placed",
      "topic:order.placed>repo:shop/billing",
    ]);
  });
});

const repo: RepoDetail = {
  project_key: "shop",
  slug: "orders",
  description: null,
  notes_total: 0,
  last_merge: null,
  revisions: [],
  notes: [],
  map: null,
  map_declined: false,
  uses: [],
  used_by: [],
  messaging: {
    topics,
    schemas: [
      {
        name: "shop.events.OrderPlaced",
        kind: "avro",
        file: "schemas/order.avsc",
      },
    ],
  },
};

describe("RepoPage messages", () => {
  it("lists its topics with the far side, its schemas, and points the rail at them", async () => {
    serve({ "projects/shop/repos/orders": repo });
    render(<RepoPage projectKey="shop" slug="orders" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    const sent = within(desc).getByText("order.placed").closest("tr")!;
    expect(within(sent).getByText("Producer")).toBeInTheDocument();
    expect(
      within(sent).getByRole("link", { name: "billing" }).getAttribute("href"),
    ).toBe("/projects/shop/repos/billing");
    expect(
      within(sent)
        .getByRole("link", { name: "ops/audit" })
        .getAttribute("href"),
    ).toBe("/projects/ops/repos/audit");
    expect(within(sent).getByText("order.placed").getAttribute("title")).toBe(
      "a",
    );
    const named = within(desc).getByText("ledger").closest("tr")!;
    expect(within(named).getByText("Genannt")).toBeInTheDocument();
    expect(within(named).queryByText("Keine unter den Quellen")).toBeNull();
    expect(
      within(desc).getByText("shop.events.OrderPlaced"),
    ).toBeInTheDocument();
    const rail = screen.getByRole("navigation", { name: "Inhalt" });
    const entry = within(rail).getByText("Messaging").closest("a")!;
    expect(entry.getAttribute("href")).toBe("#messages");
    expect(entry.textContent).toContain("3 · 1");
  });

  it("says when no source repository is on the far side", async () => {
    serve({
      "projects/shop/repos/orders": {
        ...repo,
        messaging: { topics: [{ ...topics[1], peers: [] }], schemas: [] },
      },
    });
    render(<RepoPage projectKey="shop" slug="orders" />);
    const desc = await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(
      within(desc).getByText("Keine unter den Quellen"),
    ).toBeInTheDocument();
  });

  it("has no section for a repository without messages", async () => {
    serve({
      "projects/shop/repos/orders": {
        ...repo,
        messaging: { topics: [], schemas: [] },
      },
    });
    render(<RepoPage projectKey="shop" slug="orders" />);
    await screen.findByRole("tabpanel", { name: "Beschreibung" });
    expect(screen.queryByText("Messaging")).toBeNull();
  });
});
