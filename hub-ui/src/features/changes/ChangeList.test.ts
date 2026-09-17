import { describe, expect, it } from "vitest";
import type { Change } from "../../api";
import { groupChanges } from "../shared/ChangeList";

function change(overrides: Partial<Change> = {}): Change {
  return {
    id: "change-1",
    event_type: "entity.updated",
    entity_id: "system-1",
    category: "state",
    summary: "State changed",
    changed_at: "2026-09-17T10:00:00Z",
    ...overrides,
  };
}

describe("groupChanges", () => {
  it("groups repeated observations and keeps the newest groups first", () => {
    const grouped = groupChanges([
      change(),
      change({ id: "change-2", changed_at: "2026-09-17T11:00:00Z" }),
      change({ id: "change-3", entity_id: "system-2", summary: "Network scope changed", changed_at: "2026-09-17T12:00:00Z" }),
    ]);

    expect(grouped).toHaveLength(2);
    expect(grouped[0]).toMatchObject({ id: "change-3", occurrences: 1 });
    expect(grouped[1]).toMatchObject({ id: "change-1", occurrences: 2 });
  });
});
