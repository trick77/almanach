import { useEffect } from "react";
import type { RevisionDetail, StatementRef } from "./api";
import { DescriptionView, SourceChip, kindLabel } from "./Description";
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
  const changes = d.added.length + d.changed.length + d.dropped.length;
  return (
    <>
      <h2 className={h2}>{S.revision.of(d.revision, d.current)}</h2>
      <p className={meta}>
        {S.repo.revisionFrom}{" "}
        <SourceChip
          source={{
            kind: desc.source_kind,
            pr_key: desc.source_pr_key,
            commit: desc.source_commit,
          }}
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
              ‹ {S.revision.previous}
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
              {S.revision.next} ›
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
      {changes === 0 && !d.summary_changed ? (
        <Empty>{S.revision.nothing}</Empty>
      ) : (
        <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[13.5px]">
          {d.summary_changed && (
            <>
              <dt className="text-[11px] font-medium tracking-[.1em] text-faint uppercase">
                {S.rail.summary}
              </dt>
              <dd className="m-0 text-ink-dim">{S.revision.summaryChanged}</dd>
            </>
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
