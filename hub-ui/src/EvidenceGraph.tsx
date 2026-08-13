import { useEffect, useMemo, useState } from "react";
import {
  Background, BackgroundVariant, BaseEdge, Controls, Handle, MarkerType, Position, ReactFlow,
  type Edge, type EdgeProps, type Node, type NodeProps,
} from "@xyflow/react";
import {
  AlertCircle, ArrowDownLeft, ArrowUpRight, Bot, BrainCircuit, CheckCircle2,
  Container, Database, FileSearch, GitBranch, Link2, LoaderCircle, Monitor,
  Network, PlugZap, Search, Server, TerminalSquare, UserRound, Workflow,
  type LucideIcon,
} from "lucide-react";
import { API, type Confidence, type Connection, type Evidence, type SystemDetail, type SystemItem } from "./api";

type GraphNodeData = {
  role: "root" | "entity" | "evidence";
  kind: string;
  name: string;
  detail: string;
  confidence: Confidence;
  connections?: Connection[];
  evidence?: Evidence;
  system?: SystemDetail;
};

type LensNode = Node<GraphNodeData, "lens">;
type GraphModel = { nodes: LensNode[]; edges: Edge[]; hiddenConnections: number; visibleEvidence: number };

const nodeTypes = { lens: LensNodeCard };

function FlowingEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition,
  markerEnd, style, label, labelStyle, labelBgStyle, labelBgPadding, labelBgBorderRadius }: EdgeProps) {
  const dx = targetX - sourceX;
  const dy = targetY - sourceY;
  const dist = Math.sqrt(dx * dx + dy * dy);
  const cap = Math.max(Math.abs(dx) * 0.82, Math.abs(dy) * 0.82, 50);
  const offset = Math.min(dist * 0.45, cap);

  let cp1x = sourceX, cp1y = sourceY;
  let cp2x = targetX, cp2y = targetY;
  if (sourcePosition === Position.Right)       cp1x += offset;
  else if (sourcePosition === Position.Left)   cp1x -= offset;
  else if (sourcePosition === Position.Top)    cp1y -= offset;
  else                                          cp1y += offset;
  if (targetPosition === Position.Left)        cp2x -= offset;
  else if (targetPosition === Position.Right)  cp2x += offset;
  else if (targetPosition === Position.Top)    cp2y -= offset;
  else                                          cp2y += offset;

  const path = `M${sourceX},${sourceY} C${cp1x},${cp1y} ${cp2x},${cp2y} ${targetX},${targetY}`;
  const lx = 0.125 * sourceX + 0.375 * cp1x + 0.375 * cp2x + 0.125 * targetX;
  const ly = 0.125 * sourceY + 0.375 * cp1y + 0.375 * cp2y + 0.125 * targetY;
  return (
    <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style}
      label={label} labelX={lx} labelY={ly}
      labelStyle={labelStyle} labelBgStyle={labelBgStyle}
      labelBgPadding={labelBgPadding as [number, number]}
      labelBgBorderRadius={labelBgBorderRadius} />
  );
}

const edgeTypes = { flowing: FlowingEdge };
const confidenceRank: Record<Confidence, number> = { confirmed: 0, likely: 1, possible: 2 };
const kindIcons: Record<string, LucideIcon> = {
  endpoint: Monitor, repository: GitBranch, cluster: Container, workload: Container,
  agent: Bot, runtime: TerminalSquare, framework: Network, mcp_server: PlugZap,
  skill: CheckCircle2, model: BrainCircuit, model_server: Server, api_service: Database,
  api_operation: Link2, workflow: Workflow, user: UserRound, evidence: FileSearch,
};

export function EvidenceGraphPage({ api, revision }: { api: API; revision: number }) {
  const [systems, setSystems] = useState<SystemItem[]>([]);
  const [selectedSystem, setSelectedSystem] = useState("");
  const [detail, setDetail] = useState<SystemDetail>();
  const [systemSearch, setSystemSearch] = useState("");
  const [loadingSystems, setLoadingSystems] = useState(true);
  const [loadingGraph, setLoadingGraph] = useState(false);
  const [moreSystems, setMoreSystems] = useState(false);
  const [systemError, setSystemError] = useState("");
  const [graphError, setGraphError] = useState("");

  useEffect(() => {
    let active = true;
    setLoadingSystems(true);
    setSystemError("");
    const timer = window.setTimeout(() => {
      api.systems({ limit: 100, sort: "name", search: systemSearch.trim() }).then((result) => {
        if (!active) return;
        setSystems(result.items);
        setMoreSystems(Boolean(result.next_cursor));
        setSelectedSystem((current) => current || result.items[0]?.id || "");
      }).catch((reason) => active && setSystemError(String(reason))).finally(() => active && setLoadingSystems(false));
    }, systemSearch ? 220 : 0);
    return () => { active = false; window.clearTimeout(timer); };
  }, [api, revision, systemSearch]);

  useEffect(() => {
    if (!selectedSystem) { setDetail(undefined); return; }
    let active = true;
    setLoadingGraph(true);
    setGraphError("");
    api.system(selectedSystem).then((result) => active && setDetail(result)).catch((reason) => active && setGraphError(String(reason))).finally(() => active && setLoadingGraph(false));
    return () => { active = false; };
  }, [api, selectedSystem, revision]);

  const visibleSystems = systems.filter((system) => {
    const query = systemSearch.trim().toLowerCase();
    return !query || `${system.name} ${system.product_id ?? ""} ${system.system_type}`.toLowerCase().includes(query);
  });

  if (loadingSystems && !systems.length && !systemSearch) return <GraphState icon={LoaderCircle} title="Building the system index" detail="Loading root systems for the evidence map." spinning />;
  if (systemError && !systems.length) return <GraphState icon={AlertCircle} title="The graph could not be loaded" detail={systemError} />;
  if (!systems.length && !systemSearch && !loadingSystems) return <GraphState icon={Network} title="No root systems discovered" detail="The graph becomes available when Lens discovers an autonomous agent, agent-capable tool, or model runtime." />;

  return <div className="evidence-map-layout">
    <aside className="panel graph-system-panel">
      <div className="graph-panel-heading"><div><span>ROOT SYSTEMS</span><h2>Choose a system</h2><p>Searches stay server-side so large inventories remain usable.</p></div><b>{loadingSystems ? "…" : `${systems.length}${moreSystems ? "+" : ""}`}</b></div>
      <label className="graph-system-search"><Search size={14} /><input value={systemSearch} onChange={(event) => setSystemSearch(event.target.value)} placeholder="Find a system" aria-label="Find a system" /></label>
      <div className="graph-system-list">
        {visibleSystems.map((system) => <button className={selectedSystem === system.id ? "active" : ""} key={system.id} onClick={() => setSelectedSystem(system.id)} aria-pressed={selectedSystem === system.id}>
          <KindIcon kind={system.kind} /><span><b>{system.name}</b><small>{pretty(system.system_type)} · {pretty(system.state)}</small></span><i className={`confidence-dot ${system.confidence}`} title={`${pretty(system.confidence)} evidence`} />
        </button>)}
        {!visibleSystems.length && !loadingSystems && <p className="graph-list-empty">No systems match “{systemSearch}”.</p>}
        {systemError && <p className="graph-list-empty">{systemError}</p>}
      </div>
    </aside>
    <section className="panel graph-workspace">
      {loadingGraph ? <GraphState icon={LoaderCircle} title="Mapping evidence" detail="Resolving connected inventory and supporting observations." spinning /> : graphError ? <GraphState icon={AlertCircle} title="This system could not be mapped" detail={graphError} /> : detail ? <SystemEvidenceMap detail={detail} /> : null}
    </section>
  </div>;
}

function SystemEvidenceMap({ detail }: { detail: SystemDetail }) {
  const relationCounts = useMemo(() => countRelations(detail.connections), [detail.connections]);
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [showEvidence, setShowEvidence] = useState(true);
  const [selectedNode, setSelectedNode] = useState(detail.id);

  useEffect(() => {
    setHiddenKinds(new Set());
    setQuery("");
    setShowEvidence(true);
    setSelectedNode(detail.id);
  }, [detail.id]);

  const model = useMemo(() => buildGraph(detail, hiddenKinds, query, showEvidence), [detail, hiddenKinds, query, showEvidence]);
  useEffect(() => {
    if (!model.nodes.some((node) => node.id === selectedNode)) setSelectedNode(detail.id);
  }, [detail.id, model.nodes, selectedNode]);
  const selection = model.nodes.find((node) => node.id === selectedNode)?.data ?? model.nodes[0].data;
  const graphKey = `${detail.id}:${[...hiddenKinds].sort().join(",")}:${query}:${showEvidence}`;

  const toggleKind = (kind: string) => setHiddenKinds((current) => {
    const next = new Set(current);
    if (next.has(kind)) next.delete(kind); else next.add(kind);
    return next;
  });

  return <div className="system-evidence-map">
    <header className="graph-titlebar">
      <div><span>SELECTED SYSTEM</span><h2>{detail.name}</h2><p>{pretty(detail.system_type)} · {pretty(detail.state)} · {detail.target_name ?? "Unresolved target"}</p></div>
      <div className="graph-title-facts"><GraphFact label="Connections" value={String(detail.connections.length)} /><GraphFact label="Evidence facts" value={String(detail.evidence.length)} /><GraphFact label="Network" value={pretty(detail.network_scope)} /></div>
    </header>
    <div className="graph-toolbar">
      <label><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter connected nodes" aria-label="Filter connected nodes" /></label>
      <div className="relation-filters" aria-label="Relationship filters">
        {Object.entries(relationCounts).map(([kind, count]) => <button className={hiddenKinds.has(kind) ? "muted" : "active"} onClick={() => toggleKind(kind)} key={kind} aria-pressed={!hiddenKinds.has(kind)}><i className={`edge-swatch relation-${safeClass(kind)}`} />{pretty(kind)} <b>{count}</b></button>)}
        <button className={showEvidence ? "active evidence-toggle" : "muted evidence-toggle"} onClick={() => setShowEvidence((value) => !value)} aria-pressed={showEvidence}><i className="edge-swatch evidence" />Evidence <b>{detail.evidence.length}</b></button>
      </div>
    </div>
    <div className="graph-stage">
      <ReactFlow
        key={graphKey}
        nodes={model.nodes}
        edges={model.edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        fitView
        fitViewOptions={{ padding: 0.22, maxZoom: 1.15 }}
        minZoom={0.18}
        maxZoom={1.8}
        nodesDraggable={false}
        nodesConnectable={false}
        zoomOnDoubleClick={false}
        onNodeClick={(_, node) => setSelectedNode(node.id)}
        onPaneClick={() => setSelectedNode(detail.id)}
        proOptions={{ hideAttribution: true }}
        aria-label={`Evidence graph for ${detail.name}`}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="rgba(255,255,255,.11)" />
        <Controls showInteractive={false} position="bottom-left" />
      </ReactFlow>
      <GraphInspector data={selection} />
      <div className="graph-legend"><span><ArrowDownLeft size={12} /> Incoming</span><span><ArrowUpRight size={12} /> Outgoing</span><span><i className="legend-line dashed" /> Supports</span></div>
      {(model.hiddenConnections > 0 || detail.evidence.length > model.visibleEvidence) && <div className="graph-truncation">Showing a representative neighborhood · {model.hiddenConnections > 0 ? `${model.hiddenConnections} connections hidden` : ""}{model.hiddenConnections > 0 && detail.evidence.length > model.visibleEvidence ? " · " : ""}{detail.evidence.length > model.visibleEvidence ? `${detail.evidence.length - model.visibleEvidence} evidence facts hidden` : ""}</div>}
    </div>
  </div>;
}

function buildGraph(detail: SystemDetail, hiddenKinds: Set<string>, query: string, showEvidence: boolean): GraphModel {
  const normalizedQuery = query.trim().toLowerCase();
  const eligible = detail.connections
    .filter((connection) => !hiddenKinds.has(connection.relationship_kind))
    .filter((connection) => !normalizedQuery || `${connection.entity.name} ${connection.entity.kind} ${connection.relationship_kind} ${connection.label}`.toLowerCase().includes(normalizedQuery))
    .sort((left, right) => confidenceRank[left.confidence] - confidenceRank[right.confidence] || left.entity.name.localeCompare(right.entity.name));
  const visible = balancedConnections(eligible, 50);
  const incoming = mergeConnectedEntities(visible.filter((connection) => connection.direction === "incoming"));
  const outgoing = mergeConnectedEntities(visible.filter((connection) => connection.direction === "outgoing"));

  // Radial hub-and-spoke layout.
  // Spread is capped tightly so entities stay in the clean left/right visual lanes.
  // Straight-line edges are used throughout — a straight spoke literally cannot kink.
  const sideCount = Math.max(incoming.length, outgoing.length, 1);
  const ENTITY_R = Math.min(420, Math.max(360, sideCount * 26 + 240));
  const CX = 460;
  const CY = Math.max(240, (sideCount - 1) * 34 + 190);
  const ROOT_W = 290, ROOT_H = 73;
  const ENTITY_W = 240, ENTITY_H = 61;

  const nodes: LensNode[] = [{
    id: detail.id, type: "lens",
    position: { x: CX - ROOT_W / 2, y: CY - ROOT_H / 2 },
    zIndex: 3,
    data: { role: "root", kind: detail.kind, name: detail.name, detail: `${pretty(detail.system_type)} · ${pretty(detail.state)}`, confidence: detail.confidence, system: detail },
  }];
  const edges: Edge[] = [];

  const placeEntities = (items: Array<{ entity: Connection["entity"]; connections: Connection[] }>, direction: "incoming" | "outgoing") => {
    const total = items.length;
    const baseAngle = direction === "incoming" ? 180 : 0;
    // Max ±28° keeps each spoke visually distinct and edges well-separated.
    const spreadTotal = total <= 1 ? 0 : Math.min(56, total * 9);

    items.forEach((item, index) => {
      const fraction = total <= 1 ? 0.5 : index / (total - 1);
      const angleDeg = baseAngle + spreadTotal * (fraction - 0.5);
      const angleRad = angleDeg * (Math.PI / 180);
      const nx = CX + ENTITY_R * Math.cos(angleRad) - ENTITY_W / 2;
      const ny = CY + ENTITY_R * Math.sin(angleRad) - ENTITY_H / 2;
      const nodeID = `${direction}:${item.entity.id}`;
      const strongest = item.connections.reduce(
        (best, c) => confidenceRank[c.confidence] < confidenceRank[best] ? c.confidence : best,
        item.connections[0].confidence
      );
      nodes.push({
        id: nodeID, type: "lens",
        position: { x: nx, y: ny },
        data: { role: "entity", kind: item.entity.kind, name: item.entity.name, detail: item.connections.map((c) => pretty(c.relationship_kind)).join(" · "), confidence: strongest, connections: item.connections },
      });
      const outgoingEdge = direction === "outgoing";
      const primary = item.connections[0];
      const color = edgeColor(primary.relationship_kind);
      const isConfirmed = strongest === "confirmed";
      // Outgoing: root right-source → entity left-target  (entity is to the right)
      // Incoming: entity right-source → root left-target  (entity is to the left)
      // FlowingEdge uses Euclidean-distance control points for liquid arcs at any angle.
      edges.push({
        id: `bundle:${direction}:${item.entity.id}`,
        source: outgoingEdge ? detail.id : nodeID,
        target: outgoingEdge ? nodeID : detail.id,
        sourceHandle: "right-source",
        targetHandle: "left-target",
        type: "flowing",
        label: relationshipSummary(item.connections),
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color },
        style: {
          stroke: color,
          strokeWidth: isConfirmed ? 2.4 : strongest === "likely" ? 1.6 : 1.2,
          opacity: strongest === "possible" ? 0.5 : 0.92,
          filter: isConfirmed ? `drop-shadow(0 0 5px ${color}90)` : undefined,
        },
        labelStyle: { fill: "rgba(255,255,255,.68)", fontSize: 9, fontWeight: 600 },
        labelBgStyle: { fill: "#111315", fillOpacity: 0.96 },
        labelBgPadding: [6, 3],
        labelBgBorderRadius: 5,
      });
    });
  };

  placeEntities(incoming, "incoming");
  placeEntities(outgoing, "outgoing");

  // Evidence nodes: centred grid below the entity ring.
  // All edges use top-source → bottom-target (consistently vertical) with straight lines.
  const evidence = showEvidence
    ? [...detail.evidence]
        .sort((l, r) => confidenceRank[evidenceConfidence(l)] - confidenceRank[evidenceConfidence(r)] || new Date(r.observed_at).getTime() - new Date(l.observed_at).getTime())
        .slice(0, 8)
    : [];

  const EVIDENCE_W = 245, EVIDENCE_H = 57;
  const evidenceCols = Math.min(4, Math.max(1, evidence.length));
  const evidenceSpacingX = 258;
  const evidenceStartY = CY + ENTITY_R + 90;
  const evidenceStartX = CX - ((evidenceCols - 1) * evidenceSpacingX) / 2 - EVIDENCE_W / 2;

  evidence.forEach((fact, index) => {
    const col = index % evidenceCols;
    const row = Math.floor(index / evidenceCols);
    const nodeID = `evidence:${fact.source_id}:${fact.id}`;
    const confidence = evidenceConfidence(fact);
    const evX = evidenceStartX + col * evidenceSpacingX;
    const evY = evidenceStartY + row * 102;
    nodes.push({
      id: nodeID, type: "lens",
      position: { x: evX, y: evY },
      data: { role: "evidence", kind: "evidence", name: fact.detector_id, detail: `${pretty(fact.family)} · ${pretty(fact.method)}`, confidence, evidence: fact },
    });
    // Flowing arc from evidence top to root bottom.
    edges.push({
      id: `supports:${nodeID}`,
      source: nodeID, target: detail.id,
      sourceHandle: "top-source", targetHandle: "bottom-target",
      type: "flowing",
      animated: true,
      label: "supports",
      style: { stroke: "#5a9ec4", strokeWidth: 1.6, opacity: 0.75 },
      markerEnd: { type: MarkerType.ArrowClosed, width: 12, height: 12, color: "#5a9ec4" },
      labelStyle: { fill: "#7fb8d8", fontSize: 8, fontWeight: 600 },
      labelBgStyle: { fill: "#0e1620", fillOpacity: 0.95 },
      labelBgPadding: [5, 3],
      labelBgBorderRadius: 4,
    });
  });

  return { nodes, edges, hiddenConnections: Math.max(0, eligible.length - visible.length), visibleEvidence: evidence.length };
}

function mergeConnectedEntities(connections: Connection[]) {
  const values = new Map<string, { entity: Connection["entity"]; connections: Connection[] }>();
  connections.forEach((connection) => {
    const current = values.get(connection.entity.id) ?? { entity: connection.entity, connections: [] };
    current.connections.push(connection);
    values.set(connection.entity.id, current);
  });
  return [...values.values()];
}

function balancedConnections(connections: Connection[], limit: number) {
  const groups = new Map<string, Connection[]>();
  connections.forEach((connection) => groups.set(connection.relationship_kind, [...(groups.get(connection.relationship_kind) ?? []), connection]));
  const result: Connection[] = [];
  const queues = [...groups.values()];
  while (result.length < limit && queues.some((queue) => queue.length)) {
    queues.forEach((queue) => {
      const next = queue.shift();
      if (next && result.length < limit) result.push(next);
    });
  }
  return result;
}

function relationshipSummary(connections: Connection[]) {
  const labels = [...new Set(connections.map((connection) => connection.label === "observed_user" ? "Observed user" : pretty(connection.relationship_kind)))];
  return labels.length <= 2 ? labels.join(" · ") : `${labels.slice(0, 2).join(" · ")} +${labels.length - 2}`;
}

function LensNodeCard({ data, selected }: NodeProps<LensNode>) {
  const Icon = kindIcons[data.kind] ?? Network;
  return <article className={`lens-graph-node ${data.role} ${selected ? "selected" : ""}`}>
    <Handle type="target" position={Position.Left} id="left-target" isConnectable={false} />
    <Handle type="source" position={Position.Left} id="left-source" isConnectable={false} />
    <Handle type="source" position={Position.Right} id="right-source" isConnectable={false} />
    <Handle type="target" position={Position.Right} id="right-target" isConnectable={false} />
    <Handle type="source" position={Position.Top} id="top-source" isConnectable={false} />
    <Handle type="target" position={Position.Top} id="top-target" isConnectable={false} />
    <Handle type="target" position={Position.Bottom} id="bottom-target" isConnectable={false} />
    <Handle type="source" position={Position.Bottom} id="bottom-source" isConnectable={false} />
    <span className="graph-node-icon"><Icon size={data.role === "root" ? 19 : 16} /></span>
    <span className="graph-node-copy"><b>{data.name}</b><small>{data.detail}</small></span>
    <i className={`confidence-dot ${data.confidence}`} title={`${pretty(data.confidence)} evidence`} />
  </article>;
}

function GraphInspector({ data }: { data: GraphNodeData }) {
  const facts: Array<[string, string]> = [];
  if (data.role === "root" && data.system) {
    facts.push(["System type", pretty(data.system.system_type)], ["State", pretty(data.system.state)], ["Target", data.system.target_name ?? "Unresolved"], ["Surface", pretty(data.system.surface)], ["Network", pretty(data.system.network_scope)], ["Attribution", data.system.attributed ? "Established" : "Not established"]);
  } else if (data.role === "evidence" && data.evidence) {
    facts.push(["Detector", data.evidence.detector_id], ["Evidence family", pretty(data.evidence.family)], ["Method", pretty(data.evidence.method)], ["Specificity", pretty(data.evidence.specificity)], ["Observed", relative(data.evidence.observed_at)], ["Observations", String(data.evidence.observations)]);
  } else {
    facts.push(["Entity type", pretty(data.kind)], ["Evidence", pretty(data.confidence)]);
    data.connections?.forEach((connection) => facts.push([connection.direction === "incoming" ? "Incoming" : "Outgoing", connection.label === "observed_user" ? "Observed user" : pretty(connection.relationship_kind)]));
  }
  const locator = data.evidence?.locator;
  return <aside className="graph-inspector">
    <div className="graph-inspector-title"><KindIcon kind={data.kind} /><span><small>{data.role === "root" ? "ROOT SYSTEM" : data.role === "evidence" ? "EVIDENCE FACT" : "CONNECTED ENTITY"}</small><b>{data.name}</b></span></div>
    <div className="graph-inspector-facts">{facts.slice(0, 8).map(([label, value], index) => <div key={`${label}:${index}`}><span>{label}</span><b>{value}</b></div>)}</div>
    {locator && <div className="graph-locator"><span>SANITIZED LOCATOR</span><code title={locator}>{locator}</code></div>}
    <p>{data.role === "evidence" ? "This observation supports the selected system. It does not imply approval or safety." : data.role === "entity" ? "Arrows show the canonical relationship direction recorded by Lens." : "Select any connected node or evidence fact to inspect why it appears in this neighborhood."}</p>
  </aside>;
}

function GraphFact({ label, value }: { label: string; value: string }) {
  return <span><small>{label}</small><b>{value}</b></span>;
}

function KindIcon({ kind }: { kind: string }) {
  const Icon = kindIcons[kind] ?? Network;
  return <span className={`graph-kind-icon kind-${safeClass(kind)}`}><Icon size={15} /></span>;
}

function GraphState({ icon: Icon, title, detail, spinning = false }: { icon: LucideIcon; title: string; detail: string; spinning?: boolean }) {
  return <div className="graph-state"><Icon className={spinning ? "spinning" : ""} size={24} /><b>{title}</b><p>{detail}</p></div>;
}

function countRelations(connections: Connection[]) {
  return connections.reduce<Record<string, number>>((counts, connection) => {
    counts[connection.relationship_kind] = (counts[connection.relationship_kind] ?? 0) + 1;
    return counts;
  }, {});
}

function evidenceConfidence(evidence: Evidence): Confidence {
  return evidence.specificity === "high" ? "confirmed" : evidence.specificity === "medium" ? "likely" : "possible";
}

function edgeColor(kind: string) {
  const colors: Record<string, string> = { runs_on: "#eb8f52", defined_in: "#6e9fca", deployed_as: "#b184d4", uses: "#d7a950", exposes: "#df6f6f", connects_to: "#77a9bd", provides: "#64aa88", invokes: "#d08aaf", configured_by: "#8c9bab", owned_by: "#9a91d1", publishes: "#70a6a0", references: "#7693bd", describes: "#c08c61" };
  return colors[kind] ?? "#8d99a4";
}

function pretty(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function safeClass(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9_-]/g, "-");
}

function relative(value: string) {
  const delta = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(delta)) return "Unknown";
  const minutes = Math.max(0, Math.round(delta / 60000));
  if (minutes < 1) return "Just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}
