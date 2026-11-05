import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CostsPage, monthName } from "./CostsPage";
import type { Costs } from "./api";
import { S } from "./strings";

function serve(table: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const path = url.replace("/api/", "");
      if (!(path in table)) {
        return { ok: false, status: 404, json: async () => ({}) };
      }
      return { ok: true, status: 200, json: async () => table[path] };
    }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const costs: Costs = {
  months: [
    {
      month: "2026-11",
      cost_usd: "1.230",
      calls: 14,
      projects: [
        {
          project: "PLAT",
          cost_usd: "1.230",
          calls: 13,
          merges: { calls: 3, unpriced: 0, cost_usd: "0.030" },
          runs: { calls: 10, unpriced: 2, cost_usd: "1.200" },
        },
        {
          project: "ZED",
          calls: 1,
          merges: { calls: 1, unpriced: 1 },
          runs: { calls: 0, unpriced: 0 },
        },
      ],
    },
    {
      month: "2026-10",
      cost_usd: "0.500",
      calls: 4,
      projects: [
        {
          project: "PLAT",
          cost_usd: "0.500",
          calls: 4,
          merges: { calls: 0, unpriced: 0 },
          runs: { calls: 4, unpriced: 0, cost_usd: "0.500" },
        },
      ],
    },
  ],
  budget_usd: "5.000",
  today: { cost_usd: "1.284", calls: 23 },
};

describe("CostsPage", () => {
  it("lists each month's projects, merges apart from runs, with the day against the budget", async () => {
    serve({ costs });
    render(<CostsPage />);
    const nov = await screen.findByTestId("month-2026-11");
    expect(
      screen.getByRole("heading", { name: S.costs.title }),
    ).toBeInTheDocument();
    expect(within(nov).getByText(monthName("2026-11"))).toBeInTheDocument();
    expect(within(nov).getByRole("link", { name: "PLAT" })).toHaveAttribute(
      "href",
      "/projects/PLAT",
    );
    expect(within(nov).getByText("$0.03")).toBeInTheDocument();
    expect(
      within(nov).getByText(S.costs.unpriced(2), { exact: false }),
    ).toBeInTheDocument();
    expect(
      within(nov).getByText(S.costs.unpriced(1), { exact: false }),
    ).toBeInTheDocument();
    // The month's sum, and a project's cost shown as a dash where none was priced.
    const total = within(nov).getByText(S.costs.monthTotal).closest("tr")!;
    expect(total).toHaveTextContent("14");
    expect(total).toHaveTextContent("$1.23");
    expect(screen.getByTestId("month-2026-10")).toHaveTextContent("$0.50");
    // The tiles: today's spend and the budget.
    expect(screen.getByText("$1.28")).toBeInTheDocument();
    expect(screen.getByText("$5.00")).toBeInTheDocument();
    expect(
      screen.getByText(S.costs.tiles.budgetNote("$1.28", "$5.00")),
    ).toBeInTheDocument();
  });

  it("says when there is no call yet and no budget", async () => {
    serve({ costs: { months: [], today: { calls: 0 } } });
    render(<CostsPage />);
    expect(await screen.findByText(S.costs.empty)).toBeInTheDocument();
    expect(screen.getByText(S.costs.tiles.noBudget)).toBeInTheDocument();
  });

  it("says the page could not be loaded", async () => {
    serve({});
    render(<CostsPage />);
    expect(await screen.findByText(S.costs.failed)).toBeInTheDocument();
  });

  it("names a month in German", () => {
    expect(monthName("2026-11")).toBe("November 2026");
    expect(monthName("nope")).toBe("nope");
  });
});
