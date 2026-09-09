import type { CaptureResult, PostHog, PostHogConfig } from "posthog-js/dist/module.full.no-external";

export type LensPage = "overview" | "findings" | "inventory" | "connections" | "changes" | "evidence" | "settings";
type ConnectionType = "aws" | "azure" | "gcp" | "endpoint" | "github" | "kubernetes";
type Platform = "macos" | "windows" | "linux";
type Surface = LensPage | "navigation" | "export" | "setup" | "notification";
type Interaction =
  | "filter_changed" | "search_used" | "load_more" | "refresh" | "open"
  | "setup_generated" | "command_copied" | "handoff_created" | "verify_requested"
  | "scan_requested" | "baseline_saved" | "notification_opened";

export type BrowserAnalyticsEvent =
  | { name: "lens_page_viewed"; properties: { lens_page: LensPage } }
  | { name: "connection_type_selected"; properties: { connection_type: ConnectionType } }
  | { name: "install_platform_selected"; properties: { platform: Platform } }
  | { name: "inventory_viewed"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "system_opened"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "finding_opened"; properties: { severity?: "critical" | "high" | "medium" | "low"; freshness?: "fresh" | "stale"; owner_state?: "owned" | "unowned" } }
  | { name: "evidence_graph_viewed"; properties: { system_kind?: string; confidence?: "confirmed" | "likely" | "possible" } }
  | { name: "changes_viewed"; properties: Record<string, never> }
  | { name: "lens_interaction"; properties: { surface: Surface; interaction: Interaction; control?: string; connection_type?: ConnectionType; platform?: Platform; export_format?: "json" | "ndjson" | "cyclonedx" } };

type RuntimeConfig = { host: string; project_token: string; deployment_environment: string };
type SessionAnalytics = { enabled: boolean; user_id: string; workspace_id: string };

const schemaVersion = 2;
const allowedProperties: Record<BrowserAnalyticsEvent["name"], ReadonlySet<string>> = {
  lens_page_viewed: new Set(["lens_page"]),
  connection_type_selected: new Set(["connection_type"]),
  install_platform_selected: new Set(["platform"]),
  inventory_viewed: new Set(["system_kind", "confidence", "freshness", "owner_state"]),
  system_opened: new Set(["system_kind", "confidence", "freshness", "owner_state"]),
  finding_opened: new Set(["severity", "freshness", "owner_state"]),
  evidence_graph_viewed: new Set(["system_kind", "confidence"]),
  changes_viewed: new Set(),
  lens_interaction: new Set(["surface", "interaction", "control", "connection_type", "platform", "export_format"]),
};

const allowedValues: Record<string, ReadonlySet<string>> = {
  lens_page: new Set(["overview", "findings", "inventory", "connections", "changes", "evidence", "settings"]),
  surface: new Set(["overview", "findings", "inventory", "connections", "changes", "evidence", "settings", "navigation", "export", "setup", "notification"]),
  interaction: new Set(["filter_changed", "search_used", "load_more", "refresh", "open", "setup_generated", "command_copied", "handoff_created", "verify_requested", "scan_requested", "baseline_saved", "notification_opened"]),
  control: new Set(["window", "severity", "confidence", "ownership", "freshness", "state", "system_type", "network", "category", "surface", "target_type", "system", "evidence", "navigation"]),
  connection_type: new Set(["aws", "azure", "gcp", "endpoint", "github", "kubernetes"]),
  platform: new Set(["macos", "windows", "linux"]),
  export_format: new Set(["json", "ndjson", "cyclonedx"]),
  system_kind: new Set(["autonomous_agent", "agent_tool", "model_runtime"]),
  confidence: new Set(["confirmed", "likely", "possible"]),
  freshness: new Set(["fresh", "stale"]),
  severity: new Set(["critical", "high", "medium", "low"]),
  owner_state: new Set(["owned", "unowned"]),
};

// SDK-generated or Lens-generated context only. URL, referrer, DOM, campaign,
// person, and customer-derived properties are deliberately absent.
const sdkPropertyAllowlist = new Set([
  "token", "distinct_id", "$device_id", "$session_id", "$window_id", "$insert_id",
  "$lib", "$lib_version", "$browser", "$browser_version", "$os", "$os_version",
  "$device_type", "$viewport_height", "$viewport_width", "$screen_height", "$screen_width",
  "$timezone", "$timezone_offset", "$process_person_profile", "$geoip_disable",
]);
const surveyEvents = new Set(["survey shown", "survey dismissed", "survey sent", "survey abandoned"]);
const exceptionTypes = new Set(["Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError", "URIError", "EvalError", "Exception"]);
const webVitalKeys = new Set(["LCP", "CLS", "FCP", "INP"]);

let posthog: PostHog | undefined;
let currentIdentity = "";
let currentWorkspace = "";
let deploymentEnvironment = "";
let initialization = Promise.resolve();
let generation = 0;
let captureAllowed = false;

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

function safeSDKProperties(properties: Record<string, unknown>): Record<string, unknown> {
  const safe: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(properties)) {
    if (!sdkPropertyAllowlist.has(key)) continue;
    if (typeof value === "string" && value.length <= 128) safe[key] = value;
    if (typeof value === "number" && Number.isFinite(value)) safe[key] = value;
    if (typeof value === "boolean") safe[key] = value;
  }
  return safe;
}

function commonProperties(source: Record<string, unknown>): Record<string, unknown> {
  return {
    ...safeSDKProperties(source),
    schema_version: schemaVersion,
    origin: "browser",
    app_version: "2.0.0",
    deployment_environment: deploymentEnvironment,
    workspace_id: currentWorkspace,
    $process_person_profile: false,
    $geoip_disable: true,
  };
}

function sanitizeSurvey(event: CaptureResult): CaptureResult | null {
  const source = event.properties ?? {};
  const surveyID = source.$survey_id;
  if (typeof surveyID !== "string" || !/^[a-zA-Z0-9_-]{1,64}$/.test(surveyID)) return null;
  const properties = commonProperties(source);
  properties.$survey_id = surveyID;
  if (typeof source.$survey_iteration === "number" && Number.isInteger(source.$survey_iteration) && source.$survey_iteration >= 0 && source.$survey_iteration <= 1000) properties.$survey_iteration = source.$survey_iteration;
  if (typeof source.$survey_completed === "boolean") properties.$survey_completed = source.$survey_completed;
  if (typeof source.$survey_partially_completed === "boolean") properties.$survey_partially_completed = source.$survey_partially_completed;
  // Numeric/rating answers are useful and cannot contain entered customer data.
  for (const [key, value] of Object.entries(source)) {
    if (/^\$survey_response(?:_\w{1,64})?$/.test(key) && typeof value === "number" && Number.isFinite(value) && Math.abs(value) <= 100) properties[key] = value;
  }
  return { uuid: event.uuid, event: event.event, properties, timestamp: event.timestamp } as CaptureResult;
}

function sanitizeException(event: CaptureResult): CaptureResult {
  const source = event.properties ?? {};
  const first = Array.isArray(source.$exception_list) ? source.$exception_list[0] as { type?: unknown; stacktrace?: { frames?: unknown } } | undefined : undefined;
  const type = typeof first?.type === "string" && exceptionTypes.has(first.type) ? first.type : "Exception";
  const frames = Array.isArray(first?.stacktrace?.frames) ? first.stacktrace.frames.slice(-20).flatMap((candidate) => {
    if (!candidate || typeof candidate !== "object") return [];
    const frame = candidate as { function?: unknown; lineno?: unknown; colno?: unknown };
    const safeFrame: Record<string, unknown> = { filename: "lens-ui", in_app: true };
    if (typeof frame.function === "string" && /^[a-zA-Z0-9_$<>.]{1,80}$/.test(frame.function)) safeFrame.function = frame.function;
    if (typeof frame.lineno === "number" && Number.isInteger(frame.lineno) && frame.lineno >= 0 && frame.lineno <= 10_000_000) safeFrame.lineno = frame.lineno;
    if (typeof frame.colno === "number" && Number.isInteger(frame.colno) && frame.colno >= 0 && frame.colno <= 100_000) safeFrame.colno = frame.colno;
    return [safeFrame];
  }) : [];
  return {
    uuid: event.uuid,
    event: event.event,
    timestamp: event.timestamp,
    properties: {
      ...commonProperties(source),
      $exception_list: [{ type, value: "Lens UI exception", mechanism: { handled: false, synthetic: false }, ...(frames.length ? { stacktrace: { frames } } : {}) }],
    },
  } as CaptureResult;
}

function sanitizeWebVitals(event: CaptureResult): CaptureResult | null {
  const source = event.properties ?? {};
  const properties = commonProperties(source);
  let retained = false;
  for (const metric of webVitalKeys) {
    const key = `$web_vitals_${metric}_value`;
    const value = source[key];
    if (typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 900_000) {
      properties[key] = value;
      retained = true;
    }
  }
  return retained ? ({ uuid: event.uuid, event: event.event, properties, timestamp: event.timestamp } as CaptureResult) : null;
}

export function sanitizeBrowserEvent(event: CaptureResult | null): CaptureResult | null {
  if (!event?.event) return null;
  // Replay payloads are protected before serialization by the maximum-privacy
  // recorder configuration below. Rebuilding them here would corrupt playback.
  if (event.event === "$snapshot") {
    const source = event.properties ?? {};
    if (!Array.isArray(source.$snapshot_data) && typeof source.$snapshot_data !== "object") return null;
    return {
      uuid: event.uuid,
      event: event.event,
      timestamp: event.timestamp,
      properties: {
        ...commonProperties(source),
        $snapshot_data: source.$snapshot_data,
        ...(typeof source.$snapshot_bytes === "number" && Number.isFinite(source.$snapshot_bytes) ? { $snapshot_bytes: source.$snapshot_bytes } : {}),
      },
    } as CaptureResult;
  }
  if (event.event === "$exception") return sanitizeException(event);
  if (event.event === "$web_vitals") return sanitizeWebVitals(event);
  if (surveyEvents.has(event.event)) return sanitizeSurvey(event);
  if (event.event === "$feature_flag_called") {
    const source = event.properties ?? {};
    const key = source.$feature_flag;
    const response = source.$feature_flag_response;
    if (typeof key !== "string" || !/^[a-zA-Z0-9_-]{1,64}$/.test(key)) return null;
    const properties = commonProperties(source);
    properties.$feature_flag = key;
    if (typeof response === "boolean" || typeof response === "number" && Number.isFinite(response) && Math.abs(response) <= 1_000_000 || typeof response === "string" && /^[a-zA-Z0-9_-]{1,64}$/.test(response)) properties.$feature_flag_response = response;
    return { uuid: event.uuid, event: event.event, properties, timestamp: event.timestamp } as CaptureResult;
  }
  if (!(event.event in allowedProperties)) return null;
  const allowed = allowedProperties[event.event as BrowserAnalyticsEvent["name"]];
  const properties = commonProperties(event.properties ?? {});
  for (const [key, value] of Object.entries(event.properties ?? {})) {
    if (allowed.has(key) && typeof value === "string" && value.length <= 32 && allowedValues[key]?.has(value)) properties[key] = value;
  }
  return { uuid: event.uuid, event: event.event, properties, timestamp: event.timestamp } as CaptureResult;
}

export function safePostHogConfig(config: RuntimeConfig, session: SessionAnalytics): Partial<PostHogConfig> {
  return {
    api_host: config.host,
    ui_host: config.host.replace(".i.posthog.com", ".posthog.com"),
    bootstrap: { distinctID: session.user_id, isIdentifiedID: true },
    persistence: "memory",
    person_profiles: "never",
    autocapture: false,
    capture_pageview: false,
    capture_pageleave: false,
    capture_dead_clicks: false,
    capture_heatmaps: false,
    capture_exceptions: { capture_unhandled_errors: true, capture_unhandled_rejections: true, capture_console_errors: false },
    capture_performance: { network_timing: false, web_vitals: true },
    disable_session_recording: false,
    disable_surveys: false,
    advanced_disable_flags: false,
    advanced_disable_feature_flags: false,
    advanced_disable_feature_flags_on_first_load: false,
    disable_external_dependency_loading: true,
    enable_recording_console_log: false,
    mask_all_text: true,
    mask_all_element_attributes: true,
    save_campaign_params: false,
    save_referrer: false,
    respect_dnt: true,
    get_current_url: () => {
      const page = semanticPage(location.pathname);
      return page ? `${location.origin}/${page}` : `${location.origin}/private`;
    },
    session_recording: {
      maskAllInputs: true,
      maskTextSelector: "*",
      maskAllElementAttributes: true,
      blockSelector: "img,video,audio,iframe,canvas,svg,[data-analytics-private]",
      recordHeaders: false,
      recordBody: false,
      recordCrossOriginIframes: false,
      collectFonts: false,
      captureJsonLd: false,
      captureCanvas: { recordCanvas: false },
      maskCapturedNetworkRequestFn: () => null,
    },
    before_send: (event) => sanitizeBrowserEvent(event),
  };
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
      const module = await import("posthog-js/dist/module.full.no-external");
      if (requestedGeneration !== generation) return;
      posthog = module.default;
      posthog.init(config.project_token, safePostHogConfig(config, session));
      posthog.opt_in_capturing();
      posthog.startSessionRecording();
      currentIdentity = session.user_id;
      captureAllowed = true;
      return;
    }
    posthog.opt_in_capturing();
    if (currentIdentity !== session.user_id || currentWorkspace !== session.workspace_id) {
      posthog.reset({ resetDeviceID: true, bootstrap: { distinctID: session.user_id, isIdentifiedID: true } });
      currentIdentity = session.user_id;
      currentWorkspace = session.workspace_id;
    }
    posthog.startSessionRecording();
    captureAllowed = true;
  }).catch(() => undefined);
}

export function captureAnalytics(event: BrowserAnalyticsEvent) {
  void initialization.then(() => captureAllowed && posthog?.capture(event.name, event.properties));
}

export function isFeatureEnabled(key: string): boolean {
  return /^[a-zA-Z0-9_-]{1,64}$/.test(key) && posthog?.isFeatureEnabled(key) === true;
}

export function resetAnalytics() {
  generation++;
  clearAnalyticsClient();
}

function clearAnalyticsClient() {
  captureAllowed = false;
  posthog?.stopSessionRecording();
  posthog?.opt_out_capturing();
  posthog?.reset({ resetDeviceID: true });
  currentIdentity = "";
  currentWorkspace = "";
}
