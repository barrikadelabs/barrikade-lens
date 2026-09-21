import { describe, expect, it } from "vitest";
import {
  confidenceLabel, confidenceLabels, connectionStatusLabel, connectionStatusLabels,
  freshnessLabel, freshnessLabels, locationTypeLabel, locationTypeLabels, plainLabel,
  stateLabel, stateLabels, systemTypeLabel, systemTypeLabels,
} from "./copy";

describe("plain-language labels", () => {
  it.each(Object.entries(systemTypeLabels))("labels system type %s", (value, label) => expect(systemTypeLabel(value)).toBe(label));
  it.each(Object.entries(stateLabels))("labels state %s", (value, label) => expect(stateLabel(value)).toBe(label));
  it.each(Object.entries(confidenceLabels))("labels confidence %s", (value, label) => expect(confidenceLabel(value)).toBe(label));
  it.each(Object.entries(freshnessLabels))("labels freshness %s", (value, label) => expect(freshnessLabel(value)).toBe(label));
  it.each(Object.entries(locationTypeLabels))("labels location type %s", (value, label) => expect(locationTypeLabel(value)).toBe(label));
  it.each(Object.entries(connectionStatusLabels))("labels connection status %s", (value, label) => expect(connectionStatusLabel(value)).toBe(label));

  it("turns unknown values into readable fallback labels", () => {
    expect(plainLabel("new_internal.value")).toBe("New Internal Value");
    expect(systemTypeLabel("new_system_type")).toBe("New System Type");
    expect(stateLabel("new_state")).toBe("New State");
    expect(confidenceLabel("new_confidence")).toBe("New Confidence");
    expect(freshnessLabel("new_freshness")).toBe("New Freshness");
    expect(locationTypeLabel("new_location")).toBe("New Location");
    expect(connectionStatusLabel("new_status")).toBe("New Status");
  });
});
