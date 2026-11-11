import type { Problem } from "./api";
import { causeLabel, problemTone, shortDay } from "./format";
import { notePathFor } from "./routing";
import { S } from "./strings";
import {
  Chip,
  Empty,
  Pill,
  eyebrow,
  mono,
  table,
  tableBox,
  td,
  tdMono,
  tdNum,
  th,
  thNum,
} from "./ui";

/** ProblemsSection is the problem areas under their title and window, as
 *  a reading path's step shows them; nothing without one. first: it
 *  stands right under the step's title, no gap above. */
export function ProblemsSection({
  problems,
  windowDays,
  repoColumn = true,
  first = false,
}: {
  problems: Problem[];
  windowDays: number;
  repoColumn?: boolean;
  first?: boolean;
}) {
  if (problems.length === 0) return null;
  return (
    <>
      <div className={eyebrow.replace("mt-7", first ? "mt-0" : "mt-5")}>
        {S.project.problems}
        <span className="ml-2 font-normal tracking-normal normal-case text-faint">
          {S.project.problemsWindow(windowDays)}
        </span>
      </div>
      <Problems problems={problems} repoColumn={repoColumn} />
    </>
  );
}

/** Problems is the problem areas, bugfixes per shared directory: the
 *  project page's for every repository, a repository page's for its own,
 *  without the repository column. */
export function Problems({
  problems,
  repoColumn = true,
}: {
  problems: Problem[];
  repoColumn?: boolean;
}) {
  if (problems.length === 0) return <Empty>{S.project.noProblems}</Empty>;
  const [hRepo, hArea, hComp, hFixes, hCause, hPosted, hLast] =
    S.project.problemHeaders;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            {repoColumn && <th className={th}>{hRepo}</th>}
            <th className={th}>{hArea}</th>
            <th className={th}>{hComp}</th>
            <th className={thNum}>{hFixes}</th>
            <th className={th}>{hCause}</th>
            <th className={thNum}>{hPosted}</th>
            <th className={th}>{hLast}</th>
          </tr>
        </thead>
        <tbody>
          {problems.map((p) => (
            <tr key={p.repo_slug + "|" + p.area}>
              {repoColumn && <td className={tdMono}>{p.repo_slug}</td>}
              <td className={tdMono}>
                {p.area === "" ? S.project.rootArea : p.area}
              </td>
              <td className={td}>{p.component}</td>
              <td className={tdNum}>
                <Pill tone={problemTone(p.bugfixes)} dot>
                  {p.bugfixes}
                </Pill>
              </td>
              <td className={td}>
                {causeLabel(p.top_cause)}
                {p.bugfixes > 1 && (
                  <span className={"ml-1.5 text-faint " + mono}>
                    {p.top_cause_count}/{p.bugfixes}
                  </span>
                )}
              </td>
              <td className={tdNum}>
                {p.issues_posted} / {p.suggestions_posted}
              </td>
              <td className={td + " whitespace-nowrap"}>
                <Chip to={notePathFor(p.last_fix_pr_key)}>
                  {p.last_fix_pr_key}
                </Chip>{" "}
                <span className="text-faint">{shortDay(p.last_fix_at)}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
