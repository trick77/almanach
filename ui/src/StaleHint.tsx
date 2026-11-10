import type { Stale } from "./api";
import { queuePath, sourcePath } from "./routing";
import { S } from "./strings";
import { Link } from "./ui";

/** StaleHint tells a reader the page still follows older rules, and what
 *  comes next: on its own and about when, or what to do. One box under the
 *  header, never a mark over the text: a restyle changes only the
 *  statements a rule breaks, never the whole page (user, 2026-11-10). The
 *  wiki stays read-only, so it links where the action is. */
export function StaleHint({
  stale,
  scope,
  projectKey,
}: {
  stale: Stale | null;
  scope: "repo" | "project";
  projectKey: string;
}) {
  if (stale === null) {
    return null;
  }
  let next: string;
  let link: { to: string; label: string } | null = null;
  switch (stale.state) {
    case "running":
      next = S.stale.running;
      break;
    case "queued":
      next = S.stale.queued;
      link = { to: queuePath, label: S.stale.toQueue };
      break;
    case "waiting":
      next =
        stale.eta_seconds !== undefined
          ? S.stale.waiting(S.stale.eta(stale.eta_seconds), stale.rank ?? 1)
          : S.stale.waitingNoEta(stale.rank ?? 1);
      link = { to: queuePath, label: S.stale.toQueue };
      break;
    case "paused":
      next = S.stale.paused;
      link = { to: queuePath, label: S.stale.resume };
      break;
    case "budget":
      next = S.stale.budget;
      link = { to: queuePath, label: S.stale.toQueue };
      break;
    case "manual":
      next = S.stale.manual[stale.why ?? "off"] ?? S.stale.manual.off;
      link = { to: sourcePath(projectKey), label: S.stale.toSource };
      break;
  }
  return (
    <div
      role="status"
      className="mt-3 rounded-ui border border-ochre bg-ochre-wash px-3.5 py-2.5 text-[13px] leading-relaxed"
    >
      <div className="font-medium text-ochre">{S.stale.title}</div>
      <div className="text-ink-dim">{S.stale.rule(scope, stale.at)}</div>
      <div className="text-muted">
        {next}
        {link && (
          <>
            {" "}
            <Link
              to={link.to}
              className="font-medium text-ochre hover:text-ink"
            >
              {link.label} →
            </Link>
          </>
        )}
      </div>
    </div>
  );
}
