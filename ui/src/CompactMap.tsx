import { useMemo, useRef, useState, type ReactNode } from "react";
import { NodeLinks } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { drawFlow } from "./diagramDraw";
import { download, fileName, toSvgFile } from "./diagramExport";
import DiagramView from "./DiagramView";
import { palette } from "./diagramTheme";
import { DownloadIcon, ExpandIcon } from "./icons";
import { bandsOf, layoutMap } from "./mapLayout";
import { S } from "./strings";
import { Link, Teaser } from "./ui";

/**
 * CompactMap is the project page's system map, compact: its bands in the
 * full drawing's order, each a line of names, and the repositories, what a
 * reader comes for, the one band of boxes. No arrows: a box pointed at (or
 * focused) lights what it is connected to, each with the arrow's way and
 * label, and dims the rest. The drawing with its arrows, mapLayout's, is the
 * full view and the file (user, 2026-11-03: the 22-box map stood 1458px
 * tall and the repositories came after it).
 *
 * links names the page a node opens, null for none, as for the drawing.
 * teasers is a repository box's line, by node id.
 */
export function CompactMap({
  ir,
  title,
  links,
  teasers,
}: {
  ir: FlowIR;
  title: string;
  links: (id: string) => string | null;
  teasers: Map<string, string[]>;
}): ReactNode {
  const [hot, setHot] = useState<string | null>(null);
  const [full, setFull] = useState(false);
  const card = useRef<HTMLDivElement>(null);
  const ok = fits(ir, true);
  const bands = useMemo(() => bandsOf(ir), [ir]);
  const names = useMemo(
    () => new Map(ir.nodes.map((n) => [n.id, n.label])),
    [ir],
  );
  // The full view's and the file's drawing, laid out by code without the
  // engine: synchronous and cheap, once per map.
  const drawn = useMemo(() => {
    if (!ok) return null;
    const layout = layoutMap(ir);
    return { layout, svg: drawFlow(layout, palette(), "almanach-system-map") };
  }, [ir, ok]);
  const lit = useMemo(() => (hot === null ? null : ways(ir, hot)), [ir, hot]);

  // Every map the backend stores fits; one that does not is a defect, said
  // rather than drawn crowded, as for the drawing.
  if (!ok)
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.failed}</p>;

  function save() {
    if (drawn) download(fileName("flow"), toSvgFile(drawn, card.current));
  }

  const point = (id: string) => ({
    onMouseEnter: () => setHot(id),
    onMouseLeave: () => setHot((h) => (h === id ? null : h)),
    onFocus: () => setHot(id),
    onBlur: () => setHot((h) => (h === id ? null : h)),
  });
  const state = (id: string) =>
    lit === null ? "" : id === hot || lit.has(id) ? "lit" : "off";

  return (
    <figure className="my-3" aria-label={title} ref={card}>
      <div className="mb-1 flex items-center justify-end gap-0.5">
        <button
          type="button"
          onClick={() => setFull(true)}
          title={S.diagram.full}
          aria-label={S.diagram.fullOf(title)}
          className="grid h-7 w-7 place-items-center rounded-ui-sm text-faint hover:bg-active hover:text-ink-dim"
        >
          <ExpandIcon />
        </button>
        <button
          type="button"
          onClick={save}
          title={S.diagram.download}
          aria-label={S.diagram.downloadOf(title)}
          className="grid h-7 w-7 place-items-center rounded-ui-sm text-faint hover:bg-active hover:text-ink-dim"
        >
          <DownloadIcon />
        </button>
      </div>
      <div className="rounded-ui-sm border border-border bg-panel px-4 py-2">
        {bands.map((b) => {
          const boxes = b.zone === "repositories";
          return (
            <div
              key={b.zone ?? ""}
              role="group"
              aria-label={b.title || undefined}
              className={
                "grid gap-x-3 gap-y-1.5 border-border-soft py-2.5 not-first:border-t sm:grid-cols-[150px_minmax(0,1fr)] " +
                (boxes ? "items-start" : "items-center")
              }
            >
              <div
                className={
                  "text-[12px] font-semibold " +
                  (boxes ? "text-ink-dim sm:pt-2" : "text-muted")
                }
              >
                {b.title}
              </div>
              <ul
                className={
                  boxes
                    ? "grid grid-cols-[repeat(auto-fill,minmax(190px,1fr))] gap-2.5"
                    : "flex flex-wrap gap-1.5"
                }
              >
                {b.ids.map((id) => (
                  <li key={id} className="contents">
                    {boxes ? (
                      <Box
                        id={id}
                        label={names.get(id) ?? id}
                        teaser={teasers.get(id) ?? []}
                        to={links(id)}
                        state={state(id)}
                        way={lit?.get(id)}
                        point={point(id)}
                      />
                    ) : (
                      <Chip
                        label={names.get(id) ?? id}
                        to={links(id)}
                        state={state(id)}
                        way={lit?.get(id)}
                        point={point(id)}
                      />
                    )}
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
        <p className="pt-1 pb-1.5 text-[12px] text-faint">
          {S.diagram.compactHint}
        </p>
      </div>
      {full && drawn && (
        <DiagramView
          title={title}
          kind="flow"
          svg={drawn.svg}
          onClose={() => setFull(false)}
          overlay={
            <NodeLinks
              layout={drawn.layout}
              links={links}
              names={names}
              onFollow={() => setFull(false)}
            />
          }
        />
      )}
    </figure>
  );
}

/** Point is what a box does when pointed at or focused: lights its
 *  connections, and puts them out again. */
type Point = {
  onMouseEnter: () => void;
  onMouseLeave: () => void;
  onFocus: () => void;
  onBlur: () => void;
};

/** ways is every node an edge joins to id, with what the edges say seen
 * from id: their way (out, in, both; none for an edge without a head) and
 * their label, each pair once. */
export function ways(ir: FlowIR, id: string): Map<string, string> {
  const out = new Map<string, string[]>();
  for (const e of ir.edges) {
    const other = e.from === id ? e.to : e.to === id ? e.from : null;
    if (other === null || other === id) continue;
    const way =
      e.head === "both"
        ? S.diagram.way.both
        : e.head === "none"
          ? ""
          : e.from === id
            ? S.diagram.way.out
            : S.diagram.way.in;
    const said = [way, e.label.trim()].filter((s) => s !== "").join(" ");
    const list = out.get(other) ?? [];
    if (said !== "" && !list.includes(said)) list.push(said);
    out.set(other, list);
  }
  return new Map([...out].map(([k, v]) => [k, v.join(", ")]));
}

const tone = {
  "": "border-border",
  lit: "border-accent-strong bg-accent/10",
  off: "border-border opacity-40",
} as const;

function Box({
  id,
  label,
  teaser,
  to,
  state,
  way,
  point,
}: {
  id: string;
  label: string;
  teaser: string[];
  to: string | null;
  state: "" | "lit" | "off";
  way: string | undefined;
  point: Point;
}) {
  const body = (
    <>
      <span className="flex items-baseline gap-2">
        <span className="min-w-0 truncate font-mono text-[13px] text-ink">
          {label}
        </span>
        {way && <Way text={way} />}
      </span>
      {teaser.length > 0 && (
        <span className="mt-1 line-clamp-2 text-[12.5px] leading-snug text-muted">
          <Teaser lines={teaser} />
        </span>
      )}
    </>
  );
  const cls =
    "block rounded-ui-sm border bg-bg px-3 py-2 transition-[opacity,border-color] duration-100 " +
    tone[state];
  return to === null ? (
    <span className={cls} {...point}>
      {body}
    </span>
  ) : (
    <Link
      to={to}
      title={S.diagram.repoLink(label)}
      aria-label={S.diagram.repoLink(label)}
      className={
        cls +
        " hover:border-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong"
      }
      data-node={id}
      {...point}
    >
      {body}
    </Link>
  );
}

function Chip({
  label,
  to,
  state,
  way,
  point,
}: {
  label: string;
  to: string | null;
  state: "" | "lit" | "off";
  way: string | undefined;
  point: Point;
}) {
  const cls =
    "inline-flex items-baseline gap-1.5 rounded-full border bg-bg px-2.5 py-[3px] text-[12.5px] text-ink-dim transition-[opacity,border-color] duration-100 " +
    tone[state];
  const body = (
    <>
      {label}
      {way && <Way text={way} />}
    </>
  );
  return to === null ? (
    <span className={cls} {...point}>
      {body}
    </span>
  ) : (
    <Link
      to={to}
      title={S.diagram.repoLink(label)}
      aria-label={S.diagram.repoLink(label)}
      className={
        cls +
        " font-mono hover:border-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong"
      }
      {...point}
    >
      {body}
    </Link>
  );
}

function Way({ text }: { text: string }) {
  return (
    <span className="shrink-0 font-sans text-[11.5px] text-ochre">{text}</span>
  );
}
