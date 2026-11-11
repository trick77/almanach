import { useEffect, useMemo, useRef, useState } from "react";
import type { DependencyEnd, Problem, ProjectDetail, RepoDetail } from "./api";
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
import { Diagram, titledZones } from "./Diagram";
import type { FlowIR } from "./diagramIR";
import { RepoInterfaces } from "./Interfaces";
import { RepoMessages } from "./Messaging";
import { NotesTable } from "./NotesTable";
import { RepoParts } from "./Parts";
import { ProblemsSection } from "./Problems";
import { Revisions } from "./Revisions";
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
import { RepoSwitcher } from "./RepoSwitcher";
import { factIds } from "./repoFacts";
import { StaleHint } from "./StaleHint";
import { refName } from "./format";
import {
  projectExportHref,
  projectPath,
  repoExportHref,
  repoPath,
} from "./routing";
import { S } from "./strings";
import { useCanonical } from "./useCanonical";
import { useFetch } from "./useFetch";
import {
  Crumbs,
  Empty,
  ExportLine,
  Failed,
  Link,
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
  th,
  teaserLines,
} from "./ui";

/** The sections' ids, the rail's anchors: the path's steps, the facts on
 *  the reference tab (fact tables elsewhere link to them), the history's
 *  lists. */
const ids = {
  what: "what",
  map: "structure",
  guard: "guard",
  change: "change",
  pitfalls: "pitfalls",
  ...factIds,
  notes: "notes",
  revisions: "revisions",
};

/** RepoPage is a repository's page; single when it is a project of one's,
 *  shown at the project's path: the repository's page is the project's. */
export function RepoPage({
  projectKey,
  slug,
  single = false,
}: {
  projectKey: string;
  slug: string;
  single?: boolean;
}) {
  const { data, failed } = useFetch<RepoDetail>(
    `projects/${encodeURIComponent(projectKey)}/repos/${encodeURIComponent(slug)}`,
  );
  useEffect(() => {
    document.title = `${slug} ${S.common.sep} ${projectKey} ${S.common.sep} ${S.app}`;
  }, [projectKey, slug]);
  useCanonical(
    data
      ? single
        ? projectPath(data.project_key)
        : repoPath(data.project_key, data.slug)
      : null,
  );

  if (failed || data === null)
    return (
      <div className={page}>
        <div className={column}>
          <RepoCrumbs projectKey={projectKey} slug={single ? "" : slug} />
          {failed ? <Failed message={S.repo.failed} /> : <Skeleton />}
        </div>
      </div>
    );
  return <Body data={data} single={single} />;
}

function RepoCrumbs({
  projectKey,
  slug,
}: {
  projectKey: string;
  slug: string;
}) {
  // A project of one's page is its repository's: no third crumb.
  return (
    <Crumbs
      items={
        slug === ""
          ? [
              { label: S.project.breadcrumbRoot, to: "/" },
              { label: projectKey },
            ]
          : [
              { label: S.project.breadcrumbRoot, to: "/" },
              { label: projectKey, to: projectPath(projectKey) },
              { label: slug },
            ]
      }
    />
  );
}

function Body({ data, single: given }: { data: RepoDetail; single: boolean }) {
  const scroller = useRef<HTMLDivElement>(null);
  const { tab, setTab, target, setTarget, landing } = useHashTab(
    pageTabs,
    START,
  );
  const [query, setQuery] = useState("");
  // The project, for its other repositories and this one's problem areas:
  // the project endpoint has both. Failing, the page goes without them.
  const { data: project } = useFetch<ProjectDetail>(
    `projects/${encodeURIComponent(data.project_key)}`,
  );
  // The question chip pressed on the reference tab: a tab switch by the
  // tab bar lets it go, a path's "Alle n" presses one.
  const { ask, setAsk, kinds, rejump } = useAsk(data.description);
  // Without a description the tab still holds the facts, a link's target.
  useJumpTo(landing, tab === DESCRIPTION, rejump);
  const problems = useMemo(
    () => (project?.problems ?? []).filter((p) => p.repo_slug === data.slug),
    [project, data.slug],
  );
  // A project of one: its page is this one, at the project's path.
  // Known at once when the project page renders this one; else when the
  // project detail lands.
  const single = given || (project?.single ?? false);
  useCanonical(single ? projectPath(data.project_key) : null);
  const current = data.description?.revision ?? 0;
  const pagePath = single
    ? projectPath(data.project_key)
    : repoPath(data.project_key, data.slug);
  // The next repository in the project's order; none after the last, none
  // for a project of one.
  const at = project?.repos.findIndex((r) => r.slug === data.slug) ?? -1;
  const next = !single && at >= 0 ? project?.repos[at + 1] : undefined;
  const nextTeaser = useMemo(
    () => (next ? teaserLines(next.summary) : []),
    [next],
  );
  const { openTab, openAsk } = useTabMoves({ tab, setTab, setAsk, scroller });

  // A search is over the description: typing elsewhere goes there.
  function onQuery(q: string) {
    setQuery(q);
    if (q.trim() !== "" && tab !== DESCRIPTION) openTab(DESCRIPTION);
  }

  // A map refused on every try says so: never just a missing section.
  const showMap = data.map !== null || data.map_declined;
  const showDeps = data.uses.length + data.used_by.length > 0;
  const showParts = data.parts.edges.length > 0;
  const msgs = data.messaging;
  const showMsgs = msgs.topics.length + msgs.schemas.length > 0;
  const apis = data.interfaces;
  const offered = apis.filter((a) => a.direction === "offers").length;
  // Not on every keystroke of a search: the path is the data's alone.
  const steps = useMemo(
    () =>
      entrySteps(
        data,
        pagePath,
        showMap,
        problems,
        project?.problem_window_days ?? 0,
      ),
    [data, pagePath, showMap, problems, project?.problem_window_days],
  );
  // The facts read from the code, after the description: what it is tied
  // to, then what it offers and sends.
  const facts: RailItem[] = [
    ...(showDeps
      ? [
          {
            id: ids.deps,
            label: S.deps.title,
            tab: DESCRIPTION,
            count: S.deps.count(data.uses.length, data.used_by.length),
          },
        ]
      : []),
    ...(showParts
      ? [
          {
            id: ids.parts,
            label: S.parts.title,
            tab: DESCRIPTION,
            count: data.parts.units.length,
          },
        ]
      : []),
    ...(apis.length > 0
      ? [
          {
            id: ids.interfaces,
            label: S.ifc.title,
            tab: DESCRIPTION,
            count: S.ifc.count(offered, apis.length - offered),
          },
        ]
      : []),
    ...(showMsgs
      ? [
          {
            id: ids.messages,
            label: S.msg.title,
            tab: DESCRIPTION,
            count: S.msg.count(msgs.topics.length, msgs.schemas.length),
          },
        ]
      : []),
  ];
  const desc = descriptionRail(data.description, query, DESCRIPTION, kinds);
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
          id: ids.notes,
          label: S.repo.notes,
          tab: HISTORY,
          count: data.notes_total,
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
          head={
            // A project of one has no other repository and no page of
            // its own: no head.
            project && !single ? (
              <RepoSwitcher
                key={data.slug}
                projectKey={data.project_key}
                repos={project.repos}
                slug={data.slug}
              />
            ) : undefined
          }
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
        <RepoCrumbs
          projectKey={data.project_key}
          slug={single ? "" : data.slug}
        />
      </div>
      <h2 className={h2}>{data.slug}</h2>
      <p className={meta}>
        {single ? (
          S.repo.single
        ) : (
          <>
            {S.repo.project}{" "}
            <Link
              to={projectPath(data.project_key)}
              className="text-ink-dim hover:text-ink"
            >
              {data.project_key}
            </Link>
          </>
        )}
      </p>
      <StandLine
        revision={data.description?.revision ?? null}
        notes={data.notes_total}
        lastMerge={data.last_merge}
        onHistory={() => openTab(HISTORY)}
      />
      <ExportLine
        href={
          single
            ? projectExportHref(data.project_key)
            : repoExportHref(data.project_key, data.slug)
        }
      />
      <StaleHint
        stale={data.stale}
        scope="repo"
        projectKey={data.project_key}
      />

      <Tabs
        tabs={[
          { id: START, label: S.tabs.start },
          {
            id: DESCRIPTION,
            label: S.tabs.reference,
            count: data.description?.statements.length,
          },
          { id: HISTORY, label: S.tabs.history, count: data.notes_total },
        ]}
        value={tab}
        onChange={openTab}
      />

      <div role="tabpanel" aria-label={tabLabels[tab]}>
        {tab === START && (
          <>
            <EntryPath steps={steps} onAsk={openAsk} />
            {next && project && (
              <NextCard
                to={repoPath(project.key, next.slug)}
                slug={next.slug}
                teaser={nextTeaser}
              />
            )}
          </>
        )}
        {tab === DESCRIPTION && (
          <>
            <AskChips
              statements={data.description?.statements ?? []}
              value={ask}
              onChange={setAsk}
            />
            <DescriptionView
              description={data.description}
              emptyHint={S.projects.firstDescription}
              pageRepo={data.slug}
              query={query}
              kinds={kinds}
              tab={DESCRIPTION}
              target={target}
              onTarget={setTarget}
              tail={<RepoFacts data={data} />}
            />
          </>
        )}
        {tab === HISTORY && <History data={data} current={current} />}
      </div>
    </RailPage>
  );
}

/** RepoFacts is what the code tells of the repository, after its
 *  description on the reference tab: what it is tied to, its parts, what
 *  it offers and sends; not there without any. */
function RepoFacts({ data }: { data: RepoDetail }) {
  const showDeps = data.uses.length + data.used_by.length > 0;
  const showParts = data.parts.edges.length > 0;
  const msgs = data.messaging;
  const showMsgs = msgs.topics.length + msgs.schemas.length > 0;
  const apis = data.interfaces;
  if (!showDeps && !showParts && apis.length === 0 && !showMsgs) return null;
  return (
    <>
      <FactsTitle />
      {showDeps && (
        <section id={ids.deps} className="scroll-mt-16">
          <div className={eyebrow}>{S.deps.title}</div>
          <Dependencies data={data} />
        </section>
      )}
      {showParts && (
        <section id={ids.parts} className="scroll-mt-16">
          <div className={eyebrow}>{S.parts.title}</div>
          <RepoParts parts={data.parts} />
        </section>
      )}
      {apis.length > 0 && (
        <section id={ids.interfaces} className="scroll-mt-16">
          <div className={eyebrow}>{S.ifc.title}</div>
          <RepoInterfaces apis={apis} />
        </section>
      )}
      {showMsgs && (
        <section id={ids.messages} className="scroll-mt-16">
          <div className={eyebrow}>{S.msg.title}</div>
          <RepoMessages
            self={{ project: data.project_key, slug: data.slug }}
            data={msgs}
          />
        </section>
      )}
    </>
  );
}

/** entrySteps is the repository's reading path, each step there only
 *  when it has something to show: what it is for, how it is built, what
 *  must not break, where to change what, its pitfalls with its problem
 *  areas; a step with more of its kind than it shows names the question
 *  that shows them all. page is the page's path, a statement's link's. */
function entrySteps(
  data: RepoDetail,
  page: string,
  showMap: boolean,
  problems: Problem[],
  windowDays: number,
): Step[] {
  const statements = data.description?.statements ?? [];
  const steps: Step[] = [
    {
      id: ids.what,
      title: S.path.repoWhat,
      body: (
        <EntryLead
          description={data.description}
          page={page}
          empty={S.projects.firstDescription}
        />
      ),
    },
  ];
  if (showMap)
    steps.push({
      id: ids.map,
      title: S.repo.map,
      body:
        data.map === null ? (
          <Empty>{S.repo.mapDeclined}</Empty>
        ) : (
          <RepoMap map={data.map} />
        ),
    });
  const guards = kindStep(statements, "guard", page, false, S.path.allGuards);
  if (guards.total > 0)
    steps.push({
      id: ids.guard,
      title: S.path.guard,
      body: guards.body,
      more: guards.more,
    });
  const change = kindStep(
    statements,
    "where",
    page,
    false,
    S.path.allExtensions,
  );
  if (change.total > 0)
    steps.push({
      id: ids.change,
      title: S.path.change,
      body: change.body,
      more: change.more,
    });
  // The pitfalls first, then the problem areas under them.
  const hurt = kindStep(statements, "hurt", page, false, S.path.allPitfalls);
  if (hurt.total > 0 || problems.length > 0)
    steps.push({
      id: ids.pitfalls,
      title: S.path.pitfalls,
      body: (
        <>
          {hurt.body}
          <ProblemsSection
            problems={problems}
            windowDays={windowDays}
            repoColumn={false}
            first={hurt.total === 0}
          />
        </>
      ),
      more: hurt.more,
    });
  return steps;
}

/** Dependencies is what the repository uses, with who else uses each, and
 *  what uses it. A section of the column, not rail lists: the rail is the
 *  page's sections. Another project's repository is named with its
 *  project. */
function Dependencies({ data }: { data: RepoDetail }) {
  const name = (d: DependencyEnd) =>
    d.project === data.project_key ? d.slug : refName(d.project, d.slug);
  return (
    <div className="grid gap-y-4">
      <DepTable
        title={S.deps.uses}
        name={name}
        rows={data.uses.map((u) => ({
          end: u,
          also:
            u.also.length > 0
              ? S.deps.also(u.also.map(name).join(", "))
              : undefined,
        }))}
      />
      <DepTable
        title={S.deps.usedBy}
        name={name}
        rows={data.used_by.map((u) => ({ end: u }))}
      />
    </div>
  );
}

/** DepTable is one of Dependencies' lists, none when empty. */
function DepTable({
  title,
  name,
  rows,
}: {
  title: string;
  name: (d: DependencyEnd) => string;
  rows: { end: DependencyEnd; also?: string }[];
}) {
  if (rows.length === 0) return null;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{title}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ end, also }) => (
            <tr key={`${end.project}/${end.slug}`}>
              <td className={td}>
                <Link
                  to={repoPath(end.project, end.slug)}
                  className={mono + " text-ink-dim hover:text-ink"}
                >
                  {name(end)}
                </Link>
                {also !== undefined && (
                  <span className="ml-2 text-[12.5px] text-faint">{also}</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** RepoMap is the repository from inside, its zones titled from strings.ts
 *  by id. Its nodes are parts, not pages: nothing links. */
function RepoMap({ map }: { map: FlowIR }) {
  return (
    <Diagram
      ir={titledZones(map, S.diagram.repoZones)}
      title={S.diagram.titles.repoMap}
      map
    />
  );
}

/** History is where the description came from: the notes of the merged
 *  pull requests, and the revisions they and the baseline runs wrote. */
function History({ data, current }: { data: RepoDetail; current: number }) {
  return (
    <>
      <section id={ids.notes} className="scroll-mt-16">
        <div className={eyebrow}>{S.repo.notes}</div>
        <NotesTable projectKey={data.project_key} notes={data.notes} />
      </section>

      <section id={ids.revisions} className="scroll-mt-16">
        <div className={eyebrow}>{S.revision.list}</div>
        <Revisions
          projectKey={data.project_key}
          slug={data.slug}
          revisions={data.revisions}
          current={current}
        />
      </section>
    </>
  );
}
