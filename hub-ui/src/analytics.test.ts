import { describe, expect, it } from "vitest";
import { safePostHogConfig, sanitizeBrowserEvent, semanticPage } from "./analytics";

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
        token: "phc_browser_safe",
        distinct_id: "phu_safe",
        system_kind: "autonomous_agent",
        confidence: "confirmed",
        $current_url: "https://lens.example/systems/customer-secret?token=secret",
        $referrer: "https://customer.example/repository",
        elements: [{ text: "sensitive customer command" }],
        entity_id: "customer-hostname",
        raw_error: "dial tcp db.prod.internal",
      },
    } as never);
    expect(result?.properties).toMatchObject({ token: "phc_browser_safe", distinct_id: "phu_safe", system_kind: "autonomous_agent", confidence: "confirmed", schema_version: 2, origin: "browser" });
    expect(JSON.stringify(result)).not.toMatch(/customer|secret|hostname|raw_error|current_url|referrer|elements|dial tcp/);
  });

  it("drops unknown event names", () => {
    expect(sanitizeBrowserEvent({ uuid: "id", event: "$pageview", properties: { $current_url: "https://sensitive.example" } } as never)).toBeNull();
  });

  it("redacts exception messages and stack traces while retaining a safe error type", () => {
    const result = sanitizeBrowserEvent({
      uuid: "id",
      event: "$exception",
      properties: {
        token: "phc_browser_safe",
        $current_url: "https://lens.example/systems/customer-secret",
        $exception_list: [{ type: "TypeError", value: "customer-host failed", stacktrace: { frames: [{ filename: "https://customer.example/private.js", function: "renderLens", lineno: 42, colno: 7 }] } }],
      },
    } as never);
    expect(result?.properties.$exception_list).toEqual([{ type: "TypeError", value: "Lens UI exception", mechanism: { handled: false, synthetic: false }, stacktrace: { frames: [{ filename: "lens-ui", in_app: true, function: "renderLens", lineno: 42, colno: 7 }] } }]);
    expect(JSON.stringify(result)).not.toMatch(/customer|private\.js|current_url/);
  });

  it("keeps only bounded numeric survey answers", () => {
    const result = sanitizeBrowserEvent({
      uuid: "id",
      event: "survey sent",
      properties: { token: "phc_browser_safe", $survey_id: "activation-pulse", $survey_iteration: 2, $survey_response: "my secret hostname", $survey_response_rating: 9 },
    } as never);
    expect(result?.properties).toMatchObject({ token: "phc_browser_safe", $survey_id: "activation-pulse", $survey_iteration: 2, $survey_response_rating: 9 });
    expect(JSON.stringify(result)).not.toContain("secret hostname");
  });

  it("uses maximum-privacy replay and disables DOM-derived analytics", () => {
    const config = safePostHogConfig(
      { host: "https://eu.i.posthog.com", project_token: "phc_test", deployment_environment: "staging" },
      { enabled: true, user_id: "phu_test", workspace_id: "phw_test" },
    );
    expect(config).toMatchObject({
      persistence: "memory",
      person_profiles: "never",
      autocapture: false,
      capture_pageview: false,
      capture_pageleave: false,
      capture_heatmaps: false,
      capture_dead_clicks: false,
      disable_session_recording: false,
      disable_surveys: false,
      disable_external_dependency_loading: true,
      session_recording: {
        maskAllInputs: true,
        maskTextSelector: "*",
        maskAllElementAttributes: true,
        recordHeaders: false,
        recordBody: false,
        recordCrossOriginIframes: false,
        collectFonts: false,
        captureJsonLd: false,
        captureCanvas: { recordCanvas: false },
      },
    });
    expect(config.session_recording?.maskCapturedNetworkRequestFn?.({ name: "https://customer.example/private" } as never)).toBeNull();
  });

  it("keeps masked replay data but removes raw snapshot URL and hostname metadata", () => {
    const maskedReplay = [{ type: 3, data: { source: 1, texts: [{ value: "***" }] }, timestamp: 1 }];
    const result = sanitizeBrowserEvent({
      uuid: "id",
      event: "$snapshot",
      properties: {
        token: "phc_browser_safe",
        $session_id: "session-safe",
        $snapshot_data: maskedReplay,
        $snapshot_bytes: 123,
        $snapshot_host: "customer.internal",
        $current_url: "https://lens.example/systems/customer-secret?token=secret",
      },
    } as never);
    expect(result?.properties).toMatchObject({ token: "phc_browser_safe", $session_id: "session-safe", $snapshot_data: maskedReplay, $snapshot_bytes: 123 });
    expect(JSON.stringify(result)).not.toMatch(/customer\.internal|customer-secret|current_url/);
  });
});
