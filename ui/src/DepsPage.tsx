import { Fragment, useEffect } from "react";
import type { Dependencies, RepoRef, Sources } from "./api";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { factPath } from "./repoFacts";
import { repoPath } from "./routing";
import { refName } from "./format";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Empty,
  Failed,
  Link,
  Pill,
  Skeleton,
  column,
  eyebrow,
  h2,
  lede,
  mono,
  page,
  table,
  tableBox,
  td,
  tdMono,
  th,
} from "./ui";

const refId = (r: RepoRef) => `${r.project}/${r.slug}`;

/** repoName is a repository as the page names it: its slug when every
 *  repository of the graph is one project's, else project/slug. */
export function repoNamer(d: Dependencies): (r: RepoRef) => string {
  const one = new Set(d.repos.map((r) => r.project)).size <= 1;
  return (r) => (one ? r.slug : refName(r.project, r.slug));
}

type DepEdge = Dependencies["edges"][number];

/** isMain reports whether an edge is a use at runtime. */
export const isMain = (e: DepEdge) => e.scope === "main";

/** depGraph is the graph as a flow, left to right: a repository something
 *  is used stands among the shared ones, the rest among the users; an edge per
 *  use, dashed and labelled with what it is for when it is not a use at
 *  runtime (mainOnly: those left out). No version: the relation is what
 *  counts (user, 2026-11-09). Only repositories with
 *  an edge are drawn. Built by code from the graph the store joined, not
 *  a model's: no validation to pass, every label escaped by the drawing
 *  as any other. */
export function depGraph(d: Dependencies, mainOnly = false): FlowIR {
  const name = repoNamer(d);
  const edges = mainOnly ? d.edges.filter(isMain) : d.edges;
  const used = new Set(edges.map((e) => refId(e.to)));
  const nodes = new Map<string, RepoRef>();
  for (const e of edges) {
    nodes.set(refId(e.from), e.from);
    nodes.set(refId(e.to), e.to);
  }
  return {
    kind: "flow",
    direction: "LR",
    zones: [
      { id: "users", title: "users" },
      { id: "shared", title: "shared" },
    ],
    nodes: [...nodes.entries()].map(([id, r]) => ({
      id,
      label: name(r),
      shape: "pill",
      zone: used.has(id) ? "shared" : "users",
    })),
    edges: edges.map((e) => ({
      from: refId(e.from),
      to: refId(e.to),
      label: S.deps.scopes[e.scope] ?? "",
      dashed: !isMain(e),
      head: "end",
    })),
  };
}

/** SharedRow is one row of the table: a used repository and its users, each
 *  with what it uses it for. */
interface SharedRow {
  lib: RepoRef;
  users: { ref: RepoRef; scope: DepEdge["scope"] }[];
}

/** sharedRows is every used repository, in the graph's order. */
export function sharedRows(d: Dependencies): SharedRow[] {
  const rows = new Map<string, SharedRow>();
  for (const e of d.edges) {
    const id = refId(e.to);
    if (!rows.has(id)) rows.set(id, { lib: e.to, users: [] });
    rows.get(id)!.users.push({ ref: e.from, scope: e.scope });
  }
  return [...rows.values()];
}

/** DepsPage is the dependency graph over the source repositories. */
export function DepsPage() {
  const { data, failed } = useFetch<Dependencies>("dependencies");
  const { data: sources, failed: sourcesFailed } = useFetch<Sources>("sources");
  useEffect(() => {
    document.title = `${S.deps.title} ${S.common.sep} ${S.app}`;
  }, []);

  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.deps.title}</h2>
        <p className={lede}>{S.deps.lede}</p>
        {failed || sourcesFailed ? (
          <Failed message={S.deps.failed} />
        ) : data === null || sources === null ? (
          <Skeleton />
        ) : !sources.enabled ? (
          <Empty>{S.deps.disabled}</Empty>
        ) : data.edges.length === 0 && data.ambiguous.length === 0 ? (
          <Empty>{S.deps.empty}</Empty>
        ) : (
          <DepsBody data={data} />
        )}
      </div>
    </div>
  );
}

/** drawnGraph is the graph the page draws: every use when it fits, else
 *  the uses at runtime alone when those fit (mainOnly), else none. */
export function drawnGraph(data: Dependencies): {
  ir: FlowIR;
  draw: boolean;
  mainOnly: boolean;
} {
  const all = titledZones(depGraph(data), S.diagram.depZones);
  if (fits(all) || data.edges.every(isMain)) {
    return { ir: all, draw: fits(all), mainOnly: false };
  }
  const main = titledZones(depGraph(data, true), S.diagram.depZones);
  if (main.edges.length > 0 && fits(main)) {
    return { ir: main, draw: true, mainOnly: true };
  }
  return { ir: all, draw: false, mainOnly: false };
}

function DepsBody({ data }: { data: Dependencies }) {
  const name = repoNamer(data);
  const { ir, draw, mainOnly } = drawnGraph(data);
  const rows = sharedRows(data);
  const [hd0, hd1] = S.deps.headers;
  const note = "my-2 text-[13.5px] text-faint";
  return (
    <>
      {data.edges.length > 0 && (
        <>
          <div className={eyebrow}>{S.deps.graph}</div>
          {draw ? (
            <>
              <Diagram
                ir={ir}
                title={S.deps.title}
                links={(id) => {
                  const r = data.repos.find((x) => refId(x) === id);
                  return r ? repoPath(r.project, r.slug) : null;
                }}
              />
              {mainOnly ? (
                <p className={note}>{S.deps.mainOnly}</p>
              ) : (
                ir.edges.some((e) => e.dashed) && (
                  <p className={note}>{S.deps.dashed}</p>
                )
              )}
            </>
          ) : (
            <p className={note}>{S.deps.tooLarge(ir.nodes.length)}</p>
          )}
          <div className={eyebrow}>{S.deps.shared}</div>
          <div className={tableBox}>
            <table className={table}>
              <thead>
                <tr>
                  <th className={th}>{hd0}</th>
                  <th className={th}>{hd1}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) =>
                  row.users.map((u, i) => (
                    <tr key={refId(row.lib) + " " + refId(u.ref)}>
                      {i === 0 && (
                        <td className={tdMono} rowSpan={row.users.length}>
                          <RepoLink r={row.lib} name={name} />
                        </td>
                      )}
                      <td className={tdMono}>
                        <RepoLink r={u.ref} name={name} />
                        {u.scope !== "main" && (
                          // Neutral: a test use is no warning. -my keeps
                          // the row as tall as the rest.
                          <Pill tone="none" className="ml-2 -my-1">
                            {S.deps.scopes[u.scope]}
                          </Pill>
                        )}
                      </td>
                    </tr>
                  )),
                )}
              </tbody>
            </table>
          </div>
        </>
      )}
      {data.ambiguous.length > 0 && (
        <>
          <div className={eyebrow}>{S.deps.ambiguous}</div>
          <p className="mb-3 text-[13.5px] text-muted">
            {S.deps.ambiguousHint}
          </p>
          <ul className="space-y-1">
            {data.ambiguous.map((a) => (
              <li key={a.coord} className={td + " px-0"}>
                <code className={mono}>{a.coord}</code>{" "}
                <span className="text-faint">{S.common.sep}</span>{" "}
                {a.providers.map((p, i) => (
                  <Fragment key={refId(p)}>
                    {i > 0 && ", "}
                    <RepoLink r={p} name={name} />
                  </Fragment>
                ))}
                {a.cloned_twice && (
                  <span className="ml-2 text-ochre">{S.deps.clonedTwice}</span>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

function RepoLink({ r, name }: { r: RepoRef; name: (r: RepoRef) => string }) {
  return (
    <Link
      to={factPath(r.project, r.slug, "deps")}
      className={mono + " text-ink hover:text-accent-strong"}
    >
      {name(r)}
    </Link>
  );
}
