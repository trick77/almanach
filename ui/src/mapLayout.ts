// Almanach's own, not rongo's: a map laid out by code, not by ELK.
import type { FlowIR } from "./diagramIR";
import {
  boxWidth,
  frame,
  labelBox,
  lay,
  margin,
  tidy,
  type EdgeRoute,
  type FlowLayout,
  type LabelBox,
  type NodeBox,
  type Point,
  type ZoneBox,
} from "./diagramLayout";
import { ramp, textWidth } from "./diagramTheme";

/**
 * A map, the system map or a repository's: its zones stacked as bands, top
 * to bottom in the order a request travels (who calls, where it enters, the
 * parts, the stores, what is outside), each band's boxes in rows no wider
 * than the page column. A zone too wide for one row wraps into more, so the
 * map grows downwards and is never shrunk to fit (user, 2026-11-01: graphs
 * grow vertically too).
 *
 * Not ELK: its layered layout put every hop of a chain in a layer of its
 * own and the zones beside each other, loom's 21 boxes 2310px wide. A layer
 * bound (COFFMAN_GRAHAM layerBound) does not hold inside zones, nor does a
 * layer chosen per node, and on a flat graph its edge dummies kept it past
 * 1900px all the same (spiked 2026-11-01).
 *
 * Arrows run in the gaps, at right angles: out of a box's bottom (its top
 * when the target sits higher), along a track of the channel between two
 * rows, down a lane of the gutter between two columns past the rows between,
 * and along a track of the channel next to the target into it. Every
 * stretch of track and lane is one arrow's, every label sits above its own
 * track, slid clear of the lines crossing it.
 */

/** The widest a map shows unscaled: the page column's main track (Rail.tsx:
 * 1400px less its padding, the rail and the gap beside it). Wider, the card
 * shrinks it and its text with it. */
export const pageColumn = 1080;
/** The bands' order; a zone not named here comes after them, as written. */
const bandOrder = [
  "clients",
  "entry",
  "repositories",
  "parts",
  "shared",
  "stores",
  "external",
];
/** At most this many boxes side by side, however narrow they are. */
const maxPerRow = 5;
/** Between two columns, and outside the outer ones: room for five lanes,
 * the outer two 8px clear of the boxes. */
const gutter = 40;
const laneOffsets = [0, -12, 12, -6, 6];
/** A frame's inner margin above (its title) and below its rows. */
const zonePad = { top: 44, bottom: 20 };
/** Between a frame and the tracks outside it. */
const frameGap = 12;
/** The shortest straight run at a box, room for an arrowhead. */
const stub = 16;
/** The least space between two rows of one band. */
const rowGap = 40;
/** Between two tracks without a label. */
const trackStep = 12;
/** A label's bottom above its track, and its clearance from other lines. */
const labelGap = 8;
const labelClear = 6;
/** Where drawFlow sets a zone's title. */
const titleX = 18;
/** Ports of a box's top and bottom sit this far either side of where a
 * spread puts them, so a box's way out never lines up with the way into the
 * box under it. */
const portSkew = 6;

type Band = { zone: string | null; title: string; ids: string[] };
type Row = { band: number; ids: string[]; top: number; h: number };
type Side = "top" | "bottom";
type Seg = {
  edge: number;
  ch: number;
  x0: number;
  x1: number;
  key: number;
  label: { lines: string[]; w: number; h: number } | null;
  y: number;
};
type Route = {
  edge: number;
  from: string;
  to: string;
  out: Side;
  in: Side;
  lane: number | null;
  segs: Seg[];
};

/** layoutMap places a map. Its columns are as many as fit the page column,
 * at most maxPerRow, and fewer while the map would be wider than twice its
 * height: a small map stays a block, not a strip. */
export function layoutMap(ir: FlowIR): FlowLayout {
  const w = boxWidth(ir.nodes, 160, 200);
  const fit = Math.floor((pageColumn - 2 * margin - gutter) / (w + gutter));
  const bands = bandsOf(ir);
  const widest = Math.max(1, ...bands.map((b) => b.ids.length));
  let out: FlowLayout | null = null;
  for (
    let cols = clamp(Math.min(fit, widest), 1, maxPerRow);
    cols >= 1;
    cols--
  ) {
    out = place(ir, bands, w, cols);
    if (out.width <= 2 * out.height) break;
  }
  return out!;
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(hi, v));
}

/** bandsOf is the map's zones in band order, each with its nodes as
 * written; nodes in no declared zone make a last band without a frame. */
function bandsOf(ir: FlowIR): Band[] {
  const rank = (id: string) => {
    const i = bandOrder.indexOf(id);
    return i < 0 ? bandOrder.length : i;
  };
  const zones = ir.zones
    .map((z, i) => ({ ...z, i }))
    .sort((a, b) => rank(a.id) - rank(b.id) || a.i - b.i);
  const known = new Set(zones.map((z) => z.id));
  const bands: Band[] = zones.map((z) => ({
    zone: z.id,
    title: z.title,
    ids: ir.nodes.filter((n) => n.zone === z.id).map((n) => n.id),
  }));
  const loose = ir.nodes
    .filter((n) => n.zone === undefined || !known.has(n.zone))
    .map((n) => n.id);
  bands.push({ zone: null, title: "", ids: loose });
  return bands.filter((b) => b.ids.length > 0);
}

/** bandLayers is a band's boxes in layers by the arrows between them: a
 * box one layer below the lowest box of the band pointing at it, a cycle
 * broken at the arrow that closes it, walked as written. */
function bandLayers(
  ids: string[],
  ir: FlowIR,
  written: Map<string, number>,
): string[][] {
  const inBand = new Set(ids);
  const outs = new Map<string, string[]>(ids.map((id) => [id, []]));
  for (const e of ir.edges)
    if (e.from !== e.to && inBand.has(e.from) && inBand.has(e.to))
      outs.get(e.from)!.push(e.to);
  // Depth-first, as written: an arrow back to a box on the way is a cycle's.
  const state = new Map<string, "on" | "done">();
  const ins = new Map<string, string[]>(ids.map((id) => [id, []]));
  const walk = (id: string) => {
    state.set(id, "on");
    for (const to of outs.get(id)!) {
      if (state.get(to) === "on") continue;
      ins.get(to)!.push(id);
      if (!state.has(to)) walk(to);
    }
    state.set(id, "done");
  };
  for (const id of ids) if (!state.has(id)) walk(id);
  const layer = new Map<string, number>();
  const depth = (id: string): number => {
    if (!layer.has(id)) {
      layer.set(id, 0);
      layer.set(id, Math.max(0, ...ins.get(id)!.map((u) => depth(u) + 1)));
    }
    return layer.get(id)!;
  };
  const out: string[][] = [];
  for (const id of ids) (out[depth(id)] ??= []).push(id);
  return out
    .filter((l) => l !== undefined)
    .map((l) => l.sort((a, b) => written.get(a)! - written.get(b)!));
}

function place(ir: FlowIR, bands: Band[], w: number, cols: number): FlowLayout {
  const pitch = w + gutter;
  const zoneW = cols * pitch + gutter;
  const colX = (c: number) => gutter + c * pitch;
  const written = new Map(ir.nodes.map((n, i) => [n.id, i]));
  const near = new Map<string, string[]>(ir.nodes.map((n) => [n.id, []]));
  for (const e of ir.edges) {
    if (e.from === e.to) continue;
    near.get(e.from)?.push(e.to);
    near.get(e.to)?.push(e.from);
  }

  // Rows. Inside a band, a box sits a layer below every box of the band
  // that points at it, so an arrow inside a band runs down to the next
  // row, not across (loom's parts: the job manager, then what it starts,
  // then what those use). Whole layers share a row while they fit; one too
  // wide wraps into rows filled column by column, so its order across still
  // reads along x. That order is as written first, then, twice, by where
  // each box's neighbours sit; a row then takes the columns nearest them.
  // A title's run, where no lane or stub may cross it.
  const titleOf = (bi: number) => {
    const t = bands[bi].zone === null ? "" : bands[bi].title;
    return t === ""
      ? null
      : {
          x0: titleX - labelClear,
          x1: titleX + textWidth(t, ramp.bold, "sans") + labelClear,
        };
  };
  const layers = bands.map((b) => bandLayers(b.ids, ir, written));
  const cx = new Map<string, number>();
  const col = new Map<string, number>();
  const want = new Map<string, number>();
  let rows: Row[] = [];
  const pack = () => {
    rows = [];
    layers.forEach((ls, bi) => {
      let cur: Row | null = null;
      for (const l of ls) {
        if (l.length > cols) {
          const r = Math.ceil(l.length / cols);
          const chunks = Array.from({ length: r }, () => [] as string[]);
          l.forEach((id, k) => chunks[k % r].push(id));
          for (const ids of chunks) rows.push({ band: bi, ids, top: 0, h: 0 });
          cur = null;
        } else if (cur && cur.ids.length + l.length <= cols) {
          cur.ids.push(...l);
        } else {
          cur = { band: bi, ids: [...l], top: 0, h: 0 };
          rows.push(cur);
        }
      }
    });
    rows.forEach((row, k) => {
      const n = row.ids.length;
      // A band's first row leaves the first column to a title too wide
      // for an arrow to reach the box beside it, when it has room to.
      const t = titleOf(row.band);
      const first = k === 0 || rows[k - 1].band !== row.band;
      const min =
        first && t && colX(0) + w - 12 - 16 <= t.x1 && n < cols ? 1 : 0;
      let off = Math.max(min, Math.floor((cols - n) / 2));
      let cost = Infinity;
      if (row.ids.some((id) => want.has(id)))
        for (let o = min; o <= cols - n; o++) {
          const c = row.ids.reduce(
            (s, id, k) =>
              s +
              Math.abs(
                colX(o + k) + w / 2 - (want.get(id) ?? colX(o + k) + w / 2),
              ),
            0,
          );
          if (c < cost) [off, cost] = [o, c];
        }
      row.ids.forEach((id, k) => {
        col.set(id, off + k);
        cx.set(id, colX(off + k) + w / 2);
      });
    });
  };
  pack();
  for (let pass = 0; pass < 2; pass++) {
    want.clear();
    for (const n of ir.nodes) {
      const o = near.get(n.id)!;
      if (o.length > 0)
        want.set(n.id, o.reduce((s, m) => s + cx.get(m)!, 0) / o.length);
    }
    const at = (id: string) => want.get(id) ?? cx.get(id)!;
    for (const ls of layers)
      for (const l of ls)
        l.sort((a, b) => at(a) - at(b) || written.get(a)! - written.get(b)!);
    pack();
  }

  const firstRow: number[] = [];
  rows.forEach((r, k) => (firstRow[r.band] ??= k));
  const rowOf = new Map<string, number>();
  rows.forEach((r, k) => r.ids.forEach((id) => rowOf.set(id, k)));
  const boxes = new Map<string, NodeBox>();
  for (const n of ir.nodes) {
    const b = lay(n, n.shape === "pill" ? "pill" : "step", w);
    b.x = colX(col.get(n.id)!);
    boxes.set(n.id, b);
  }
  for (const r of rows) r.h = Math.max(...r.ids.map((id) => boxes.get(id)!.h));

  // Lanes: an arrow past rows between its ends takes the free lane of the
  // gutter nearest the straight way, clear of every title it passes.
  const lanes: { x: number; lo: number; hi: number }[] = [];
  const routes: Route[] = [];
  ir.edges.forEach((e, i) => {
    if (e.from === e.to || !boxes.has(e.from) || !boxes.has(e.to)) return;
    const ru = rowOf.get(e.from)!;
    const rv = rowOf.get(e.to)!;
    const down = rv > ru;
    const out: Side = rv < ru ? "top" : "bottom";
    const into: Side = down ? "top" : "bottom";
    const c1 = out === "bottom" ? ru + 1 : ru;
    const c2 = into === "top" ? rv : rv + 1;
    let lane: number | null = null;
    if (c1 !== c2) {
      const lo = Math.min(c1, c2);
      const hi = Math.max(c1, c2);
      const passed = bands
        .map((_, bi) => bi)
        .filter((bi) => lo <= firstRow[bi] && firstRow[bi] < hi)
        .map(titleOf)
        .filter((t) => t !== null);
      const gx = (j: number) => j * pitch + gutter / 2;
      const a = cx.get(e.from)!;
      const b = cx.get(e.to)!;
      const order = Array.from({ length: cols + 1 }, (_, j) => j).sort(
        (p, q) =>
          Math.abs(gx(p) - a) +
            Math.abs(gx(p) - b) -
            (Math.abs(gx(q) - a) + Math.abs(gx(q) - b)) || p - q,
      );
      search: for (const j of order) {
        for (const off of laneOffsets) {
          const x = gx(j) + off;
          if (passed.some((t) => x >= t.x0 && x <= t.x1)) continue;
          if (lanes.some((l) => l.x === x && l.lo <= hi && lo <= l.hi))
            continue;
          lane = x;
          break search;
        }
      }
      lane ??= gx(order[0]);
      lanes.push({ x: lane, lo, hi });
    }
    routes.push({
      edge: i,
      from: e.from,
      to: e.to,
      out,
      in: into,
      lane,
      segs: [
        { edge: i, ch: c1, x0: 0, x1: 0, key: 0, label: null, y: 0 },
        ...(lane !== null
          ? [{ edge: i, ch: c2, x0: 0, x1: 0, key: 0, label: null, y: 0 }]
          : []),
      ],
    });
  });

  // Ports: each side's arrows spread along it, in the order of where each
  // heads, so they leave without crossing. A first row's top keeps clear of
  // its band's title.
  type End = { route: Route; from: boolean; toward: number };
  const sides = new Map<string, { id: string; side: Side; ends: End[] }>();
  const key = (id: string, side: Side) => `${side} ${id}`;
  const attach = (id: string, side: Side, end: End) => {
    const k = key(id, side);
    if (!sides.has(k)) sides.set(k, { id, side, ends: [] });
    sides.get(k)!.ends.push(end);
  };
  for (const r of routes) {
    attach(r.from, r.out, {
      route: r,
      from: true,
      toward: r.lane ?? cx.get(r.to)!,
    });
    attach(r.to, r.in, {
      route: r,
      from: false,
      toward: r.lane ?? cx.get(r.from)!,
    });
  }
  const port = new Map<Route, { from: number; to: number }>();
  for (const r of routes) port.set(r, { from: 0, to: 0 });
  for (const { id, side, ends } of sides.values()) {
    const b = boxes.get(id)!;
    let lo = b.x + 12;
    const hi = b.x + b.w - 12;
    const ri = rowOf.get(id)!;
    const t = titleOf(rows[ri].band);
    if (
      side === "top" &&
      t &&
      firstRow[rows[ri].band] === ri &&
      lo <= t.x1 &&
      hi - (t.x1 + 1) >= 16
    )
      lo = t.x1 + 1;
    const skew = side === "top" ? -portSkew : portSkew;
    ends
      .sort((p, q) => p.toward - q.toward || p.route.edge - q.route.edge)
      .forEach((end, j) => {
        const x = Math.round(
          lo + ((hi - lo) * (j + 1)) / (ends.length + 1) + skew,
        );
        const p = port.get(end.route)!;
        if (end.from) p.from = clamp(x, lo, hi);
        else p.to = clamp(x, lo, hi);
      });
  }
  // A lone arrow between two boxes one above the other runs straight.
  for (const r of routes) {
    const p = port.get(r)!;
    const alone = (id: string, s: Side) =>
      sides.get(key(id, s))!.ends.length === 1;
    if (
      r.lane === null &&
      r.out !== r.in &&
      Math.abs(p.from - p.to) <= 2 * portSkew + 4 &&
      alone(r.from, r.out) &&
      alone(r.to, r.in)
    )
      p.from = p.to = Math.round((p.from + p.to) / 2);
  }

  // Segments on their channels. key orders the tracks: one whose lines
  // both go up sits highest, both down lowest, so few lines cross a track.
  const below = (ch: number, rowIdx: number) => (rowIdx === ch ? 1 : -1);
  const segsAt = new Map<number, Seg[]>();
  for (const r of routes) {
    const p = port.get(r)!;
    const ru = rowOf.get(r.from)!;
    const rv = rowOf.get(r.to)!;
    const e = ir.edges[r.edge];
    const label = e.label !== "" ? labelBox(e.label) : null;
    const [a, b] = r.segs;
    if (r.lane === null) {
      Object.assign(a, {
        x0: p.from,
        x1: p.to,
        label,
        key: below(a.ch, ru) + below(a.ch, rv),
      });
    } else {
      const toward = (ch: number, other: number) => (other > ch ? 1 : -1);
      Object.assign(a, {
        x0: p.from,
        x1: r.lane,
        key: below(a.ch, ru) + toward(a.ch, b.ch),
      });
      Object.assign(b, {
        x0: r.lane,
        x1: p.to,
        label,
        key: toward(b.ch, a.ch) + below(b.ch, rv),
      });
    }
    for (const s of r.segs) segsAt.set(s.ch, [...(segsAt.get(s.ch) ?? []), s]);
  }

  // Tracks: a segment shares a track with others of its key whose run,
  // label included, it does not meet.
  type Track = {
    key: number;
    spans: [number, number][];
    labelH: number;
    segs: Seg[];
  };
  const tracksAt = new Map<number, Track[]>();
  for (const [ch, segs] of segsAt) {
    const tracks: Track[] = [];
    segs.sort((p, q) => p.key - q.key || p.edge - q.edge);
    for (const s of segs) {
      let a = Math.min(s.x0, s.x1) - 8;
      let b = Math.max(s.x0, s.x1) + 8;
      if (s.label) {
        const mid = (s.x0 + s.x1) / 2;
        const l0 = clamp(mid - s.label.w / 2, 0, zoneW - s.label.w);
        a = Math.min(a, l0 - 8);
        b = Math.max(b, l0 + s.label.w + 8);
      }
      let t = tracks.find(
        (t) => t.key === s.key && t.spans.every(([c, d]) => b < c || a > d),
      );
      if (!t) {
        t = { key: s.key, spans: [], labelH: 0, segs: [] };
        tracks.push(t);
      }
      t.spans.push([a, b]);
      t.segs.push(s);
      if (s.label) t.labelH = Math.max(t.labelH, s.label.h);
    }
    tracksAt.set(ch, tracks);
  }
  const lay_tracks = (ch: number, start: number): number => {
    let y = start;
    for (const t of tracksAt.get(ch) ?? []) {
      y += t.labelH > 0 ? labelClear + t.labelH + labelGap : trackStep;
      for (const s of t.segs) s.y = y;
    }
    return y;
  };

  // Down the page: rows, the channels between them, the frames.
  const zoneTop: number[] = [];
  const zoneBottom: number[] = [];
  const R = rows.length;
  zoneTop[rows[0].band] = 0;
  {
    const end = lay_tracks(0, zonePad.top);
    rows[0].top = end + (end > zonePad.top ? stub : 0);
  }
  for (let c = 1; c <= R; c++) {
    const prev = rows[c - 1];
    const bottom = prev.top + prev.h;
    if (c === R) {
      const end = lay_tracks(c, bottom + stub);
      zoneBottom[prev.band] = Math.max(bottom + zonePad.bottom, end + stub);
    } else if (rows[c].band === prev.band) {
      const end = lay_tracks(c, bottom + stub);
      rows[c].top = Math.max(end + stub, bottom + rowGap);
    } else {
      zoneBottom[prev.band] = bottom + zonePad.bottom;
      const end = lay_tracks(c, zoneBottom[prev.band] + frameGap);
      zoneTop[rows[c].band] = end + frameGap;
      rows[c].top = zoneTop[rows[c].band] + zonePad.top;
    }
  }
  for (const r of rows)
    for (const id of r.ids) {
      const b = boxes.get(id)!;
      b.y = r.top + Math.round((r.h - b.h) / 2);
    }

  // The routes as points.
  const at = (id: string, s: Side) => {
    const b = boxes.get(id)!;
    return s === "top" ? b.y : b.y + b.h;
  };
  const edges: EdgeRoute[] = [];
  const verticals: { route: number; x: number; y0: number; y1: number }[] = [];
  const drawn = routes.map((r, ri) => {
    const p = port.get(r)!;
    const [a, b] = r.segs;
    const points: Point[] = [
      { x: p.from, y: at(r.from, r.out) },
      { x: p.from, y: a.y },
    ];
    if (r.lane !== null)
      points.push({ x: r.lane, y: a.y }, { x: r.lane, y: b.y });
    const last = r.lane !== null ? b : a;
    points.push({ x: p.to, y: last.y }, { x: p.to, y: at(r.to, r.in) });
    const pts = tidy(points);
    for (let k = 1; k < pts.length; k++)
      if (pts[k].x === pts[k - 1].x)
        verticals.push({
          route: ri,
          x: pts[k].x,
          y0: Math.min(pts[k].y, pts[k - 1].y),
          y1: Math.max(pts[k].y, pts[k - 1].y),
        });
    const e = ir.edges[r.edge];
    const route: EdgeRoute = {
      from: e.from,
      to: e.to,
      points: pts,
      dashed: e.dashed,
      warn: e.dashed,
      head: e.head,
    };
    return { route, seg: last };
  });

  // Labels above their tracks, slid along the track to where the fewest
  // other lines cross them; never onto their own line or another label.
  // Within reach of the stretch they name first, the whole track after.
  const placed: LabelBox[] = [];
  drawn.forEach(({ route, seg }, ri) => {
    edges.push(route);
    const l = seg.label;
    if (!l) return;
    const y = seg.y - labelGap - l.h;
    const a = Math.min(seg.x0, seg.x1);
    const b = Math.max(seg.x0, seg.x1);
    const base = clamp(Math.round((a + b) / 2 - l.w / 2), 0, zoneW - l.w);
    const crossing = (x: number) =>
      verticals.filter(
        (v) =>
          v.x >= x - labelClear &&
          v.x <= x + l.w + labelClear &&
          v.y1 > y - 2 &&
          v.y0 < y + l.h + 2,
      );
    const clear = (x: number) =>
      crossing(x).every((v) => v.route !== ri) &&
      placed.every(
        (o) =>
          o.x + o.w + 4 <= x ||
          x + l.w + 4 <= o.x ||
          o.y + o.h <= y ||
          y + l.h <= o.y,
      );
    const near = (x: number) => x + l.w >= a - 24 && x <= b + 24;
    let best: { x: number; n: number; d: number } | null = null;
    for (const within of [true, false]) {
      for (let x = 0; x <= zoneW - l.w; x += 2) {
        if ((within && !near(x)) || !clear(x)) continue;
        const n = crossing(x).length;
        const d = Math.abs(x - base);
        if (!best || n < best.n || (n === best.n && d < best.d))
          best = { x, n, d };
      }
      if (best) break;
    }
    route.label = { ...l, x: best?.x ?? base, y };
    placed.push(route.label);
  });

  const zones: ZoneBox[] = [];
  bands.forEach((b, bi) => {
    if (b.zone === null) return;
    zones.push({
      id: b.zone,
      title: b.title,
      x: 0,
      y: zoneTop[bi],
      w: zoneW,
      h: zoneBottom[bi] - zoneTop[bi],
    });
  });
  return frame(
    ir.nodes.map((n) => boxes.get(n.id)!),
    edges,
    zones,
  );
}
