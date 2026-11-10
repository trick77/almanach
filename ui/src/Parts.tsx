import type { Part, Parts } from "./api";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { S } from "./strings";
import { eyebrow, mono, table, tableBox, td, th } from "./ui";

/** reduced is edges without those another path already says: a→c goes
 *  when a→b→c stands. Taken over the graph's strongly connected
 *  components, so a cycle keeps every edge inside it and an edge between
 *  two components goes only when another path joins them. In the given
 *  order. */
export function reduced(
  edges: { from: string; to: string }[],
): { from: string; to: string }[] {
  const nodes = [...new Set(edges.flatMap((e) => [e.from, e.to]))];
  const out = new Map<string, string[]>();
  for (const e of edges) {
    if (!out.has(e.from)) out.set(e.from, []);
    out.get(e.from)!.push(e.to);
  }
  // Tarjan's components: comp maps a node to its component's index.
  const comp = new Map<string, number>();
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const stack: string[] = [];
  const on = new Set<string>();
  let next = 0;
  let count = 0;
  const visit = (v: string) => {
    index.set(v, next);
    low.set(v, next);
    next++;
    stack.push(v);
    on.add(v);
    for (const w of out.get(v) ?? []) {
      if (!index.has(w)) {
        visit(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (on.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      for (;;) {
        const w = stack.pop()!;
        on.delete(w);
        comp.set(w, count);
        if (w === v) break;
      }
      count++;
    }
  };
  for (const v of nodes) if (!index.has(v)) visit(v);
  // The components' graph, and what each reaches in it.
  const cout = new Map<number, Set<number>>();
  for (const e of edges) {
    const a = comp.get(e.from)!;
    const b = comp.get(e.to)!;
    if (a === b) continue;
    if (!cout.has(a)) cout.set(a, new Set());
    cout.get(a)!.add(b);
  }
  // Is b reached from a by a path of two steps or more?
  const longer = (a: number, b: number): boolean => {
    const seen = new Set<number>();
    const todo = [...(cout.get(a) ?? [])].filter((x) => x !== b);
    while (todo.length > 0) {
      const x = todo.pop()!;
      if (x === b) return true;
      if (seen.has(x)) continue;
      seen.add(x);
      todo.push(...(cout.get(x) ?? []));
    }
    return false;
  };
  // Two edges joining the same two components say one thing: the first
  // stays (a→b, a→c with b and c in a cycle: a→c is a→b→c).
  const joined = new Set<string>();
  return edges.filter((e) => {
    const a = comp.get(e.from)!;
    const b = comp.get(e.to)!;
    if (a === b) return true;
    const k = `${a} ${b}`;
    if (joined.has(k) || longer(a, b)) return false;
    joined.add(k);
    return true;
  });
}

/** partLabel names each part: its name, its folder where two share one. */
export function partLabel(parts: Part[]): (p: Part) => string {
  const n = new Map<string, number>();
  for (const p of parts) n.set(p.name, (n.get(p.name) ?? 0) + 1);
  return (p) => ((n.get(p.name) ?? 0) > 1 ? p.dir : p.name);
}

/** partsGraph is the parts the graph draws as a flow, top to bottom: the
 *  apps in a zone of their own above the rest, when there are any; an
 *  edge per use, reduced. Built by code, not a model's: no validation to
 *  pass, every label escaped by the drawing as any other. */
export function partsGraph(p: Parts): FlowIR {
  const label = partLabel(p.units);
  const byDir = new Map(p.units.map((u) => [u.dir, u]));
  const drawn = p.graph.nodes
    .map((d) => byDir.get(d))
    .filter((u): u is Part => u !== undefined);
  const apps = drawn.some((u) => u.kind === "app");
  return {
    kind: "flow",
    direction: "TB",
    zones: apps
      ? [
          { id: "apps", title: "apps" },
          { id: "parts", title: "parts" },
        ]
      : [],
    nodes: drawn.map((u) => ({
      id: u.dir,
      label: label(u),
      shape: u.kind === "app" ? "step" : "pill",
      ...(apps ? { zone: u.kind === "app" ? "apps" : "parts" } : {}),
    })),
    edges: reduced(p.graph.edges).map((e) => ({
      from: e.from,
      to: e.to,
      label: "",
      dashed: false,
      head: "end",
    })),
  };
}

/** RepoParts is the parts inside a repository: the graph when it fits,
 *  and a table of every part, what it uses and what uses it. */
export function RepoParts({ parts }: { parts: Parts }) {
  const ir = titledZones(partsGraph(parts), S.diagram.partZones);
  const label = partLabel(parts.units);
  const byDir = new Map(parts.units.map((u) => [u.dir, u]));
  const names = (dirs: string[]) =>
    dirs
      .map((d) => byDir.get(d))
      .filter((u): u is Part => u !== undefined)
      .map(label)
      .join(", ");
  const [h0, h1, h2] = S.parts.headers;
  return (
    <div className="grid gap-y-3">
      <p className="text-[13.5px] text-faint">{S.parts.hint}</p>
      {ir.nodes.length === 0 ? null : fits(ir) ? (
        <>
          <div className={eyebrow}>{S.parts.graph}</div>
          <Diagram ir={ir} title={S.parts.title} />
        </>
      ) : (
        <p className="text-[13.5px] text-faint">
          {S.parts.tooLarge(ir.nodes.length)}
        </p>
      )}
      <div className={tableBox}>
        <table className={table}>
          <thead>
            <tr>
              <th className={th}>{h0}</th>
              <th className={th}>{h1}</th>
              <th className={th}>{h2}</th>
            </tr>
          </thead>
          <tbody>
            {parts.units.map((u) => (
              <tr key={u.dir}>
                <td className={td}>
                  <span className={mono}>{label(u)}</span>
                  {u.kind !== "" && (
                    <span className="ml-2 text-[12.5px] text-faint">
                      {S.parts.kinds[u.kind]}
                    </span>
                  )}
                  {u.dir !== label(u) && (
                    <span className="ml-2 text-[12.5px] text-faint">
                      {u.dir}
                    </span>
                  )}
                </td>
                <td className={td}>
                  {names(
                    parts.edges
                      .filter((e) => e.from === u.dir)
                      .map((e) => e.to),
                  )}
                </td>
                <td className={td}>
                  {names(
                    parts.edges
                      .filter((e) => e.to === u.dir)
                      .map((e) => e.from),
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
