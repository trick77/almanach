import { Fragment, useEffect } from "react";
import type { Dependencies, RepoRef, Sources } from "./api";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { repoPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Empty,
  Failed,
  Link,
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
  // A project of one is named once: llmwire, not llmwire/llmwire.
  return (r) => (one || r.project === r.slug ? r.slug : refId(r));
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

/** depGraph is the graph as a flow, left to right: a repository something
 *  is used stands among the shared ones, the rest among the users; an edge per
 *  use, labelled with the version the user names. Only repositories with
 *  an edge are drawn. Built by code from the graph the store joined, not
 *  a model's: no validation to pass, every label escaped by the drawing
 *  as any other. */
export function depGraph(d: Dependencies): FlowIR {
  const name = repoNamer(d);
  const used = new Set(d.edges.map((e) => refId(e.to)));
  const nodes = new Map<string, RepoRef>();
  for (const e of d.edges) {
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
    edges: d.edges.map((e) => ({
      from: refId(e.from),
      to: refId(e.to),
      label: shortVersion(e.version),
      dashed: false,
      head: "end",
    })),
  };
}

/** SharedRow is one row of the table: a used repository and its users, each
 *  with the version it names; odd marks a version the others do not name. */
interface SharedRow {
  lib: RepoRef;
  users: { ref: RepoRef; version: string; odd: boolean }[];
}

/** sharedRows is every used repository, in the graph's order. */
export function sharedRows(d: Dependencies): SharedRow[] {
  const rows = new Map<string, SharedRow>();
  for (const e of d.edges) {
    const id = refId(e.to);
    if (!rows.has(id)) rows.set(id, { lib: e.to, users: [] });
    rows.get(id)!.users.push({ ref: e.from, version: e.version, odd: false });
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
          <Failed what={S.deps.what} />
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

function DepsBody({ data }: { data: Dependencies }) {
  const name = repoNamer(data);
  const ir = titledZones(depGraph(data), S.diagram.depZones);
  const rows = sharedRows(data);
  const [hd0, hd1, hd2] = S.deps.headers;
  return (
    <>
      {data.edges.length > 0 && (
        <>
          <div className={eyebrow}>{S.deps.graph}</div>
          {fits(ir) ? (
            <Diagram
              ir={ir}
              title={S.deps.title}
              links={(id) => {
                const r = data.repos.find((x) => refId(x) === id);
                return r ? repoPath(r.project, r.slug) : null;
              }}
            />
          ) : (
            <p className="my-2 text-[13.5px] text-faint">
              {S.deps.tooLarge(ir.nodes.length)}
            </p>
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
                      <td
                        className={tdMono + (u.odd ? " text-ochre" : "")}
                        title={u.odd ? S.deps.otherVersion : undefined}
                      >
                        {u.version || S.deps.noVersion}
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
