import type { Revision } from "./api";
import { SourceChip } from "./Description";
import { day } from "./format";
import { revisionPath } from "./routing";
import { S } from "./strings";
import { Empty, Link } from "./ui";

/** Revisions is a page's history as a list, newest first: each revision
 *  a link to its view (what it changed, the page as it stood), with the
 *  source that wrote it and its size; the current one marked. slug "" is
 *  a project's page. */
export function Revisions({
  projectKey,
  slug,
  revisions,
  current,
}: {
  projectKey: string;
  slug: string;
  revisions: Revision[];
  current: number;
}) {
  if (revisions.length === 0) return <Empty>{S.repo.noRevisions}</Empty>;
  return (
    <ol className="m-0 list-none p-0">
      {revisions.map((r) => (
        <li
          key={r.revision}
          className="flex items-baseline gap-3.5 border-b border-border-soft py-2 text-[13.5px] text-ink-dim"
        >
          <Link
            to={revisionPath(projectKey, slug, r.revision)}
            className="w-[34px] font-semibold text-ink-dim tabular-nums hover:text-ink hover:underline"
          >
            {r.revision}
          </Link>
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
  );
}
