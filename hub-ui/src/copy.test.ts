import { describe, expect, it } from "vitest";
import {
  changeCategoryLabel, changeCategoryLabels, changeFieldLabel, changeFieldLabels,
  changeSummaryLabel, changeSummaryLabels, changeValueLabel, confidenceLabel,
  confidenceLabels, connectionStatusLabel, connectionStatusLabels,
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
  it.each(Object.entries(changeCategoryLabels))("labels change category %s", (value, label) => expect(changeCategoryLabel(value)).toBe(label));
  it.each(Object.entries(changeSummaryLabels))("labels change summary %s", (value, label) => expect(changeSummaryLabel(value)).toBe(label));
  it.each(Object.entries(changeFieldLabels))("labels change field %s", (value, label) => expect(changeFieldLabel(value)).toBe(label));

  it("rewrites status transitions and technical change values", () => {
    expect(changeSummaryLabel("Configured → Running")).toBe("Set up → Running now");
    expect(changeValueLabel("attributes.installation_methods", ["executable_path", "ide_extension_manifest"])).toBe("Installed command, Editor extension details");
    expect(changeValueLabel("attributes.running_at_scan", true)).toBe("Running");
    expect(changeValueLabel("current", false)).toBe("Not included");
  });

  it("turns unknown values into readable fallback labels", () => {
    expect(plainLabel("new_internal.value")).toBe("New Internal Value");
    expect(systemTypeLabel("new_system_type")).toBe("New System Type");
    expect(stateLabel("new_state")).toBe("New State");
    expect(confidenceLabel("new_confidence")).toBe("New Confidence");
    expect(freshnessLabel("new_freshness")).toBe("New Freshness");
    expect(locationTypeLabel("new_location")).toBe("New Location");
    expect(connectionStatusLabel("new_status")).toBe("New Status");
    expect(changeCategoryLabel("new_category")).toBe("New Category");
    expect(changeSummaryLabel("A new summary")).toBe("A new summary");
    expect(changeFieldLabel("attributes.new_field")).toBe("New Field");
    expect(changeValueLabel("attributes.new_field", "kept as written")).toBe("kept as written");
  });
});
