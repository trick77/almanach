import { Fragment, useEffect } from "react";
import type { Dependencies, RepoRef, Sources } from "./api";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
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

/** versionMax is the most of a version an edge label shows: a git URL or a
 *  commit hash as a version would stretch the drawing; the table shows it
 *  whole. */
const versionMax = 16;

/** shortVersion is a version as an edge label: one line, cut at
 *  versionMax with an ellipsis. */
export function shortVersion(v: string): string {
  const one = v.split(/\s+/).join(" ").trim();
  return one.length <= versionMax ? one : one.slice(0, versionMax - 1) + "…";
}

type DepEdge = Dependencies["edges"][number];

/** isMain reports whether an edge is a use at runtime. */
export const isMain = (e: DepEdge) => e.scope === "main";

/** edgeLabel is an edge's label: the version the user names, and for a use
 *  other than at runtime what it is for. */
export function edgeLabel(e: DepEdge): string {
  const v = shortVersion(e.version);
  const what = S.deps.scopes[e.scope] ?? "";
  return [what, v].filter((s) => s !== "").join(` ${S.common.sep} `);
}

/** depGraph is the graph as a flow, left to right: a repository something
 *  is used stands among the shared ones, the rest among the users; an edge per
 *  use, labelled with the version the user names, dashed when it is not a
 *  use at runtime (mainOnly: those left out). Only repositories with
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
      label: edgeLabel(e),
      dashed: !isMain(e),
      head: "end",
    })),
  };
}

/** SharedRow is one row of the table: a used repository and its users, each
 *  with the version it names and what for; odd marks a version the others
 *  do not name. */
interface SharedRow {
  lib: RepoRef;
  users: {
    ref: RepoRef;
    version: string;
    scope: DepEdge["scope"];
    odd: boolean;
  }[];
}

/** sharedRows is every used repository, in the graph's order. */
export function sharedRows(d: Dependencies): SharedRow[] {
  const rows = new Map<string, SharedRow>();
  for (const e of d.edges) {
    const id = refId(e.to);
    if (!rows.has(id)) rows.set(id, { lib: e.to, users: [] });
    rows.get(id)!.users.push({
      ref: e.from,
      version: e.version,
      scope: e.scope,
      odd: false,
    });
  }
  for (const row of rows.values()) {
    const odd = oddVersions(row.users.map((u) => u.version));
    row.users.forEach((u, i) => (u.odd = odd[i]));
  }
  return [...rows.values()];
}

/** oddVersions marks, among the versions one repository's users name, those
 *  apart from the rest: all the same, none; otherwise every version fewer
 *  users name than the most common one, and on a tie for most common,
 *  every one, since none is the rule. A user naming no version ("", a
 *  property the manifest leaves open) neither counts nor is marked. */
export function oddVersions(versions: string[]): boolean[] {
  const count = new Map<string, number>();
  for (const v of versions) if (v !== "") count.set(v, (count.get(v) ?? 0) + 1);
  if (count.size <= 1) return versions.map(() => false);
  const most = Math.max(...count.values());
  const tie = [...count.values()].filter((n) => n === most).length > 1;
  return versions.map((v) => v !== "" && (tie || count.get(v)! < most));
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
  const [hd0, hd1, hd2] = S.deps.headers;
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
                  <th className={th}>{hd2}</th>
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
                      </td>
                      <td className={tdMono}>
                        <span
                          className={u.odd ? "text-ochre" : undefined}
                          title={u.odd ? S.deps.otherVersion : undefined}
                        >
                          {u.version || S.deps.noVersion}
                        </span>
                        {u.scope !== "main" && (
                          // Neutral: ochre in this cell marks a version
                          // apart from the others, and a test use is no
                          // warning. -my keeps the row as tall as the rest.
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
      to={repoPath(r.project, r.slug)}
      className={mono + " text-ink hover:text-accent-strong"}
    >
      {name(r)}
    </Link>
  );
}
