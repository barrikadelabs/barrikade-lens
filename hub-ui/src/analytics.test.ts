import { describe, expect, it } from "vitest";
import { sanitizeBrowserEvent, semanticPage } from "./analytics";

describe("privacy-minimized analytics", () => {
  it("maps only semantic application routes", () => {
    expect(semanticPage("/overview")).toBe("overview");
    expect(semanticPage("/systems/system-secret")).toBe("inventory");
    expect(semanticPage("/systems/system-secret/evidence")).toBe("evidence");
    expect(semanticPage("/install")).toBeUndefined();
    expect(semanticPage("/sign-in")).toBeUndefined();
  });

  it("removes URLs, referrers, DOM fields, identifiers, and unknown properties", () => {
    const result = sanitizeBrowserEvent({
      uuid: "00000000-0000-4000-8000-000000000000",
      event: "system_opened",
      properties: {
        system_kind: "autonomous_agent",
        confidence: "confirmed",
        $current_url: "https://lens.example/systems/customer-secret?token=secret",
        $referrer: "https://customer.example/repository",
        elements: [{ text: "sensitive customer command" }],
        entity_id: "customer-hostname",
        raw_error: "dial tcp db.prod.internal",
      },
    } as never);
    expect(result?.properties).toMatchObject({ system_kind: "autonomous_agent", confidence: "confirmed", schema_version: 1, origin: "browser" });
    expect(JSON.stringify(result)).not.toMatch(/customer|secret|hostname|raw_error|current_url|referrer|elements|dial tcp/);
  });

  it("drops unknown event names", () => {
    expect(sanitizeBrowserEvent({ uuid: "id", event: "$pageview", properties: { $current_url: "https://sensitive.example" } } as never)).toBeNull();
  });
});
