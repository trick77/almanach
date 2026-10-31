// Copied from rongo (ui/src/DiagramView.test.tsx), kept in step by hand.
// Almanach: title and kind as props, German copy.
import { describe, it, expect, vi } from "vitest";
import { render, fireEvent, createEvent } from "@testing-library/react";
import DiagramView from "./DiagramView";
import { S } from "./strings";

const svg =
  '<svg viewBox="0 0 300 120" style="max-width: 300px"><g class="drawn"></g></svg>';

function view(onClose: () => void = () => {}) {
  return render(
    <DiagramView
      title={S.diagram.titles.sequence}
      kind="sequence"
      svg={svg}
      onClose={onClose}
    />,
  );
}

describe("DiagramView", () => {
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

  it("downloads the picture it is showing", () => {
    const create = vi.fn(() => "blob:x");
    Object.defineProperty(URL, "createObjectURL", {
      value: create,
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
    const { getByText } = view();
    fireEvent.click(getByText(S.diagram.download));
    expect(create).toHaveBeenCalled();
    expect(name).toBe("almanach-sequence-diagram.svg");
    click.mockRestore();
  });
});
