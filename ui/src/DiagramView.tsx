// Copied from rongo (ui/src/DiagramView.tsx), kept in step by hand.
// Almanach: the title and the kind come in as props (rongo reads them off
// the mermaid source), the copy is strings.ts's, and the drawing's wrapper
// (rongo's MermaidSvg) lives here as DiagramSvg. The full view draws 1:1 on
// a sheet as wide as the window, not scaled into rongo's 1100px: the page
// column is 1400px, so a scaled sheet could open smaller than the page.
import { useRef, type ReactNode } from "react";
import { download, fileName, toSvgFile } from "./diagramExport";
import OverlayShell from "./OverlayShell";
import { DownloadIcon } from "./icons";
import { S } from "./strings";

/**
 * DiagramView is the diagram seen whole. On the page a wide picture is
 * scaled down into the column; here it is drawn at its own size, 1:1, and
 * scrolls when it is wider or taller than the window, so it never opens
 * smaller than the page showed it.
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
      dialogClassName="grid h-full w-full grid-rows-[auto_1fr] overflow-hidden rounded-none border-0 bg-panel font-sans shadow-panel sm:rounded-ui-lg sm:border sm:border-elevated-border"
      headerClassName="flex items-center gap-2 border-b border-border px-3 py-2.5 sm:gap-3 sm:px-4.5 sm:py-3"
      onClose={onClose}
      header={
        <>
          <span className="min-w-0 truncate text-[13.5px] text-ink">
            {title}
          </span>
          <span className="ml-auto" />
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
      {/* 1:1: the SVG's own width, the page's max-width lifted. Centred
          while it fits; center-safe, so a wider one starts at the left edge
          and scrolls instead of being cut off on both sides. */}
      <div
        ref={body}
        className="grid min-h-0 place-items-center-safe overflow-auto p-6"
      >
        <div className="diagram-full w-max">
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
