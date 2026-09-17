import { describe, expect, it } from "vitest";
import { pageForPath, pagePath } from "./navigation";

describe("Hub navigation", () => {
  it.each([
    ["/overview", "Overview"],
    ["/findings/finding-1", "Findings"],
    ["/inventory", "Inventory"],
    ["/systems/system-1", "Inventory"],
    ["/connections/new", "Connections"],
    ["/changes", "Changes"],
    ["/systems/system-1/evidence", "Evidence"],
    ["/settings", "Settings"],
  ] as const)("maps %s to %s", (path, page) => {
    expect(pageForPath(path)).toBe(page);
  });

  it("keeps every page addressable by the shell", () => {
    expect(Object.keys(pagePath)).toEqual(["Overview", "Findings", "Inventory", "Connections", "Changes", "Evidence", "Settings"]);
  });
});
