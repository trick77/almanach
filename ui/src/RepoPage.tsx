import { useEffect, useRef, useState } from "react";
import type { DependencyEnd, ProjectDetail, RepoDetail } from "./api";
import { oddVersions } from "./DepsPage";
import { DescriptionView, SourceChip, descriptionRail } from "./Description";
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
  type RailItem,
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
} from "./ui";

/** The repository page's tabs by their hash: the page is a leaf, its
 *  content first; notes and revisions are where it came from. */
const DESCRIPTION = "beschreibung";
const HISTORY = "verlauf";
const TABS = [DESCRIPTION, HISTORY];

const ids = {
  map: "aufbau",
  problems: "problemzonen",
  notes: "notizen",
  revisions: "revisionen",
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
          {failed ? <Failed what={S.repo.what} /> : <Skeleton />}
        </div>
      </div>
    );
  return <Body data={data} />;
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

function Body({ data }: { data: RepoDetail }) {
  const scroller = useRef<HTMLDivElement>(null);
  const [tab, setTab] = useHashTab(TABS, DESCRIPTION);
  const [query, setQuery] = useState("");
  // The project, for its other repositories and this one's problem areas:
  // the project endpoint has both. Failing, the page goes without them.
  const { data: project } = useFetch<ProjectDetail>(
    `projects/${encodeURIComponent(data.project_key)}`,
  );
  const problems = (project?.problems ?? []).filter(
    (p) => p.repo_slug === data.slug,
  );
  // A project of one: its page is this one, at the project's path.
  const single = project?.single ?? false;
  useCanonical(single ? projectPath(data.project_key) : null);
  const lastMerge = data.last_merge;
  const current = data.description?.revision ?? 0;

  // A new tab starts at its top, not where the last one was left.
  function openTab(t: string) {
    setTab(t);
    if (scroller.current) scroller.current.scrollTop = 0;
  }

  // A search is over the description: typing in the history goes there.
  function onQuery(q: string) {
    setQuery(q);
    if (q.trim() !== "" && tab !== DESCRIPTION) openTab(DESCRIPTION);
  }

  const lead: RailItem[] =
    data.map !== null
      ? [{ id: ids.map, label: S.repo.map, tab: DESCRIPTION }]
      : [];
  const extra: RailItem[] =
    problems.length > 0
      ? [
          {
            id: ids.problems,
            label: S.project.problems,
            tab: DESCRIPTION,
            count: problems.length,
          },
        ]
      : [];
  const desc = descriptionRail(
    data.description,
    false,
    query,
    DESCRIPTION,
    lead,
    extra,
  );
  const groups: RailGroup[] = [
    { title: S.tabs.description, items: desc.items },
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
          label: S.repo.revisions,
          tab: HISTORY,
          count: data.revisions.length,
        },
      ],
    },
  ];
  if (project && project.repos.length > 1)
    groups.push({
      title: (
        <>
          {S.rail.project}{" "}
          <Link
            to={projectPath(data.project_key)}
            className="tracking-normal normal-case text-muted hover:text-ink"
          >
            {data.project_key}
          </Link>
        </>
      ),
      links: project.repos.map((r) => ({
        label: r.slug,
        to: repoPath(data.project_key, r.slug),
        current: r.slug === data.slug,
      })),
    });
  groups.push(...depGroups(data));

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
        <Sep />
        {S.projects.notes(data.notes_total)}
        <Sep />
        {data.description
          ? S.project.descriptionRevision(data.description.revision)
          : S.projects.noDescription}
        <Sep />
        {S.projects.lastMerge}{" "}
        {lastMerge === null ? S.common.never : day(lastMerge)}
      </p>

      <Tabs
        tabs={[
          {
            id: DESCRIPTION,
            label: S.tabs.description,
            count: data.description?.statements.length,
          },
          { id: HISTORY, label: S.tabs.history, count: data.notes_total },
        ]}
        value={tab}
        onChange={openTab}
      />

      <div
        role="tabpanel"
        aria-label={tab === DESCRIPTION ? S.tabs.description : S.tabs.history}
      >
        {tab === DESCRIPTION ? (
          <DescriptionView
            description={data.description}
            emptyHint={S.projects.firstDescription}
            pageRepo={data.slug}
            query={query}
            lead={
              data.map !== null && (
                <section id={ids.map} className="scroll-mt-16">
                  <div className={eyebrow}>{S.repo.map}</div>
                  <RepoMap map={data.map} />
                </section>
              )
            }
            afterSummary={
              problems.length > 0 && (
                <section id={ids.problems} className="scroll-mt-16">
                  <div className={eyebrow}>
                    {S.project.problems}
                    <span className="ml-2 font-normal tracking-normal normal-case text-faint">
                      {S.project.problemsWindow(
                        project?.problem_window_days ?? 0,
                      )}
                    </span>
                  </div>
                  <Problems problems={problems} repoColumn={false} />
                </section>
              )
            }
          />
        ) : (
          <History data={data} current={current} />
        )}
      </div>
    </RailPage>
  );
}

/** depGroups are the rail's dependency lists: what the repository uses,
 *  with the version it names and who else uses each, and what uses it. A
 *  version the other users do not name is marked. Another project's
 *  repository is named with its project. */
export function depGroups(data: RepoDetail): RailGroup[] {
  const name = (d: DependencyEnd) =>
    d.project === data.project_key || d.project === d.slug
      ? d.slug
      : `${d.project}/${d.slug}`;
  const groups: RailGroup[] = [];
  if (data.uses.length > 0)
    groups.push({
      title: S.deps.uses,
      links: data.uses.map((u) => {
        const odd = oddVersions([
          u.version,
          ...u.also.map((o) => o.version),
        ])[0];
        return {
          label: name(u),
          to: repoPath(u.project, u.slug),
          current: false,
          note: u.version || undefined,
          odd,
          noteTitle: odd ? S.deps.otherVersion : undefined,
          sub:
            u.also.length > 0
              ? S.deps.also(u.also.map(name).join(", "))
              : undefined,
        };
      }),
    });
  const odd = oddVersions(data.used_by.map((u) => u.version));
  if (data.used_by.length > 0)
    groups.push({
      title: S.deps.usedBy,
      links: data.used_by.map((u, i) => ({
        label: name(u),
        to: repoPath(u.project, u.slug),
        current: false,
        note: u.version || undefined,
        odd: odd[i],
        noteTitle: odd[i] ? S.deps.otherVersion : undefined,
      })),
    });
  return groups;
}

/** RepoMap is the repository from inside, its zones titled from strings.ts
 *  by id. Its nodes are parts, not pages: nothing links. */
function RepoMap({ map }: { map: FlowIR }) {
  return (
    <Diagram
      ir={titledZones(map, S.diagram.repoZones)}
      title={S.diagram.titles.repoMap}
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
        <div className={eyebrow}>{S.repo.revisions}</div>
        {data.revisions.length === 0 ? (
          <Empty>{S.repo.noRevisions}</Empty>
        ) : (
          <ol className="m-0 list-none p-0">
            {data.revisions.map((r) => (
              <li
                key={r.revision}
                className="flex items-baseline gap-3.5 border-b border-border-soft py-2 text-[13.5px] text-ink-dim"
              >
                <span className="w-[34px] tabular-nums text-muted">
                  {r.revision}
                </span>
                <span className="flex-1">
                  {S.repo.revisionFrom}{" "}
                  <SourceChip
                    source={{
                      kind: r.source_kind,
                      pr_key: r.source_pr_key,
                      commit: r.source_commit,
                    }}
                  />
                  <span className="ml-2 text-muted">
                    {S.repo.statements(r.statements)} {S.common.sep}{" "}
                    {S.repo.chars(r.chars)}
                  </span>
                </span>
                {r.revision === current && (
                  <span className="text-[11px] tracking-[.08em] text-ochre uppercase">
                    {S.repo.current}
                  </span>
                )}
                <span className="whitespace-nowrap text-muted">
                  {day(r.created_at)}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </>
  );
}
