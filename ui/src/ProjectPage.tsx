import { useEffect, useMemo, useRef, useState } from "react";
import type { ProjectDetail, SharedRepo } from "./api";
import { DescriptionView, descriptionRail } from "./Description";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { NotesTable } from "./NotesTable";
import { Problems } from "./Problems";
import { RepoPage } from "./RepoPage";
import {
  Rail,
  RailPage,
  Tabs,
  stickyCrumbs,
  useHashTab,
  type RailGroup,
} from "./Rail";
import { day, refName } from "./format";
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
const OVERVIEW = "overview";
const DESCRIPTION = "description";
const TABS = [OVERVIEW, DESCRIPTION];
const tabLabel = (t: string) =>
  t === OVERVIEW ? S.tabs.overview : S.tabs.description;

/** The overview's section ids, the rail's anchors. */
const ids = {
  map: "map",
  repos: "repositories",
  problems: "problems",
  notes: "notes",
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
  // A project of one: its repository's page is the project's.
  if (data.single && data.repos[0])
    return <RepoPage projectKey={data.key} slug={data.repos[0].slug} single />;
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
              shared={data.shared}
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
  shared,
}: {
  map: FlowIR;
  projectKey: string;
  slugs: string[];
  shared: SharedRepo[];
}) {
  const ir = useMemo(
    () => titledZones(withShared(map, shared), S.diagram.zones),
    [map, shared],
  );
  const repoZone = new Map(map.nodes.map((n) => [n.id, n.zone]));
  const byId = new Map(shared.map((r) => [sharedId(r), r]));
  return (
    <Diagram
      ir={ir}
      title={S.diagram.titles.map}
      links={(id) => {
        const r = byId.get(id);
        if (r) return repoPath(r.project, r.slug);
        return repoZone.get(id) === "repositories" && slugs.includes(id)
          ? repoPath(projectKey, id)
          : null;
      }}
    />
  );
}

const sharedId = (r: SharedRepo) => `shared:${r.project}/${r.slug}`;

/** withShared is the stored map with the repositories of other projects
 *  the project's repositories depend on, drawn by code, never by the model:
 *  a zone of their own, a node each, an edge from each user the map shows.
 *  A node the model drew for one anyway gives way to it, its edges moved
 *  over, so nothing is drawn twice: an external node named by its slug (by
 *  project/slug when two of them share it) whose every edge is a `library`
 *  use, nothing else. Only repositories a repository on the map depends on
 *  (a map older than a repository does not show its user), as many as one
 *  drawing holds, in the order given; with none of them fitting, the map
 *  as stored. */
export function withShared(map: FlowIR, repos: SharedRepo[]): FlowIR {
  const own = new Set(
    map.nodes.filter((n) => n.zone === "repositories").map((n) => n.id),
  );
  const drawn = repos.filter((r) => r.used_by.some((u) => own.has(u.slug)));
  for (let n = drawn.length; n > 0; n--) {
    const out = drawShared(map, drawn.slice(0, n), repos);
    if (fits(out)) return out;
  }
  return map;
}

/** libraryEdge says an edge the model drew is a build-time use: its label
 *  starts with the map's `library` kind. */
const libraryEdge = (e: FlowIR["edges"][number]) =>
  e.label.trim().toLowerCase().startsWith("library");

/** drawShared draws repos; all is every shared repository of the project,
 *  so a slug two of them share stays ambiguous when one was cut. */
function drawShared(
  map: FlowIR,
  repos: SharedRepo[],
  all: SharedRepo[],
): FlowIR {
  const own = new Set(
    map.nodes.filter((n) => n.zone === "repositories").map((n) => n.id),
  );
  const slugCount = new Map<string, number>();
  for (const r of all) {
    const s = r.slug.toLowerCase();
    slugCount.set(s, (slugCount.get(s) ?? 0) + 1);
  }
  const drawnAs = new Map<string, string>();
  for (const n of map.nodes) {
    if (n.zone !== "external") continue;
    const touching = map.edges.filter((e) => e.from === n.id || e.to === n.id);
    if (touching.length === 0 || !touching.every(libraryEdge)) continue;
    const names = [n.id, n.label].map((s) => s.toLowerCase());
    const r = repos.find((r) => {
      const full = `${r.project}/${r.slug}`.toLowerCase();
      const slug = r.slug.toLowerCase();
      return (
        names.includes(full) ||
        (slugCount.get(slug) === 1 && names.includes(slug))
      );
    });
    if (r) drawnAs.set(n.id, sharedId(r));
  }
  const to = (id: string) => drawnAs.get(id) ?? id;
  const edges: FlowIR["edges"] = [];
  const seen = new Set<string>();
  // A pair once, whichever way the model drew it: a use the model drew
  // backwards is the use code draws.
  const add = (e: FlowIR["edges"][number]) => {
    const k = [e.from, e.to].sort().join(">");
    if (e.from === e.to || seen.has(k)) return;
    seen.add(k);
    edges.push(e);
  };
  for (const e of map.edges) add({ ...e, from: to(e.from), to: to(e.to) });
  for (const r of repos)
    for (const u of r.used_by)
      if (own.has(u.slug))
        add({
          from: u.slug,
          to: sharedId(r),
          label: S.diagram.sharedEdge,
          dashed: false,
          head: "end",
        });
  return {
    ...map,
    nodes: [
      ...map.nodes.filter((n) => !drawnAs.has(n.id)),
      ...repos.map((r) => ({
        id: sharedId(r),
        label: refName(r.project, r.slug),
        shape: "pill" as const,
        zone: "shared",
      })),
    ],
    edges,
    zones: [
      ...map.zones.filter((z) =>
        map.nodes.some((n) => n.zone === z.id && !drawnAs.has(n.id)),
      ),
      { id: "shared", title: "shared" },
    ],
  };
}
