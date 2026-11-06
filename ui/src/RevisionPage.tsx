import { useEffect } from "react";
import type { RevisionDetail, StatementRef } from "./api";
import { DescriptionView, RevisionSources, kindLabel } from "./Description";
import { dayTime } from "./format";
import { projectPath, repoPath, revisionPath } from "./routing";
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

/** RevisionPage is one revision of a page: what it changed against the
 *  one before (added, changed, dropped statements, by name), then the
 *  description as it stood. slug "" is the project's page. */
export function RevisionPage({
  projectKey,
  slug,
  n,
}: {
  projectKey: string;
  slug: string;
  n: number;
}) {
  const base = `projects/${encodeURIComponent(projectKey)}`;
  const { data, failed } = useFetch<RevisionDetail>(
    (slug === "" ? base : `${base}/repos/${encodeURIComponent(slug)}`) +
      `/revisions/${n}`,
  );
  useEffect(() => {
    const where = slug === "" ? projectKey : `${projectKey}/${slug}`;
    document.title = `${S.revision.title(n)} ${S.common.sep} ${where} ${S.common.sep} ${S.app}`;
  }, [projectKey, slug, n]);
  useCanonical(
    data ? revisionPath(data.project_key, data.repo_slug ?? "", n) : null,
  );
  return (
    <div className={page}>
      <div className={column}>
        <Crumbs
          items={[
            { label: S.project.breadcrumbRoot, to: "/" },
            { label: projectKey, to: projectPath(projectKey) },
            ...(slug === ""
              ? []
              : [{ label: slug, to: repoPath(projectKey, slug) }]),
            { label: S.revision.title(n) },
          ]}
        />
        {failed ? (
          <Failed message={S.revision.failed} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body d={data} />
        )}
      </div>
    </div>
  );
}

function Body({ d }: { d: RevisionDetail }) {
  const slug = d.repo_slug ?? "";
  const pagePath =
    d.scope === "project"
      ? projectPath(d.project_key)
      : repoPath(d.project_key, slug);
  const desc = d.description;
  // A run writes a revision for a map, a shape or a check alone: each is
  // named, "nothing" only when none moved.
  const changes =
    d.added.length +
    d.changed.length +
    d.dropped.length +
    d.rechecked.length +
    (d.summary_changed ? 1 : 0) +
    (d.map_changed ? 1 : 0) +
    (d.shape_changed ? 1 : 0);
  return (
    <>
      <h2 className={h2}>{S.revision.of(d.revision, d.current)}</h2>
      <p className={meta}>
        {S.revision.from}{" "}
        <RevisionSources
          kind={desc.source_kind}
          prKey={desc.source_pr_key}
          prKeys={desc.source_pr_keys}
          commit={desc.source_commit}
        />
        <Sep />
        {dayTime(d.created_at)}
        <Sep />
        {d.previous > 0 && (
          <>
            <Link
              to={revisionPath(d.project_key, slug, d.previous)}
              className="text-ink-dim hover:text-ink"
            >
              {S.revision.previous}
            </Link>
            <Sep />
          </>
        )}
        {d.revision < d.current && (
          <>
            <Link
              to={revisionPath(d.project_key, slug, d.revision + 1)}
              className="text-ink-dim hover:text-ink"
            >
              {S.revision.next}
            </Link>
            <Sep />
          </>
        )}
        <Link to={pagePath} className="text-ink-dim hover:text-ink">
          {S.revision.current}
        </Link>
      </p>

      <div className={eyebrow}>
        {S.revision.changes}
        <span className="ml-2 font-normal tracking-normal normal-case text-faint">
          {d.previous === 0 ? S.revision.first : S.revision.against(d.previous)}
        </span>
      </div>
      {changes === 0 ? (
        <Empty>{S.revision.nothing}</Empty>
      ) : (
        <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13.5px]">
          {d.summary_changed && (
            <Flag label={S.rail.summary} text={S.revision.summaryChanged} />
          )}
          {d.map_changed && (
            <Flag label={S.project.map} text={S.revision.mapChanged} />
          )}
          {d.shape_changed && (
            <Flag label={S.revision.shape} text={S.revision.shapeChanged} />
          )}
          <Changed
            label={S.revision.added}
            refs={d.added}
            project={d.scope === "project"}
          />
          <Changed
            label={S.revision.changed}
            refs={d.changed}
            project={d.scope === "project"}
          />
          <Changed
            label={S.revision.dropped}
            refs={d.dropped}
            project={d.scope === "project"}
            gone
          />
          <Changed
            label={S.revision.rechecked}
            refs={d.rechecked}
            project={d.scope === "project"}
          />
        </dl>
      )}

      <div className={eyebrow}>{S.revision.description}</div>
      <DescriptionView
        description={desc}
        emptyHint=""
        pageRepo={d.scope === "repository" ? slug : undefined}
      />
    </>
  );
}

/** Flag is one row of the diff that names no statement: the summary, the
 *  map or the shape moved. */
function Flag({ label, text }: { label: string; text: string }) {
  return (
    <>
      <dt className="text-[11px] font-medium tracking-[.1em] text-faint uppercase">
        {label}
      </dt>
      <dd className="m-0 text-ink-dim">{text}</dd>
    </>
  );
}

/** Changed is one row of the diff: the statements of one kind of change,
 *  each by its kind and name; a dropped one struck through: it is no
 *  longer on the page. */
function Changed({
  label,
  refs,
  project,
  gone = false,
}: {
  label: string;
  refs: StatementRef[];
  project: boolean;
  gone?: boolean;
}) {
  if (refs.length === 0) return null;
  return (
    <>
      <dt className="text-[11px] font-medium tracking-[.1em] text-faint uppercase">
        {label}
      </dt>
      <dd className="m-0">
        <ul className="m-0 list-none p-0">
          {refs.map((r) => (
            <li key={r.id} className={gone ? "text-muted line-through" : ""}>
              <span className="mr-2 text-[11px] tracking-[.08em] text-faint uppercase">
                {kindLabel(r.kind, project)}
              </span>
              <span className={gone ? "" : "text-ink-dim"}>{r.name}</span>
            </li>
          ))}
        </ul>
      </dd>
    </>
  );
}
