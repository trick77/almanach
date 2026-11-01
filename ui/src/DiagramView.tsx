// Copied from rongo (ui/src/DiagramView.tsx), kept in step by hand.
// Almanach: the title and the kind come in as props (rongo reads them off
// the mermaid source), the copy is strings.ts's, and the drawing's wrapper
// (rongo's MermaidSvg) lives here as DiagramSvg. The sheet is sized to the
// drawing up to the window, not rongo's fixed 1100px: the page column is
// 1400px, and the full view must have more room than the page, not less.
import { useRef, type ReactNode } from "react";
import { download, fileName, toSvgFile } from "./diagramExport";
import OverlayShell from "./OverlayShell";
import { DownloadIcon } from "./icons";
import { S } from "./strings";

/**
 * DiagramView is the diagram seen whole. On the page a wide picture is
 * scaled into the column; here the sheet takes the drawing's own size, up
 * to the window, so a small one gets a small sheet and a wide one the whole
 * window. A phone gets the full screen. On a window just over the column's
 * width, the sheet's padding leaves a wide drawing a few pixels under the
 * page's (user's choice, 2026-11-01): the whole drawing in view, a phone's
 * included, beats a 1:1 crop that has to be panned.
 */
export default function DiagramView({
  title,
  kind,
  svg,
  onClose,
}: {
  title: string;
  kind: "flow" | "sequence" | "er";
  svg: string;
  onClose: () => void;
}) {
  const body = useRef<HTMLDivElement>(null);

  function save() {
    download(fileName(kind), toSvgFile({ svg }, body.current));
  }

  return (
    <OverlayShell
      label={title}
      dialogClassName="flex h-full w-full flex-col overflow-hidden sm:h-auto sm:max-h-full sm:w-fit sm:max-w-full sm:min-w-[min(100%,560px)] rounded-none border-0 bg-panel font-sans shadow-panel sm:rounded-ui-lg sm:border sm:border-elevated-border"
      headerClassName="flex items-center gap-2 border-b border-border px-3 py-2.5 sm:gap-3 sm:px-4.5 sm:py-3"
      onClose={onClose}
      header={
        <>
          {/* contain: inline-size: the title adds no width to the sheet,
              which is sized to the drawing; it takes the room there is and
              truncates. */}
          <span className="min-w-0 flex-1 truncate text-[13.5px] text-ink [contain:inline-size]">
            {title}
          </span>
          <button
            type="button"
            onClick={save}
            className="hidden h-8 items-center gap-1.5 rounded-ui-sm border border-border px-2.5 text-[13px] text-ink-dim hover:border-elevated-border hover:bg-active sm:flex"
          >
            <DownloadIcon />
            {S.diagram.download}
          </button>
        </>
      }
    >
      {/* The drawing scales to the sheet's width and no further: the SVG
          carries its own max-width, so a small diagram stays 1:1 and is
          centred, and a wide one fits. A flex column, so a tall one
          scrolls here instead of overflowing the sheet's max height. */}
      <div
        ref={body}
        className="grid min-h-0 flex-1 place-items-center overflow-auto p-6"
      >
        <div className="w-full">
          <DiagramSvg svg={svg} title={title} />
        </div>
      </div>
    </OverlayShell>
  );
}

/** DiagramSvg is the drawing itself: the SVG carries a viewBox and its own
 * width, so it fits a narrow column by scaling down and never grows past
 * 1:1. innerHTML from model text is safe here because every label went
 * through diagramDraw's esc() on its way into the string. */
export function DiagramSvg({
  svg,
  title,
}: {
  svg: string;
  title: string;
}): ReactNode {
  return (
    // font-sans explicitly: a node label is a name from the code, not prose.
    <div
      className="rongo-diagram font-sans"
      role="img"
      aria-label={title}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
