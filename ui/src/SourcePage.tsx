import { useEffect, useState } from "react";
import {
  ApiError,
  send,
  type SourceDetail,
  type SourceRepo,
  type TokenAuth,
} from "./api";
import {
  bytes,
  clock,
  day,
  dayTime,
  shortSHA,
  stateLabel,
  stateTone,
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

/** running reports whether a clone is in progress, which is when the page
 *  polls. */
function running(d: SourceDetail | null): number {
  return d?.repos.some((r) => r.state === "pending" || r.state === "syncing")
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
  const failed = data.repos.filter((r) => r.state === "failed").length;
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
      await send("POST", `${path}/sync`);
      window.setTimeout(() => setQueued(false), 1500);
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
          disabled={queued}
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
          {active > 0 && (
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
  removing,
  onAskRemove,
  onRemove,
}: {
  data: SourceDetail;
  onSync: (slug: string) => void;
  removing: string | null;
  onAskRemove: (slug: string | null) => void;
  onRemove: (slug: string) => void;
}) {
  const [hd0, hd1, hd2, hd3, hd4] = S.sources.repoHeaders;
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
  onAskRemove,
}: {
  r: SourceRepo;
  isStatic: boolean;
  onSync: () => void;
  onAskRemove: () => void;
}) {
  const running = r.state === "pending" || r.state === "syncing";
  const removable = isStatic || r.state === "missing";
  return (
    <>
      <tr>
        <td className={tdMono}>
          {r.slug}
          <div className="mt-0.5 text-[11.5px] text-faint">{r.clone_url}</div>
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
        <td className={td + " text-right whitespace-nowrap"}>
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
        </td>
      </tr>
      {r.state === "failed" && r.last_error !== "" && (
        <tr>
          <td
            colSpan={6}
            className="border-b border-border-soft py-1.5 text-[13px] text-danger-ink"
          >
            {r.slug}: {r.last_error}
          </td>
        </tr>
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
            <input
              className={input}
              type="password"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder={data.has_token ? f.newToken : f.tokenPlaceholder}
            />
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
