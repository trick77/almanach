import { useEffect } from "react";
import type { Problem, ProjectDetail } from "./api";
import { DescriptionView } from "./Description";
import { Diagram } from "./Diagram";
import type { FlowIR } from "./diagramIR";
import { NotesTable } from "./NotesTable";
import { causeLabel, day, problemTone, shortDay } from "./format";
import { notePathFor, repoPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Chip,
  Crumbs,
  Empty,
  Failed,
  Link,
  Pill,
  Sep,
  Skeleton,
  column,
  eyebrow,
  h2,
  meta,
  mono,
  page,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
} from "./ui";

export function ProjectPage({ projectKey }: { projectKey: string }) {
  const { data, failed } = useFetch<ProjectDetail>(
    `projects/${encodeURIComponent(projectKey)}`,
  );
  useEffect(() => {
    document.title = `${projectKey} ${S.common.sep} ${S.app}`;
  }, [projectKey]);

  return (
    <div className={page}>
      <div className={column}>
        <Crumbs
          items={[
            { label: S.project.breadcrumbRoot, to: "/" },
            { label: projectKey },
          ]}
        />
        {failed ? (
          <Failed what={S.project.what} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body data={data} />
        )}
      </div>
    </div>
  );
}

function Body({ data }: { data: ProjectDetail }) {
  const notes = data.repos.reduce((n, r) => n + r.notes, 0);
  // By instant, not by string: an RFC 3339 stamp with an offset does not
  // sort chronologically as text.
  const lastMerge = data.repos
    .map((r) => r.last_merge)
    .filter((d): d is string => d !== null)
    .sort((a, b) => Date.parse(a) - Date.parse(b))
    .at(-1);
  const [hRepo, hDesc, hNotes, hMerge] = S.project.repoHeaders;
  return (
    <>
      <h2 className={h2}>{data.key}</h2>
      <p className={meta}>
        {data.team !== "" && (
          <>
            {S.projects.team} <span className="text-ink-dim">{data.team}</span>
            <Sep />
          </>
        )}
        {S.projects.repos(data.repos.length)}
        <Sep />
        {S.projects.notes(notes)}
        <Sep />
        {data.description
          ? S.project.descriptionRevision(data.description.revision)
          : S.projects.noDescription}
        <Sep />
        {S.projects.lastMerge}{" "}
        {lastMerge === undefined ? S.common.never : day(lastMerge)}
      </p>

      {/* Only a baseline run draws the map, and only for a project with a
          source and not too many: where none will come, or a run asked and
          none was accepted, a placeholder promising one would be a false
          hint. */}
      {(data.map !== null || (data.map_expected && !data.map_declined)) && (
        <>
          <div className={eyebrow}>{S.project.map}</div>
          {data.map === null ? (
            <Empty>{S.project.noMap}</Empty>
          ) : (
            <SystemMap
              map={data.map}
              projectKey={data.key}
              slugs={data.repos.map((r) => r.slug)}
            />
          )}
        </>
      )}

      <DescriptionView
        description={data.description}
        emptyHint={S.project.noDescriptionHint}
      />

      <div className={eyebrow}>{S.project.repos}</div>
      {data.repos.length === 0 ? (
        <Empty>{S.project.noRepos}</Empty>
      ) : (
        <div className={tableBox}>
          <table className={table}>
            <thead>
              <tr>
                <th className={th}>{hRepo}</th>
                <th className={th}>{hDesc}</th>
                <th className={thNum}>{hNotes}</th>
                <th className={th}>{hMerge}</th>
              </tr>
            </thead>
            <tbody>
              {data.repos.map((r) => (
                <tr key={r.slug}>
                  <td className={tdMono}>
                    <Link
                      to={repoPath(data.key, r.slug)}
                      className="text-ink-dim hover:text-ink"
                    >
                      {r.slug}
                    </Link>
                  </td>
                  <td className={td}>
                    {r.summary === "" ? (
                      <span className="text-faint">
                        {S.project.noDescription}
                      </span>
                    ) : (
                      r.summary
                    )}
                  </td>
                  <td className={tdNum}>{r.notes}</td>
                  <td className={td + " whitespace-nowrap"}>
                    {r.last_merge === null ? S.common.never : day(r.last_merge)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className={eyebrow}>
        {S.project.problems}
        <span className="ml-2 font-normal tracking-normal normal-case text-faint">
          {S.project.problemsWindow(data.problem_window_days)}
        </span>
      </div>
      <Problems problems={data.problems} />

      <div className={eyebrow}>{S.project.recentNotes}</div>
      <NotesTable projectKey={data.key} notes={data.recent_notes} />
    </>
  );
}

/** SystemMap is the project's overview: its zones titled from strings.ts by
 *  id, and every repository node a link to its page, when the project lists
 *  that repository. */
function SystemMap({
  map,
  projectKey,
  slugs,
}: {
  map: FlowIR;
  projectKey: string;
  slugs: string[];
}) {
  const ir: FlowIR = {
    ...map,
    zones: map.zones.map((z) => ({
      ...z,
      title: S.diagram.zones[z.id] ?? z.title,
    })),
  };
  const repoZone = new Map(map.nodes.map((n) => [n.id, n.zone]));
  return (
    <Diagram
      ir={ir}
      title={S.diagram.titles.map}
      links={(id) =>
        repoZone.get(id) === "repositories" && slugs.includes(id)
          ? repoPath(projectKey, id)
          : null
      }
    />
  );
}

function Problems({ problems }: { problems: Problem[] }) {
  if (problems.length === 0) return <Empty>{S.project.noProblems}</Empty>;
  const [hRepo, hArea, hComp, hFixes, hCause, hPosted, hLast] =
    S.project.problemHeaders;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{hRepo}</th>
            <th className={th}>{hArea}</th>
            <th className={th}>{hComp}</th>
            <th className={thNum}>{hFixes}</th>
            <th className={th}>{hCause}</th>
            <th className={thNum}>{hPosted}</th>
            <th className={th}>{hLast}</th>
          </tr>
        </thead>
        <tbody>
          {problems.map((p) => (
            <tr key={p.repo_slug + "|" + p.area}>
              <td className={tdMono}>{p.repo_slug}</td>
              <td className={tdMono}>
                {p.area === "" ? S.project.rootArea : p.area}
              </td>
              <td className={td}>{p.component}</td>
              <td className={tdNum}>
                <Pill tone={problemTone(p.bugfixes)} dot>
                  {p.bugfixes}
                </Pill>
              </td>
              <td className={td}>
                {causeLabel(p.top_cause)}
                {p.bugfixes > 1 && (
                  <span className={"ml-1.5 text-faint " + mono}>
                    {p.top_cause_count}/{p.bugfixes}
                  </span>
                )}
              </td>
              <td className={tdNum}>
                {p.issues_posted} / {p.suggestions_posted}
              </td>
              <td className={td + " whitespace-nowrap"}>
                <Chip to={notePathFor(p.last_fix_pr_key)}>
                  {p.last_fix_pr_key}
                </Chip>{" "}
                <span className="text-faint">{shortDay(p.last_fix_at)}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
