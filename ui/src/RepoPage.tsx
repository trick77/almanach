import { useEffect } from "react";
import type { RepoDetail } from "./api";
import { DescriptionView, SourceChip } from "./Description";
import { NotesTable } from "./NotesTable";
import { day } from "./format";
import { projectPath } from "./routing";
import { S } from "./strings";
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

export function RepoPage({
  projectKey,
  slug,
}: {
  projectKey: string;
  slug: string;
}) {
  const { data, failed } = useFetch<RepoDetail>(
    `projects/${encodeURIComponent(projectKey)}/repos/${encodeURIComponent(slug)}`,
  );
  useEffect(() => {
    document.title = `${slug} ${S.common.sep} ${projectKey} ${S.common.sep} ${S.app}`;
  }, [projectKey, slug]);

  return (
    <div className={page}>
      <div className={column}>
        <Crumbs
          items={[
            { label: S.project.breadcrumbRoot, to: "/" },
            { label: projectKey, to: projectPath(projectKey) },
            { label: slug },
          ]}
        />
        {failed ? (
          <Failed what={S.repo.what} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body data={data} />
        )}
      </div>
    </div>
  );
}

function Body({ data }: { data: RepoDetail }) {
  const lastMerge = data.last_merge;
  const current = data.description?.revision ?? 0;
  return (
    <>
      <h2 className={h2}>{data.slug}</h2>
      <p className={meta}>
        {S.repo.project}{" "}
        <Link
          to={projectPath(data.project_key)}
          className="text-ink-dim hover:text-ink"
        >
          {data.project_key}
        </Link>
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

      <DescriptionView
        description={data.description}
        emptyHint={S.projects.firstDescription}
        pageRepo={data.slug}
      />

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

      <div className={eyebrow}>{S.repo.notes}</div>
      <NotesTable projectKey={data.project_key} notes={data.notes} />
    </>
  );
}
