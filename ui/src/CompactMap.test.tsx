// The project page's compact map: its drawing made only when the full view
// or the file asks for it, in the package's bands.
import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, fireEvent, screen, within } from "@testing-library/react";
import type { FlowIR, Rendered } from "@trick77/diagram";
import { CompactMap } from "./CompactMap";
import * as diagram from "./Diagram";
import { S } from "./strings";

vi.mock("./Diagram", { spy: true });

const step = (id: string, zone: string) => ({
  id,
  label: id,
  shape: "step" as const,
  zone,
});
const edge = (from: string, to: string) => ({
  from,
  to,
  label: "HTTP",
  dashed: false,
  head: "end" as const,
});

const map: FlowIR = {
  kind: "flow",
  direction: "TB",
  zones: [
    { id: "repositories", title: "Repositories" },
    { id: "clients", title: "Clients" },
  ],
  nodes: [step("shop-api", "repositories"), step("browser", "clients")],
  edges: [edge("browser", "shop-api")],
};
const grown: FlowIR = {
  ...map,
  nodes: [...map.nodes, step("shop-web", "repositories")],
  edges: [...map.edges, edge("shop-web", "shop-api")],
};

function compact(ir: FlowIR) {
  return (
    <CompactMap
      ir={ir}
      title={S.diagram.titles.map}
      links={(id) =>
        id === "shop-api" ? "/projects/SHOP/repos/shop-api" : null
      }
      teasers={new Map()}
    />
  );
}

const fullButton = () =>
  screen.getByRole("button", { name: S.diagram.fullOf(S.diagram.titles.map) });
const saveButton = () =>
  screen.getByRole("button", {
    name: S.diagram.downloadOf(S.diagram.titles.map),
  });

/** held is a drawing the test hands out when it chooses. */
function held() {
  let release: (d: Rendered) => void = () => {};
  const p = new Promise<Rendered>((r) => (release = r));
  return { p, release };
}

// jsdom has no object URLs: stubbed for a download, put back after.
const urlMethods = ["createObjectURL", "revokeObjectURL"] as const;
const saved = urlMethods.map((k) => Object.getOwnPropertyDescriptor(URL, k));
let names: string[] = [];
beforeEach(() => {
  Object.defineProperty(URL, "createObjectURL", {
    value: vi.fn(() => "blob:x"),
    configurable: true,
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    value: vi.fn(),
    configurable: true,
  });
  names = [];
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    if (this.download) names.push(this.download);
  });
});
afterEach(() => {
  urlMethods.forEach((k, i) => {
    const d = saved[i];
    if (d) Object.defineProperty(URL, k, d);
    else delete (URL as unknown as Record<string, unknown>)[k];
  });
  vi.mocked(diagram.drawIR).mockClear();
  vi.restoreAllMocks();
  window.history.replaceState({}, "", "/");
});

describe("CompactMap", () => {
  it("lists its bands in the maps' order, whatever order they were written", () => {
    render(compact(map));
    const groups = screen
      .getAllByRole("group")
      .map((g) => g.getAttribute("aria-label"));
    expect(groups).toEqual(["Clients", "Repositories"]);
  });

  it("draws the file only when asked, named as the drawing's", async () => {
    render(compact(map));
    expect(screen.queryByRole("img")).toBeNull();
    fireEvent.click(saveButton());
    await vi.waitFor(() =>
      expect(names).toEqual(["almanach-flowchart-diagram.svg"]),
    );
  });

  it("draws once while asked twice, says so, and holds its buttons", async () => {
    const wait = held();
    const real = await vi.importActual<typeof diagram>("./Diagram");
    vi.mocked(diagram.drawIR).mockImplementationOnce(() => wait.p);
    render(compact(map));
    fireEvent.click(fullButton());
    expect(screen.getByText(S.diagram.pending)).toBeInTheDocument();
    expect(fullButton()).toBeDisabled();
    expect(saveButton()).toBeDisabled();
    fireEvent.click(fullButton());
    fireEvent.click(saveButton());
    wait.release(await real.drawIR(map, "t", true));
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(diagram.drawIR).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(S.diagram.pending)).toBeNull();
    // The drawing made is the drawing kept: the file and the view again
    // take it as it is.
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.click(saveButton());
    fireEvent.click(fullButton());
    await screen.findByRole("dialog");
    expect(names).toContain("almanach-flowchart-diagram.svg");
    expect(diagram.drawIR).toHaveBeenCalledTimes(1);
  });

  it("keeps the open view's drawing while a changed map is drawn anew", async () => {
    const { rerender } = render(compact(map));
    fireEvent.click(fullButton());
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).not.toContain("shop-web");
    rerender(compact(grown));
    // Still open, the old drawing until the new one is done.
    expect(screen.getByRole("dialog")).toBe(dialog);
    await vi.waitFor(() =>
      expect(screen.getByRole("dialog").textContent).toContain("shop-web"),
    );
    expect(
      within(screen.getByRole("dialog")).getByRole("link", {
        name: S.diagram.repoLink("shop-api"),
      }),
    ).toBeInTheDocument();
  });

  it("drops a drawing the closed view no longer wants", async () => {
    const real = await vi.importActual<typeof diagram>("./Diagram");
    const { rerender } = render(compact(map));
    fireEvent.click(fullButton());
    await screen.findByRole("dialog");
    const wait = held();
    vi.mocked(diagram.drawIR).mockImplementationOnce(() => wait.p);
    rerender(compact(grown));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    wait.release(await real.drawIR(grown, "t", true));
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
