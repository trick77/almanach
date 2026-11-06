import { Fragment, useEffect } from "react";
import type { CostMonth, CostSum, Costs } from "./api";
import { money } from "./format";
import { projectPath } from "./routing";
import { S } from "./strings";
import {
  Empty,
  Failed,
  Link,
  Skeleton,
  Tile,
  Tiles,
  column,
  eyebrow,
  h2,
  lede,
  page,
  table,
  tableBox,
  td,
  tdNum,
  th,
  thNum,
} from "./ui";
import { useFetch } from "./useFetch";

/** CostsPage is Almanach's own ledger: what its model calls cost by month
 *  and project, the merges apart from the runs and the briefs, with the day's spend
 *  against the budget. Read only. */
export function CostsPage() {
  const { data, failed } = useFetch<Costs>("costs");

  useEffect(() => {
    document.title = `${S.costs.title} ${S.common.sep} ${S.app}`;
  }, []);

  return (
    <div className={page}>
      <div className={column}>
        <h2 className={h2}>{S.costs.title}</h2>
        <p className={lede}>{S.costs.lede}</p>
        {failed ? (
          <Failed message={S.costs.failed} />
        ) : data === null ? (
          <Skeleton />
        ) : (
          <Body c={data} />
        )}
      </div>
    </div>
  );
}

function Body({ c }: { c: Costs }) {
  const spent = c.today.calls === 0 ? S.queue.idle : money(c.today.cost_usd);
  // Against the budget an amount always: no priced call yet is $0.00, as
  // the budget counts priced calls only.
  const againstBudget = money(c.today.cost_usd || "0");
  return (
    <>
      <Tiles>
        <Tile
          label={S.costs.tiles.today}
          value={spent}
          note={S.costs.calls(c.today.calls)}
        />
        <Tile
          label={S.costs.tiles.budget}
          value={c.budget_usd ? money(c.budget_usd) : S.queue.idle}
          note={
            c.budget_usd
              ? S.costs.tiles.budgetNote(againstBudget, money(c.budget_usd))
              : S.costs.tiles.noBudget
          }
        />
      </Tiles>
      {c.months.length === 0 ? (
        <div className="mt-7">
          <Empty>{S.costs.empty}</Empty>
        </div>
      ) : (
        c.months.map((m) => <Month key={m.month} m={m} />)
      )}
    </>
  );
}

/** monthName prints YYYY-MM as the month's German name and year. */
export function monthName(ym: string): string {
  const [y, m] = ym.split("-").map(Number);
  if (!y || !m) return ym;
  return new Date(Date.UTC(y, m - 1, 1)).toLocaleDateString("de-CH", {
    month: "long",
    year: "numeric",
    timeZone: "UTC",
  });
}

function Month({ m }: { m: CostMonth }) {
  const [hProject, hMerges, hRuns, hBriefs, hCalls, hCost] = S.costs.headers;
  return (
    <section data-testid={`month-${m.month}`}>
      <div className={eyebrow}>{monthName(m.month)}</div>
      <div className={tableBox}>
        <table className={table}>
          <thead>
            <tr>
              <th className={th}>{hProject}</th>
              <th className={thNum}>{hMerges}</th>
              <th className={thNum}>{hRuns}</th>
              <th className={thNum}>{hBriefs}</th>
              <th className={thNum}>{hCalls}</th>
              <th className={thNum}>{hCost}</th>
            </tr>
          </thead>
          <tbody>
            {m.projects.map((p) => (
              <tr key={p.project}>
                <td className={td}>
                  <Link to={projectPath(p.project)}>{p.project}</Link>
                </td>
                <td className={tdNum}>
                  <Sum s={p.merges} />
                </td>
                <td className={tdNum}>
                  <Sum s={p.runs} />
                </td>
                <td className={tdNum}>
                  <Sum s={p.briefs} />
                </td>
                <td className={tdNum}>{p.calls}</td>
                <td className={tdNum}>
                  {p.cost_usd ? money(p.cost_usd) : S.queue.idle}
                </td>
              </tr>
            ))}
            <tr className="font-medium text-ink">
              <td className={td}>{S.costs.monthTotal}</td>
              <td className={tdNum} />
              <td className={tdNum} />
              <td className={tdNum} />
              <td className={tdNum}>{m.calls}</td>
              <td className={tdNum}>
                {m.cost_usd ? money(m.cost_usd) : S.queue.idle}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  );
}

/** Sum is one kind's cost over its calls; unpriced calls are said, their
 *  cost being unknown rather than nothing. */
function Sum({ s }: { s: CostSum }) {
  if (s.calls === 0) return <span className="text-faint">{S.queue.idle}</span>;
  return (
    <Fragment>
      {s.cost_usd ? money(s.cost_usd) : S.queue.idle}
      <span className="block text-[12px] text-faint">
        {S.costs.calls(s.calls)}
        {s.unpriced > 0 && `, ${S.costs.unpriced(s.unpriced)}`}
      </span>
    </Fragment>
  );
}
