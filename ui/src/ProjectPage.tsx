import { useEffect, useRef, useState } from "react";
import type { ProjectDetail } from "./api";
import { DescriptionView, descriptionRail } from "./Description";
import { Diagram, titledZones } from "./Diagram";
import type { FlowIR } from "./diagramIR";
import { NotesTable } from "./NotesTable";
import { Problems } from "./Problems";
import {
  Rail,
  RailPage,
  Tabs,
  stickyCrumbs,
  useHashTab,
  type RailGroup,
} from "./Rail";
import { day } from "./format";
import { projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { useCanonical } from "./useCanonical";
import { useFetch } from "./useFetch";
import {
  Crumbs,
  Empty,
  Failed,
  Link,
  Sep,
  Skeleton,
  column,
  eyebrow,
  h2,
  meta,
  page,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
} from "./ui";

/** The project page's tabs by their hash: the overview first, the hub's
 *  job; the description second. */
const OVERVIEW = "uebersicht";
const DESCRIPTION = "beschreibung";
const TABS = [OVERVIEW, DESCRIPTION];
const tabLabel = (t: string) =>
  t === OVERVIEW ? S.tabs.overview : S.tabs.description;

/** The overview's section ids, the rail's anchors. */
const ids = {
  map: "systemkarte",
  repos: "repositories",
  problems: "problemzonen",
  notes: "notizen",
};

export function ProjectPage({ projectKey }: { projectKey: string }) {
  const { data, failed } = useFetch<ProjectDetail>(
    `projects/${encodeURIComponent(projectKey)}`,
  );
  useEffect(() => {
    document.title = `${projectKey} ${S.common.sep} ${S.app}`;
  }, [projectKey]);
  useCanonical(data ? projectPath(data.key) : null);

  if (failed || data === null)
    return (
      <div className={page}>
        <div className={column}>
          <ProjectCrumbs projectKey={projectKey} />
          {failed ? <Failed what={S.project.what} /> : <Skeleton />}
        </div>
      </div>
    );
  return <Body data={data} />;
}

function ProjectCrumbs({ projectKey }: { projectKey: string }) {
  return (
    <Crumbs
      items={[
        { label: S.project.breadcrumbRoot, to: "/" },
        { label: projectKey },
      ]}
    />
  );
}

function Body({ data }: { data: ProjectDetail }) {
  const scroller = useRef<HTMLDivElement>(null);
  const [tab, setTab] = useHashTab(TABS, OVERVIEW);
  const [query, setQuery] = useState("");
  const notes = data.repos.reduce((n, r) => n + r.notes, 0);
  // By instant, not by string: an RFC 3339 stamp with an offset does not
  // sort chronologically as text.
  const lastMerge = data.repos
    .map((r) => r.last_merge)
    .filter((d): d is string => d !== null)
    .sort((a, b) => Date.parse(a) - Date.parse(b))
    .at(-1);
  // Only a baseline run draws the map, and only for a project with a
  // source and not too many: where none will come, or a run asked and none
  // was accepted, a placeholder promising one would be a false hint.
  const showMap =
    data.map !== null || (data.map_expected && !data.map_declined);

  // A new tab starts at its top, not where the last one was left.
  function openTab(t: string) {
    setTab(t);
    if (scroller.current) scroller.current.scrollTop = 0;
  }

  // A search is over the description: typing on the overview goes there.
  function onQuery(q: string) {
    setQuery(q);
    if (q.trim() !== "" && tab !== DESCRIPTION) openTab(DESCRIPTION);
  }

  const desc = descriptionRail(data.description, true, query, DESCRIPTION);
  const groups: RailGroup[] = [
    {
      title: S.tabs.overview,
      items: [
        ...(showMap
          ? [{ id: ids.map, label: S.project.map, tab: OVERVIEW }]
          : []),
        {
          id: ids.repos,
          label: S.project.repos,
          tab: OVERVIEW,
          count: data.repos.length,
        },
        {
          id: ids.problems,
          label: S.project.problems,
          tab: OVERVIEW,
          count: data.problems.length,
        },
        {
          id: ids.notes,
          label: S.project.recentNotes,
          tab: OVERVIEW,
          count: data.recent_notes.length,
        },
      ],
    },
    { title: S.tabs.description, items: desc.items },
  ];

  return (
    <RailPage
      scroller={scroller}
      rail={
        <Rail
          groups={groups}
          tab={tab}
          onTab={setTab}
          query={query}
          onQuery={onQuery}
          hitLine={desc.hitLine}
          scroller={scroller}
        />
      }
    >
      <div className={stickyCrumbs}>
        <ProjectCrumbs projectKey={data.key} />
      </div>
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

      <Tabs
        tabs={[
          { id: OVERVIEW, label: S.tabs.overview },
          {
            id: DESCRIPTION,
            label: S.tabs.description,
            count: data.description?.statements.length,
          },
        ]}
        value={tab}
        onChange={openTab}
      />

      <div role="tabpanel" aria-label={tabLabel(tab)}>
        {tab === OVERVIEW ? (
          <Overview data={data} showMap={showMap} />
        ) : (
          <DescriptionView
            description={data.description}
            emptyHint={S.project.noDescriptionHint}
            query={query}
          />
        )}
      </div>
    </RailPage>
  );
}

/** Overview is the hub: the map, the repositories, where it broke lately,
 *  what merged lately. */
function Overview({
  data,
  showMap,
}: {
  data: ProjectDetail;
  showMap: boolean;
}) {
  const [hRepo, hDesc, hNotes, hMerge] = S.project.repoHeaders;
  return (
    <>
      {showMap && (
        <section id={ids.map} className="scroll-mt-16">
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
        </section>
      )}

      <section id={ids.repos} className="scroll-mt-16">
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
                      {r.last_merge === null
                        ? S.common.never
                        : day(r.last_merge)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section id={ids.problems} className="scroll-mt-16">
        <div className={eyebrow}>
          {S.project.problems}
          <span className="ml-2 font-normal tracking-normal normal-case text-faint">
            {S.project.problemsWindow(data.problem_window_days)}
          </span>
        </div>
        <Problems problems={data.problems} />
      </section>

      <section id={ids.notes} className="scroll-mt-16">
        <div className={eyebrow}>{S.project.recentNotes}</div>
        <NotesTable projectKey={data.key} notes={data.recent_notes} />
      </section>
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
  const ir = titledZones(map, S.diagram.zones);
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
