import { useEffect, useMemo, useRef, useState } from "react";
import {
  Background, BackgroundVariant, BaseEdge, Controls, Handle, Position, ReactFlow,
  type EdgeProps, type NodeProps,
} from "@xyflow/react";
import {
  AlertCircle, ArrowDownLeft, ArrowUpRight, Bot, BrainCircuit, CheckCircle2,
  Container, Database, FileSearch, GitBranch, Link2, LoaderCircle, Monitor,
  Network, PlugZap, Search, Server, TerminalSquare, UserRound, Workflow,
  type LucideIcon,
} from "lucide-react";
import { API, type SystemDetail, type SystemItem } from "./api";
import { captureAnalytics } from "./analytics";
import { stateLabel, systemTypeLabel } from "./copy";
import { buildGraph, countRelations, evidenceNodeDetail, evidenceNodeName, pretty, prioritizedEvidenceFacts, relative, relationshipCardLabel, relationshipExplanation, safeClass, type GraphNodeData, type LensNode } from "./features/evidence/graph-model";

const nodeTypes = { lens: LensNodeCard, cluster: GraphClusterCard };

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
const kindIcons: Record<string, LucideIcon> = {
  endpoint: Monitor, repository: GitBranch, cluster: Container, workload: Container,
  agent: Bot, runtime: TerminalSquare, framework: Network, mcp_server: PlugZap,
  skill: CheckCircle2, model: BrainCircuit, model_server: Server, api_service: Database,
  api_operation: Link2, workflow: Workflow, user: UserRound, evidence: FileSearch,
};

export function EvidenceGraphPage({ api, revision, initialSystemId = "" }: { api: API; revision: number; initialSystemId?: string }) {
  const [systems, setSystems] = useState<SystemItem[]>([]);
  const [selectedSystem, setSelectedSystem] = useState(initialSystemId);
  const [detail, setDetail] = useState<SystemDetail>();
  const [systemSearch, setSystemSearch] = useState("");
  const [loadingSystems, setLoadingSystems] = useState(true);
  const [loadingGraph, setLoadingGraph] = useState(false);
  const [moreSystems, setMoreSystems] = useState(false);
  const [systemError, setSystemError] = useState("");
  const [graphError, setGraphError] = useState("");
	const viewedSystem = useRef("");

  useEffect(() => {
    let active = true;
    setLoadingSystems(true);
    setSystemError("");
    const timer = window.setTimeout(() => {
      api.systems({ limit: 100, sort: "name", freshness: initialSystemId ? "all" : "fresh", search: systemSearch.trim() }).then((result) => {
        if (!active) return;
        setSystems(result.items);
        setMoreSystems(Boolean(result.next_cursor));
        setSelectedSystem((current) => current || result.items.find((item) => item.id === initialSystemId)?.id || result.items[0]?.id || "");
      }).catch((reason) => active && setSystemError(String(reason))).finally(() => active && setLoadingSystems(false));
    }, systemSearch ? 220 : 0);
    return () => { active = false; window.clearTimeout(timer); };
  }, [api, revision, systemSearch, initialSystemId]);

  useEffect(() => {
    if (initialSystemId) setSelectedSystem(initialSystemId);
  }, [initialSystemId]);

  useEffect(() => {
    if (!selectedSystem) { setDetail(undefined); return; }
    let active = true;
    setLoadingGraph(true);
    setGraphError("");
    api.system(selectedSystem).then((result) => active && setDetail(result)).catch((reason) => active && setGraphError(String(reason))).finally(() => active && setLoadingGraph(false));
    return () => { active = false; };
  }, [api, selectedSystem, revision]);

	useEffect(() => {
		if (!detail || viewedSystem.current === detail.id) return;
		viewedSystem.current = detail.id;
		captureAnalytics({ name: "evidence_graph_viewed", properties: { system_kind: detail.system_type, confidence: detail.confidence } });
	}, [detail]);

  const availableSystems = detail && !systems.some((system) => system.id === detail.id) ? [detail, ...systems] : systems;
  const visibleSystems = availableSystems.filter((system) => {
    const query = systemSearch.trim().toLowerCase();
    return !query || `${system.name} ${system.product_id ?? ""} ${system.system_type}`.toLowerCase().includes(query);
  });

  if (loadingSystems && !systems.length && !systemSearch && !initialSystemId) return <GraphState icon={LoaderCircle} title="Loading AI tools and agents" detail="Lens is preparing the connection map." spinning />;
  if (systemError && !systems.length && !initialSystemId) return <GraphState icon={AlertCircle} title="Lens could not load this map" detail={systemError} />;
  if (!systems.length && !systemSearch && !loadingSystems && !initialSystemId) return <GraphState icon={Network} title="No AI tools or agents found" detail="This map becomes available after Lens finds an autonomous agent, AI agent tool, or AI model runtime." />;

  return <div className="evidence-map-layout">
    <aside className="panel graph-system-panel">
      <div className="graph-panel-heading"><div><span>AI INVENTORY</span><h2>Choose a tool or agent</h2><p>Search your organization’s AI inventory.</p></div><b>{loadingSystems ? "…" : `${availableSystems.length}${moreSystems ? "+" : ""}`}</b></div>
      <label className="graph-system-search"><Search size={14} /><input value={systemSearch} onChange={(event) => setSystemSearch(event.target.value)} placeholder="Find an AI tool or agent" aria-label="Find an AI tool or agent" /></label>
      <div className="graph-system-list">
        {visibleSystems.map((system) => <button className={selectedSystem === system.id ? "active" : ""} key={system.id} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "evidence", interaction: "open", control: "system" } }); setSelectedSystem(system.id); }} aria-pressed={selectedSystem === system.id}>
          <KindIcon kind={system.kind} /><span><b>{system.name}</b><small>{systemTypeLabel(system.system_type)} · {stateLabel(system.state)}</small></span><i className={`confidence-dot ${system.confidence}`} title={`${pretty(system.confidence)} confidence`} />
        </button>)}
        {!visibleSystems.length && !loadingSystems && <p className="graph-list-empty">No systems match “{systemSearch}”.</p>}
        {systemError && <p className="graph-list-empty">{systemError}</p>}
      </div>
    </aside>
    <section className="panel graph-workspace">
      {loadingGraph ? <GraphState icon={LoaderCircle} title="Building the connection map" detail="Lens is loading connected items and supporting details." spinning /> : graphError ? <GraphState icon={AlertCircle} title="Lens could not map this item" detail={graphError} /> : detail ? <SystemEvidenceMap detail={detail} /> : null}
    </section>
  </div>;
}

function SystemEvidenceMap({ detail }: { detail: SystemDetail }) {
  const relationCounts = useMemo(() => countRelations(detail.connections), [detail.connections]);
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [showEvidence, setShowEvidence] = useState(false);
  const [selectedNode, setSelectedNode] = useState(detail.id);

  useEffect(() => {
    setHiddenKinds(new Set());
    setQuery("");
    setShowEvidence(false);
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
      <div><span>SELECTED TOOL OR AGENT</span><h2>{detail.name}</h2><p>{systemTypeLabel(detail.system_type)} · {stateLabel(detail.state)} · {detail.target_name ?? "Location unresolved"}</p></div>
      <div className="graph-title-facts"><GraphFact label="Connected items" value={String(detail.connections.length)} /><GraphFact label="Supporting details" value={String(detail.evidence.length)} /><GraphFact label="Network" value={pretty(detail.network_scope)} /></div>
    </header>
    <div className="graph-toolbar">
      <label><Search size={14} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter connected items" aria-label="Filter connected items" /></label>
      <div className="relation-filters" aria-label="Relationship filters">
        {Object.entries(relationCounts).map(([kind, count]) => <button className={hiddenKinds.has(kind) ? "muted" : "active"} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "evidence", interaction: "filter_changed" } }); toggleKind(kind); }} key={kind} aria-pressed={!hiddenKinds.has(kind)}><i className={`edge-swatch relation-${safeClass(kind)}`} />{pretty(kind)} <b>{count}</b></button>)}
        <button className={showEvidence ? "active evidence-toggle" : "muted evidence-toggle"} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "evidence", interaction: "filter_changed", control: "evidence" } }); setShowEvidence((value) => !value); }} aria-pressed={showEvidence}><i className="edge-swatch evidence" />{showEvidence ? "Hide supporting details" : "Show supporting details"} <b>{detail.evidence.length}</b></button>
      </div>
    </div>
    <div className="graph-stage">
      <div className={`graph-canvas ${model.layout}`}>
        {model.layout === "clustered" && <div className="graph-layout-note"><Network size={11} /> Grouped by resource type</div>}
        <ReactFlow
          key={graphKey}
          nodes={model.nodes}
          edges={model.edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          fitView
          fitViewOptions={{ padding: 0.16, maxZoom: 1.05 }}
          minZoom={0.18}
          maxZoom={1.8}
          nodesDraggable={false}
          nodesConnectable={false}
          zoomOnDoubleClick={false}
          onNodeClick={(_, node) => { if (node.data.role !== "cluster") { captureAnalytics({ name: "lens_interaction", properties: { surface: "evidence", interaction: "open", control: "evidence" } }); setSelectedNode(node.id); } }}
          onPaneClick={() => setSelectedNode(detail.id)}
          proOptions={{ hideAttribution: true }}
          aria-label={`Evidence graph for ${detail.name}`}
        >
          <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="rgba(255,255,255,.11)" />
          <Controls showInteractive={false} position="bottom-left" />
        </ReactFlow>
        <div className="graph-legend"><span><ArrowDownLeft size={12} /> Connects in</span><span><ArrowUpRight size={12} /> Connects out</span><span><i className="legend-line dashed" /> Supporting detail → item</span></div>
        {(model.hiddenConnections > 0 || showEvidence && detail.evidence.length > model.visibleEvidence) && <div className="graph-truncation">Showing the closest connections · {model.hiddenConnections > 0 ? `${model.hiddenConnections} connections hidden` : ""}{model.hiddenConnections > 0 && showEvidence && detail.evidence.length > model.visibleEvidence ? " · " : ""}{showEvidence && detail.evidence.length > model.visibleEvidence ? `${detail.evidence.length - model.visibleEvidence} supporting details hidden` : ""}</div>}
      </div>
      <GraphInspector data={selection} />
    </div>
  </div>;
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
    <i className={`confidence-dot ${data.confidence}`} title={`${pretty(data.confidence)} confidence`} />
  </article>;
}

function GraphClusterCard({ data }: NodeProps<LensNode>) {
  return <section className={`graph-cluster cluster-${safeClass(data.kind)}`}>
    <span><b>{data.name}</b><small>{data.detail}</small></span>
    <i>{data.count}</i>
  </section>;
}

function GraphInspector({ data }: { data: GraphNodeData }) {
  const facts: Array<[string, string]> = [];
  if (data.role === "root" && data.system) {
    facts.push(["System type", pretty(data.system.system_type)], ["State", pretty(data.system.state)], ["Target", data.system.target_name ?? "Unresolved"], ["Surface", pretty(data.system.surface)], ["Network", pretty(data.system.network_scope)], ["Attribution", data.system.attributed ? "Established" : "Not established"]);
  } else if (data.role === "evidence" && data.evidence) {
    facts.push(
      ["Exact resource", data.evidence.subject?.name ?? "Not resolved"],
      ["Resource type", pretty(data.evidence.subject?.entity_kind ?? data.evidence.family)],
      ["Found on", data.evidence.target_name ?? "Reporting target"],
      ["Reporting", pretty(data.evidence.target_freshness ?? "unknown")],
      ["Method", pretty(data.evidence.method)],
      ["Specificity", pretty(data.evidence.specificity)],
      ["Observed", relative(data.evidence.observed_at)],
      ["Observations", String(data.evidence.observations)],
    );
  } else {
    const connections = data.connections ?? [];
		facts.push(
			["Entity type", pretty(data.kind)],
			["Relationship", relationshipCardLabel(connections, data.kind)],
			["Direction", connections[0]?.direction === "incoming" ? "Into system" : "Out from system"],
			["Observed as", [...new Set(connections.flatMap((connection) => connection.observation_states ?? []))].map(pretty).join(", ") || "Unknown"],
			["Surfaces", [...new Set(connections.flatMap((connection) => connection.surfaces ?? []))].map(pretty).join(", ") || "Unknown"],
			["Last observed", connections[0]?.observed_at ? relative(connections[0].observed_at) : "Unknown"],
			["Evidence", pretty(data.confidence)],
    );
  }
  const evidence = data.evidence;
  const location = evidence?.location;
  const matchedFacts = prioritizedEvidenceFacts(evidence?.matched_facts ?? []);
  const visibleMatchedFacts = matchedFacts.slice(0, 8);
  const relationshipContext = data.role === "entity" ? relationshipExplanation(data) : "";
  const supportingEvidence = data.supportingEvidence ?? [];
  return <aside className="graph-inspector">
    <div className="graph-inspector-title"><KindIcon kind={data.kind} /><span><small>{data.role === "root" ? "AI TOOL OR AGENT" : data.role === "evidence" ? "SUPPORTING DETAIL" : "CONNECTED ITEM"}</small><b>{data.name}</b></span></div>
    {relationshipContext && <div className="graph-relationship-summary"><span>WHY THIS IS LINKED</span><p>{relationshipContext}</p></div>}
    <div className="graph-inspector-facts">{facts.slice(0, 8).map(([label, value], index) => <div key={`${label}:${index}`}><span>{label}</span><b>{value}</b></div>)}</div>
    {supportingEvidence.length ? <div className="graph-supporting-evidence"><span>SUPPORTING DETAILS</span>{supportingEvidence.slice(0, 4).map((finding) => <div key={`${finding.source_id}:${finding.id}`}><b>{evidenceNodeName(finding)}</b><small>{evidenceNodeDetail(finding)}</small></div>)}{supportingEvidence.length > 4 && <small>+{supportingEvidence.length - 4} more details</small>}</div> : null}
    {evidence?.summary && <p className="graph-evidence-summary">{evidence.summary}</p>}
    {location && <div className="graph-locator"><span>WHERE LENS FOUND IT</span><code title={location}>{location}</code></div>}
    {visibleMatchedFacts.length ? <div className="graph-inspector-matched"><span>DISCOVERED DETAILS</span><div>{visibleMatchedFacts.map((fact) => <b key={fact.label}>{fact.label}: {fact.value}</b>)}</div>{matchedFacts.length > visibleMatchedFacts.length && <small>+{matchedFacts.length - visibleMatchedFacts.length} more in system details</small>}</div> : null}
    {evidence?.why_it_matched && <div className="graph-evidence-explanation"><span>WHY THIS IS LINKED</span><p>{evidence.why_it_matched}</p></div>}
    {evidence?.investigation_hint && <div className="graph-evidence-explanation action"><span>WHAT TO CHECK NEXT</span><p>{evidence.investigation_hint}</p></div>}
    <p className="graph-inspector-note">{data.role === "evidence" ? "This observation points to the exact item it supports. Technical integrity hashes remain available below." : data.role === "entity" ? "Arrows show the technical relationship direction recorded by Lens." : "Select a connected item or supporting detail to see why it appears here."}</p>
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
