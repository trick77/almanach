import type { NoteSummary } from "./api";
import { changeLabel, changeTone, day } from "./format";
import { notePath } from "./routing";
import { S } from "./strings";
import { Chip, Empty, Pill, table, tableBox, td, tdNum, th, thNum } from "./ui";

/** NotesTable lists notes: PR, title, kind, item count, posted findings,
 *  merge day. The PR key is a chip that opens the note. */
export function NotesTable({
  projectKey,
  notes,
}: {
  projectKey: string;
  notes: NoteSummary[];
}) {
  if (notes.length === 0) return <Empty>{S.project.noNotes}</Empty>;
  const [hPR, hTitle, hKind, hItems, hPosted, hMerge] = S.noteHeaders;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{hPR}</th>
            <th className={th}>{hTitle}</th>
            <th className={th}>{hKind}</th>
            <th className={thNum}>{hItems}</th>
            <th className={thNum}>{hPosted}</th>
            <th className={th}>{hMerge}</th>
          </tr>
        </thead>
        <tbody>
          {notes.map((n) => (
            <tr key={n.pr_key}>
              <td className={td}>
                <Chip to={notePath(projectKey, n.repo_slug, n.pr_id)}>
                  {n.pr_key}
                </Chip>
              </td>
              <td className={td}>{n.pr_title}</td>
              <td className={td}>
                <Pill tone={changeTone(n.change_kind ?? "")}>
                  {changeLabel(n.change_kind ?? "")}
                </Pill>
              </td>
              <td className={tdNum}>{n.items}</td>
              <td className={tdNum}>
                {n.issues_posted} / {n.suggestions_posted}
              </td>
              <td className={td + " whitespace-nowrap"}>{day(n.merged_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
