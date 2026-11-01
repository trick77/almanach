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
  return (r) => (one ? r.slug : refId(r));
}

/** depGraph is the graph as a flow, left to right: a repository something
 *  uses stands among the libraries, the rest among the users; an edge per
 *  use, labelled with the version the user names. Only repositories with
 *  an edge are drawn. */
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
      { id: "libraries", title: "libraries" },
    ],
    nodes: [...nodes.entries()].map(([id, r]) => ({
      id,
      label: name(r),
      shape: "pill",
      zone: used.has(id) ? "libraries" : "users",
    })),
    edges: d.edges.map((e) => ({
      from: refId(e.from),
      to: refId(e.to),
      label: e.version,
      dashed: false,
      head: "end",
    })),
  };
}

/** Library is one row of the table: a used repository and its users, each
 *  with the version it names; odd marks a version the others do not name. */
interface LibraryRow {
  lib: RepoRef;
  users: { ref: RepoRef; version: string; odd: boolean }[];
}

/** libraryRows is every used repository, in the graph's order. */
export function libraryRows(d: Dependencies): LibraryRow[] {
  const rows = new Map<string, LibraryRow>();
  for (const e of d.edges) {
    const id = refId(e.to);
    if (!rows.has(id)) rows.set(id, { lib: e.to, users: [] });
    rows.get(id)!.users.push({ ref: e.from, version: e.version, odd: false });
  }
  for (const row of rows.values()) {
    for (const u of row.users)
      u.odd = row.users.some((o) => o !== u && o.version !== u.version);
  }
  return [...rows.values()];
}

/** DepsPage is the dependency graph over the source repositories. */
export function DepsPage() {
  const { data, failed } = useFetch<Dependencies>("dependencies");
  const { data: sources } = useFetch<Sources>("sources");
  useEffect(() => {
    document.title = `${S.deps.title} ${S.common.sep} ${S.app}`;
  }, []);

  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.deps.title}</h2>
        <p className={lede}>{S.deps.lede}</p>
        {failed ? (
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
  const rows = libraryRows(data);
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
          <div className={eyebrow}>{S.deps.libraries}</div>
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
