// Copied from rongo (ui/src/OverlayShell.tsx), kept in step by hand.
// Almanach: the diagram's full view is its only overlay, so the parts the
// other viewers share in rongo are gone; the close label is strings.ts's.
import { useRef, type ReactNode } from "react";
import { useEscape, useFocusOnOpen, useTabTrap } from "./dialog";
import { useBackdropDismiss } from "./dismiss";
import { S } from "./strings";

/** The close button: a 44px target on a phone, 32px from sm up. */
const overlayClose =
  "grid h-11 w-11 place-items-center rounded-ui-sm text-lg leading-none text-muted hover:bg-active hover:text-ink sm:h-8 sm:w-8";

/**
 * OverlayShell is a diagram seen whole: the scrim, the sheet, the header
 * with its × and the keyboard. Focus moves in on open and back on close,
 * Escape closes, and Tab stays inside — the dialog is modal, and a Tab that
 * left it would land in the dimmed page behind.
 *
 * Edge to edge on a phone. A press beside the sheet closes it and does
 * nothing else: see useBackdropDismiss for why that is the click and not
 * the pointerdown.
 */
export default function OverlayShell({
  label,
  dialogClassName,
  headerClassName,
  header,
  onClose,
  children,
}: {
  label: string;
  /** The sheet's own grid and width. */
  dialogClassName: string;
  headerClassName: string;
  /** What sits in the header before the × . */
  header: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  const closeButton = useRef<HTMLButtonElement>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const {
    ref: scrim,
    onPointerDown,
    onPointerUp,
  } = useBackdropDismiss(onClose);

  useFocusOnOpen(closeButton);
  useEscape(onClose);
  useTabTrap(dialog);

  return (
    <div
      className="fixed inset-0 z-30 flex items-center justify-center bg-black/55 p-0 sm:p-6 md:p-10"
      ref={scrim}
      onPointerDown={onPointerDown}
      onPointerUp={onPointerUp}
    >
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-label={label}
        className={dialogClassName}
      >
        <header className={headerClassName}>
          {header}
          <button
            ref={closeButton}
            type="button"
            onClick={onClose}
            aria-label={S.diagram.close}
            className={overlayClose}
          >
            ×
          </button>
        </header>
        {children}
      </div>
    </div>
  );
}
