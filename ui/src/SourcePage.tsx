import { useEffect, useState } from "react";
import {
  ApiError,
  send,
  type Baseline,
  type SourceDetail,
  type SourceRepo,
  type TokenAuth,
} from "./api";
import {
  bytes,
  clock,
  day,
  dayTime,
  money,
  shortSHA,
  stateLabel,
  stateTone,
  type Tone,
} from "./format";
import { navigateTo, sourcesPath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Chip,
  Crumbs,
  Empty,
  Failed,
  Panel,
  Pill,
  Segmented,
  Sep,
  Skeleton,
  Tile,
  Tiles,
  btn,
  btnDanger,
  btnPrimary,
  column,
  eyebrow,
  fieldHint,
  fieldLabel,
  h2,
  input,
  meta,
  mono,
  page,
  rowBtn,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
} from "./ui";

/** pollEvery is how often the page refetches while a clone runs. */
const pollEvery = 3000;

/** runOpen reports whether a baseline run is queued or under way. */
export function runOpen(b: Baseline | null): boolean {
  return b !== null && b.state !== "done" && b.state !== "failed";
}

/** running reports whether a clone or a baseline run is in progress, which
 *  is when the page polls. */
function running(d: SourceDetail | null): number {
  return d?.repos.some(
    (r) =>
      r.state === "pending" || r.state === "syncing" || runOpen(r.baseline),
  )
    ? pollEvery
    : 0;
}

export function SourcePage({ projectKey }: { projectKey: string }) {
  const { data, failed, reload } = useFetch<SourceDetail>(
    `sources/${encodeURIComponent(projectKey)}`,
    running,
  );
  useEffect(() => {
    document.title = `${projectKey} ${S.common.sep} ${S.sources.title} ${S.common.sep} ${S.app}`;
  }, [projectKey]);

  return (
    <div className={page}>
      <div className={column}>
        <Crumbs
          items={[
            { label: S.sources.title, to: sourcesPath },
            { label: projectKey },
          ]}
        />
        {failed && data === null ? (
          <Failed what={S.sources.whatOne} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body data={data} reload={reload} />
        )}
      </div>
    </div>
  );
}

type Open = "none" | "access" | "add" | "remove";

function Body({ data, reload }: { data: SourceDetail; reload: () => void }) {
  const [open, setOpen] = useState<Open>("none");
  const [queued, setQueued] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState("");
  const isStatic = data.kind === "static";
  const ready = data.repos.filter((r) => r.state === "ready").length;
  const active = data.repos.filter(
    (r) => r.state === "pending" || r.state === "syncing",
  ).length;
  const syncing = data.repos.some((r) => r.state === "syncing");
  const failed = data.repos.filter((r) => r.state === "failed").length;
  const describing = data.repos.filter((r) => runOpen(r.baseline)).length;
  const described = data.repos.filter(
    (r) => r.baseline !== null && r.baseline.documented_commit !== "",
  ).length;
  const lastSync = data.repos
    .map((r) => r.last_synced_at)
    .filter((t): t is string => t !== null)
    .sort()
    .at(-1);

  const act = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
      reload();
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : S.common.failed(S.sources.whatOne),
      );
    }
  };
  const path = `sources/${encodeURIComponent(data.project_key)}`;
  const sync = () =>
    act(async () => {
      setQueued(true);
      try {
        await send("POST", `${path}/sync`);
        window.setTimeout(() => setQueued(false), 1500);
      } catch (e) {
        setQueued(false);
        throw e;
      }
    });
  const removeSource = () =>
    act(async () => {
      await send("DELETE", path);
      navigateTo(sourcesPath);
    });

  return (
    <>
      <h2 className={h2}>{data.project_key}</h2>
      <p className={meta}>
        <Chip to={null}>{S.sources.kind[data.kind]}</Chip>
        <Sep />
        {isStatic ? (
          S.sources.manual
        ) : (
          <span className={mono}>{data.host}</span>
        )}
        <Sep />
        {data.has_token ? (
          <>
            {S.sources.tokenStored} (
            {S.sources.tokenAs(data.token_auth, data.token_user)})
          </>
        ) : (
          <Pill tone="quiet">{S.sources.tokenNone}</Pill>
        )}
        {!isStatic && (
          <>
            <Sep />
            {data.last_listed_at === null
              ? S.sources.neverListed
              : `${S.sources.listedAt} ${dayTime(data.last_listed_at)}`}
          </>
        )}
      </p>

      {data.last_error !== "" && (
        <Panel tone="danger">
          <b className="font-medium text-ink">{S.sources.listingError}</b>{" "}
          <span className={mono}>{data.last_error}</span>{" "}
          {S.sources.listingErrorHint}
        </Panel>
      )}

      <div className="mt-4">
        <Tiles>
          <Tile
            label={S.sources.tiles.repos}
            value={data.repos.length}
            note={S.sources.tiles.reposNote[data.kind]}
          />
          <Tile
            label={S.sources.tiles.ready}
            value={ready}
            note={[
              active > 0 ? S.sources.counts.active(active) : "",
              failed > 0 ? S.sources.counts.failed(failed) : "",
              described > 0 ? S.baseline.described(described) : "",
            ]
              .filter((s) => s !== "")
              .join(", ")}
          />
          <Tile
            label={S.sources.tiles.lastSync}
            value={lastSync ? clock(lastSync) : S.common.never}
            note={
              active > 0
                ? S.sources.tiles.running
                : lastSync
                  ? day(lastSync)
                  : undefined
            }
          />
          <Tile
            label={S.sources.tiles.disk}
            value={bytes(data.disk_bytes)}
            note={data.dir}
          />
        </Tiles>
      </div>

      <div className="mt-4 flex flex-wrap gap-2">
        <button
          type="button"
          className={btnPrimary}
          // A clone or fetch being worked holds the button. Pending does
          // not: a stuck queue would lock it for good. Nor does a baseline
          // run, which pins its commit.
          disabled={queued || syncing}
          title={syncing ? S.sources.actions.syncRunning : undefined}
          onClick={sync}
        >
          {queued ? S.sources.actions.queued : S.sources.actions.sync}
        </button>
        {isStatic && (
          <button
            type="button"
            className={btn}
            onClick={() => setOpen(open === "add" ? "none" : "add")}
          >
            {S.sources.actions.addRepo}
          </button>
        )}
        <button
          type="button"
          className={btn}
          onClick={() => setOpen(open === "access" ? "none" : "access")}
        >
          {S.sources.actions.access[data.kind]}
        </button>
        <button
          type="button"
          className={btnDanger}
          onClick={() => setOpen(open === "remove" ? "none" : "remove")}
        >
          {S.sources.actions.remove}
        </button>
        {error !== "" && (
          <span
            role="alert"
            className="self-center text-[13px] text-danger-ink"
          >
            {error}
          </span>
        )}
      </div>

      {open === "access" && (
        <AccessForm
          data={data}
          onDone={() => {
            setOpen("none");
            reload();
          }}
          onCancel={() => setOpen("none")}
        />
      )}
      {open === "add" && (
        <AddRepoForm
          data={data}
          onDone={() => {
            setOpen("none");
            reload();
          }}
          onCancel={() => setOpen("none")}
        />
      )}
      {open === "remove" && (
        <Panel tone="danger">
          {S.sources.confirmRemove(
            data.project_key,
            data.repos.length,
            data.dir,
          )}
          <div className="mt-2.5 flex gap-2">
            <button type="button" className={btnDanger} onClick={removeSource}>
              {S.sources.yesRemove}
            </button>
            <button
              type="button"
              className={btn}
              onClick={() => setOpen("none")}
            >
              {S.sources.form.cancel}
            </button>
          </div>
        </Panel>
      )}

      <div className={eyebrow + " flex items-baseline gap-3"}>
        {S.sources.repos}
        <span className="ml-auto font-normal tracking-normal normal-case">
          {S.sources.reposNote[data.kind]}
          {active + describing > 0 && (
            <>
              <Sep />
              {S.sources.polling}
            </>
          )}
        </span>
      </div>
      {data.repos.length === 0 ? (
        <Empty>{S.sources.noRepos[data.kind]}</Empty>
      ) : (
        <ReposTable
          data={data}
          onSync={(slug) =>
            act(() =>
              send("POST", `${path}/repos/${encodeURIComponent(slug)}/sync`),
            )
          }
          onDescribe={(slug, mode) =>
            act(() =>
              send(
                "POST",
                `${path}/repos/${encodeURIComponent(slug)}/describe`,
                {
                  mode,
                },
              ),
            )
          }
          removing={removing}
          onAskRemove={setRemoving}
          onRemove={(slug) =>
            act(async () => {
              await send("DELETE", `${path}/repos/${encodeURIComponent(slug)}`);
              setRemoving(null);
            })
          }
        />
      )}
      {!isStatic && <Empty>{S.sources.listedOnly}</Empty>}
    </>
  );
}

function ReposTable({
  data,
  onSync,
  onDescribe,
  removing,
  onAskRemove,
  onRemove,
}: {
  data: SourceDetail;
  onSync: (slug: string) => void;
  onDescribe: (slug: string, mode: "reconcile" | "full") => Promise<void>;
  removing: string | null;
  onAskRemove: (slug: string | null) => void;
  onRemove: (slug: string) => void;
}) {
  const [hd0, hd1, hd2, hd3, hd4, hd5] = S.sources.repoHeaders;
  const rows = [...data.repos].sort((a, b) => a.slug.localeCompare(b.slug));
  return (
    <>
      <div className={tableBox}>
        <table className={table}>
          <thead>
            <tr>
              <th className={th}>{hd0}</th>
              <th className={th}>{hd1}</th>
              <th className={th}>{hd2}</th>
              <th className={th}>{hd3}</th>
              <th className={th + " text-right"}>{hd4}</th>
              <th className={th}>{hd5}</th>
              <th className={th}></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <RepoRow
                key={r.slug}
                r={r}
                isStatic={data.kind === "static"}
                onSync={() => onSync(r.slug)}
                onDescribe={(mode) => onDescribe(r.slug, mode)}
                onAskRemove={() => onAskRemove(r.slug)}
              />
            ))}
          </tbody>
        </table>
      </div>
      {removing !== null && (
        <Panel tone="danger">
          {S.sources.confirmRemoveRepo(removing, `${data.dir}/${removing}`)}
          <div className="mt-2.5 flex gap-2">
            <button
              type="button"
              className={btnDanger}
              onClick={() => onRemove(removing)}
            >
              {S.sources.yesRemove}
            </button>
            <button
              type="button"
              className={btn}
              onClick={() => onAskRemove(null)}
            >
              {S.sources.form.cancel}
            </button>
          </div>
        </Panel>
      )}
    </>
  );
}

function RepoRow({
  r,
  isStatic,
  onSync,
  onDescribe,
  onAskRemove,
}: {
  r: SourceRepo;
  isStatic: boolean;
  onSync: () => void;
  onDescribe: (mode: "reconcile" | "full") => Promise<void>;
  onAskRemove: () => void;
}) {
  const running = r.state === "pending" || r.state === "syncing";
  const removable = isStatic || r.state === "missing";
  const documented = r.baseline !== null && r.baseline.documented_commit !== "";
  const [sending, setSending] = useState(false);
  const noRun = sending || runOpen(r.baseline) || r.head_commit === "";
  // A reconcile of a head already documented would read nothing.
  const upToDate =
    documented && r.baseline?.documented_commit === r.head_commit;
  const describe = async (mode: "reconcile" | "full") => {
    setSending(true);
    try {
      await onDescribe(mode);
    } finally {
      setSending(false);
    }
  };
  return (
    <>
      <tr>
        <td className={tdMono}>
          {r.slug}
          <div
            className="mt-0.5 max-w-[220px] truncate text-[11.5px] text-faint"
            title={r.clone_url}
          >
            {r.clone_url}
          </div>
        </td>
        <td className={tdMono}>{r.branch === "" ? "—" : r.branch}</td>
        <td className={tdMono}>
          {r.head_commit === "" ? "—" : shortSHA(r.head_commit)}
        </td>
        <td className={td}>
          <Pill tone={stateTone(r.state)} dot>
            {stateLabel(r.state)}
          </Pill>
        </td>
        <td className={tdNum}>
          {r.last_synced_at === null ? "—" : dayTime(r.last_synced_at)}
        </td>
        <td className={td}>
          <BaselineCell b={r.baseline} />
        </td>
        <td className={td + " text-right"}>
          {/* Stacked: four actions side by side push the table past the
              column on a source with long clone URLs. */}
          <div className="flex flex-col items-end gap-0.5 whitespace-nowrap">
            {r.state !== "missing" && (
              <button
                type="button"
                className={rowBtn}
                disabled={running}
                onClick={onSync}
              >
                {r.state === "failed"
                  ? S.sources.repoActions.retry
                  : S.sources.repoActions.sync}
              </button>
            )}
            {r.state !== "missing" && (
              <button
                type="button"
                className={rowBtn}
                disabled={noRun || upToDate}
                title={upToDate ? S.baseline.upToDate : undefined}
                onClick={() => describe("reconcile")}
              >
                {documented
                  ? S.baseline.actions.reconcile
                  : S.baseline.actions.describe}
              </button>
            )}
            {r.state !== "missing" && documented && (
              <button
                type="button"
                className={rowBtn}
                disabled={noRun}
                onClick={() => describe("full")}
              >
                {S.baseline.actions.full}
              </button>
            )}
            {removable && (
              <button
                type="button"
                className={rowBtn + " hover:text-danger-ink"}
                onClick={onAskRemove}
              >
                {r.state === "missing"
                  ? S.sources.repoActions.removeClone
                  : S.sources.repoActions.remove}
              </button>
            )}
          </div>
        </td>
      </tr>
      {r.state === "failed" && r.last_error !== "" && (
        <tr>
          <td
            colSpan={7}
            className="border-b border-border-soft py-1.5 text-[13px] text-danger-ink"
          >
            {r.slug}: {r.last_error}
          </td>
        </tr>
      )}
      {r.baseline !== null &&
        r.baseline.state === "failed" &&
        r.baseline.error !== "" && (
          <tr>
            <td
              colSpan={7}
              className="border-b border-border-soft py-1.5 text-[13px] text-danger-ink"
            >
              {r.slug}: {S.baseline.failedRun} {r.baseline.error}
            </td>
          </tr>
        )}
    </>
  );
}

/** baselineState is a run's state as the column shows it: done with
 *  skipped parts is partial. */
function baselineState(b: Baseline): { label: string; tone: Tone } {
  switch (b.state) {
    case "done":
      return b.skipped > 0
        ? { label: S.baseline.state.partial, tone: "warn" }
        : { label: S.baseline.state.done, tone: "ok" };
    case "failed":
      return { label: S.baseline.state.failed, tone: "fail" };
    case "describing":
      return {
        label: S.baseline.state.describing(
          // The worker saves the last step before it moves on to the project:
          // the count never runs past its total.
          Math.min(b.step + 1, Math.max(b.steps - 1, 1)),
          Math.max(b.steps - 1, 1),
        ),
        tone: "warn",
      };
    default:
      return { label: S.baseline.state[b.state], tone: "warn" };
  }
}

/** BaselineCell is a repository's description state: the newest run, the
 *  commit last documented, what the run cost, when it ended. */
function BaselineCell({ b }: { b: Baseline | null }) {
  if (b === null) {
    return (
      <Pill tone="quiet" dot>
        {S.baseline.state.none}
      </Pill>
    );
  }
  const { label, tone } = baselineState(b);
  const open = runOpen(b);
  const sha =
    b.documented_commit !== "" ? b.documented_commit : open ? b.commit : "";
  const parts = [
    sha !== "" ? S.baseline.at(shortSHA(sha)) : "",
    b.cost_usd !== undefined
      ? open
        ? `${money(b.cost_usd)} ${S.baseline.soFar}`
        : money(b.cost_usd)
      : "",
    !open && b.finished_at !== null ? day(b.finished_at) : "",
    b.state === "done" && b.skipped > 0 ? S.baseline.skipped(b.skipped) : "",
  ].filter((s) => s !== "");
  return (
    <>
      <Pill tone={tone} dot>
        {label}
      </Pill>
      {parts.length > 0 && (
        <div className="mt-0.5 max-w-[190px] text-[11.5px] text-faint">
          {parts.join(` ${S.common.sep} `)}
        </div>
      )}
    </>
  );
}

/** AccessForm edits the host (Bitbucket) and the token: an omitted token
 *  keeps the stored one, so the field starts as "Hinterlegt" with Ersetzen
 *  and Löschen rather than an input. */
function AccessForm({
  data,
  onDone,
  onCancel,
}: {
  data: SourceDetail;
  onDone: () => void;
  onCancel: () => void;
}) {
  const f = S.sources.form;
  const [host, setHost] = useState(data.host);
  const [auth, setAuth] = useState<TokenAuth>(data.token_auth);
  const [user, setUser] = useState(data.token_user);
  // keep sends no token (the stored one stays); replace sends the typed
  // one; clear sends "" (the source becomes public).
  const [mode, setMode] = useState<"keep" | "replace" | "clear">(
    data.has_token ? "keep" : "replace",
  );
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const body: Record<string, string> = {
        token_auth: auth,
        token_user: auth === "basic" ? user.trim() : "",
      };
      if (data.kind === "bitbucket") body.host = host.trim();
      // An empty replacement keeps the stored token: only Löschen clears.
      if (mode === "replace" && token.trim() !== "") body.token = token.trim();
      if (mode === "clear") body.token = "";
      await send(
        "PATCH",
        `sources/${encodeURIComponent(data.project_key)}`,
        body,
      );
      onDone();
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : S.common.failed(S.sources.whatOne),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel>
      <h3 className="mb-3 font-serif text-[17px] font-medium text-ink">
        {S.sources.actions.access[data.kind]}
      </h3>
      <div className="grid gap-x-4 gap-y-3 sm:grid-cols-2">
        {data.kind === "bitbucket" && (
          <label className="sm:col-span-2">
            <span className={fieldLabel}>{f.host}</span>
            <input
              className={input}
              value={host}
              onChange={(e) => setHost(e.target.value)}
            />
          </label>
        )}
        <div>
          <span className={fieldLabel}>{f.tokenAs}</span>
          <Segmented
            value={auth}
            onChange={setAuth}
            options={[
              { value: "bearer", label: "Bearer" },
              { value: "basic", label: "Basic" },
            ]}
          />
          <span className={fieldHint}>
            {auth === "bearer" ? f.bearerHint : f.basicHint}
          </span>
        </div>
        {auth === "basic" && (
          <label>
            <span className={fieldLabel}>{f.user}</span>
            <input
              className={input}
              value={user}
              onChange={(e) => setUser(e.target.value)}
              placeholder="x-access-token"
            />
            <span className={fieldHint}>{f.userHint}</span>
          </label>
        )}
        <div className="sm:col-span-2">
          <span className={fieldLabel}>{f.token}</span>
          {mode === "keep" ? (
            <div className="flex flex-wrap items-center gap-2">
              <Pill tone="none">{S.sources.tokenStored}</Pill>
              <button
                type="button"
                className={btn}
                onClick={() => setMode("replace")}
              >
                {f.replace}
              </button>
              <button
                type="button"
                className={btn}
                onClick={() => setMode("clear")}
              >
                {f.clear}
              </button>
              <span className="text-[12px] text-faint">{f.storedHint}</span>
            </div>
          ) : mode === "clear" ? (
            <div className="flex items-center gap-2">
              <Pill tone="quiet">{S.sources.tokenNone}</Pill>
              <button
                type="button"
                className={btn}
                onClick={() => setMode("keep")}
              >
                {f.cancel}
              </button>
            </div>
          ) : (
            <>
              <input
                className={input}
                type="password"
                autoComplete="off"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder={data.has_token ? f.newToken : f.tokenPlaceholder}
              />
              <span className={fieldHint}>{f.tokenHint[data.kind]}</span>
            </>
          )}
        </div>
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={btnPrimary}
          disabled={busy}
          onClick={submit}
        >
          {busy ? f.saving : f.save}
        </button>
        <button type="button" className={btn} onClick={onCancel}>
          {f.cancel}
        </button>
        {error !== "" && (
          <span role="alert" className="text-[13px] text-danger-ink">
            {error}
          </span>
        )}
      </div>
    </Panel>
  );
}

function AddRepoForm({
  data,
  onDone,
  onCancel,
}: {
  data: SourceDetail;
  onDone: () => void;
  onCancel: () => void;
}) {
  const f = S.sources.addForm;
  const [slug, setSlug] = useState("");
  const [url, setUrl] = useState("");
  const [branch, setBranch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      await send(
        "POST",
        `sources/${encodeURIComponent(data.project_key)}/repos`,
        {
          slug: slug.trim(),
          clone_url: url.trim(),
          branch: branch.trim(),
        },
      );
      onDone();
    } catch (e) {
      setError(
        e instanceof ApiError ? e.message : S.common.failed(S.sources.whatOne),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel>
      <h3 className="mb-3 font-serif text-[17px] font-medium text-ink">
        {f.title}
      </h3>
      <div className="grid gap-x-4 gap-y-3 sm:grid-cols-2">
        <label>
          <span className={fieldLabel}>{f.slug}</span>
          <input
            className={input + " " + mono}
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder="acme-cli"
          />
          <span className={fieldHint}>{f.slugHint(data.dir)}</span>
        </label>
        <label>
          <span className={fieldLabel}>{f.branch}</span>
          <input
            className={input + " " + mono}
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            placeholder={f.branchPlaceholder}
          />
          <span className={fieldHint}>{f.branchHint}</span>
        </label>
        <label className="sm:col-span-2">
          <span className={fieldLabel}>{f.url}</span>
          <input
            className={input + " " + mono}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://github.com/acme/acme-cli.git"
          />
          <span className={fieldHint}>{f.urlHint}</span>
        </label>
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={btnPrimary}
          disabled={busy || slug.trim() === "" || url.trim() === ""}
          onClick={submit}
        >
          {busy ? S.sources.form.saving : f.add}
        </button>
        <button type="button" className={btn} onClick={onCancel}>
          {S.sources.form.cancel}
        </button>
        {error !== "" && (
          <span role="alert" className="text-[13px] text-danger-ink">
            {error}
          </span>
        )}
      </div>
    </Panel>
  );
}
