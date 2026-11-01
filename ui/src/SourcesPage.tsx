import { useEffect, useState } from "react";
import {
  ApiError,
  send,
  type SourceDetail,
  type SourceKind,
  type SourceSummary,
  type Sources,
  type TokenAuth,
} from "./api";
import { dayTime } from "./format";
import { navigateTo, sourcePath } from "./routing";
import { S } from "./strings";
import { useFetch } from "./useFetch";
import {
  Chip,
  Empty,
  Failed,
  Link,
  Panel,
  Pill,
  Segmented,
  Skeleton,
  btn,
  btnPrimary,
  column,
  eyebrow,
  fieldHint,
  fieldLabel,
  h2,
  input,
  lede,
  mono,
  page,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
} from "./ui";

/** SourcesPage lists every source and takes a new one. */
export function SourcesPage() {
  const { data, failed, reload } = useFetch<Sources>("sources");
  const [adding, setAdding] = useState(false);
  useEffect(() => {
    document.title = `${S.sources.title} ${S.common.sep} ${S.app}`;
  }, []);

  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.sources.title}</h2>
        <p className={lede}>{S.sources.lede}</p>
        {failed ? (
          <Failed what={S.sources.what} />
        ) : data === null ? (
          <Skeleton />
        ) : !data.enabled ? (
          <Empty>{S.sources.disabled}</Empty>
        ) : (
          <>
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                className={btnPrimary}
                onClick={() => setAdding((a) => !a)}
              >
                {S.sources.newSource}
              </button>
            </div>
            {adding && (
              <NewSourceForm
                defaultHost={data.default_host}
                onDone={(created) => {
                  setAdding(false);
                  reload();
                  navigateTo(sourcePath(created.project_key));
                }}
                onCancel={() => setAdding(false)}
              />
            )}
            <div className={eyebrow}>{S.sources.title}</div>
            {data.sources.length === 0 ? (
              <Empty>{S.sources.empty}</Empty>
            ) : (
              <SourcesTable sources={data.sources} />
            )}
          </>
        )}
      </div>
    </div>
  );
}

function SourcesTable({ sources }: { sources: SourceSummary[] }) {
  const [hd0, hd1, hd2, hd3, hd4, hd5, hd6] = S.sources.headers;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{hd0}</th>
            <th className={th}>{hd1}</th>
            <th className={th}>{hd2}</th>
            <th className={th}>{hd3}</th>
            <th className={thNum}>{hd4}</th>
            <th className={th}>{hd5}</th>
            <th className={thNum}>{hd6}</th>
          </tr>
        </thead>
        <tbody>
          {sources.map((s) => (
            <tr key={s.project_key} className="hover:bg-active">
              <td className={tdMono}>
                <Link
                  to={sourcePath(s.project_key)}
                  className="text-ink hover:text-accent-strong"
                >
                  {s.project_key}
                </Link>
              </td>
              <td className={td}>
                <Chip to={null}>{S.sources.kind[s.kind]}</Chip>
              </td>
              <td className={tdMono}>{hostOf(s.host)}</td>
              <td className={td}>
                <Pill tone={s.has_token ? "none" : "quiet"}>
                  {s.has_token ? S.sources.tokenStored : S.sources.tokenNone}
                </Pill>
              </td>
              <td className={tdNum}>{s.repos}</td>
              <td className={td}>
                <StatePills s={s} />
              </td>
              <td className={tdNum}>{lastRun(s)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** lastRun is a Bitbucket source's last listing; a static one is never
 *  listed, so its run is the newest sync of its repositories. */
function lastRun(s: SourceSummary): string {
  const at = s.kind === "bitbucket" ? s.last_listed_at : s.last_synced_at;
  return at === null ? S.common.never : dayTime(at);
}

/** StatePills is a source's repositories counted by state, the zero
 *  counts left out; a failed listing stands in front. */
function StatePills({ s }: { s: SourceSummary }) {
  const pills = [];
  if (s.last_error !== "")
    pills.push(
      <Pill key="l" tone="fail" dot>
        {S.sources.listingFailed}
      </Pill>,
    );
  if (s.ready > 0)
    pills.push(
      <Pill key="r" tone="ok" dot>
        {S.sources.counts.ready(s.ready)}
      </Pill>,
    );
  if (s.active > 0)
    pills.push(
      <Pill key="a" tone="warn" dot>
        {S.sources.counts.active(s.active)}
      </Pill>,
    );
  if (s.failed > 0)
    pills.push(
      <Pill key="f" tone="fail" dot>
        {S.sources.counts.failed(s.failed)}
      </Pill>,
    );
  if (s.missing > 0)
    pills.push(
      <Pill key="m" tone="quiet" dot>
        {S.sources.counts.missing(s.missing)}
      </Pill>,
    );
  if (pills.length === 0)
    return <span className="text-faint">{S.sources.counts.none}</span>;
  return <div className="flex flex-wrap gap-1.5">{pills}</div>;
}

/** hostOf is a URL's host, for a dense table. */
export function hostOf(url: string): string {
  try {
    return url === "" ? "" : new URL(url).host;
  } catch {
    return url;
  }
}

/** NewSourceForm is the create form: the token is optional and comes
 *  first; how it travels and the username appear only once one is typed.
 *  The host starts as BACKEND_BITBUCKET_HOST and stays editable. */
function NewSourceForm({
  defaultHost,
  onDone,
  onCancel,
}: {
  defaultHost: string;
  onDone: (created: SourceDetail) => void;
  onCancel: () => void;
}) {
  const [kind, setKind] = useState<SourceKind>("bitbucket");
  const [key, setKey] = useState("");
  const [host, setHost] = useState(defaultHost);
  const [token, setToken] = useState("");
  const [auth, setAuth] = useState<TokenAuth>("bearer");
  const [user, setUser] = useState("");
  const [system, setSystem] = useState<"on" | "off">("on");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const f = S.sources.form;
  const sys = S.sources.system;

  const pickKind = (k: SourceKind) => {
    setKind(k);
    setAuth(k === "bitbucket" ? "bearer" : "basic");
  };

  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const created = await send<SourceDetail>("POST", "sources", {
        project_key: key.trim(),
        kind,
        host: kind === "bitbucket" ? host.trim() : "",
        token: token.trim(),
        token_auth: token.trim() === "" ? "" : auth,
        token_user: token.trim() !== "" && auth === "basic" ? user.trim() : "",
        // A Bitbucket project is always one; the API refuses false for it.
        system: kind === "bitbucket" || system === "on",
      });
      if (created) onDone(created);
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
        {S.sources.newSource}
      </h3>
      <div className="grid gap-x-4 gap-y-3 sm:grid-cols-2">
        <div>
          <span className={fieldLabel}>{f.kind}</span>
          <Segmented
            value={kind}
            onChange={pickKind}
            options={[
              { value: "bitbucket", label: S.sources.kindLabel.bitbucket },
              { value: "static", label: S.sources.kindLabel.static },
            ]}
          />
          <span className={fieldHint}>{S.sources.kindHint[kind]}</span>
        </div>
        <label>
          <span className={fieldLabel}>{f.slug}</span>
          <input
            className={input + " " + mono}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="SHOP"
          />
          <span className={fieldHint}>{f.slugHint}</span>
        </label>
        {kind === "bitbucket" && (
          <label className="sm:col-span-2">
            <span className={fieldLabel}>{f.host}</span>
            <input
              className={input}
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="https://bitbucket.example.invalid"
            />
            <span className={fieldHint}>{f.hostHint}</span>
          </label>
        )}
        {kind === "static" && (
          <div className="sm:col-span-2">
            <span className={fieldLabel}>{sys.label}</span>
            <Segmented
              value={system}
              onChange={setSystem}
              options={[
                { value: "on", label: sys.on },
                { value: "off", label: sys.off },
              ]}
            />
            <span className={fieldHint}>
              {system === "on" ? sys.hintOn : sys.createHintOff}{" "}
              {sys.createHint}
            </span>
          </div>
        )}
        <label className="sm:col-span-2">
          <span className={fieldLabel}>
            {f.token} <span className="text-faint">{f.optional}</span>
          </span>
          <input
            className={input}
            type="password"
            autoComplete="off"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder={f.tokenPlaceholder}
          />
          <span className={fieldHint}>{f.tokenHint[kind]}</span>
        </label>
        {token !== "" && (
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
        )}
        {token !== "" && auth === "basic" && (
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
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={btnPrimary}
          disabled={busy || key.trim() === ""}
          onClick={submit}
        >
          {busy ? f.saving : f.create}
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
