import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Kind, Statement } from "./api";
import {
  AskChips,
  EntryMore,
  EntryPath,
  EntryStatement,
  NextCard,
  StandLine,
  askKinds,
  pick,
} from "./Entry";
import { S } from "./strings";

/** st is a statement of a kind with n sources. */
function st(id: string, kind: Kind, n: number): Statement {
  return {
    id,
    kind,
    name: `Name ${id}`,
    text: `Text ${id}.`,
    files: [],
    sources: Array.from({ length: n }, (_, i) => ({
      pr_key: `PAY/pay-api#${i + 1}`,
    })),
  };
}

describe("pick", () => {
  const all = [
    st("a", "rule", 1),
    st("b", "invariant", 3),
    st("c", "component", 9),
    st("d", "rule", 3),
    st("e", "invariant", 0),
    st("f", "rule", 2),
  ];

  it("takes the kinds asked, most sources first, the page's order on a tie", () => {
    expect(pick(all, askKinds.guard, 3).map((s) => s.id)).toEqual([
      "b",
      "d",
      "f",
    ]);
  });

  it("takes no more than n, all when fewer", () => {
    expect(pick(all, askKinds.guard, 10).map((s) => s.id)).toEqual([
      "b",
      "d",
      "f",
      "a",
      "e",
    ]);
    expect(pick(all, ["pitfall"], 3)).toEqual([]);
  });

  it("leaves the page's own order alone", () => {
    const copy = [...all];
    pick(all, askKinds.guard, 3);
    expect(all).toEqual(copy);
  });
});

describe("AskChips", () => {
  it("counts each question's statements and leaves out one with none", () => {
    const onChange = vi.fn();
    render(
      <AskChips
        statements={[
          st("a", "rule", 0),
          st("b", "invariant", 0),
          st("c", "pitfall", 0),
          st("d", "component", 0),
        ]}
        value="all"
        onChange={onChange}
      />,
    );
    const group = screen.getByRole("group", { name: S.ask.label });
    const chips = within(group).getAllByRole("button");
    expect(chips.map((b) => b.textContent)).toEqual([
      `${S.ask.all}4`,
      `${S.ask.guard}2`,
      `${S.ask.hurt}1`,
    ]);
    expect(chips[0]).toHaveAttribute("aria-pressed", "true");
    expect(chips[1]).toHaveAttribute("aria-pressed", "false");
    fireEvent.click(chips[2]!);
    expect(onChange).toHaveBeenCalledWith("hurt");
  });

  it("is not there when no question has a statement", () => {
    const { container } = render(
      <AskChips
        statements={[st("a", "component", 0), st("b", "entity", 0)]}
        value="all"
        onChange={() => {}}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("EntryPath", () => {
  it("numbers the steps it is given, each a section with its id", () => {
    const onAsk = vi.fn();
    render(
      <EntryPath
        steps={[
          { id: "what", title: "Eins", body: <p>erster</p> },
          {
            id: "guard",
            title: "Zwei",
            body: <p>zweiter</p>,
            more: { label: S.path.allGuards(5), ask: "guard" },
          },
        ]}
        onAsk={onAsk}
      />,
    );
    const headings = screen.getAllByRole("heading", { level: 3 });
    expect(headings.map((h) => h.textContent)).toEqual([
      S.path.step(1, "Eins"),
      S.path.step(2, "Zwei"),
    ]);
    const guard = screen.getByText("zweiter").closest("section")!;
    expect(guard.id).toBe("guard");
    // A step's more is its last line, a button that asks its question.
    fireEvent.click(within(guard).getByRole("button"));
    expect(onAsk).toHaveBeenCalledWith("guard");
    expect(
      within(screen.getByText("erster").closest("section")!).queryByRole(
        "button",
      ),
    ).toBeNull();
  });
});

describe("EntryStatement", () => {
  it("names the kind, links the name to its place and shows the text's start", () => {
    render(
      <EntryStatement
        statement={{
          ...st("s7", "invariant", 1),
          text: "Erst `a`.\n\nDann b.",
        }}
        to="/projects/PAY#description/s7"
        project
      />,
    );
    expect(screen.getByText("Invariante")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Name s7" }).getAttribute("href"),
    ).toBe("/projects/PAY#description/s7");
    expect(screen.getByText("a").tagName).toBe("CODE");
    expect(screen.getByText(/Dann b\./)).toHaveClass("line-clamp-3");
  });
});

describe("EntryMore, NextCard, StandLine", () => {
  it("offers the rest as a button", () => {
    const onClick = vi.fn();
    render(<EntryMore label={S.path.allGuards(9)} onClick={onClick} />);
    fireEvent.click(screen.getByRole("button", { name: /Alle 9 Invarianten/ }));
    expect(onClick).toHaveBeenCalled();
  });

  it("points at the next repository's start", () => {
    render(
      <NextCard
        to="/projects/PAY/repos/pay-api"
        slug="pay-api"
        teaser={["Nimmt an."]}
      />,
    );
    const link = screen.getByRole("link");
    expect(link.getAttribute("href")).toBe("/projects/PAY/repos/pay-api");
    expect(link).toHaveTextContent(S.path.next);
    expect(link).toHaveTextContent("pay-api");
    expect(link).toHaveTextContent("Nimmt an.");
    expect(link).toHaveTextContent(S.path.readEntry);
  });

  it("says the page's figures quietly and opens its history", () => {
    const onHistory = vi.fn();
    const { rerender } = render(
      <StandLine
        revision={14}
        notes={55}
        lastMerge="2026-11-09T10:00:00Z"
        onHistory={onHistory}
      />,
    );
    const line = screen.getByRole("button");
    expect(line).toHaveTextContent(
      "Stand: Beschreibung Revision 14·55 Notizen·letzter Merge 09.11.2026·Verlauf",
    );
    fireEvent.click(line);
    expect(onHistory).toHaveBeenCalled();
    rerender(
      <StandLine
        revision={null}
        notes={1}
        lastMerge={null}
        onHistory={onHistory}
      />,
    );
    expect(screen.getByRole("button")).toHaveTextContent(
      "Stand: noch keine Beschreibung·1 Notiz·letzter Merge nie·Verlauf",
    );
  });
});
