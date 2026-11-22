// The package's full view and card as almanach wires them: its overlay
// shell, its words, its file names, its node links.
import { describe, it, expect, vi, afterEach } from "vitest";
import {
  render,
  fireEvent,
  createEvent,
  screen,
  within,
} from "@testing-library/react";
import { svgSize, type FlowIR, type SequenceIR } from "@trick77/diagram";
import { Diagram, FullDiagram, drawIR } from "./Diagram";
import { S } from "./strings";

const svg =
  '<svg viewBox="0 0 300 120" style="max-width: 300px"><g class="drawn"></g></svg>';

function view(onClose: () => void = () => {}) {
  return render(
    <FullDiagram
      title={S.diagram.titles.sequence}
      kind="sequence"
      drawn={{ svg, layout: null }}
      names={new Map()}
      onClose={onClose}
    />,
  );
}

/** saving catches the file a click hands the browser: its name. */
function saving(): { name: () => string; restore: () => void } {
  Object.defineProperty(URL, "createObjectURL", {
    value: vi.fn(() => "blob:x"),
    configurable: true,
  });
  Object.defineProperty(URL, "revokeObjectURL", {
    value: vi.fn(),
    configurable: true,
  });
  let name = "";
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(function (this: HTMLAnchorElement) {
      name = this.download;
    });
  return { name: () => name, restore: () => click.mockRestore() };
}

const shopApi = (id: string) =>
  id === "shop-api" ? "/projects/SHOP/repos/shop-api" : null;

const map: FlowIR = {
  kind: "flow",
  direction: "TB",
  zones: [
    { id: "clients", title: "Clients" },
    { id: "repositories", title: "Repositories" },
  ],
  nodes: [
    { id: "web", label: "Browser", shape: "pill", zone: "clients" },
    { id: "shop-api", label: "shop-api", shape: "step", zone: "repositories" },
  ],
  edges: [
    { from: "web", to: "shop-api", label: "HTTP", dashed: false, head: "end" },
  ],
};

const message = {
  type: "message" as const,
  from: "a",
  to: "b",
  label: "reservieren",
  line: "solid" as const,
  head: "filled" as const,
};

const sequence: SequenceIR = {
  kind: "sequence",
  actors: [
    { id: "a", label: "Shop" },
    { id: "b", label: "Lager" },
  ],
  rows: [message],
};

afterEach(() => {
  window.history.replaceState({}, "", "/");
});

describe("FullDiagram", () => {
  it("opens with the focus on its close button", () => {
    const { getByLabelText } = view();
    expect(document.activeElement).toBe(getByLabelText(S.diagram.close));
  });

  it("names the picture in its header", () => {
    const { getByRole } = view();
    expect(getByRole("dialog").getAttribute("aria-label")).toBe(
      S.diagram.titles.sequence,
    );
    expect(getByRole("dialog").querySelector("svg .drawn")).toBeTruthy();
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    view(onClose);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("closes when the scrim itself is clicked, not the sheet", () => {
    // The click, not the press: the sheet is gone by the time the click is
    // dispatched, and it would otherwise land on the page behind it.
    const onClose = vi.fn();
    const { getByRole } = view(onClose);
    const dialog = getByRole("dialog");
    const scrim = dialog.parentElement as HTMLElement;

    fireEvent(dialog, createEvent.pointerDown(dialog, { bubbles: true }));
    fireEvent.click(dialog);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent(scrim, createEvent.pointerDown(scrim, { bubbles: true }));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(scrim);
    expect(onClose).toHaveBeenCalled();
  });

  it("takes a drag across the edge for a drag, not a cancel", () => {
    const onClose = vi.fn();
    const { getByRole } = view(onClose);
    const dialog = getByRole("dialog");
    const scrim = dialog.parentElement as HTMLElement;

    fireEvent(dialog, createEvent.pointerDown(dialog, { bubbles: true }));
    fireEvent(scrim, createEvent.pointerUp(scrim, { bubbles: true }));
    fireEvent.click(scrim);
    expect(onClose).not.toHaveBeenCalled();

    fireEvent(scrim, createEvent.pointerDown(scrim, { bubbles: true }));
    fireEvent(dialog, createEvent.pointerUp(dialog, { bubbles: true }));
    fireEvent.click(scrim);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("keeps Tab inside, so it cannot reach the links behind the scrim", () => {
    const { getByRole, getByLabelText, getByText } = view();
    const dialog = getByRole("dialog");
    const close = getByLabelText(S.diagram.close);
    expect(document.activeElement).toBe(close);
    fireEvent.keyDown(document, { key: "Tab" });
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).toBe(getByText(S.diagram.download));
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  it("draws its chrome in the sans face, not the page's serif prose", () => {
    const { getByRole } = view();
    expect(getByRole("dialog").className).toContain("font-sans");
  });

  it("downloads the picture it is showing, under almanach's name", () => {
    const file = saving();
    const { getByText } = view();
    fireEvent.click(getByText(S.diagram.download));
    expect(file.name()).toBe("almanach-sequence-diagram.svg");
    file.restore();
  });

  it("links a map's nodes over their boxes in the bands, and a followed link closes it", async () => {
    const onClose = vi.fn();
    const drawn = await drawIR(map, "t", true);
    render(
      <FullDiagram
        title={S.diagram.titles.map}
        kind="flow"
        drawn={drawn}
        links={shopApi}
        names={new Map([["shop-api", "shop-api"]])}
        onClose={onClose}
      />,
    );
    const links = within(screen.getByRole("dialog")).getAllByRole("link");
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe("/projects/SHOP/repos/shop-api");
    // Over the box the bands put the node in, in percent of the drawing,
    // whose size is the layout's.
    const l = drawn.layout!;
    const box = l.nodes.find((n) => n.id === "shop-api")!;
    const at = (v: string, n: number, of: number) =>
      expect(parseFloat(v)).toBeCloseTo((n / of) * 100, 3);
    at(links[0].style.left, box.x, l.width);
    at(links[0].style.top, box.y, l.height);
    at(links[0].style.width, box.w, l.width);
    at(links[0].style.height, box.h, l.height);
    expect(svgSize(drawn.svg)).toEqual({ w: l.width, h: l.height });
    fireEvent.click(links[0]);
    expect(onClose).toHaveBeenCalled();
    expect(window.location.pathname).toBe("/projects/SHOP/repos/shop-api");
  });
});

describe("Diagram", () => {
  it("names a map's file a flowchart, a sequence's a sequence", async () => {
    const file = saving();
    render(<Diagram ir={map} title={S.diagram.titles.repoMap} map />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: S.diagram.downloadOf(S.diagram.titles.repoMap),
      }),
    );
    expect(file.name()).toBe("almanach-flowchart-diagram.svg");
    render(<Diagram ir={sequence} title={S.diagram.titles.sequence} />);
    fireEvent.click(
      await screen.findByRole("button", {
        name: S.diagram.downloadOf(S.diagram.titles.sequence),
      }),
    );
    expect(file.name()).toBe("almanach-sequence-diagram.svg");
    file.restore();
  });

  it("sits on its own canvas, never in the renderer's framed panel", async () => {
    render(<Diagram ir={sequence} title={S.diagram.titles.sequence} />);
    const drawing = await screen.findByRole("img", {
      name: S.diagram.titles.sequence,
    });
    // The card: as wide as the drawing, no frame of its own, its buttons in
    // a row above the drawing rather than over its corner.
    const card = drawing.parentElement?.parentElement as HTMLElement;
    expect(card.className).toContain("w-max");
    expect(card.className).not.toContain("border");
    expect(card.firstElementChild?.className).not.toContain("absolute");
  });

  it("links its nodes in the card and the full view, a followed one closing it", async () => {
    render(
      <Diagram ir={map} title={S.diagram.titles.repoMap} map links={shopApi} />,
    );
    const open = await screen.findByRole("button", {
      name: S.diagram.fullOf(S.diagram.titles.repoMap),
    });
    expect(screen.getAllByRole("link")).toHaveLength(1);
    fireEvent.click(open);
    const dialog = screen.getByRole("dialog");
    fireEvent.click(within(dialog).getByRole("link"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(window.location.pathname).toBe("/projects/SHOP/repos/shop-api");
  });

  it("says a sequence past the backend's rows, frames counted, is not drawn", () => {
    render(
      <Diagram
        ir={{
          ...sequence,
          rows: [
            { type: "frame", keyword: "loop", label: "" },
            ...Array.from({ length: 19 }, () => message),
            { type: "end" },
          ],
        }}
        title={S.diagram.titles.sequence}
      />,
    );
    expect(screen.getByText(S.diagram.failed)).toBeInTheDocument();
  });
});
