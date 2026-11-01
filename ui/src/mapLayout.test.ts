import { describe, expect, it } from "vitest";
import type { FlowEdgeIR, FlowIR, FlowNodeIR } from "./diagramIR";
import { layoutMap, pageColumn } from "./mapLayout";
import { ramp, textWidth } from "./diagramTheme";
import { check } from "./test/diagramCheck";

const node = (id: string, label: string, zone: string): FlowNodeIR => ({
  id,
  label,
  shape: "step",
  zone,
});
const edge = (from: string, to: string, label = ""): FlowEdgeIR => ({
  from,
  to,
  label,
  dashed: false,
  head: "end",
});
const repoZones = [
  { id: "entry", title: "Eingang" },
  { id: "parts", title: "Teile" },
  { id: "stores", title: "Datenspeicher" },
  { id: "external", title: "Externe Systeme" },
];
const map = (
  nodes: FlowNodeIR[],
  edges: FlowEdgeIR[],
  zones = repoZones,
): FlowIR => ({
  kind: "flow",
  direction: "LR",
  nodes,
  edges,
  zones: zones.filter((z) => nodes.some((n) => n.zone === z.id)),
});

// loom's map as its description names it: the API and the SPA, its ten
// components, SQLite and the user volume, every side service its own box.
const loom = map(
  [
    node("api", "HTTP-API", "entry"),
    node("spa", "Web-Oberfläche (SPA)", "entry"),
    node("jobs", "Auftragsverwaltung", "parts"),
    node("docs", "Dokumentenverarbeitung", "parts"),
    node("convert", "Konvertierung", "parts"),
    node("sandbox", "Sandbox-Steuerung", "parts"),
    node("music", "Musikgenerierung", "parts"),
    node("images", "Bildgenerierung", "parts"),
    node("llm", "Modellanbindung", "parts"),
    node("users", "Benutzerverwaltung", "parts"),
    node("files", "Dateiablage", "parts"),
    node("events", "Ereignisse", "parts"),
    node("sqlite", "SQLite", "stores"),
    node("volume", "Benutzer-Volume", "stores"),
    node("tika", "Tika", "external"),
    node("gotenberg", "Gotenberg", "external"),
    node("obscura", "Obscura", "external"),
    node("lsandbox", "loom-sandbox", "external"),
    node("tunewire", "loom-tunewire", "external"),
    node("fal", "Fal", "external"),
    node("llmwire", "llmwire", "external"),
  ],
  [
    edge("spa", "api", "HTTP"),
    edge("api", "jobs", "call"),
    edge("api", "users", "call"),
    edge("api", "files", "call"),
    edge("jobs", "docs", "call"),
    edge("jobs", "music", "call"),
    edge("jobs", "images", "call"),
    edge("jobs", "sandbox", "call"),
    edge("jobs", "events", "event: Auftrag fertig"),
    edge("docs", "convert", "call"),
    edge("docs", "llm", "call"),
    edge("docs", "tika", "HTTP: Text extrahieren"),
    edge("convert", "gotenberg", "HTTP: PDF erzeugen"),
    edge("docs", "obscura", "HTTP"),
    edge("sandbox", "lsandbox", "HTTP"),
    edge("music", "tunewire", "HTTP"),
    edge("images", "fal", "HTTP: Bild erzeugen"),
    edge("llm", "llmwire", "library"),
    edge("jobs", "sqlite", "DB"),
    edge("users", "sqlite", "DB"),
    edge("files", "volume", "file"),
    edge("events", "spa", "event: Fortschritt"),
  ],
);

// llmwire's: a library entered by its API, its providers outside.
const llmwire = map(
  [
    node("api", "Go-API (Client)", "entry"),
    node("cli", "Kommandozeile", "entry"),
    node("profiles", "Profile", "parts"),
    node("wire", "Anfrage und Antwort", "parts"),
    node("schema", "Strikte Schemas", "parts"),
    node("retry", "Wiederholung", "parts"),
    node("cost", "Kostenrechnung", "parts"),
    node("config", "Konfigurationsdatei", "stores"),
    node("openai", "OpenAI", "external"),
    node("anthropic", "Anthropic", "external"),
    node("gemini", "Gemini", "external"),
    node("mimo", "MiMo", "external"),
    node("gateway", "LLM-Gateway", "external"),
  ],
  [
    edge("api", "profiles", "call"),
    edge("cli", "profiles", "call"),
    edge("api", "wire", "call"),
    edge("profiles", "config", "file"),
    edge("wire", "schema", "call"),
    edge("wire", "retry", "call"),
    edge("wire", "cost", "call"),
    edge("retry", "openai", "HTTP"),
    edge("retry", "anthropic", "HTTP"),
    edge("retry", "gemini", "HTTP"),
    edge("retry", "mimo", "HTTP"),
    edge("retry", "gateway", "HTTP"),
  ],
);

// A chain: every hop one more layer in ELK, one more column across.
const chainParts = Array.from({ length: 12 }, (_, i) =>
  node(`p${i}`, `Schritt ${i + 1}`, "parts"),
);
const chain = map(
  [
    node("in", "Webhook", "entry"),
    ...chainParts,
    node("db", "PostgreSQL", "stores"),
    node("bus", "Kafka", "external"),
  ],
  [
    edge("in", "p0", "HTTP"),
    ...chainParts
      .slice(1)
      .map((p, i) =>
        edge(chainParts[i].id, p.id, i % 3 === 0 ? "event: weiter" : "call"),
      ),
    edge("p11", "db", "DB"),
    edge("p11", "bus", "event: fertig"),
  ],
);

// A fan: one part talks to every system outside.
const outside = Array.from({ length: 18 }, (_, i) =>
  node(`x${i}`, `Dienst ${i + 1}`, "external"),
);
const fan = map(
  [node("api", "HTTP-API", "entry"), node("core", "Kern", "parts"), ...outside],
  [
    edge("api", "core", "call"),
    ...outside.map((x, i) =>
      edge("core", x.id, i % 2 ? "HTTP" : "event: Auftrag"),
    ),
  ],
);

// As much as a map may hold: 22 nodes, 33 edges.
const full = map(
  [
    node("api", "HTTP-API", "entry"),
    node("ui", "Oberfläche", "entry"),
    node("cron", "Zeitplan", "entry"),
    ...Array.from({ length: 11 }, (_, i) =>
      node(`m${i}`, `Modul ${i + 1}`, "parts"),
    ),
    node("pg", "PostgreSQL", "stores"),
    node("redis", "Redis", "stores"),
    node("s3", "Objektspeicher", "stores"),
    ...Array.from({ length: 5 }, (_, i) =>
      node(`e${i}`, `Partner ${i + 1}`, "external"),
    ),
  ],
  [
    edge("ui", "api", "HTTP"),
    ...Array.from({ length: 11 }, (_, i) =>
      edge(i % 2 ? "cron" : "api", `m${i}`, "call"),
    ),
    ...Array.from({ length: 10 }, (_, i) => edge(`m${i}`, `m${i + 1}`)),
    ...Array.from({ length: 6 }, (_, i) =>
      edge(`m${i * 2}`, ["pg", "redis", "s3"][i % 3], "DB"),
    ),
    ...Array.from({ length: 5 }, (_, i) => edge(`m${i + 5}`, `e${i}`, "HTTP")),
  ],
);

// The system map: callers, repositories, stores, outside, and the shared
// repositories the UI adds.
const system = map(
  [
    node("browser", "Browser", "clients"),
    node("cli", "Kommandozeile", "clients"),
    ...["almanach", "noergler", "rongo", "ship", "kit", "llmwire"].map((s) =>
      node(s, s, "repositories"),
    ),
    node("sqlite", "SQLite", "stores"),
    node("forge", "Bitbucket", "external"),
    node("gw", "LLM-Gateway", "external"),
    node("oidc", "OIDC-Anbieter", "external"),
    { ...node("shared:x/lib", "x/lib", "shared"), shape: "pill" },
  ],
  [
    edge("browser", "almanach", "HTTP"),
    edge("cli", "ship", "call"),
    edge("noergler", "almanach", "HTTP: Webhook"),
    edge("almanach", "sqlite", "DB"),
    edge("almanach", "forge", "HTTP: git"),
    edge("almanach", "gw", "HTTP"),
    edge("noergler", "gw", "HTTP"),
    edge("almanach", "oidc", "HTTP"),
    edge("almanach", "rongo", "library"),
    edge("almanach", "kit", "library"),
    edge("ship", "kit", "library"),
    edge("almanach", "llmwire", "library"),
    edge("noergler", "shared:x/lib", "library"),
  ],
  [
    { id: "clients", title: "Aufrufer" },
    { id: "repositories", title: "Repositories" },
    { id: "stores", title: "Datenspeicher" },
    { id: "external", title: "Externe Systeme" },
    { id: "shared", title: "Gemeinsame Repositories" },
  ],
);

const small = map(
  [
    node("api", "HTTP-API", "entry"),
    node("billing", "Abrechnung", "parts"),
    node("db", "Datenbank", "stores"),
  ],
  [edge("api", "billing", "call"), edge("billing", "db", "DB")],
);

const fixtures: Record<string, FlowIR> = {
  loom,
  llmwire,
  chain,
  fan,
  full,
  system,
  small,
};

describe("layoutMap", () => {
  // The gate: every map, up to the cap of 22 nodes, fits the page column
  // unscaled and is at most twice as wide as it is tall. Red = not done.
  for (const [name, ir] of Object.entries(fixtures)) {
    it(`${name}: fits the column, at most 2:1`, () => {
      const l = layoutMap(ir);
      console.log(
        `${name}: ${ir.nodes.length} nodes, ${l.width} x ${l.height}, ${(l.width / l.height).toFixed(2)}:1`,
      );
      expect(l.width).toBeLessThanOrEqual(pageColumn);
      expect(l.width / l.height).toBeLessThanOrEqual(2);
      // Every arrow square, clear of the boxes it does not join, none on
      // another; labels clear of lines and boxes. A map may cross.
      expect(check(l, { written: true })).toEqual([]);
      // No line through a zone's title.
      for (const z of l.zones) {
        const x0 = z.x + 18;
        const x1 = x0 + textWidth(z.title, ramp.bold, "sans");
        const struck = l.edges.filter((e) =>
          e.points.slice(1).some((p, k) => {
            const q = e.points[k];
            return (
              p.x === q.x &&
              p.x >= x0 &&
              p.x <= x1 &&
              Math.min(p.y, q.y) < z.y + 32 &&
              Math.max(p.y, q.y) > z.y + 12
            );
          }),
        );
        expect(struck.map((e) => `${e.from}->${e.to} over ${z.id}`)).toEqual(
          [],
        );
      }
    });
  }

  it("stacks the zones in the order a request travels", () => {
    const l = layoutMap(loom);
    const y = (id: string) => l.zones.find((z) => z.id === id)!.y;
    expect(y("entry")).toBeLessThan(y("parts"));
    expect(y("parts")).toBeLessThan(y("stores"));
    expect(y("stores")).toBeLessThan(y("external"));
    const s = layoutMap(system);
    const ys = (id: string) => s.zones.find((z) => z.id === id)!.y;
    expect(ys("clients")).toBeLessThan(ys("repositories"));
    expect(ys("repositories")).toBeLessThan(ys("shared"));
    expect(ys("shared")).toBeLessThan(ys("stores"));
  });

  it("wraps a wide zone into rows, every node its own box", () => {
    const l = layoutMap(loom);
    const ext = l.nodes.filter(
      (n) => loom.nodes.find((m) => m.id === n.id)!.zone === "external",
    );
    expect(ext).toHaveLength(7);
    expect(new Set(ext.map((n) => n.y)).size).toBeGreaterThan(1);
    const perRow = Math.max(
      ...[...new Set(l.nodes.map((n) => n.y))].map(
        (y) => l.nodes.filter((n) => n.y === y).length,
      ),
    );
    expect(perRow).toBeLessThanOrEqual(5);
  });

  it("is the same map every time", () => {
    expect(layoutMap(loom)).toEqual(layoutMap(loom));
  });
});
