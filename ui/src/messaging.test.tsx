import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RepoDetail, RepoTopic, TopicFlow } from "./api";
import { ProjectMessages, flowGraph, topicGraph } from "./Messaging";
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
  window.history.replaceState({}, "", "/");
  // jsdom has no scrollIntoView; a test that stubs one leaves none.
  delete (Element.prototype as Partial<Element>).scrollIntoView;
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
    expect(ir.zones.find((z) => z.id === "in")?.title).toBe("Consumed");
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
  stale: null,
  notes_total: 0,
  last_merge: null,
  revisions: [],
  notes: [],
  map: null,
  map_declined: false,
  uses: [],
  used_by: [],
  interfaces: [],
  parts: { units: [], edges: [], graph: { nodes: [], edges: [] } },
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
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    const desc = screen.getByRole("tabpanel", { name: "Nachschlagen" });
    const sent = within(desc).getByText("order.placed").closest("tr")!;
    expect(within(sent).getByText("Producer")).toBeInTheDocument();
    // Each peer to its own messages: its start tab shows none.
    expect(
      within(sent).getByRole("link", { name: "billing" }).getAttribute("href"),
    ).toBe("/projects/shop/repos/billing#description/messages");
    expect(
      within(sent)
        .getByRole("link", { name: "ops/audit" })
        .getAttribute("href"),
    ).toBe("/projects/ops/repos/audit#description/messages");
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
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    const desc = screen.getByRole("tabpanel", { name: "Nachschlagen" });
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
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    expect(screen.queryByText("Messaging")).toBeNull();
  });

  it("links each repository of its drawing to its messages", async () => {
    serve({ "projects/shop/repos/orders": repo });
    render(<RepoPage projectKey="shop" slug="orders" />);
    fireEvent.click(await screen.findByRole("tab", { name: /Nachschlagen/ }));
    const node = (slug: string) =>
      screen.findByRole(
        "link",
        { name: `Repository ${slug} öffnen` },
        { timeout: 5000 },
      );
    expect((await node("ops/audit")).getAttribute("href")).toBe(
      "/projects/ops/repos/audit#description/messages",
    );
    // billing both sends and receives: two nodes, one target.
    expect(
      screen
        .getAllByRole("link", { name: "Repository billing öffnen" })
        .map((a) => a.getAttribute("href")),
    ).toEqual([
      "/projects/shop/repos/billing#description/messages",
      "/projects/shop/repos/billing#description/messages",
    ]);
    // Its own node: the section it is in, no link.
    expect(
      screen.queryByRole("link", { name: "Repository orders öffnen" }),
    ).toBeNull();
  });

  it("opens on its messages from a link to them, without a description", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    window.history.replaceState(
      {},
      "",
      "/projects/shop/repos/orders#description/messages",
    );
    serve({ "projects/shop/repos/orders": repo });
    render(<RepoPage projectKey="shop" slug="orders" />);
    expect(
      await screen.findByRole("tab", { name: /Nachschlagen/ }),
    ).toHaveAttribute("aria-selected", "true");
    await waitFor(() =>
      expect(scrolled.mock.contexts).toContain(
        document.getElementById("messages"),
      ),
    );
  });

  it("opens the reference tab on a link to a section it does not have", async () => {
    const scrolled = vi.fn();
    Element.prototype.scrollIntoView = scrolled;
    window.history.replaceState(
      {},
      "",
      "/projects/shop/repos/orders#description/interfaces",
    );
    serve({ "projects/shop/repos/orders": repo });
    render(<RepoPage projectKey="shop" slug="orders" />);
    const desc = await screen.findByRole("tabpanel", { name: "Nachschlagen" });
    expect(within(desc).getByText("order.placed")).toBeInTheDocument();
    expect(scrolled).not.toHaveBeenCalled();
  });
});

describe("ProjectMessages", () => {
  it("links each sender and receiver to its messages", () => {
    const flows: TopicFlow[] = [
      {
        name: "order.placed",
        producers: [ref("shop", "orders")],
        consumers: [ref("ops", "audit")],
        declared: [],
      },
    ];
    render(<ProjectMessages project="shop" flows={flows} />);
    expect(
      screen.getByRole("link", { name: "orders" }).getAttribute("href"),
    ).toBe("/projects/shop/repos/orders#description/messages");
    expect(
      screen.getByRole("link", { name: "ops/audit" }).getAttribute("href"),
    ).toBe("/projects/ops/repos/audit#description/messages");
  });

  it("links each repository of its drawing to its messages", async () => {
    const flows: TopicFlow[] = [
      {
        name: "order.placed",
        producers: [ref("shop", "orders")],
        consumers: [ref("ops", "audit")],
        declared: [],
      },
    ];
    render(<ProjectMessages project="shop" flows={flows} />);
    const node = (slug: string) =>
      screen.findByRole(
        "link",
        { name: `Repository ${slug} öffnen` },
        { timeout: 5000 },
      );
    expect((await node("orders")).getAttribute("href")).toBe(
      "/projects/shop/repos/orders#description/messages",
    );
    expect((await node("ops/audit")).getAttribute("href")).toBe(
      "/projects/ops/repos/audit#description/messages",
    );
  });
});
