import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Stale } from "./api";
import { StaleHint } from "./StaleHint";
import { S } from "./strings";

function hint(stale: Stale | null, scope: "repo" | "project" = "repo") {
  return render(<StaleHint stale={stale} scope={scope} projectKey="tools" />);
}

describe("StaleHint", () => {
  it("says nothing on a page at today's rules", () => {
    const { container } = hint(null);
    expect(container).toBeEmptyDOMElement();
  });

  it("names the rule level and what it brought", () => {
    hint({ epoch: 2, at: 3, state: "running" });
    expect(screen.getByRole("status")).toHaveTextContent(S.stale.title);
    expect(screen.getByRole("status")).toHaveTextContent(
      S.stale.rule("repo", 3),
    );
    expect(screen.getByText(S.stale.running)).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("says when it happens on its own, and links the queue", () => {
    hint({ epoch: 2, at: 3, state: "waiting", rank: 4, eta_seconds: 2400 });
    expect(
      screen.getByText(S.stale.waiting("in etwa 40 Minuten", 4), {
        exact: false,
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", "/queue");
    hint({ epoch: 2, at: 3, state: "waiting", rank: 2 });
    expect(
      screen.getByText(S.stale.waitingNoEta(2), { exact: false }),
    ).toBeInTheDocument();
  });

  it.each([
    ["queued", S.stale.queued, S.stale.toQueue, "/queue"],
    ["paused", S.stale.paused, S.stale.resume, "/queue"],
    ["budget", S.stale.budget, S.stale.toQueue, "/queue"],
  ] as const)(
    "%s: what holds it and where to look",
    (state, text, label, to) => {
      hint({ epoch: 2, at: 3, state });
      expect(screen.getByText(text, { exact: false })).toBeInTheDocument();
      expect(screen.getByRole("link", { name: `${label} →` })).toHaveAttribute(
        "href",
        to,
      );
    },
  );

  it.each(["off", "failed"] as const)(
    "manual (%s): the source page's button",
    (why) => {
      hint({ epoch: 2, at: 3, state: "manual", why }, "project");
      expect(
        screen.getByText(S.stale.manual[why], { exact: false }),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("link", { name: `${S.stale.toSource} →` }),
      ).toHaveAttribute("href", "/sources/tools");
    },
  );
});

describe("S.stale.eta", () => {
  it.each([
    [20, "in etwa einer Minute"],
    [2400, "in etwa 40 Minuten"],
    [3600, "in etwa einer Stunde"],
    [5 * 3600, "in etwa 5 Stunden"],
    [72 * 3600, "in etwa 3 Tagen"],
  ])("%i s: %s", (sec, want) => {
    expect(S.stale.eta(sec)).toBe(want);
  });
});
