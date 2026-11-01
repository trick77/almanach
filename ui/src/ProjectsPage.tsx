import type { Project, Projects, Repo } from "./api";
import { day } from "./format";
import { projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Empty,
  Failed,
  Link,
  Sep,
  Skeleton,
  column,
  eyebrow,
  h2,
  lede,
  page,
} from "./ui";

const tile =
  "block rounded-ui border border-border bg-panel px-4.5 py-4 text-ink-dim hover:border-elevated-border hover:bg-active";
const grid = "grid gap-3.5 sm:grid-cols-2";

/** ProjectsPage is the entry: one card per project a note has arrived
 *  for, and one per repository of a folder whose repositories stand
 *  alone. The two get a heading each only when both are there. */
export function ProjectsPage() {
  const { data, failed } = useFetch<Projects>("projects");
  const systems = data?.projects.filter((p) => p.system) ?? [];
  const alone =
    data?.projects
      .filter((p) => !p.system)
      .flatMap((p) => p.repositories.map((r) => ({ key: p.key, repo: r }))) ??
    [];
  const both = systems.length > 0 && alone.length > 0;
  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.projects.title}</h2>
        <p className={lede}>{S.projects.lede}</p>
        {failed ? (
          <Failed what={S.projects.what} />
        ) : data === null ? (
          <Skeleton />
        ) : systems.length === 0 && alone.length === 0 ? (
          <Empty>{S.projects.empty}</Empty>
        ) : (
          <>
            {systems.length > 0 && (
              <section>
                {both && (
                  <Heading
                    title={S.projects.systems}
                    aside={S.projects.systemsAside}
                  />
                )}
                <div className={grid}>
                  {systems.map((p) => (
                    <ProjectTile key={p.key} p={p} />
                  ))}
                </div>
              </section>
            )}
            {alone.length > 0 && (
              <section>
                {both && (
                  <Heading
                    title={S.projects.standalone}
                    aside={S.projects.standaloneAside}
                  />
                )}
                <div className={grid}>
                  {alone.map(({ key, repo }) => (
                    <RepoTile key={key + "/" + repo.slug} pkey={key} r={repo} />
                  ))}
                </div>
              </section>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function Heading({ title, aside }: { title: string; aside: string }) {
  return (
    <h3 className={eyebrow + " flex items-baseline gap-2.5"}>
      {title}
      <span className="tracking-normal normal-case">{aside}</span>
    </h3>
  );
}

function ProjectTile({ p }: { p: Project }) {
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
        {p.revision > 0 ? (
          S.projects.revision(p.revision)
        ) : (
          <span className="text-faint">{S.projects.noDescription}</span>
        )}
      </div>
      {/* A line of its own: its first letter a capital, where the
          headers' inline "letzter Merge" stays lowercase. */}
      <div className="mt-3 text-[13px] text-muted first-letter:uppercase">
        {S.projects.lastMerge}{" "}
        {p.last_merge === null ? S.common.never : day(p.last_merge)}
      </div>
    </Link>
  );
}

/** RepoTile is a repository that stands alone: its slug, its folder, the
 *  first sentence of its description. */
function RepoTile({ pkey, r }: { pkey: string; r: Repo }) {
  return (
    <Link to={repoPath(pkey, r.slug)} className={tile}>
      <h3 className="font-mono text-[17px] text-ink">{r.slug}</h3>
      <div className="mt-1 text-[13px] text-muted">
        {pkey}
        <Sep />
        {r.revision > 0 ? (
          S.projects.revision(r.revision)
        ) : (
          <span className="text-faint">{S.projects.noDescription}</span>
        )}
      </div>
      {r.summary !== "" && (
        <div className="mt-3 text-[13.5px] text-muted">{r.summary}</div>
      )}
    </Link>
  );
}
