import type { Dependencies, Project, Projects } from "./api";
import { day } from "./format";
import { projectPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Chip,
  Empty,
  Failed,
  Link,
  Sep,
  Skeleton,
  column,
  h2,
  lede,
  page,
} from "./ui";

const tile =
  "block rounded-ui border border-border bg-panel px-4.5 py-4 text-ink-dim hover:border-elevated-border hover:bg-active";
const grid = "grid gap-3.5 sm:grid-cols-2";

/** Links is what a project's repositories have to do with other projects'
 *  by the manifests: the repositories they depend on, named project/slug,
 *  and how many repositories elsewhere depend on theirs. */
export interface Links {
  uses: string[];
  usedBy: number;
}

/** linksOf is every project's links, by key. */
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
    if (e.from.project === e.to.project) continue;
    // A project of one is named once: llmwire, not llmwire/llmwire.
    const name =
      e.to.project === e.to.slug ? e.to.slug : `${e.to.project}/${e.to.slug}`;
    const from = at(e.from.project);
    if (!from.uses.includes(name)) from.uses.push(name);
    const set = users.get(e.to.project) ?? new Set<string>();
    set.add(`${e.from.project}/${e.from.slug}`);
    users.set(e.to.project, set);
  }
  for (const [key, set] of users) at(key).usedBy = set.size;
  return out;
}

/** ProjectsPage is the entry: one card per project. A project of one (a
 *  source with one repository) shows its repository's description; its
 *  page is the repository's. */
export function ProjectsPage() {
  const { data, failed } = useFetch<Projects>("projects");
  const { data: deps } = useFetch<Dependencies>("dependencies");
  const links = linksOf(deps);
  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.projects.title}</h2>
        <p className={lede}>{S.projects.lede}</p>
        {failed ? (
          <Failed what={S.projects.what} />
        ) : data === null ? (
          <Skeleton />
        ) : data.projects.length === 0 ? (
          <Empty>{S.projects.empty}</Empty>
        ) : (
          <div className={grid}>
            {data.projects.map((p) => (
              <ProjectTile key={p.key} p={p} links={links.get(p.key)} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ProjectTile({ p, links }: { p: Project; links?: Links }) {
  // A project of one reads as its repository: its revision and summary.
  const revision = p.repo ? p.repo.revision : p.revision;
  return (
    <Link to={projectPath(p.key)} className={tile}>
      <h3 className="font-serif text-[20px] font-medium text-ink">{p.key}</h3>
      <div className="mt-1 text-[13px] text-muted">
        {/* A project known only as a source has no team. */}
        {p.team !== "" && (
          <>
            {S.projects.team} {p.team}
            <Sep />
          </>
        )}
        {S.projects.repos(p.repos)}
        <Sep />
        {S.projects.notes(p.notes)}
        <Sep />
        {revision > 0 ? (
          S.projects.revision(revision)
        ) : (
          <span className="text-faint">{S.projects.noDescription}</span>
        )}
      </div>
      {p.repo !== null && p.repo.summary !== "" && (
        <div className="mt-3 line-clamp-3 text-[13.5px] text-muted">
          {p.repo.summary}
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
      {/* A line of its own: its first letter a capital, where the
          headers' inline "letzter Merge" stays lowercase. */}
      <div className="mt-3 text-[13px] text-muted first-letter:uppercase">
        {S.projects.lastMerge}{" "}
        {p.last_merge === null ? S.common.never : day(p.last_merge)}
      </div>
    </Link>
  );
}
