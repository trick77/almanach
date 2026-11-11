import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { ProjectDetail, Repo, SharedRepo } from "./api";
import { DESCRIPTION, DescriptionView, descriptionRail } from "./Description";
import {
  AskChips,
  EntryLead,
  EntryPath,
  FactsTitle,
  HISTORY,
  NextCard,
  START,
  StandLine,
  kindStep,
  pageTabs,
  tabLabels,
  useAsk,
  useTabMoves,
  type Step,
} from "./Entry";
import { CompactMap } from "./CompactMap";
import { titledZones } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { ProjectInterfaces } from "./Interfaces";
import { ProjectMessages } from "./Messaging";
import { NotesTable } from "./NotesTable";
import { ProblemsSection } from "./Problems";
import { RepoPage } from "./RepoPage";
import { Revisions } from "./Revisions";
import { StaleHint } from "./StaleHint";
import {
  Rail,
  RailPage,
  Tabs,
  stickyCrumbs,
  useHashTab,
  useJumpTo,
  type RailGroup,
  type RailItem,
} from "./Rail";
import { day, refName } from "./format";
import { projectExportHref, projectPath, repoPath } from "./routing";
import { S } from "./strings";
import { useCanonical } from "./useCanonical";
import { useFetch } from "./useFetch";
import {
  Crumbs,
  Empty,
  ExportLine,
  Failed,
  Link,
  Sep,
  Skeleton,
  column,
  eyebrow,
  h2,
  meta,
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
  Teaser,
  teaserLines,
} from "./ui";

/** The sections' ids, the rail's anchors: the path's steps, the facts
 *  on the reference tab, the history's tables. */
const ids = {
  what: "what",
  how: "map",
  repos: "repositories",
  guard: "guard",
  hurt: "hurt",
  interfaces: "interfaces",
  messages: "messages",
  repoStats: "repo-stats",
  notes: "notes",
  revisions: "revisions",
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
          {failed ? <Failed message={S.project.failed} /> : <Skeleton />}
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
  const { tab, setTab, target, setTarget, landing } = useHashTab(
    pageTabs,
    START,
  );
  const [query, setQuery] = useState("");
  const statements = data.description?.statements ?? [];
  // The question chip pressed on the reference tab: a tab switch by the
  // tab bar lets it go, a path's "Alle n" presses one.
  const { ask, setAsk, kinds, rejump } = useAsk(data.description);
  useJumpTo(landing, tab === DESCRIPTION && data.description !== null, rejump);
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

  const { openTab, openAsk } = useTabMoves({ tab, setTab, setAsk, scroller });

  // A search is over the description: typing elsewhere goes there.
  function onQuery(q: string) {
    setQuery(q);
    if (q.trim() !== "" && tab !== DESCRIPTION) openTab(DESCRIPTION);
  }

  // Once for the map's boxes and the repositories' table both.
  const teasers = useMemo(
    () => new Map(data.repos.map((r) => [r.slug, teaserLines(r.summary)])),
    [data.repos],
  );
  // Not on every keystroke of a search: the path is the data's alone.
  const steps = useMemo(
    () => entrySteps(data, showMap, teasers),
    [data, showMap, teasers],
  );
  const desc = descriptionRail(data.description, query, DESCRIPTION, kinds);
  const facts: RailItem[] = [
    ...(data.interfaces.length > 0
      ? [
          {
            id: ids.interfaces,
            label: S.ifc.title,
            tab: DESCRIPTION,
            count: data.interfaces.length,
          },
        ]
      : []),
    ...(data.topics.length > 0
      ? [
          {
            id: ids.messages,
            label: S.msg.title,
            tab: DESCRIPTION,
            count: data.topics.length,
          },
        ]
      : []),
  ];
  const groups: RailGroup[] = [
    {
      title: S.path.label,
      items: steps.map((s, i) => ({
        id: s.id,
        label: S.path.step(i + 1, s.title),
        tab: START,
      })),
    },
    { title: S.tabs.reference, items: [...desc.items, ...facts] },
    {
      title: S.tabs.history,
      items: [
        {
          id: ids.repoStats,
          label: S.project.repos,
          tab: HISTORY,
          count: data.repos.length,
        },
        {
          id: ids.notes,
          label: S.project.recentNotes,
          tab: HISTORY,
          count: data.recent_notes.length,
        },
        {
          id: ids.revisions,
          label: S.revision.list,
          tab: HISTORY,
          count: data.revisions.length,
        },
      ],
    },
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
        {data.shape !== null && (
          <>
            <span
              className="text-ink-dim"
              title={S.project.shapeHint[data.shape]}
              data-shape={data.shape}
            >
              {S.project.shape[data.shape]}
            </span>
            <Sep />
          </>
        )}
        {data.team !== "" && (
          <>
            {S.projects.team} <span className="text-ink-dim">{data.team}</span>
            <Sep />
          </>
        )}
        {S.projects.repos(data.repos.length)}
      </p>
      <StandLine
        revision={data.description?.revision ?? null}
        notes={notes}
        lastMerge={lastMerge ?? null}
        onHistory={() => openTab(HISTORY)}
      />
      <ExportLine href={projectExportHref(data.key)} />
      <StaleHint stale={data.stale} scope="project" projectKey={data.key} />

      <Tabs
        tabs={[
          { id: START, label: S.tabs.start },
          {
            id: DESCRIPTION,
            label: S.tabs.reference,
            count: data.description?.statements.length,
          },
          { id: HISTORY, label: S.tabs.history },
        ]}
        value={tab}
        onChange={openTab}
      />

      <div role="tabpanel" aria-label={tabLabels[tab]}>
        {tab === START && (
          <>
            <EntryPath steps={steps} onAsk={openAsk} />
            {data.repos[0] && (
              <NextCard
                to={repoPath(data.key, data.repos[0].slug)}
                slug={data.repos[0].slug}
                teaser={teasers.get(data.repos[0].slug) ?? []}
              />
            )}
          </>
        )}
        {tab === DESCRIPTION && (
          <>
            <AskChips statements={statements} value={ask} onChange={setAsk} />
            <DescriptionView
              description={data.description}
              emptyHint={S.project.noDescriptionHint}
              query={query}
              kinds={kinds}
              tab={DESCRIPTION}
              target={target}
              onTarget={setTarget}
              tail={<Facts data={data} />}
            />
          </>
        )}
        {tab === HISTORY && <History data={data} />}
      </div>
    </RailPage>
  );
}

/** entrySteps is the start tab's reading path, each step there only when
 *  it has something to show: what the project is, how its parts connect,
 *  its repositories, what must not break, where it hurt; a step with more
 *  of its kind than it shows names the question that shows them all. */
function entrySteps(
  data: ProjectDetail,
  showMap: boolean,
  teasers: Map<string, string[]>,
): Step[] {
  const statements = data.description?.statements ?? [];
  const page = projectPath(data.key);
  const steps: Step[] = [];

  steps.push({
    id: ids.what,
    title:
      data.shape === "collection" ? S.path.collectionWhat : S.path.projectWhat,
    body: (
      <EntryLead
        description={data.description}
        page={page}
        empty={S.project.noDescriptionHint}
      />
    ),
  });

  if (showMap)
    steps.push({
      id: ids.how,
      title: S.path.how,
      body:
        data.map === null ? (
          <Empty>{S.project.noMap}</Empty>
        ) : (
          <SystemMap
            map={data.map}
            projectKey={data.key}
            slugs={data.repos.map((r) => r.slug)}
            teasers={teasers}
            shared={data.shared}
          />
        ),
    });

  steps.push({
    id: ids.repos,
    title: S.path.repos,
    body: <RepoTable data={data} teasers={teasers} />,
  });

  const guards = kindStep(statements, "guard", page, true, S.path.allGuards);
  if (guards.total > 0)
    steps.push({
      id: ids.guard,
      title: S.path.guard,
      body: guards.body,
      more: guards.more,
    });

  // The problem areas first, then the pitfalls under them.
  const hurt = kindStep(statements, "hurt", page, true, S.path.allPitfalls);
  if (data.problems.length > 0 || hurt.total > 0)
    steps.push({
      id: ids.hurt,
      title: S.path.hurt,
      body: (
        <>
          <ProblemsSection
            problems={data.problems}
            windowDays={data.problem_window_days}
            first
          />
          <div className="mt-3">{hurt.body}</div>
        </>
      ),
      more: hurt.more,
    });
  return steps;
}

/** RepoRows is the project's repositories as a table, each row opening
 *  its page by its slug under first, the other columns heads and cells;
 *  noRepos when it has none. The path's table and the history's. */
function RepoRows({
  data,
  first,
  heads,
  cells,
}: {
  data: ProjectDetail;
  first: string;
  heads: ReactNode;
  cells: (r: Repo) => ReactNode;
}) {
  if (data.repos.length === 0) return <Empty>{S.project.noRepos}</Empty>;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{first}</th>
            {heads}
          </tr>
        </thead>
        <tbody>
          {data.repos.map((r) => {
            const to = repoPath(data.key, r.slug);
            return (
              <tr key={r.slug} {...rowOpens(to)}>
                <td className={tdMono}>
                  <Link to={to} className={rowLink}>
                    {r.slug}
                  </Link>
                </td>
                {cells(r)}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** RepoTable is the path's repositories, each with its summary's teaser. */
function RepoTable({
  data,
  teasers,
}: {
  data: ProjectDetail;
  teasers: Map<string, string[]>;
}) {
  const [hRepo, hDesc] = S.project.repoHeaders;
  return (
    <RepoRows
      data={data}
      first={hRepo}
      heads={<th className={th}>{hDesc}</th>}
      cells={(r) => {
        const teaser = teasers.get(r.slug) ?? [];
        return (
          <td className={td}>
            {teaser.length === 0 ? (
              <span className="text-faint">{S.project.noDescription}</span>
            ) : (
              <Teaser lines={teaser} />
            )}
          </td>
        );
      }}
    />
  );
}

/** Facts is what the code tells of the project, after its description on
 *  the reference tab: the interfaces it offers, the topics it touches;
 *  not there without either. */
function Facts({ data }: { data: ProjectDetail }) {
  if (data.interfaces.length === 0 && data.topics.length === 0) return null;
  return (
    <>
      <FactsTitle />
      {data.interfaces.length > 0 && (
        <section id={ids.interfaces} className="scroll-mt-16">
          <div className={eyebrow}>{S.ifc.title}</div>
          <ProjectInterfaces apis={data.interfaces} />
        </section>
      )}
      {data.topics.length > 0 && (
        <section id={ids.messages} className="scroll-mt-16">
          <div className={eyebrow}>{S.msg.title}</div>
          <ProjectMessages project={data.key} flows={data.topics} />
        </section>
      )}
    </>
  );
}

/** History is what changed: each repository's figures, the latest notes,
 *  the project page's revisions. */
function History({ data }: { data: ProjectDetail }) {
  const [hRepo, hRev, hNotes, hMerge] = S.project.repoStatHeaders;
  return (
    <>
      <section id={ids.repoStats} className="scroll-mt-16">
        <div className={eyebrow}>{S.project.repos}</div>
        <RepoRows
          data={data}
          first={hRepo}
          heads={
            <>
              <th className={thNum}>{hRev}</th>
              <th className={thNum}>{hNotes}</th>
              <th className={th}>{hMerge}</th>
            </>
          }
          cells={(r) => (
            <>
              <td className={tdNum}>
                {r.revision === 0 ? (
                  <span className="text-faint">{S.projects.noDescription}</span>
                ) : (
                  r.revision
                )}
              </td>
              <td className={tdNum}>{r.notes}</td>
              <td className={td + " whitespace-nowrap"}>
                {r.last_merge === null ? S.common.never : day(r.last_merge)}
              </td>
            </>
          )}
        />
      </section>

      <section id={ids.notes} className="scroll-mt-16">
        <div className={eyebrow}>{S.project.recentNotes}</div>
        <NotesTable projectKey={data.key} notes={data.recent_notes} />
      </section>

      <section id={ids.revisions} className="scroll-mt-16">
        <div className={eyebrow}>{S.revision.list}</div>
        <Revisions
          projectKey={data.key}
          slug=""
          revisions={data.revisions}
          current={data.description?.revision ?? 0}
        />
      </section>
    </>
  );
}

/** SystemMap is the project's overview, compact (CompactMap): its zones
 *  titled from strings.ts by id, every repository node a link to its page
 *  when the project lists that repository, with its summary's teaser. */
function SystemMap({
  map,
  projectKey,
  slugs,
  teasers,
  shared,
}: {
  map: FlowIR;
  projectKey: string;
  slugs: string[];
  teasers: Map<string, string[]>;
  shared: SharedRepo[];
}) {
  const ir = useMemo(
    () => titledZones(withShared(map, shared), S.diagram.zones),
    [map, shared],
  );
  const repoZone = new Map(map.nodes.map((n) => [n.id, n.zone]));
  const byId = new Map(shared.map((r) => [sharedId(r), r]));
  return (
    <CompactMap
      ir={ir}
      title={S.diagram.titles.map}
      teasers={teasers}
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
    if (fits(out, true)) return out;
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
