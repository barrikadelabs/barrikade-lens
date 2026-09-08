import type { CaptureResult, PostHog } from "posthog-js/dist/module.no-external";

export type LensPage = "overview" | "findings" | "inventory" | "connections" | "changes" | "evidence" | "settings";
type ConnectionType = "aws" | "azure" | "gcp" | "endpoint" | "github" | "kubernetes";
type Platform = "macos" | "windows" | "linux";

export type BrowserAnalyticsEvent =
  | { name: "lens_page_viewed"; properties: { lens_page: LensPage } }
  | { name: "connection_type_selected"; properties: { connection_type: ConnectionType } }
  | { name: "install_platform_selected"; properties: { platform: Platform } }
  | { name: "inventory_viewed"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "system_opened"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "finding_opened"; properties: { severity?: "critical" | "high" | "medium" | "low"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "evidence_graph_viewed"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible" } }
  | { name: "changes_viewed"; properties: Record<string, never> };

type RuntimeConfig = { host: string; project_token: string; deployment_environment: string };
type SessionAnalytics = { enabled: boolean; user_id: string; workspace_id: string };

const allowedProperties: Record<BrowserAnalyticsEvent["name"], ReadonlySet<string>> = {
  lens_page_viewed: new Set(["lens_page"]),
  connection_type_selected: new Set(["connection_type"]),
  install_platform_selected: new Set(["platform"]),
  inventory_viewed: new Set(["system_kind", "confidence", "freshness", "owner_state"]),
  system_opened: new Set(["system_kind", "confidence", "freshness", "owner_state"]),
  finding_opened: new Set(["severity", "freshness", "owner_state"]),
  evidence_graph_viewed: new Set(["system_kind", "confidence"]),
  changes_viewed: new Set(),
};

const allowedValues: Record<string, ReadonlySet<string>> = {
  lens_page: new Set(["overview", "findings", "inventory", "connections", "changes", "evidence", "settings"]),
  connection_type: new Set(["aws", "azure", "gcp", "endpoint", "github", "kubernetes"]),
  platform: new Set(["macos", "windows", "linux"]),
  system_kind: new Set(["autonomous_agent", "agent_tool", "model_runtime"]),
  confidence: new Set(["confirmed", "likely", "possible"]),
  freshness: new Set(["fresh", "stale"]),
  severity: new Set(["critical", "high", "medium", "low"]),
  owner_state: new Set(["owned", "unowned"]),
};

let posthog: PostHog | undefined;
let currentIdentity = "";
let currentWorkspace = "";
let deploymentEnvironment = "";
let initialization = Promise.resolve();
let generation = 0;

export function browserPrivacySignal(): boolean {
  const privacyNavigator = navigator as Navigator & { globalPrivacyControl?: boolean };
  return privacyNavigator.globalPrivacyControl === true || navigator.doNotTrack === "1" || (window as Window & { doNotTrack?: string }).doNotTrack === "1";
}

export function semanticPage(pathname: string): LensPage | undefined {
  if (pathname === "/" || pathname.startsWith("/overview")) return "overview";
  if (pathname.startsWith("/findings")) return "findings";
  if (pathname.startsWith("/inventory") || /^\/systems\/[^/]+$/.test(pathname)) return "inventory";
  if (pathname.startsWith("/connections")) return "connections";
  if (pathname.startsWith("/changes")) return "changes";
  if (pathname.endsWith("/evidence")) return "evidence";
  if (pathname.startsWith("/settings")) return "settings";
  return undefined;
}

export function sanitizeBrowserEvent(event: CaptureResult | null): CaptureResult | null {
  if (!event?.event || !(event.event in allowedProperties)) return null;
  const allowed = allowedProperties[event.event as BrowserAnalyticsEvent["name"]];
  const properties: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(event.properties ?? {})) {
	if (allowed.has(key) && typeof value === "string" && value.length <= 32 && allowedValues[key]?.has(value)) properties[key] = value;
  }
  properties.schema_version = 1;
  properties.origin = "browser";
  properties.app_version = "2.0.0";
  properties.deployment_environment = deploymentEnvironment;
  properties.workspace_id = currentWorkspace;
  properties.$process_person_profile = false;
  properties.$geoip_disable = true;
  return { ...event, properties } as CaptureResult;
}

export function configureAnalytics(config: RuntimeConfig | undefined, session: SessionAnalytics | undefined) {
	const requestedGeneration = ++generation;
  initialization = initialization.then(async () => {
    if (!config || !session?.enabled || !session.user_id || !session.workspace_id || browserPrivacySignal()) {
	  clearAnalyticsClient();
      return;
    }
	if (requestedGeneration !== generation) return;
    deploymentEnvironment = config.deployment_environment;
    if (!posthog) {
	  currentWorkspace = session.workspace_id;
      const module = await import("posthog-js/dist/module.no-external");
	  if (requestedGeneration !== generation) return;
      posthog = module.default;
      posthog.init(config.project_token, {
        api_host: config.host,
        ui_host: config.host,
        bootstrap: { distinctID: session.user_id, isIdentifiedID: true },
        persistence: "memory",
        person_profiles: "never",
        autocapture: false,
        capture_pageview: false,
        capture_pageleave: false,
        capture_exceptions: false,
        disable_session_recording: true,
        disable_surveys: true,
        advanced_disable_flags: true,
        advanced_disable_feature_flags: true,
        advanced_disable_feature_flags_on_first_load: true,
        disable_external_dependency_loading: true,
        respect_dnt: true,
        before_send: (event) => sanitizeBrowserEvent(event),
      });
      currentIdentity = session.user_id;
      return;
    }
    if (currentIdentity !== session.user_id || currentWorkspace !== session.workspace_id) {
      posthog.reset({ resetDeviceID: true, bootstrap: { distinctID: session.user_id, isIdentifiedID: true } });
      currentIdentity = session.user_id;
      currentWorkspace = session.workspace_id;
    }
  }).catch(() => undefined);
}

export function captureAnalytics(event: BrowserAnalyticsEvent) {
  void initialization.then(() => posthog?.capture(event.name, event.properties));
}

export function resetAnalytics() {
	generation++;
	clearAnalyticsClient();
}

function clearAnalyticsClient() {
  posthog?.reset({ resetDeviceID: true });
  currentIdentity = "";
  currentWorkspace = "";
}
