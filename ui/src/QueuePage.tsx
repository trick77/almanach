import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import type { Queue, QueueItem, QueueRecent, QueueRun, RunStep } from "./api";
import { bytes, changeLabel, clock, dayTime, money, shortSHA } from "./format";
import { notePathFor, projectPath, repoPath } from "./routing";
import { S } from "./strings";
import {
  column,
  Empty,
  eyebrow,
  Failed,
  h2,
  Link,
  meta,
  mono,
  page,
  Sep,
  Skeleton,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
  Tile,
  Tiles,
} from "./ui";
import { useFetch } from "./useFetch";

/** pollEvery is how often the page refetches while anything is queued or
 *  a sync runs; idleEvery while nothing is. */
const pollEvery = 3000;
const idleEvery = 15000;

/** busy reports whether the queue has work or a sync runs. */
export function queueBusy(q: Queue | null): boolean {
  return (
    q !== null && (q.current !== null || q.next.length > 0 || !!q.sync?.running)
  );
}

/** QueuePage is every model call the worker has in hand: the one in flight,
 *  the rest in the order it takes them, why the next one waits, and what
 *  it finished lately. Read only. */
export function QueuePage() {
  const { data, failed } = useFetch<Queue>("queue", (d) =>
    queueBusy(d) ? pollEvery : idleEvery,
  );

  useEffect(() => {
    document.title = `${S.queue.title} ${S.common.sep} ${S.app}`;
  }, []);

  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.queue.title}</h2>
        {failed && !data && <Failed what={S.queue.what} />}
        {!failed && !data && <Skeleton />}
        {data && <Body q={data} />}
      </div>
    </div>
  );
}

/** useNow is the time, ticking every second, so a running call's age and
 *  a hold's countdown move between two fetches. */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return now;
}

/** Worker is what the cards say of the worker: how often it looks on its
 *  own, the attempt it gives an item up at. */
const Worker = createContext<{ poll: number; maxAttempts: number } | null>(
  null,
);

/** useWorker is Worker's value; the timeline always provides it, from the
 *  body, so there is no default to drift from the backend. */
function useWorker(): { poll: number; maxAttempts: number } {
  const w = useContext(Worker);
  if (w === null) throw new Error("useWorker outside Worker.Provider");
  return w;
}

function Body({ q }: { q: Queue }) {
  const now = useNow();
  const notes = q.next.filter((i) => i.kind === "note").length;
  const runs = q.next.filter((i) => i.kind === "run").length;
  const first = q.current ?? q.next[0];
  const hold = q.current === null ? q.next[0]?.wait : undefined;
  return (
    <>
      {queueBusy(q) && (
        <p className={meta}>{S.queue.polling(pollEvery / 1000)}</p>
      )}
      <div className="mt-5">
        <Tiles>
          <Tile
            label={S.queue.tiles.now}
            value={
              q.current
                ? q.current.run
                  ? S.queue.run(q.current.run.id)
                  : S.queue.note
                : S.queue.idle
            }
            note={
              q.current?.run
                ? stepNote(q.current.run)
                : q.current
                  ? q.current.pr_key
                  : hold
                    ? S.queue.wait[hold.reason]
                    : first
                      ? undefined
                      : S.queue.nothingToDo
            }
          />
          <Tile
            label={S.queue.tiles.waiting}
            value={q.next.length}
            note={S.queue.counts(notes, runs) || undefined}
          />
          <Tile
            label={S.queue.tiles.spent}
            value={q.spent.calls === 0 ? S.queue.idle : money(q.spent.cost_usd)}
            note={S.queue.calls(q.spent.calls)}
          />
          {q.sync && (
            <Tile
              label={S.queue.tiles.sync}
              value={q.sync.running ? S.queue.sync.running : S.queue.sync.ready}
              note={
                q.sync.running && q.sync.repo
                  ? S.queue.sync.progress(
                      q.sync.repo,
                      q.sync.done,
                      q.sync.total,
                    )
                  : q.sync.next_at
                    ? S.queue.sync.next(clock(q.sync.next_at))
                    : S.queue.sync.manual
              }
            />
          )}
        </Tiles>
      </div>

      {first ? (
        <Worker.Provider
          value={{ poll: q.poll_seconds, maxAttempts: q.max_attempts }}
        >
          <Timeline q={q} now={now} />
          {q.more > 0 && (
            <p className="mt-1 pl-7 text-[13px] text-muted">
              {S.queue.more(q.more)}
            </p>
          )}
        </Worker.Provider>
      ) : (
        <div className="mt-7">
          <Empty>
            {q.sync?.next_at && !q.sync.running
              ? S.queue.emptyNext(clock(q.sync.next_at))
              : S.queue.empty}
          </Empty>
        </div>
      )}

      <div className={eyebrow}>{S.queue.recent}</div>
      <Recent rows={q.recent} />
    </>
  );
}

/** stepNote is the tile's line for a run: which step of how many. */
function stepNote(run: QueueRun): string {
  const i = run.steps.findIndex((s) => s.state === "current");
  return S.queue.stepOf(i < 0 ? 1 : i + 1, run.steps.length);
}

/** The timeline: one line, a dot per item on it. */
function Timeline({ q, now }: { q: Queue; now: number }) {
  const startedRun = (it: QueueItem) =>
    it.run !== undefined && it.run.state !== "queued";
  return (
    <div className="relative mt-7 pl-7" data-testid="timeline">
      <div
        aria-hidden
        className="absolute top-2 bottom-2 left-[8px] w-[2px] bg-border"
      />
      {q.current && (
        <>
          <Lane>{S.queue.lane.now}</Lane>
          <Item item={q.current} dot="now" now={now} steps />
        </>
      )}
      {q.next.map((it, i) => {
        const firstWaiting = q.current === null && i === 0;
        return (
          <div key={itemKey(it)}>
            {firstWaiting && <Lane>{S.queue.lane.next}</Lane>}
            {((q.current !== null && i === 0) ||
              (q.current === null && i === 1)) && (
              <Lane>{S.queue.lane.after}</Lane>
            )}
            <Item
              item={it}
              dot={firstWaiting ? "next" : it.held_by ? "held" : "later"}
              now={now}
              steps={firstWaiting && startedRun(it)}
              continues={!firstWaiting && startedRun(it)}
            />
          </div>
        );
      })}
    </div>
  );
}

function itemKey(it: QueueItem): string {
  return it.run ? `run-${it.run.id}` : `note-${it.pr_key}`;
}

function Lane({ children }: { children: ReactNode }) {
  return (
    <div className="mt-4 mb-2 text-[11px] font-medium tracking-[.12em] text-faint uppercase first:mt-0">
      {children}
    </div>
  );
}

type Dot = "now" | "next" | "held" | "later";

const dotClass: Record<Dot, string> = {
  now: "border-accent-strong bg-accent-strong ring-4 ring-accent-dim",
  next: "border-ochre bg-bg",
  held: "border-dashed border-faint bg-bg",
  later: "border-faint bg-bg",
};

/** Item is one card on the timeline. A run in flight, or the started run
 *  that waits first, lists its steps; the started run further down says
 *  what of it is left. */
function Item({
  item,
  dot,
  now,
  steps = false,
  continues = false,
}: {
  item: QueueItem;
  dot: Dot;
  now: number;
  steps?: boolean;
  continues?: boolean;
}) {
  const run = item.run;
  return (
    <div className="relative mb-2.5" data-testid="queue-item">
      {/* Centred on the line: the line's centre is 9px into the padding,
          the dot 14px wide, the card 28px in. */}
      <span
        aria-hidden
        className={
          "absolute top-[14px] -left-[26px] h-3.5 w-3.5 rounded-full border-2 " +
          dotClass[dot]
        }
      />
      <div
        className={
          "rounded-ui border bg-panel px-3.5 py-3 " +
          (dot === "now" ? "border-accent-dim" : "border-border")
        }
      >
        <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <KindChip item={item} />
          <span className="text-[15px] text-ink">
            {run ? <Where item={item} /> : <NoteTitle item={item} />}
          </span>
          <span className="flex-1" />
          <span className="text-[12.5px] text-faint">
            {run
              ? run.cost_usd
                ? `${money(run.cost_usd)} ${S.queue.soFar}`
                : run.started_at
                  ? ""
                  : S.queue.queuedSince(clock(run.queued_at))
              : item.received_at
                ? S.queue.received(clock(item.received_at))
                : ""}
          </span>
        </div>
        <div className="mt-0.5 text-[12.5px] text-faint">
          {run ? (
            <RunMeta run={run} attempts={item.attempts} now={now} />
          ) : (
            <NoteMeta item={item} now={now} />
          )}
        </div>
        {item.wait && <WaitLine wait={item.wait} now={now} />}
        {item.held_by && (
          <div className="mt-2 rounded-ui-sm bg-active px-2.5 py-1.5 text-[13px] text-muted">
            {S.queue.heldBy(item.held_by)}
          </div>
        )}
        {run && steps && <Steps run={run} now={now} since={item.since} />}
        {run && continues && <Rest run={run} />}
      </div>
    </div>
  );
}

function KindChip({ item }: { item: QueueItem }) {
  const isRun = item.kind === "run";
  return (
    <span
      className={
        "rounded-full border px-2 py-px text-[11.5px] " +
        (isRun
          ? "border-accent-dim text-accent-strong"
          : "border-border text-muted")
      }
    >
      {item.run ? S.queue.run(item.run.id) : S.queue.note}
    </span>
  );
}

function Where({ item }: { item: QueueItem }) {
  return (
    <>
      <Link
        to={projectPath(item.project)}
        className="text-accent-strong hover:text-ink"
      >
        {item.project}
      </Link>
      <span className="text-muted"> / </span>
      <Link
        to={repoPath(item.project, item.repo)}
        className="text-accent-strong hover:text-ink"
      >
        {item.repo}
      </Link>
    </>
  );
}

function NoteTitle({ item }: { item: QueueItem }) {
  const to = item.pr_key ? notePathFor(item.pr_key) : null;
  return (
    <>
      {to ? (
        <Link to={to} className="text-accent-strong hover:text-ink">
          {item.pr_key}
        </Link>
      ) : (
        <span>{item.pr_key}</span>
      )}{" "}
      {item.title}
    </>
  );
}

function NoteMeta({ item, now }: { item: QueueItem; now: number }) {
  const { maxAttempts } = useWorker();
  const kind = item.change_kind ? changeLabel(item.change_kind) : "";
  return (
    <>
      <Where item={item} />
      {kind && (
        <>
          <Sep />
          {kind}
        </>
      )}
      {item.since && (
        <>
          <Sep />
          {S.queue.callRunning(since(item.since, now))}
        </>
      )}
      {item.attempts > 0 && (
        <>
          <Sep />
          <b className="font-medium text-muted">
            {S.queue.attempt(item.attempts, maxAttempts)}
          </b>
        </>
      )}
    </>
  );
}

function RunMeta({
  run,
  attempts,
  now,
}: {
  run: QueueRun;
  attempts: number;
  now: number;
}) {
  const { maxAttempts } = useWorker();
  return (
    <>
      <b className="font-medium text-muted">{S.queue.mode[run.mode]}</b>,{" "}
      {S.queue.trigger[run.trigger]}
      {run.commit && (
        <>
          <Sep />
          <span className={mono}>
            {S.queue.commit(
              run.mode === "reconcile" ? shortSHA(run.base_commit) : "",
              shortSHA(run.commit),
            )}
          </span>
        </>
      )}
      {run.started_at && (
        <>
          <Sep />
          {S.queue.started(clock(run.started_at), since(run.started_at, now))}
          <Sep />
          {S.queue.calls(run.calls)}
        </>
      )}
      {attempts > 0 && (
        <>
          <Sep />
          <b className="font-medium text-muted">
            {S.queue.attempt(attempts, maxAttempts)}
          </b>
        </>
      )}
    </>
  );
}

function WaitLine({
  wait,
  now,
}: {
  wait: NonNullable<QueueItem["wait"]>;
  now: number;
}) {
  const { poll } = useWorker();
  let when: string;
  if (wait.until) {
    when = S.queue.until(clock(wait.until), left(wait.until, now));
  } else {
    when = S.queue.soon(poll);
  }
  return (
    <div
      role="status"
      className="mt-2 rounded-ui-sm bg-ochre-wash px-2.5 py-1.5 text-[13px] text-ochre"
    >
      <span className="font-semibold text-ink">{when}</span>
      <Sep />
      {S.queue.wait[wait.reason]}
      {wait.since && <> ({S.queue.waitingSince(since(wait.since, now))})</>}
    </div>
  );
}

function stepName(s: RunStep): string {
  return s.kind === "subject" ? s.name : S.queue.step[s.kind];
}

function Steps({
  run,
  now,
  since: takenAt,
}: {
  run: QueueRun;
  now: number;
  since?: string;
}) {
  return (
    <ol className="mt-3 border-t border-border-soft" data-testid="steps">
      {run.steps.map((s, i) => (
        <li
          key={i}
          data-state={s.state}
          className={
            "grid grid-cols-[22px_1fr_auto] items-baseline gap-2 border-b border-border-soft py-1.5 last:border-b-0 " +
            (s.state === "current"
              ? "bg-gradient-to-r from-accent/10 to-transparent"
              : "")
          }
        >
          <span
            aria-hidden
            className={
              "text-center " +
              (s.state === "done"
                ? "text-online"
                : s.state === "current"
                  ? "text-accent-strong"
                  : "text-faint")
            }
          >
            {s.state === "done"
              ? "✓"
              : s.state === "skipped"
                ? "–"
                : s.state === "current"
                  ? "●"
                  : "○"}
          </span>
          <span
            className={
              s.state === "todo"
                ? "text-faint"
                : s.state === "current"
                  ? "text-ink"
                  : "text-ink-dim"
            }
          >
            {stepName(s)}
            <span className="block text-[12px] text-faint">
              {stepSub(s, now, takenAt)}
            </span>
          </span>
          <span className="text-[12.5px] whitespace-nowrap text-faint">
            {s.state === "current"
              ? S.queue.stepOf(i + 1, run.steps.length)
              : s.at
                ? clock(s.at)
                : ""}
            {s.cost_usd && (
              <>
                <Sep />
                <span className="font-mono text-muted">
                  {money(s.cost_usd)}
                </span>
              </>
            )}
          </span>
        </li>
      ))}
    </ol>
  );
}

/** stepSub is a step's second line: what it reads, why it was skipped,
 *  how long its call runs. */
function stepSub(s: RunStep, now: number, takenAt?: string): string {
  const parts: string[] = [];
  if (s.kind === "subject") parts.push(S.queue.files(s.files, bytes(s.bytes)));
  else parts.push(S.queue.stepSub[s.kind]);
  if (s.state === "skipped")
    parts.push(S.queue.skipped(S.baseline.skipReason[s.skip]));
  if (s.failed > 0) parts.push(S.queue.callsFailed(s.failed));
  if (s.state === "current" && takenAt)
    parts.push(S.queue.callRunning(since(takenAt, now)));
  return parts.join(" · ");
}

/** Rest is what is left of a started run further down the timeline. */
function Rest({ run }: { run: QueueRun }) {
  const left = run.steps.filter(
    (s) => s.state === "todo" || s.state === "current",
  );
  return (
    <div className="mt-1.5 text-[13px] text-muted">
      {S.queue.continues(left.length)}: {left.map(stepName).join(", ")}
    </div>
  );
}

function Recent({ rows }: { rows: QueueRecent[] }) {
  if (rows.length === 0) return <Empty>{S.queue.recentEmpty}</Empty>;
  const [hTime, hWhat, hResult, hCost] = S.queue.recentHeaders;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{hTime}</th>
            <th className={th}>{hWhat}</th>
            <th className={th}>{hResult}</th>
            <th className={thNum}>{hCost}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.kind + (r.run_id ?? r.pr_key)}>
              <td className={tdMono}>{clock(r.at)}</td>
              <td className={td}>
                {r.kind === "run" ? S.queue.run(r.run_id ?? 0) : r.pr_key}
                <span className="text-faint">
                  {" "}
                  {r.project} / {r.repo}
                </span>
              </td>
              <td className={td}>
                <RecentResult r={r} />
              </td>
              <td className={tdNum}>{r.cost_usd ? money(r.cost_usd) : "–"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function RecentResult({ r }: { r: QueueRecent }) {
  if (r.kind === "note") {
    const text = S.queue.noteOutcome[r.outcome ?? ""] ?? r.outcome;
    return (
      <span
        className={
          r.outcome === "ok"
            ? "text-online"
            : r.outcome
              ? "text-danger-ink"
              : "text-faint"
        }
      >
        {text}
      </span>
    );
  }
  if (r.state === "done")
    return (
      <span className="text-online">
        {S.queue.runDone}
        {r.skipped ? `, ${S.baseline.skipped(r.skipped)}` : ""}
      </span>
    );
  return (
    <>
      <span className="text-danger-ink">{S.queue.runFailed}</span> {r.error}
      {r.retry_at && (
        <span className="block text-[12px] text-faint">
          {S.queue.retryAt(dayTime(r.retry_at))}
        </span>
      )}
    </>
  );
}

/** since is how long ago iso was, as a duration. */
function since(iso: string, now: number): string {
  return duration(now - new Date(iso).getTime());
}

/** left is how long until iso. */
function left(iso: string, now: number): string {
  return duration(new Date(iso).getTime() - now);
}

/** duration prints ms the way a wait is read: 0:42 under ten minutes,
 *  then 12 min, then 1 h 5 min. */
export function duration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 600)
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  const m = Math.floor(s / 60);
  if (m < 60) return S.queue.minutes(m);
  return S.queue.hours(Math.floor(m / 60), m % 60);
}
