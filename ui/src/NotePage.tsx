import { useEffect } from "react";
import type { NoteDetail, NoteItem } from "./api";
import {
  causeLabel,
  changeLabel,
  changeTone,
  dayTime,
  money,
  share,
} from "./format";
import { notePath, projectPath, repoPath } from "./routing";
import { S, fmtInt } from "./strings";
import { useCanonical } from "./useCanonical";
import { useFetch } from "./useFetch";
import {
  Chip,
  Crumbs,
  Empty,
  Failed,
  Pill,
  Prose,
  Sep,
  Skeleton,
  Summary,
  Tile,
  Tiles,
  boxWidth,
  column,
  eyebrow,
  h2,
  meta,
  mono,
  page,
} from "./ui";

export function NotePage({
  projectKey,
  slug,
  pr,
}: {
  projectKey: string;
  slug: string;
  pr: number;
}) {
  const { data, failed } = useFetch<NoteDetail>(
    `projects/${encodeURIComponent(projectKey)}/repos/${encodeURIComponent(slug)}/notes/${pr}`,
  );
  useEffect(() => {
    document.title = `${projectKey}/${slug}#${pr} ${S.common.sep} ${S.app}`;
  }, [projectKey, slug, pr]);
  useCanonical(data ? notePath(data.project_key, data.repo_slug, pr) : null);

  return (
    <div className={page}>
      <div className={column}>
        <Crumbs
          items={[
            { label: S.project.breadcrumbRoot, to: "/" },
            { label: projectKey, to: projectPath(projectKey) },
            { label: slug, to: repoPath(projectKey, slug) },
            { label: `#${pr}` },
          ]}
        />
        {failed ? (
          <Failed what={S.note.what} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body n={data} />
        )}
      </div>
    </div>
  );
}

const LISTS: {
  key: "components" | "rules" | "invariants" | "entities";
  label: string;
}[] = [
  { key: "components", label: S.kinds.component },
  { key: "rules", label: S.kinds.rule },
  { key: "invariants", label: S.kinds.invariant },
  { key: "entities", label: S.kinds.entity },
];

function Body({ n }: { n: NoteDetail }) {
  const kind = n.change_kind ?? n.note?.change_kind ?? "";
  const bugfix = n.bugfix ?? n.note?.bugfix ?? null;
  const summary = n.note?.summary ?? null;
  const call = n.note_call;
  const hit = call ? share(call.cache_read_tokens, call.input_tokens) : null;
  return (
    <>
      <h2 className={h2}>{n.pr_title}</h2>
      <p className={meta}>
        <Chip to={null}>{n.pr_key}</Chip>
        <Sep />
        <Pill tone={changeTone(kind)}>{changeLabel(kind)}</Pill>
        {n.ticket_key && (
          <>
            <Sep />
            {S.note.ticket} <span className="text-ink-dim">{n.ticket_key}</span>
            {n.jira_issue_type && ` (${n.jira_issue_type})`}
          </>
        )}
        <Sep />
        {S.note.merge} {dayTime(n.merged_at)}
      </p>

      <div className={eyebrow}>{S.note.summary}</div>
      {summary ? <Summary text={summary} /> : <Empty>{S.note.noSummary}</Empty>}

      {bugfix && (
        <>
          <div className={eyebrow}>{S.note.bugfix}</div>
          <div
            className={
              boxWidth + " rounded-ui border border-border bg-panel px-4.5 py-4"
            }
          >
            <dl className="m-0 text-[13.5px]">
              <Fact label={S.note.symptom} aligned>
                {bugfix.symptom}
              </Fact>
              <Fact label={S.note.cause} aligned>
                {bugfix.cause}
              </Fact>
              <Fact label={S.note.causeKind} aligned last>
                <Pill tone="warn">{causeLabel(bugfix.cause_kind)}</Pill>
              </Fact>
            </dl>
          </div>
        </>
      )}

      {LISTS.map(({ key, label }) => {
        const items = n.note?.[key] ?? [];
        return (
          <section key={key}>
            <div className={eyebrow}>
              {label}
              {items.length === 0 && (
                <span className="ml-2 font-normal tracking-normal normal-case text-faint">
                  {S.note.noneOfKind}
                </span>
              )}
            </div>
            {items.map((it, i) => (
              <ItemBlock key={i} item={it} />
            ))}
          </section>
        );
      })}

      <div className={eyebrow}>{S.note.facts}</div>
      <dl
        className={
          boxWidth +
          " m-0 grid grid-cols-1 gap-x-10 text-[13.5px] sm:grid-cols-2"
        }
      >
        <Fact label={S.note.rounds}>{call ? call.rounds : S.common.none}</Fact>
        <Fact label={S.note.posted}>
          {n.issues_posted} / {n.suggestions_posted}
        </Fact>
        {n.reviewed_commit && (
          <Fact label={S.note.reviewedCommit}>
            <span className={mono}>{n.reviewed_commit}</span>
          </Fact>
        )}
        <Fact label={S.note.sourceCommit}>
          <span className={mono}>{n.source_commit}</span>
        </Fact>
        {n.merge_commit && (
          <Fact label={S.note.mergeCommit}>
            <span className={mono}>{n.merge_commit}</span>
          </Fact>
        )}
        {n.model_label && (
          <Fact label={S.note.model}>
            <span className={mono}>{n.model_label}</span>
          </Fact>
        )}
        <Fact label={S.note.received}>{dayTime(n.received_at)}</Fact>
        <Fact label={S.note.consolidated}>
          {n.consolidated_at === null ? (
            <span className="text-faint">{S.note.notYet}</span>
          ) : (
            dayTime(n.consolidated_at)
          )}
        </Fact>
      </dl>

      <div className={eyebrow}>
        {S.note.call}
        <span className="ml-2 font-normal tracking-normal normal-case text-faint">
          {S.note.callHint}
        </span>
      </div>
      {call === null ? (
        <Empty>{S.note.noCall}</Empty>
      ) : (
        <div className={boxWidth}>
          <Tiles>
            <Tile
              label={S.note.cacheHit}
              value={hit === null ? S.common.none : `${hit} %`}
              note={
                call.cache_read_tokens !== null && call.input_tokens !== null
                  ? S.note.cacheHitNote(
                      call.cache_read_tokens,
                      call.input_tokens,
                    )
                  : undefined
              }
            />
            <Tile
              label={S.note.output}
              value={
                call.output_tokens === null
                  ? S.common.none
                  : fmtInt(call.output_tokens)
              }
              note={S.note.outputNote(null)}
            />
            <Tile
              label={S.note.cost}
              value={money(call.cost_usd)}
              note={S.note.costNote}
            />
            <Tile
              label={S.note.allRounds}
              value={money(call.rounds_cost_usd)}
              note={S.note.allRoundsNote(call.rounds, call.rounds_input_tokens)}
            />
          </Tiles>
          <p className="mt-2.5 text-[12.5px] text-faint wrap-break-word">
            {[call.model, call.outcome, call.note_status]
              .filter((s) => s)
              .join(` ${S.common.sep} `)}
          </p>
        </div>
      )}
    </>
  );
}

/** Fact is a label/value row: a short value at the right edge, or, with
 *  `aligned`, every value of the box in one left-aligned column beside a
 *  label column: the bugfix box, whose symptom and cause are sentences and
 *  read badly right-ragged. A long token (a path, a SHA, a model label)
 *  breaks inside its row, never past the box, and the label never gives
 *  way to it. */
function Fact({
  label,
  last = false,
  aligned = false,
  children,
}: {
  label: string;
  last?: boolean;
  aligned?: boolean;
  children: React.ReactNode;
}) {
  const row = aligned
    ? // A phone stacks the label over the value: beside an 8.5rem label a
      // sentence had 168px.
      "grid grid-cols-1 gap-x-4 gap-y-0.5 sm:grid-cols-[8.5rem_minmax(0,1fr)]"
    : "flex justify-between gap-4";
  return (
    <div
      className={
        row + " py-[7px]" + (last ? "" : " border-b border-border-soft")
      }
    >
      <dt className={"text-muted " + (aligned ? "" : "shrink-0")}>{label}</dt>
      <dd
        className={
          "m-0 text-ink-dim wrap-break-word " +
          (aligned ? "" : "min-w-0 text-right")
        }
      >
        {children}
      </dd>
    </div>
  );
}

function ItemBlock({ item }: { item: NoteItem }) {
  return (
    <article className="border-t border-border-soft py-3 first:border-t-0">
      <h4 className="font-semibold text-ink">{item.name}</h4>
      <Prose
        text={item.text}
        className="mt-1 mb-1.5 font-serif text-[15.5px] leading-[1.55] text-ink-dim"
      />
      {(item.files ?? []).length > 0 && (
        <div className={mono + " text-muted"}>{item.files!.join(", ")}</div>
      )}
    </article>
  );
}
