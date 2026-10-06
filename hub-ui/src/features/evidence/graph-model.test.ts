import { describe, expect, it } from "vitest";
import { buildGraph, type LensNode } from "./graph-model";
import { denseGraph } from "./__fixtures__/dense-graph";

function rectangle(node: LensNode, nodes: LensNode[]) {
  const parent = nodes.find((item) => item.id === node.parentId);
  return { x: node.position.x + (parent?.position.x ?? 0), y: node.position.y + (parent?.position.y ?? 0), width: Number(node.style?.width), height: Number(node.style?.height) };
}

describe("evidence graph layout", () => {
  it.each([0, 3, 8, 18])("keeps cards and group boundaries apart with %i connections", (count) => {
    const detail = denseGraph();
    detail.connections = detail.connections.slice(0, count);
    const model = buildGraph(detail, new Set(), "", true);
    for (const [index, node] of model.nodes.entries()) {
      const a = rectangle(node, model.nodes);
      for (const other of model.nodes.slice(index + 1)) {
        if (node.id === other.parentId || other.id === node.parentId) continue;
        const b = rectangle(other, model.nodes);
        expect(a.x + a.width <= b.x || b.x + b.width <= a.x || a.y + a.height <= b.y || b.y + b.height <= a.y, `${node.id} overlaps ${other.id}`).toBe(true);
      }
      if (node.parentId) {
        const parent = rectangle(model.nodes.find((item) => item.id === node.parentId)!, model.nodes);
        expect(a.x).toBeGreaterThanOrEqual(parent.x);
        expect(a.y).toBeGreaterThanOrEqual(parent.y);
        expect(a.x + a.width).toBeLessThanOrEqual(parent.x + parent.width);
        expect(a.y + a.height).toBeLessThanOrEqual(parent.y + parent.height);
      }
    }
    expect(model.hiddenConnections).toBe(Math.max(0, count - 16));
  });


  it("routes relationships and supporting details through gaps without crossing other cards", () => {
    const detail = denseGraph();
    // Exercise both root and connected-resource evidence, with incoming arrows too.
    detail.connections[0].direction = "incoming";
    detail.evidence[1].subject!.entity_id = "item-1";
    detail.evidence[2].subject!.entity_id = "item-7";
    const model = buildGraph(detail, new Set(), "", true);
    const anchor = (id: string, handle: string) => {
      const box = rectangle(model.nodes.find((node) => node.id === id)!, model.nodes);
      return { x: handle.startsWith("left") ? box.x : handle.startsWith("right") ? box.x + box.width : box.x + box.width / 2,
        y: handle.startsWith("bottom") ? box.y + box.height : handle.startsWith("top") ? box.y : box.y + box.height / 2 };
    };
    for (const edge of model.edges) {
      const waypoints = edge.data!.waypoints as Array<{ x: number; y: number }>;
      const points = [anchor(edge.source, edge.sourceHandle!), ...waypoints, anchor(edge.target, edge.targetHandle!)];
      for (const [index, point] of points.slice(0, -1).entries()) {
        const next = points[index + 1];
        for (const node of model.nodes.filter((item) => item.data.role !== "cluster" && item.id !== edge.source && item.id !== edge.target)) {
          const box = rectangle(node, model.nodes);
          const crosses = point.x === next.x
            ? point.x > box.x && point.x < box.x + box.width && Math.max(point.y, next.y) > box.y && Math.min(point.y, next.y) < box.y + box.height
            : point.y > box.y && point.y < box.y + box.height && Math.max(point.x, next.x) > box.x && Math.min(point.x, next.x) < box.x + box.width;
          expect(crosses, `${edge.id} crosses ${node.id}`).toBe(false);
        }
      }
    }
  });

  it("retains report history and represents the newest report for each exact resource", () => {
    const model = buildGraph(denseGraph(), new Set(), "", true);
    const facts = model.nodes.filter((node) => node.data.role === "evidence");
    expect(facts).toHaveLength(8);
    expect(model.visibleEvidence).toBe(12);
    expect(facts[0].data.evidenceGroup).toHaveLength(2);
    expect(facts[0].data.evidence?.observed_at).toBe("2026-09-19T12:00:00Z");
  });

  it("does not merge observations with unresolved locators", () => {
    const detail = denseGraph();
    detail.evidence = detail.evidence.slice(0, 2).map((fact) => ({ ...fact, location: undefined }));
    const facts = buildGraph(detail, new Set(), "", true).nodes.filter((node) => node.data.role === "evidence");
    expect(facts).toHaveLength(2);
  });

  it("keeps stale running observations historical in the graph root", () => {
    expect(buildGraph(denseGraph(), new Set(), "", false).nodes[0].data.detail).toContain("Running when last checked");
  });
});
