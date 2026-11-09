import type { Interface } from "./api";
import { repoPath } from "./routing";
import { S } from "./strings";
import { Link, mono, table, tableBox, td, tdMono, th } from "./ui";

// A repository's interfaces and a project's: read from the code by the
// poller, never a model's. Every operation listed; an interface with many
// starts folded, never cut.

const stack = "grid grid-cols-[minmax(0,1fr)] gap-y-4";
const note = "my-2 text-[13.5px] text-faint";

/** openUpTo is how many operations an interface may have and start open. */
const openUpTo = 60;

/** ifcName is an interface's name, or what stands for the routes code
 *  declares without a spec. */
function ifcName(a: Interface): string {
  return a.name === "" ? S.ifc.codeRoutes : a.name;
}

/** Block is one interface: its head (kind, name, version, base path,
 *  whose, files), its operations below. */
function Block({ a, owner }: { a: Interface; owner: boolean }) {
  return (
    <details
      open={a.endpoints.length <= openUpTo}
      className="group rounded border border-border-soft"
      data-testid="interface"
    >
      <summary className="flex cursor-pointer list-none flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2">
        <span
          aria-hidden
          className="inline-block w-3 text-faint transition-transform group-open:rotate-90"
        >
          ›
        </span>
        <span className="text-[12px] uppercase tracking-wide text-faint">
          {S.ifc.kinds[a.kind]}
        </span>
        <span
          className={
            a.name === "" ? "text-ink-dim" : mono + " text-[13.5px] text-ink"
          }
        >
          {ifcName(a)}
        </span>
        {a.version !== "" && (
          <span className={mono + " text-muted"}>{a.version}</span>
        )}
        {a.base_path !== "" && (
          <span className={mono + " text-muted"}>
            {S.ifc.base(a.base_path)}
          </span>
        )}
        {owner && (
          <Link
            to={repoPath(a.repo.project, a.repo.slug)}
            className={mono + " text-ink-dim hover:text-ink"}
          >
            {a.repo.slug}
          </Link>
        )}
        {a.direction === "declares" && (
          <span className="text-[12.5px] text-faint" title={S.ifc.declaresHint}>
            {S.ifc.declares}
          </span>
        )}
        <span className="text-[12.5px] text-faint">
          {S.ifc.operations(a.endpoints.length)}
        </span>
        <span
          className={mono + " min-w-0 truncate text-faint"}
          title={a.files.join("\n")}
        >
          {a.files.join(", ")}
        </span>
      </summary>
      {a.endpoints.length > 0 && (
        <div className={tableBox + " px-3 pb-2"}>
          <table className={table}>
            <thead>
              <tr>
                <th className={th + " w-[90px]"}>{S.ifc.method}</th>
                <th className={th}>{S.ifc.path}</th>
                <th className={th}>{S.ifc.summary}</th>
              </tr>
            </thead>
            <tbody>
              {a.endpoints.map((e) => (
                <tr key={`${e.method} ${e.path}`}>
                  <td className={tdMono}>{e.method}</td>
                  <td className={tdMono} title={e.files.join("\n")}>
                    {e.path}
                  </td>
                  <td className={td}>{e.summary}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </details>
  );
}

/** RepoInterfaces is a repository's section: what its code offers, then
 *  what it only declares, each in the store's order. */
export function RepoInterfaces({ apis }: { apis: Interface[] }) {
  const ordered = [
    ...apis.filter((a) => a.direction === "offers"),
    ...apis.filter((a) => a.direction !== "offers"),
  ];
  return (
    <div className={stack}>
      <p className={note}>{S.ifc.hint}</p>
      {ordered.map((a) => (
        <Block key={a.key} a={a} owner={false} />
      ))}
    </div>
  );
}

/** ProjectInterfaces is a project's section: what its repositories offer,
 *  each with its repository. */
export function ProjectInterfaces({ apis }: { apis: Interface[] }) {
  return (
    <div className={stack}>
      <p className={note}>{S.ifc.projectHint}</p>
      {apis.map((a) => (
        <Block key={a.key} a={a} owner={true} />
      ))}
    </div>
  );
}
