import type { Projects } from "./api";
import { day } from "./format";
import { projectPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
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

/** ProjectsPage is the entry: one card per project a note has arrived
 *  for. */
export function ProjectsPage() {
  const { data, failed } = useFetch<Projects>("projects");
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
          <div className="grid gap-3.5 sm:grid-cols-2">
            {data.projects.map((p) => (
              <Link
                key={p.key}
                to={projectPath(p.key)}
                className="block rounded-ui border border-border bg-panel px-4.5 py-4 text-ink-dim hover:border-elevated-border hover:bg-active"
              >
                <h3 className="font-serif text-[20px] font-medium text-ink">
                  {p.key}
                </h3>
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
                    <span className="text-faint">
                      {S.projects.noDescription}
                    </span>
                  )}
                </div>
                {/* A line of its own: its first letter a capital, where the
                    headers' inline "letzter Merge" stays lowercase. */}
                <div className="mt-3 text-[13px] text-muted first-letter:uppercase">
                  {S.projects.lastMerge}{" "}
                  {p.last_merge === null ? S.common.never : day(p.last_merge)}
                </div>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
