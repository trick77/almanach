import {
  memo,
  useEffect,
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
  // Pointer, focus and click each hold their own: leaving one never puts
  // out what another lit. A click pins, as not every browser focuses what
  // it clicks (Safari); a click elsewhere or Escape lets go.
  const [hovered, setHovered] = useState<string | null>(null);
  const [focused, setFocused] = useState<string | null>(null);
  const [pinned, setPinned] = useState<string | null>(null);
  const hot = hovered ?? focused ?? pinned;
  const hotNow = useRef(hot);
  useEffect(() => {
    hotNow.current = hot;
  });
  // The full view is open; its drawing follows the map. failedFor is the
  // map whose drawing threw: said until the map changes.
  const [open, setOpen] = useState(false);
  const [failedFor, setFailedFor] = useState<FlowIR | null>(null);
  const card = useRef<HTMLElement>(null);
  // A tap on a link lights it first and opens it only once lit: a touch
  // screen has no pointing. A touch that turns into a scroll is forgotten.
  const touch = useRef<{ id: string; lit: boolean } | null>(null);
  const ok = useMemo(() => fits(ir, true), [ir]);
  const bands = useMemo(() => bandsOf(ir), [ir]);
  const names = useMemo(
    () => new Map(ir.nodes.map((n) => [n.id, n.label])),
    [ir],
  );
  const lit = useMemo(() => (hot === null ? null : ways(ir, hot)), [ir, hot]);
  const full = useMemo(() => (open ? tryDraw(ir) : null), [open, ir]);
  const broken = failedFor === ir || (open && full === null);
  // One set of handlers per map, so a light changing re-renders only the
  // nodes whose look changes.
  const points = useMemo(
    () =>
      new Map(
        ir.nodes.map((n): [string, Point] => {
          const id = n.id;
          return [
            id,
            {
              onMouseEnter: () => setHovered(id),
              onMouseLeave: () => setHovered((h) => (h === id ? null : h)),
              onFocus: () => setFocused(id),
              onBlur: () => setFocused((f) => (f === id ? null : f)),
              onTouchStart: () => {
                touch.current = { id, lit: hotNow.current === id };
              },
              onTouchMove: () => {
                touch.current = null;
              },
              onTouchCancel: () => {
                touch.current = null;
              },
              onClickCapture: (e) => {
                const t = touch.current;
                touch.current = null;
                if (t?.id !== id || t.lit) return;
                e.preventDefault();
                e.stopPropagation();
                setPinned(id);
              },
              onClick: () => setPinned((p) => (p === id ? null : id)),
            },
          ];
        }),
      ),
    [ir],
  );

  useEffect(() => {
    if (pinned === null) return;
    const away = (e: Event) => {
      if (
        e instanceof KeyboardEvent
          ? e.key === "Escape"
          : !card.current?.contains(e.target as Node)
      )
        setPinned(null);
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", away);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", away);
    };
  }, [pinned]);

  // Every map the backend stores fits; one that does not is a defect, said
  // rather than drawn crowded, as for the drawing.
  if (!ok)
    return <p className="my-2 text-[13.5px] text-faint">{S.diagram.failed}</p>;

  function save() {
    const d = full ?? tryDraw(ir);
    if (d) download(fileName("flow"), toSvgFile(d, card.current));
    else setFailedFor(ir);
  }
  function openFull() {
    if (tryDraw(ir)) setOpen(true);
    else setFailedFor(ir);
  }

  const said =
    hot === null || lit === null
      ? ""
      : S.diagram.connections(
          names.get(hot) ?? hot,
          [...lit].map(([k, w]) =>
            w.text === ""
              ? (names.get(k) ?? k)
              : `${names.get(k) ?? k} (${w.text})`,
          ),
        );

  return (
    <figure className="my-3" aria-label={title} ref={card}>
      <DiagramTools title={title} onFull={openFull} onSave={save} />
      {broken && (
        <p className="mb-2 text-[13.5px] text-faint">{S.diagram.failed}</p>
      )}
      <div className="rounded-ui-sm border border-border bg-panel px-4 py-2">
        {bands.map((b) => {
          const boxes = b.zone === "repositories";
          // Every gap above a row of nodes keeps 8px or more, the room a lit
          // node's legend takes above its border (Node's wayMark): the
          // band's top padding, the gap under its title in one column (a
          // phone), the gaps between rows.
          return (
            <div
              key={b.zone ?? ""}
              role="group"
              aria-label={b.title || undefined}
              className={
                "grid gap-x-3 gap-y-2.5 border-border-soft py-2.5 not-first:border-t sm:grid-cols-[150px_minmax(0,1fr)] " +
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
                    : "flex flex-wrap gap-x-1.5 gap-y-2"
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
                      point={points.get(id)!}
                    />
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
        {/* Lit, the line says the box's ways whole, which its neighbours'
            legends may cut. Under every node: its height moves none. */}
        <p
          // The ways are said by the live region below, once.
          aria-hidden={said !== "" || undefined}
          className={
            "pt-1 pb-1.5 text-[12px] " +
            (said === "" ? "text-faint" : "text-ink-dim")
          }
        >
          {said === "" ? S.diagram.compactHint : said}
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
          onClose={() => setOpen(false)}
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
  onTouchMove: () => void;
  onTouchCancel: () => void;
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
const Node = memo(function Node({
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
    "relative border bg-bg text-left transition-[opacity,border-color] duration-100 hover:border-accent-strong focus-visible:outline-2 focus-visible:outline-accent-strong " +
    (box
      ? "block rounded-ui-sm px-3 py-2 "
      : "inline-flex items-baseline gap-1.5 rounded-full px-2.5 py-[3px] text-[12.5px] text-ink-dim " +
        (to === null ? "" : "font-mono ")) +
    tone[state];
  // The way sits on the node's top border, a legend cut to the node's
  // width: it takes no room. Written into the node it grew it (a long way
  // wrapped under a long slug), the rows below moved, and a box pointed at
  // near its top slid from under the pointer, put its light out, slid back
  // and lit again, flickering until the pointer moved. The light changes
  // colours, never a size. The full way is said to a screen reader (said).
  // A chip's legend keeps to the straight part of its pill's border. Cut
  // short, the way is read whole in the line under the map (CompactMap).
  // Its 8px above the border are the room the rows keep (see the bands).
  const wayMark = way && way.text !== "" && (
    <span
      aria-hidden="true"
      className={
        "pointer-events-none absolute -top-[8px] truncate bg-panel px-1 font-sans text-[11px] leading-3 text-ochre " +
        (box
          ? "left-2 max-w-[calc(100%-1rem)]"
          : "left-3 max-w-[calc(100%-1.5rem)]")
      }
    >
      {way.text}
    </span>
  );
  const body = box ? (
    <>
      <span className="block truncate font-mono text-[13px] text-ink">
        {label}
      </span>
      {wayMark}
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
});

/** tryDraw is the map's drawing, or null where its layout throws: the
 *  full view and the file are lost then, never the page. */
function tryDraw(ir: FlowIR): ReturnType<typeof drawMap> | null {
  try {
    return drawMap(ir, "almanach-system-map");
  } catch {
    return null;
  }
}
