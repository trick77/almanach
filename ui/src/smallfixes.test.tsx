import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { Problem } from "./api";
import { Problems } from "./Problems";
import { Tabs } from "./Rail";
import { S } from "./strings";

function TabsHost() {
  const [value, setValue] = useState("a");
  return (
    <Tabs
      tabs={[
        { id: "a", label: "Eins" },
        { id: "b", label: "Zwei" },
        { id: "c", label: "Drei" },
      ]}
      value={value}
      onChange={setValue}
    />
  );
}

describe("Tabs", () => {
  it("steps by the arrows, wrapping, and jumps by Home and End; only the selected tab is in the tab order", () => {
    render(<TabsHost />);
    const tab = (name: string) => screen.getByRole("tab", { name });
    expect(tab("Eins")).toHaveAttribute("tabindex", "0");
    expect(tab("Zwei")).toHaveAttribute("tabindex", "-1");

    fireEvent.keyDown(tab("Eins"), { key: "ArrowRight" });
    expect(tab("Zwei")).toHaveAttribute("aria-selected", "true");
    expect(tab("Zwei")).toHaveFocus();

    fireEvent.keyDown(tab("Zwei"), { key: "End" });
    expect(tab("Drei")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(tab("Drei"), { key: "ArrowRight" });
    expect(tab("Eins")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(tab("Eins"), { key: "ArrowLeft" });
    expect(tab("Drei")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(tab("Drei"), { key: "Home" });
    expect(tab("Eins")).toHaveAttribute("aria-selected", "true");
    expect(tab("Eins")).toHaveFocus();

    // Any other key leaves the selection; so does a modifier (the
    // browser's: Alt+ArrowLeft goes back) and a held key's repeat.
    fireEvent.keyDown(tab("Eins"), { key: "a" });
    fireEvent.keyDown(tab("Eins"), { key: "ArrowLeft", altKey: true });
    fireEvent.keyDown(tab("Eins"), { key: "ArrowRight", metaKey: true });
    fireEvent.keyDown(tab("Eins"), { key: "End", shiftKey: true });
    fireEvent.keyDown(tab("Eins"), { key: "ArrowRight", repeat: true });
    expect(tab("Eins")).toHaveAttribute("aria-selected", "true");
  });

  it("takes no key with no tabs, and steps from the first when the value is none of them", () => {
    const seen: string[] = [];
    const { rerender } = render(
      <Tabs tabs={[]} value="x" onChange={(id) => seen.push(id)} />,
    );
    fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowRight" });
    rerender(
      <Tabs
        tabs={[
          { id: "a", label: "Eins" },
          { id: "b", label: "Zwei" },
          { id: "c", label: "Drei" },
        ]}
        value="x"
        onChange={(id) => seen.push(id)}
      />,
    );
    fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowLeft" });
    expect(seen).toEqual(["c"]);
  });
});

const problem: Problem = {
  repo_slug: "gateway",
  area: "internal/auth",
  component: "Anmeldung",
  bugfixes: 3,
  top_cause: "logic",
  top_cause_count: 2,
  issues_posted: 1,
  suggestions_posted: 0,
  last_fix_pr_key: "PLAT/gateway#7",
  last_fix_at: "2026-11-10T09:00:00Z",
};

describe("Problems", () => {
  it("links a problem area's repository the wiki shows to its page", () => {
    render(
      <Problems
        problems={[problem]}
        projectKey="PLAT"
        linkable={new Set(["gateway"])}
      />,
    );
    expect(screen.getByRole("link", { name: "gateway" })).toHaveAttribute(
      "href",
      "/projects/PLAT/repos/gateway",
    );
  });

  it("names one that left, or with no project, without a link", () => {
    const { unmount } = render(
      <Problems
        problems={[problem]}
        projectKey="PLAT"
        linkable={new Set(["other"])}
      />,
    );
    expect(screen.queryByRole("link", { name: "gateway" })).toBeNull();
    expect(screen.getByText("gateway")).toBeInTheDocument();
    unmount();
    render(<Problems problems={[problem]} />);
    expect(screen.queryByRole("link", { name: "gateway" })).toBeNull();
  });
});

describe("strings", () => {
  it("says how many notes of how many a repository's history shows", () => {
    expect(S.repo.notesCut(100, 240)).toBe("Die neusten 100 von 240 Notizen.");
  });
});
