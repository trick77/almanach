import { Fragment, useId, useMemo, useState, type ReactNode } from "react";
import type { Dependencies, Project, Projects } from "./api";
import { day, refName } from "./format";
import { useHashTab } from "./Rail";
import { projectPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Chip,
  Empty,
  Failed,
  Link,
  Segmented,
  Sep,
  Skeleton,
  Teaser,
  column,
  eyebrowBase,
  teaserLines,
  h2,
  lede,
  page,
  rowLink,
  rowOpens,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
} from "./ui";

const tile =
  "block rounded-ui border border-border bg-panel px-4.5 py-4 text-ink-dim hover:border-elevated-border hover:bg-active";
const grid = "grid gap-3.5 sm:grid-cols-2";

/** The page's two views by their hash: the tiles first, no hash; the
 *  table of figures at #table. */
const TILES = "tiles";
const TABLE = "table";
const VIEWS = [TILES, TABLE];

/** Links is what a project's repositories have to do with other projects'
 *  by the manifests: the repositories they depend on, named project/slug,
 *  and how many repositories elsewhere depend on theirs. */
export interface Links {
  uses: string[];
  usedBy: number;
}

/** linksOf is every project's links, by key: uses at runtime only, as the
 *  maps count them. */
export function linksOf(d: Dependencies | null): Map<string, Links> {
  const out = new Map<string, Links>();
  const at = (key: string) => {
    let l = out.get(key);
    if (!l) {
      l = { uses: [], usedBy: 0 };
      out.set(key, l);
    }
    return l;
  };
  const users = new Map<string, Set<string>>();
  for (const e of d?.edges ?? []) {
    if (e.from.project === e.to.project || e.scope !== "main") continue;
    const name = refName(e.to.project, e.to.slug);
    const from = at(e.from.project);
    if (!from.uses.includes(name)) from.uses.push(name);
    const set = users.get(e.to.project) ?? new Set<string>();
    set.add(`${e.from.project}/${e.from.slug}`);
    users.set(e.to.project, set);
  }
  for (const [key, set] of users) at(key).usedBy = set.size;
  return out;
}

/** ProjectsPage is the entry: the systems first as tiles, the other
 *  projects compact under them, or one grid of tiles while no project is
 *  judged a system; the figures in a table, the other view. A project of
 *  one (a source with one repository) shows its repository's
 *  description; its page is the repository's. */
export function ProjectsPage() {
  const { data, failed } = useFetch<Projects>("projects");
  const { tab: view, setTab: setView } = useHashTab(VIEWS, TILES);
  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.projects.title}</h2>
        <p className={lede}>{S.projects.lede}</p>
        {failed ? (
          <Failed message={S.projects.failed} />
        ) : data === null ? (
          <Skeleton />
        ) : data.projects.length === 0 ? (
          <Empty>{S.projects.empty}</Empty>
        ) : (
          <>
            <div
              role="group"
              aria-label={S.projects.view}
              className="mb-4 flex justify-end"
            >
              <Segmented
                value={view}
                options={[
                  { value: TILES, label: S.projects.tiles },
                  { value: TABLE, label: S.projects.table },
                ]}
                onChange={setView}
              />
            </div>
            {view === TABLE ? (
              <ProjectsTable projects={data.projects} />
            ) : (
              <Tiles projects={data.projects} />
            )}
          </>
        )}
      </div>
    </div>
  );
}

/** revisionOf is the revision the project's page reads: a project of
 *  one's is its repository's. */
const revisionOf = (p: Project) => (p.repo ? p.repo.revision : p.revision);

/** Tiles is the projects as tiles: with a system among them, the systems
 *  under their title, the rest as compact rows under theirs, each group a
 *  titled section. The tiles alone show the projects' links, so they
 *  alone fetch them. */
function Tiles({ projects }: { projects: Project[] }) {
  const { data: deps } = useFetch<Dependencies>("dependencies");
  const links = useMemo(() => linksOf(deps), [deps]);
  const systemsId = useId();
  const othersId = useId();
  const systems: Project[] = [];
  const others: Project[] = [];
  for (const p of projects) (p.shape === "system" ? systems : others).push(p);
  if (systems.length === 0)
    return (
      <div className={grid}>
        {projects.map((p) => (
          <ProjectTile key={p.key} p={p} links={links.get(p.key)} />
        ))}
      </div>
    );
  return (
    <>
      <section aria-labelledby={systemsId}>
        <h3 id={systemsId} className={"mt-0 " + eyebrowBase}>
          {S.projects.systems}
        </h3>
        <div className={grid}>
          {systems.map((p) => (
            <ProjectTile
              key={p.key}
              p={p}
              links={links.get(p.key)}
              heading="h4"
            />
          ))}
        </div>
      </section>
      {others.length > 0 && (
        <section aria-labelledby={othersId}>
          <h3 id={othersId} className={"mt-9 " + eyebrowBase}>
            {S.projects.others}
          </h3>
          <div className="border-t border-border-soft">
            {others.map((p) => (
              <ProjectRow key={p.key} p={p} />
            ))}
          </div>
        </section>
      )}
    </>
  );
}

/** ProjectRow is a project that is no system, compact: its key, its
 *  teaser on one line, its form and size on the right. A project of one
 *  says neither (every such row would repeat "ein Repository"), only its
 *  repository's slug, and that only where it differs from the key. */
function ProjectRow({ p }: { p: Project }) {
  const teaser = teaserLines(p.summary);
  const kind = p.repo ? "" : kindOf(p);
  const size = p.repo
    ? p.repo.slug.toLowerCase() !== p.key.toLowerCase() && (
        <span className="font-mono text-[12.5px]">{p.repo.slug}</span>
      )
    : S.projects.repos(p.repos);
  return (
    <Link
      to={projectPath(p.key)}
      className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-b border-border-soft px-1 py-3 text-ink-dim hover:bg-active"
    >
      {/* At least the width of a short key, so the teasers line up; a
          longer one takes what it needs. */}
      <span className="min-w-[88px] shrink-0 font-serif text-[17px] font-medium text-ink">
        {p.key}
      </span>
      <span className="min-w-0 flex-1 basis-[220px] truncate text-[13.5px] text-muted">
        {teaser.length > 0 ? (
          <Teaser lines={teaser} />
        ) : (
          <span className="text-faint">{S.projects.noDescription}</span>
        )}
      </span>
      {size && (
        // A capital for the form's lowercase word only, never a slug's.
        <span
          className={
            "ml-auto text-[13px] whitespace-nowrap text-muted" +
            (kind !== "" ? " first-letter:uppercase" : "")
          }
        >
          {kind !== "" && (
            <>
              {kind}
              <Sep />
            </>
          )}
          {size}
        </span>
      )}
    </Link>
  );
}

function ProjectTile({
  p,
  links,
  heading: H = "h3",
}: {
  p: Project;
  links?: Links;
  /** heading: h4 under a group's title, h3 without one. */
  heading?: "h3" | "h4";
}) {
  const teaser = teaserLines(p.summary);
  return (
    <Link to={projectPath(p.key)} className={tile}>
      <H className="font-serif text-[20px] font-medium text-ink">{p.key}</H>
      <div className="mt-1 text-[13px] text-muted">
        {/* A project known only as a source has no team. */}
        {p.team !== "" && (
          <>
            {S.projects.team} {p.team}
            <Sep />
          </>
        )}
        {S.projects.repos(p.repos)}
        {revisionOf(p) === 0 && (
          <>
            <Sep />
            <span className="text-faint">{S.projects.noDescription}</span>
          </>
        )}
      </div>
      {teaser.length > 0 && (
        <div className="mt-3 line-clamp-3 text-[13.5px] text-muted">
          <Teaser lines={teaser} />
        </div>
      )}
      {links && (links.uses.length > 0 || links.usedBy > 0) && (
        <div className="mt-3 flex flex-wrap gap-1">
          {links.uses.map((u) => (
            <Chip key={u} to={null} quiet>
              {u}
            </Chip>
          ))}
          {links.usedBy > 0 && (
            <Chip to={null} quiet>
              {S.projects.usedBy(links.usedBy)}
            </Chip>
          )}
        </div>
      )}
    </Link>
  );
}

/** kindOf is a project's form in the table: a project of one as such,
 *  else its judged shape; "" before a judgment. */
function kindOf(p: Project): string {
  if (p.repo) return S.projects.oneRepo;
  return p.shape ? S.project.shape[p.shape] : "";
}

/** A column of the table: its label's key, whether it holds a number (a
 *  figure or a date, the largest or newest first on a first click), what
 *  it sorts by (null for a value a project lacks: no merge, no judgment,
 *  no description, which stands last either way, its cell empty) and its
 *  cell. A cell may open on a lowercase word ("nie"): its first letter is
 *  a capital there. */
type Col = keyof typeof S.projects.headers;
const columns: {
  key: Col;
  num?: boolean;
  newestFirst?: boolean;
  value: (p: Project) => string | number | null;
  cell: (p: Project) => ReactNode;
}[] = [
  {
    key: "key",
    value: (p) => p.key,
    cell: (p) => (
      <td className={tdMono}>
        <Link to={projectPath(p.key)} className={rowLink}>
          {p.key}
        </Link>
      </td>
    ),
  },
  {
    key: "kind",
    value: (p) => kindOf(p) || null,
    cell: (p) => {
      const kind = kindOf(p);
      return <td className={td + " first-letter:uppercase"}>{kind}</td>;
    },
  },
  {
    key: "team",
    value: (p) => p.team || null,
    cell: (p) => <td className={td}>{p.team}</td>,
  },
  {
    key: "repos",
    num: true,
    newestFirst: true,
    value: (p) => p.repos,
    cell: (p) => <td className={tdNum}>{p.repos}</td>,
  },
  {
    key: "notes",
    num: true,
    newestFirst: true,
    value: (p) => p.notes,
    cell: (p) => <td className={tdNum}>{p.notes}</td>,
  },
  {
    key: "revision",
    num: true,
    newestFirst: true,
    value: (p) => revisionOf(p) || null,
    cell: (p) => {
      const revision = revisionOf(p);
      return <td className={tdNum}>{revision === 0 ? "" : revision}</td>;
    },
  },
  {
    key: "merge",
    newestFirst: true,
    value: (p) => (p.last_merge === null ? null : Date.parse(p.last_merge)),
    cell: (p) => (
      <td className={td + " whitespace-nowrap first-letter:uppercase"}>
        {p.last_merge === null ? S.common.never : day(p.last_merge)}
      </td>
    ),
  },
];

/** ProjectsTable is the projects' figures, one row each that opens its
 *  page. A click on a column's head sorts by it (a word A to Z, a figure
 *  or date largest first), a second turns it round; a value a project
 *  lacks stands last either way, ties by key. */
function ProjectsTable({ projects }: { projects: Project[] }) {
  const [sort, setSort] = useState<{ key: Col; up: boolean }>({
    key: "key",
    up: true,
  });
  const sorted = useMemo(() => {
    const by = columns.find((c) => c.key === sort.key)!;
    return [...projects].sort((a, b) => {
      const x = by.value(a);
      const y = by.value(b);
      if (x === null || y === null) {
        if (x === y) return a.key.localeCompare(b.key, "de");
        return x === null ? 1 : -1;
      }
      const c =
        typeof x === "number" && typeof y === "number"
          ? x - y
          : String(x).localeCompare(String(y), "de", { sensitivity: "base" });
      return (sort.up ? c : -c) || a.key.localeCompare(b.key, "de");
    });
  }, [projects, sort]);
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            {columns.map((c) => {
              const on = sort.key === c.key;
              return (
                <th
                  key={c.key}
                  className={c.num ? thNum : th}
                  aria-sort={
                    on ? (sort.up ? "ascending" : "descending") : undefined
                  }
                >
                  <button
                    type="button"
                    onClick={() =>
                      setSort({
                        key: c.key,
                        up: on ? !sort.up : !c.newestFirst,
                      })
                    }
                    className={
                      "uppercase hover:text-ink " +
                      (on
                        ? "text-ink " +
                          (sort.up
                            ? "after:content-['_↑']"
                            : "after:content-['_↓']")
                        : "")
                    }
                  >
                    {S.projects.headers[c.key]}
                  </button>
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {sorted.map((p) => (
            <tr key={p.key} {...rowOpens(projectPath(p.key))}>
              {columns.map((c) => (
                <Fragment key={c.key}>{c.cell(p)}</Fragment>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
