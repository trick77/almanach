import type {
  MessageSchema,
  RepoMessaging,
  RepoRef,
  RepoTopic,
  TopicFlow,
} from "./api";
import { Diagram, titledZones } from "./Diagram";
import { fits, type FlowIR, type FlowNodeIR } from "./diagramIR";
import { refName } from "./format";
import { factPath } from "./repoFacts";
import { repoPath } from "./routing";
import { S } from "./strings";
import { Link, mono, table, tableBox, td, tdMono, th } from "./ui";

// A repository's messages and a project's: drawn by code from what the
// store joined, never a model's. No validation to pass; every label is
// escaped by the drawing as any other.

const repoId = (side: string, r: RepoRef) => `${side}:${r.project}/${r.slug}`;

/** repoLink is the page a node of a messages diagram links to: a
 *  repository's, none for a topic. */
function repoLink(id: string): string | null {
  const m = /^(?:from|to|repo):([^/]+)\/(.+)$/.exec(id);
  return m ? repoPath(m[1], m[2]) : null;
}

/** topicGraph is a repository's messages left to right: the repositories
 *  that send what it receives, those topics, the repository, the topics it
 *  sends, the repositories that receive them. A topic only named is in the
 *  table, not drawn. */
export function topicGraph(
  self: RepoRef,
  topics: RepoTopic[],
  name: (r: RepoRef) => string,
): FlowIR {
  const nodes = new Map<string, FlowNodeIR>();
  const edges: FlowIR["edges"] = [];
  const me = repoId("repo", self);
  const node = (n: FlowNodeIR) => {
    if (!nodes.has(n.id)) nodes.set(n.id, n);
  };
  for (const t of topics) {
    if (t.direction === "declares") continue;
    const inbound = t.direction === "consumes";
    const tid = `${inbound ? "in" : "out"}:${t.name}`;
    node({ id: me, label: name(self), shape: "pill", zone: "self" });
    node({
      id: tid,
      label: t.name,
      shape: "step",
      zone: inbound ? "in" : "out",
    });
    if (inbound) {
      edges.push({ from: tid, to: me, label: "", dashed: false, head: "end" });
    } else {
      edges.push({ from: me, to: tid, label: "", dashed: false, head: "end" });
    }
    for (const p of t.peers) {
      const pid = repoId(inbound ? "from" : "to", p);
      node({
        id: pid,
        label: name(p),
        shape: "pill",
        zone: inbound ? "from" : "to",
      });
      edges.push(
        inbound
          ? { from: pid, to: tid, label: "", dashed: false, head: "end" }
          : { from: tid, to: pid, label: "", dashed: false, head: "end" },
      );
    }
  }
  return titledZones(
    {
      kind: "flow",
      direction: "LR",
      zones: ["from", "in", "self", "out", "to"].map((id) => ({
        id,
        title: id,
      })),
      nodes: [...nodes.values()],
      edges,
    },
    S.diagram.msgZones,
  );
}

/** flowGraph is a project's messages: each sender to its topic, each topic
 *  to its receivers, the project's repositories and those of others alike.
 *  A topic only named is in the table, not drawn. */
export function flowGraph(
  flows: TopicFlow[],
  name: (r: RepoRef) => string,
): FlowIR {
  const nodes = new Map<string, FlowNodeIR>();
  const edges: FlowIR["edges"] = [];
  for (const f of flows) {
    if (f.producers.length + f.consumers.length === 0) continue;
    const tid = `topic:${f.name}`;
    nodes.set(tid, { id: tid, label: f.name, shape: "step" });
    for (const p of f.producers) {
      const id = repoId("repo", p);
      nodes.set(id, { id, label: name(p), shape: "pill" });
      edges.push({ from: id, to: tid, label: "", dashed: false, head: "end" });
    }
    for (const c of f.consumers) {
      const id = repoId("repo", c);
      nodes.set(id, { id, label: name(c), shape: "pill" });
      edges.push({ from: tid, to: id, label: "", dashed: false, head: "end" });
    }
  }
  return {
    kind: "flow",
    direction: "LR",
    zones: [],
    nodes: [...nodes.values()],
    edges,
  };
}

/** namer names a repository by its slug in its own project, else
 *  project/slug. */
function namer(project: string): (r: RepoRef) => string {
  return (r) => (r.project === project ? r.slug : refName(r.project, r.slug));
}

const note = "my-2 text-[13.5px] text-faint";

/** The section's column: one track that never grows past the page's, so a
 *  long topic name scrolls its table instead of widening the page. */
const stack = "grid grid-cols-[minmax(0,1fr)] gap-y-4";

/** Drawing is a messages diagram, or why there is none. */
function Drawing({ ir }: { ir: FlowIR }) {
  if (ir.edges.length === 0) return null;
  if (!fits(ir))
    return <p className={note}>{S.msg.tooLarge(ir.nodes.length)}</p>;
  return <Diagram ir={ir} title={S.msg.title} links={repoLink} />;
}

/** Peers is a list of repositories, each linked to its page; with toFacts,
 *  to its messages there (a project's table: the start tab shows none). */
function Peers({
  refs,
  name,
  toFacts = false,
}: {
  refs: RepoRef[];
  name: (r: RepoRef) => string;
  toFacts?: boolean;
}) {
  if (refs.length === 0)
    return <span className="text-faint">{S.msg.noPeer}</span>;
  return (
    <>
      {refs.map((r, i) => (
        <span key={`${r.project}/${r.slug}`}>
          {i > 0 && ", "}
          <Link
            to={
              toFacts
                ? factPath(r.project, r.slug, "messages")
                : repoPath(r.project, r.slug)
            }
            className={mono + " text-ink-dim hover:text-ink"}
          >
            {name(r)}
          </Link>
        </span>
      ))}
    </>
  );
}

const directionWord: Record<RepoTopic["direction"], string> = {
  produces: S.msg.produces,
  consumes: S.msg.consumes,
  declares: S.msg.declares,
};

/** RepoMessages is a repository's section: the diagram, its topics with
 *  the far side of each, its schemas. */
export function RepoMessages({
  self,
  data,
}: {
  self: RepoRef;
  data: RepoMessaging;
}) {
  const name = namer(self.project);
  return (
    <div className={stack}>
      <p className={note}>{S.msg.hint}</p>
      <Drawing ir={topicGraph(self, data.topics, name)} />
      {data.topics.length > 0 && (
        <div className={tableBox}>
          <table className={table}>
            <thead>
              <tr>
                <th className={th}>{S.msg.topic}</th>
                <th className={th + " w-[110px]"}>{S.msg.direction}</th>
                <th className={th}>{S.msg.peers}</th>
              </tr>
            </thead>
            <tbody>
              {data.topics.map((t) => (
                <tr key={`${t.name} ${t.direction}`}>
                  <td className={tdMono} title={t.files.join("\n")}>
                    {t.name}
                  </td>
                  <td className={td}>{directionWord[t.direction]}</td>
                  <td className={td}>
                    {t.direction === "declares" ? null : (
                      <Peers refs={t.peers} name={name} />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Schemas schemas={data.schemas} />
    </div>
  );
}

function Schemas({ schemas }: { schemas: MessageSchema[] }) {
  if (schemas.length === 0) return null;
  return (
    <div className={tableBox}>
      <table className={table}>
        <thead>
          <tr>
            <th className={th}>{S.msg.schema}</th>
            <th className={th + " w-[80px]"}>{S.msg.kind}</th>
            <th className={th}>{S.msg.file}</th>
          </tr>
        </thead>
        <tbody>
          {schemas.map((s) => (
            <tr key={`${s.kind} ${s.name}`}>
              <td className={tdMono}>{s.name}</td>
              <td className={td}>{s.kind}</td>
              <td className={tdMono + " text-muted"}>{s.file}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** ProjectMessages is a project's section: who sends what to whom. */
export function ProjectMessages({
  project,
  flows,
}: {
  project: string;
  flows: TopicFlow[];
}) {
  const name = namer(project);
  return (
    <div className={stack}>
      <p className={note}>{S.msg.hint}</p>
      <Drawing ir={flowGraph(flows, name)} />
      <div className={tableBox}>
        <table className={table}>
          <thead>
            <tr>
              <th className={th}>{S.msg.topic}</th>
              <th className={th}>{S.msg.producers}</th>
              <th className={th}>{S.msg.consumers}</th>
            </tr>
          </thead>
          <tbody>
            {flows.map((f) => (
              <tr key={f.name}>
                <td className={tdMono}>{f.name}</td>
                <td className={td}>
                  <Peers refs={f.producers} name={name} toFacts />
                </td>
                <td className={td}>
                  <Peers refs={f.consumers} name={name} toFacts />
                  {f.declared.length > 0 && (
                    <span className="ml-2 text-[12.5px] text-faint">
                      {S.msg.declared} {f.declared.map(name).join(", ")}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
