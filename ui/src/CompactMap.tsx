import {
  useId,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
} from "react";
import { DiagramTools, FullDiagram, drawMap } from "./Diagram";
import { fits, type FlowIR } from "./diagramIR";
import { download, fileName, toSvgFile } from "./diagramExport";
import { bandsOf } from "./mapLayout";
import { S } from "./strings";
import { Link, Teaser } from "./ui";

/**
 * CompactMap is the project page's system map, compact: its bands in the
 * full drawing's order (bandsOf), each a line of names, and the
 * repositories, what a reader comes for, the one band of boxes. No arrows:
 * a box pointed at, focused or tapped lights what it is connected to, each
 * with the arrow's way and label, and dims the rest. The drawing with its
 * arrows is the full view and the file, drawn only when asked for (user,
 * 2026-11-03: the 22-box map stood 1458px tall and the repositories came
 * after it).
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
  // Pointer and focus each hold their own: leaving one never puts out what
  // the other lit.
  const [hovered, setHovered] = useState<string | null>(null);
  const [focused, setFocused] = useState<string | null>(null);
  const hot = hovered ?? focused;
  // The drawing the full view shows, null while closed.
  const [full, setFull] = useState<ReturnType<typeof drawMap> | null>(null);
  const [broken, setBroken] = useState(false);
  const card = useRef<HTMLElement>(null);
  const drawnFor = useRef<{
    ir: FlowIR;
    drawn: ReturnType<typeof drawMap>;
  } | null>(null);
  // A tap on a link lights it first and opens it only once lit: a touch
  // screen has no pointing.
  const touch = useRef<{ id: string; lit: boolean } | null>(null);
  const ok = fits(ir, true);
  const bands = useMemo(() => bandsOf(ir), [ir]);
  const names = useMemo(
    () => new Map(ir.nodes.map((n) => [n.id, n.label])),
    [ir],
  );
  const lit = useMemo(() => (hot === null ? null : ways(ir, hot)), [ir, hot]);

  // Every map the backend stores fits; one that does not is a defect, said
  // rather than drawn crowded, as for the drawing.
  if (!ok)
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.failed}</p>;

  // The drawing, laid out by code on the first ask, once per map. A layout
  // that throws costs the full view and the file, never the page.
  function drawing() {
    if (drawnFor.current?.ir === ir) return drawnFor.current.drawn;
    try {
      const drawn = drawMap(ir, "almanach-system-map");
      drawnFor.current = { ir, drawn };
      return drawn;
    } catch {
      setBroken(true);
      return null;
    }
  }
  function save() {
    const d = drawing();
    if (d) download(fileName("flow"), toSvgFile(d, card.current));
  }
  function open() {
    setFull(drawing());
  }

  const point = (id: string): Point => ({
    onMouseEnter: () => setHovered(id),
    onMouseLeave: () => setHovered((h) => (h === id ? null : h)),
    onFocus: () => setFocused(id),
    onBlur: () => setFocused((f) => (f === id ? null : f)),
    onTouchStart: () => {
      touch.current = { id, lit: hot === id };
    },
    onClickCapture: (e) => {
      const t = touch.current;
      touch.current = null;
      if (t?.id !== id || t.lit) return;
      e.preventDefault();
      e.stopPropagation();
      setFocused(id);
    },
    onClick: () => setFocused(id),
  });
  const said =
    hot === null || lit === null
      ? ""
      : S.diagram.connections(
          names.get(hot) ?? hot,
          [...lit].map(([k, w]) => `${names.get(k) ?? k} (${w.text})`),
        );

  return (
    <figure className="my-3" aria-label={title} ref={card}>
      <DiagramTools title={title} onFull={open} onSave={save} />
      {broken && (
        <p className="mb-2 text-[13.5px] text-faint">{S.diagram.failed}</p>
      )}
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
                    <Node
                      box={boxes}
                      label={names.get(id) ?? id}
                      teaser={boxes ? (teasers.get(id) ?? []) : []}
                      to={links(id)}
                      state={
                        lit === null
                          ? ""
                          : id === hot || lit.has(id)
                            ? "lit"
                            : "off"
                      }
                      way={lit?.get(id)}
                      point={point(id)}
                    />
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
        <p className="pt-1 pb-1.5 text-[12px] text-faint">
          {S.diagram.compactHint}
        </p>
        <p className="sr-only" aria-live="polite">
          {said}
        </p>
      </div>
      {full && (
        <FullDiagram
          title={title}
          kind="flow"
          drawn={full}
          links={links}
          names={names}
          onClose={() => setFull(null)}
        />
      )}
    </figure>
  );
}

/** Point is what a node does when pointed at, focused or tapped: lights
 *  its connections, and puts them out again. */
type Point = {
  onMouseEnter: () => void;
  onMouseLeave: () => void;
  onFocus: () => void;
  onBlur: () => void;
  onTouchStart: () => void;
  onClickCapture: (e: MouseEvent) => void;
  onClick: () => void;
};

/** A connection seen from the node lit: the arrows' ways and labels. */
type Way = { text: string };

/** ways is every node an edge joins to id, with what the edges say seen
 * from id: their way (out, in, both; dashed as drawn; none for an edge
 * without a head) and their label, each saying once. */
export function ways(ir: FlowIR, id: string): Map<string, Way> {
  const out = new Map<string, string[]>();
  for (const e of ir.edges) {
    const other = e.from === id ? e.to : e.to === id ? e.from : null;
    if (other === null || other === id) continue;
    const w = e.dashed ? S.diagram.dashedWay : S.diagram.way;
    const way =
      e.head === "both"
        ? w.both
        : e.head === "none"
          ? ""
          : e.from === id
            ? w.out
            : w.in;
    const said = [way, e.label.trim()].filter((s) => s !== "").join(" ");
    const list = out.get(other) ?? [];
    if (said !== "" && !list.includes(said)) list.push(said);
    out.set(other, list);
  }
  return new Map([...out].map(([k, v]) => [k, { text: v.join(", ") }]));
}

const tone = {
  "": "border-border",
  lit: "border-accent-strong bg-accent/10",
  off: "border-border opacity-40",
} as const;

/** Node is one name on the map: a repository's box with its teaser, or
 *  another band's chip. A node with a page is its link; one without is a
 *  button, so a keyboard reaches its connections too. */
function Node({
  box,
  label,
  teaser,
  to,
  state,
  way,
  point,
}: {
  box: boolean;
  label: string;
  teaser: string[];
  to: string | null;
  state: "" | "lit" | "off";
  way: Way | undefined;
  point: Point;
}) {
  const described = useId();
  const cls =
    "border bg-bg text-left transition-[opacity,border-color] duration-100 hover:border-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong " +
    (box
      ? "block rounded-ui-sm px-3 py-2 "
      : "inline-flex items-baseline gap-1.5 rounded-full px-2.5 py-[3px] text-[12.5px] text-ink-dim " +
        (to === null ? "" : "font-mono ")) +
    tone[state];
  const wayMark = way && (
    <span className="shrink-0 font-sans text-[11.5px] text-ochre">
      {way.text}
    </span>
  );
  const body = box ? (
    <>
      {/* The way wraps under a long slug rather than cut it. */}
      <span className="flex flex-wrap items-baseline gap-x-2">
        <span className="max-w-full min-w-0 truncate font-mono text-[13px] text-ink">
          {label}
        </span>
        {wayMark}
      </span>
      {teaser.length > 0 && (
        <span
          id={described}
          className="mt-1 line-clamp-2 text-[12.5px] leading-snug text-muted"
        >
          <Teaser lines={teaser} />
        </span>
      )}
    </>
  ) : (
    <>
      {label}
      {wayMark}
    </>
  );
  if (to === null)
    return (
      <button type="button" className={cls} {...point}>
        {body}
      </button>
    );
  // A link's own click opens its page.
  const { onClick: _lights, ...pointing } = point;
  return (
    <Link
      to={to}
      title={S.diagram.repoLink(label)}
      aria-label={S.diagram.repoLink(label)}
      aria-describedby={teaser.length > 0 ? described : undefined}
      className={cls}
      {...pointing}
    >
      {body}
    </Link>
  );
}
