import { MarkerType, type Edge, type Node } from "@xyflow/react";
import type { Confidence, Connection, Evidence, SystemDetail } from "../../api";

export type GraphNodeData = {
  role: "root" | "entity" | "evidence" | "cluster";
  kind: string;
  entityID?: string;
  name: string;
  detail: string;
  confidence: Confidence;
  connections?: Connection[];
  evidence?: Evidence;
  supportingEvidence?: Evidence[];
  contextName?: string;
  system?: SystemDetail;
  count?: number;
};

export type LensNode = Node<GraphNodeData, "lens" | "cluster">;
export type GraphModel = { nodes: LensNode[]; edges: Edge[]; hiddenConnections: number; visibleEvidence: number; layout: "clustered" };
type ConnectedGraphEntity = { entity: Connection["entity"]; connections: Connection[]; direction: "incoming" | "outgoing" };
type GraphClusterKey = "environment" | "people" | "models" | "resources" | "skills" | "definitions" | "other";
type GraphCluster = { key: GraphClusterKey; name: string; detail: string; items: ConnectedGraphEntity[] };

const confidenceRank: Record<Confidence, number> = { confirmed: 0, likely: 1, possible: 2 };
export function buildGraph(detail: SystemDetail, hiddenKinds: Set<string>, query: string, showEvidence: boolean): GraphModel {
  const normalizedQuery = query.trim().toLowerCase();
  const eligible = detail.connections
    .filter((connection) => !hiddenKinds.has(connection.relationship_kind))
    .filter((connection) => !normalizedQuery || `${connection.entity.name} ${connection.entity.kind} ${connection.relationship_kind} ${connection.label}`.toLowerCase().includes(normalizedQuery))
    .sort((left, right) => confidenceRank[left.confidence] - confidenceRank[right.confidence] || left.entity.name.localeCompare(right.entity.name));
  const visible = balancedConnections(eligible, 16);
  const incoming = mergeConnectedEntities(visible.filter((connection) => connection.direction === "incoming"));
  const outgoing = mergeConnectedEntities(visible.filter((connection) => connection.direction === "outgoing"));
  const connected: ConnectedGraphEntity[] = [
    ...incoming.map((item) => ({ ...item, direction: "incoming" as const })),
    ...outgoing.map((item) => ({ ...item, direction: "outgoing" as const })),
  ].sort(graphEntityOrder);
  const hiddenConnections = Math.max(0, eligible.length - visible.length);
  return buildClusteredGraph(detail, connected, hiddenConnections, showEvidence);
}

function buildClusteredGraph(detail: SystemDetail, connected: ConnectedGraphEntity[], hiddenConnections: number, showEvidence: boolean): GraphModel {
  const clusters = clusterConnectedEntities(connected);
  const ROOT_X = 390;
  const ROOT_Y = 310;
  const nodes: LensNode[] = [{
    id: detail.id, type: "lens", position: { x: ROOT_X, y: ROOT_Y }, zIndex: 8,
    data: { role: "root", entityID: detail.id, kind: detail.kind, name: detail.name, detail: `${pretty(detail.system_type)} · ${pretty(detail.state)}`, confidence: detail.confidence, supportingEvidence: evidenceForSubject(detail.evidence, detail.id), system: detail },
  }];
  const edges: Edge[] = [];
  const entityNodeByID = new Map<string, string>();
  const entityIndexByID = new Map<string, number>();

  clusters.forEach((cluster) => {
    const placement = graphClusterPlacement(cluster.key, cluster.items.length);
    const columns = cluster.items.length >= 3 ? 2 : 1;
    const rows = Math.ceil(cluster.items.length / columns);
    const width = columns === 2 ? 408 : 210;
    const height = 39 + rows * 62 + 8;
    const clusterID = `cluster:${cluster.key}`;
    nodes.push({
      id: clusterID, type: "cluster", position: { x: placement.x, y: placement.y }, zIndex: 0,
      selectable: false, focusable: false,
      style: { width, height },
      data: { role: "cluster", kind: cluster.key, name: cluster.name, detail: cluster.detail, confidence: "possible", count: cluster.items.length },
    });

    cluster.items.forEach((item, index) => {
      const nodeID = `${item.direction}:${item.entity.id}`;
      const column = index % columns;
      const row = Math.floor(index / columns);
      const strongest = strongestConnectionConfidence(item.connections);
      const entityPosition = { x: 10 + column * 198, y: 38 + row * 62 };
      nodes.push({
        id: nodeID, type: "lens", parentId: clusterID, extent: "parent", position: entityPosition, zIndex: 3,
        data: { role: "entity", entityID: item.entity.id, kind: item.entity.kind, name: item.entity.name, detail: relationshipCardLabel(item.connections, item.entity.kind), confidence: strongest, connections: item.connections, supportingEvidence: evidenceForSubject(detail.evidence, item.entity.id), contextName: detail.name },
      });
      if (!entityNodeByID.has(item.entity.id)) {
        entityNodeByID.set(item.entity.id, nodeID);
        entityIndexByID.set(item.entity.id, entityIndexByID.size);
      }

      const absoluteCenter = {
        x: placement.x + entityPosition.x + 94,
        y: placement.y + entityPosition.y + 27,
      };
      const handles = constellationHandles(ROOT_X + 135, ROOT_Y + 34, absoluteCenter.x, absoluteCenter.y);
      const outgoingEdge = item.direction === "outgoing";
      const primary = item.connections[0];
      const color = edgeColor(primary.relationship_kind);
      edges.push({
        id: `bundle:${item.direction}:${item.entity.id}`,
        source: outgoingEdge ? detail.id : nodeID,
        target: outgoingEdge ? nodeID : detail.id,
        sourceHandle: outgoingEdge ? handles.rootSource : handles.entitySource,
        targetHandle: outgoingEdge ? handles.entityTarget : handles.rootTarget,
        type: "flowing",
        markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13, color },
        style: relationshipEdgeStyle(strongest, color),
      });
    });
  });

  const evidence = showEvidence
    ? [...detail.evidence]
        .filter((fact) => !fact.subject || fact.subject.entity_id === detail.id || entityNodeByID.has(fact.subject.entity_id))
        .sort((left, right) => evidenceOrder(left, right, detail.id, entityIndexByID))
        .slice(0, 8)
    : [];
  evidence.forEach((fact, index) => {
    const column = index % 4;
    const row = Math.floor(index / 4);
    const nodeID = `evidence:${fact.source_id}:${fact.id}`;
    const targetNode = fact.subject?.entity_id === detail.id ? detail.id : entityNodeByID.get(fact.subject?.entity_id ?? "") ?? detail.id;
    nodes.push({
      id: nodeID, type: "lens", position: { x: 90 + column * 208, y: 760 + row * 66 }, zIndex: 2,
      data: { role: "evidence", kind: "evidence", name: evidenceNodeName(fact), detail: evidenceNodeDetail(fact), confidence: evidenceConfidence(fact), evidence: fact, contextName: detail.name },
    });
    edges.push({
      id: `supports:${nodeID}`, source: nodeID, target: targetNode,
      sourceHandle: "top-source", targetHandle: "bottom-target", type: "flowing",
      style: { stroke: "#5a9ec4", strokeWidth: 1.35, opacity: 0.64, strokeDasharray: "7 5" },
      markerEnd: { type: MarkerType.ArrowClosed, width: 12, height: 12, color: "#5a9ec4" },
    });
  });
  return { nodes, edges, hiddenConnections, visibleEvidence: evidence.length, layout: "clustered" };
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

function clusterConnectedEntities(connected: ConnectedGraphEntity[]): GraphCluster[] {
  const definitions: Record<GraphClusterKey, Omit<GraphCluster, "items">> = {
    environment: { key: "environment", name: "Execution environment", detail: "Where this system runs" },
    people: { key: "people", name: "Observed people", detail: "Users and attributed owners" },
    models: { key: "models", name: "Models", detail: "Models and model servers" },
    resources: { key: "resources", name: "Connected resources", detail: "Tools, MCP servers, and APIs" },
    skills: { key: "skills", name: "Skills", detail: "Discovered reusable capabilities" },
    definitions: { key: "definitions", name: "Code and definitions", detail: "Repositories, frameworks, and workflows" },
    other: { key: "other", name: "Other relationships", detail: "Additional connected inventory" },
  };
  const groups = new Map<GraphClusterKey, ConnectedGraphEntity[]>();
  connected.forEach((item) => {
    const key = graphClusterKey(item);
    groups.set(key, [...(groups.get(key) ?? []), item]);
  });
  const order: GraphClusterKey[] = ["environment", "models", "resources", "skills", "definitions", "people", "other"];
  return order
    .filter((key) => groups.has(key))
    .map((key) => ({ ...definitions[key], items: groups.get(key)!.sort(graphEntityOrder) }));
}

function graphClusterKey(item: ConnectedGraphEntity): GraphClusterKey {
  const kind = item.entity.kind;
  const relationships = new Set(item.connections.map((connection) => connection.relationship_kind));
  if (kind === "skill") return "skills";
  if (kind === "user" || relationships.has("owned_by")) return "people";
  if (kind === "model" || kind === "model_server") return "models";
  if (["endpoint", "cluster", "workload"].includes(kind) || relationships.has("runs_on") || relationships.has("deployed_as")) return "environment";
  if (["repository", "framework", "workflow"].includes(kind) || relationships.has("defined_in")) return "definitions";
  if (["mcp_server", "tool", "api_service", "api_operation"].includes(kind) || relationships.has("connects_to") || relationships.has("invokes") || relationships.has("exposes")) return "resources";
  return "other";
}

function graphClusterPlacement(key: GraphClusterKey, count: number) {
  const twoColumns = count >= 3;
  const placements: Record<GraphClusterKey, { x: number; y: number }> = {
    environment: { x: 38, y: 82 },
    models: { x: 344, y: 34 },
    resources: { x: twoColumns ? 660 : 756, y: 72 },
    skills: { x: twoColumns ? 640 : 738, y: 458 },
    definitions: { x: twoColumns ? 320 : 416, y: 570 },
    people: { x: 36, y: 490 },
    other: { x: 52, y: 282 },
  };
  return placements[key];
}

function constellationHandles(rootX: number, rootY: number, entityX: number, entityY: number) {
  const dx = entityX - rootX;
  const dy = entityY - rootY;
  if (Math.abs(dx) >= Math.abs(dy)) {
    const side = dx >= 0 ? "right" : "left";
    const inverse = dx >= 0 ? "left" : "right";
    return {
      rootSource: `${side}-source`, rootTarget: `${side}-target`,
      entitySource: `${inverse}-source`, entityTarget: `${inverse}-target`,
    };
  }
  const side = dy >= 0 ? "bottom" : "top";
  const inverse = dy >= 0 ? "top" : "bottom";
  return {
    rootSource: `${side}-source`, rootTarget: `${side}-target`,
    entitySource: `${inverse}-source`, entityTarget: `${inverse}-target`,
  };
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

export function relationshipCardLabel(connections: Connection[], entityKind: string) {
  if (connections.some((connection) => connection.label === "observed_user")) return "Observed user · not ownership";
  const kinds = new Set(connections.map((connection) => connection.relationship_kind));
  if (kinds.has("runs_on") && entityKind === "endpoint") return "Runs on this endpoint";
  if (kinds.has("provides") && entityKind === "skill") return "Available skill";
  if (kinds.has("provides")) return "Provided capability";
  if (kinds.has("uses")) return "Used by this system";
  if (kinds.has("connects_to")) return "Connected resource";
  if (kinds.has("owned_by")) return "Authoritative owner";
  return relationshipSummary(connections);
}

function evidenceForSubject(evidence: Evidence[], entityID: string) {
  return evidence.filter((finding) => finding.subject?.entity_id === entityID);
}

function strongestConnectionConfidence(connections: Connection[]) {
  return connections.reduce(
    (best, connection) => confidenceRank[connection.confidence] < confidenceRank[best] ? connection.confidence : best,
    connections[0].confidence,
  );
}

function relationshipEdgeStyle(confidence: Confidence, color: string) {
  const confirmed = confidence === "confirmed";
  return {
    stroke: color,
    strokeWidth: confirmed ? 2.1 : confidence === "likely" ? 1.5 : 1.1,
    opacity: confidence === "possible" ? 0.46 : 0.82,
    filter: confirmed ? `drop-shadow(0 0 4px ${color}70)` : undefined,
  };
}

function graphEntityOrder(
  left: { entity: Connection["entity"]; connections: Connection[]; direction: "incoming" | "outgoing" },
  right: { entity: Connection["entity"]; connections: Connection[]; direction: "incoming" | "outgoing" },
) {
  const rank: Record<string, number> = {
    endpoint: 0, user: 1, repository: 2, cluster: 2, workload: 3,
    model_server: 4, model: 5, framework: 6, mcp_server: 7, tool: 8,
    api_service: 9, workflow: 10, skill: 11,
  };
  return (rank[left.entity.kind] ?? 20) - (rank[right.entity.kind] ?? 20)
    || left.direction.localeCompare(right.direction)
    || left.entity.name.localeCompare(right.entity.name);
}

function evidenceOrder(left: Evidence, right: Evidence, rootID: string, entityIndexes: Map<string, number>) {
  const subjectRank = (evidence: Evidence) => {
    const subjectID = evidence.subject?.entity_id;
    if (subjectID && entityIndexes.has(subjectID)) return entityIndexes.get(subjectID)!;
    if (subjectID === rootID) return 10_000;
    return 20_000;
  };
  return subjectRank(left) - subjectRank(right)
    || confidenceRank[evidenceConfidence(left)] - confidenceRank[evidenceConfidence(right)]
    || new Date(right.observed_at).getTime() - new Date(left.observed_at).getTime();
}

export function relationshipExplanation(data: GraphNodeData) {
  const connections = data.connections ?? [];
  const context = data.contextName ?? "the selected system";
  if (connections.some((connection) => connection.label === "observed_user")) {
    return `${data.name} is the OS user under which ${context} was observed. This is not authoritative ownership.`;
  }
  const kinds = new Set(connections.map((connection) => connection.relationship_kind));
  if (kinds.has("runs_on") && data.kind === "endpoint") return `${context} was observed on the ${data.name} endpoint.`;
  if (kinds.has("provides") && data.kind === "skill") return `${data.name} is a discovered skill made available by ${context}.`;
  if (kinds.has("provides")) return `${data.name} is a capability provided by ${context}.`;
  if (kinds.has("uses")) return `${context} is configured to use ${data.name}.`;
  if (kinds.has("connects_to")) return `${context} has a discovered connection to ${data.name}.`;
  if (kinds.has("owned_by")) return `${data.name} is linked by authoritative ownership evidence.`;
  const direction = connections[0]?.direction === "incoming" ? "points into" : "is referenced by";
  return `${data.name} ${direction} ${context} through ${relationshipSummary(connections).toLowerCase()}.`;
}

export function prioritizedEvidenceFacts(facts: Array<{ label: string; value: string }>) {
  const priority = ["Declared Purpose", "Allowed Tools", "Compatibility", "License", "Descriptor Relative", "Skill Scope", "Provider Product Id", "Descriptor Valid"];
  return [...facts].sort((left, right) => {
    const leftRank = priority.indexOf(left.label);
    const rightRank = priority.indexOf(right.label);
    const normalizedLeft = leftRank < 0 ? priority.length : leftRank;
    const normalizedRight = rightRank < 0 ? priority.length : rightRank;
    return normalizedLeft - normalizedRight;
  });
}

export function evidenceNodeName(evidence: Evidence) {
  if (evidence.subject?.name) return evidence.subject.name;
  const fallback: Record<string, string> = {
    process: "Running process", config_shape: "Configuration", config_file: "Configuration file",
    application: "Application", package: "Installed package", extension_manifest: "IDE extension",
    executable: "Executable", listener: "Network listener", descriptor: "Descriptor",
    skill_descriptor: "Skill", agent_descriptor: "Agent definition",
  };
  return fallback[evidence.method] ?? pretty(evidence.family);
}

export function evidenceNodeDetail(evidence: Evidence) {
  const detail: Record<string, string> = {
    skill_descriptor: "Valid SKILL.md", agent_descriptor: "Valid agent definition",
    process: "Running at scan time", config_shape: "Configuration matched",
    config_file: "Known file present", application: "Application installed",
    package: "Package installed", extension_manifest: "Extension matched",
    executable: "Executable found", listener: "Listener observed", descriptor: "Descriptor validated",
  };
  return detail[evidence.method] ?? `${pretty(evidence.family)} · ${pretty(evidence.method)}`;
}

export function countRelations(connections: Connection[]) {
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

export function pretty(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function safeClass(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9_-]/g, "-");
}

export function relative(value: string) {
  const delta = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(delta)) return "Unknown";
  const minutes = Math.max(0, Math.round(delta / 60000));
  if (minutes < 1) return "Just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}
