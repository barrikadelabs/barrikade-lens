import type { SystemDetail } from "../../../api";

export function denseGraph(id = "dense-tool", target = "Engineering laptop"): SystemDetail {
  const kinds = ["endpoint", "endpoint", "endpoint", "model", "model", "model", "model", "mcp_server", "mcp_server", "mcp_server", "skill", "skill", "repository", "user", "workflow", "runtime", "mcp_server", "skill"];
  return {
    id, kind: "runtime", name: "AI tool", attributes: {}, target_id: target, target_name: target,
    target_freshness: "stale", surface: "endpoint", system_type: "agent_tool", state: "running",
    network_scope: "none", attributed: false, confidence: "confirmed", first_seen_at: "2026-09-01T12:00:00Z", last_seen_at: "2026-09-19T12:00:00Z",
    connections: kinds.map((kind, index) => ({
      relationship_id: `relation-${index}`, relationship_kind: kind === "endpoint" ? "runs_on" : kind === "model" ? "uses" : "connects_to",
      label: kind, direction: "outgoing", confidence: "confirmed", surfaces: ["endpoint"], observation_states: ["observed"], observed_at: "2026-09-19T12:00:00Z",
      attributes: {}, entity: { id: `item-${index}`, kind, name: `${kind} ${index}`, attributes: {} },
    })),
    evidence: Array.from({ length: 12 }, (_, index) => ({
      id: `fact-${index}`, source_id: "scanner", target_id: target, detector_id: "tool", detector_version: "1", method: "config_file", family: "config", specificity: "high",
      location: `config-${index % 8}`, subject: { entity_id: id, entity_kind: "runtime", name: "AI tool", confidence: "confirmed" },
      observed_at: `2026-09-${index < 8 ? "19" : "18"}T12:00:00Z`, observations: 1,
    })),
  };
}
