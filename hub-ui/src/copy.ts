export const systemTypeLabels: Record<string, string> = {
  autonomous_agent: "Autonomous agent",
  agent_tool: "AI agent tool",
  model_runtime: "AI model runtime",
};

export const stateLabels: Record<string, string> = {
  running: "Running now",
  deployed: "Deployed",
  defined: "Found in code",
  configured: "Set up",
  installed: "Installed",
  residual: "Leftover files",
  cached: "Downloaded",
  observed: "Seen",
};

export const confidenceLabels: Record<string, string> = {
  confirmed: "Confirmed",
  likely: "Likely",
  possible: "Possible",
};

export const freshnessLabels: Record<string, string> = {
  fresh: "Up to date",
  stale: "Not reporting recently",
  partial: "Some data is missing",
  unknown: "Status unknown",
};

export const locationTypeLabels: Record<string, string> = {
  endpoint: "Device",
  repository: "Repository",
  kubernetes: "Cluster",
  cloud: "Cloud account",
};

export const connectionStatusLabels: Record<string, string> = {
  setup_pending: "Setup not finished",
  awaiting_install: "Setup not finished",
  authorizing: "Waiting for GitHub",
  awaiting_selection: "Choose repositories",
  verifying: "Checking access",
  auth_error: "Access needs attention",
  connected: "Connected",
  queued: "Waiting to start",
  running: "Scan in progress",
  ingesting: "Preparing results",
  processing: "Preparing first results",
  scanning: "Scanning repositories",
  ready: "Results ready",
  partial: "Some data is missing",
  stale: "Not reporting recently",
  failed: "Scan failed",
  complete: "Results ready",
  cancelled: "Scan cancelled",
  retry_wait: "Waiting to try again",
  revoked: "Access removed",
  disconnected: "Disconnected",
};

export const changeCategoryLabels: Record<string, string> = {
  state: "Status",
  network_scope: "Network access",
  attribution: "Owner",
  capability: "Connections",
  confidence: "Confidence",
  identity: "Identity",
  freshness: "Last report",
  metadata: "Details",
};

export const changeSummaryLabels: Record<string, string> = {
  "Attribution evidence added": "Owner information added",
  "Attribution evidence removed": "Attribution evidence no longer observed",
  "Capability connection added": "Connection added",
  "Capability connection removed": "Connection removed",
  "Deployment link added": "Installation link added",
  "Deployment link removed": "Installation link removed",
  "Discovery facts changed": "Details changed",
  "Discovery inventory changed": "Inventory changed",
  "Exposure added": "Network access added",
  "Exposure removed": "Network access removed",
  "Network exposure changed": "Network access changed",
  "Runs on added": "Location link added",
  "Runs on removed": "Location link removed",
  "System discovered": "AI tool or agent found",
  "System is stale": "Not reporting recently",
  "System removed from current inventory": "No longer in current inventory",
};

export const changeFieldLabels: Record<string, string> = {
  current: "In current inventory",
  installed: "Installed",
  installation_methods: "How it was found",
  running_at_scan: "Running when checked",
  network_scope: "Network access",
  attributed: "Owner assigned",
};

const changeValueLabels: Record<string, string> = {
  executable_path: "Installed command",
  ide_extension: "Editor extension",
  ide_extension_manifest: "Editor extension details",
  config_file: "Configuration file",
};

export function plainLabel(value: string): string {
  return value.replace(/[._-]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function systemTypeLabel(value: string): string { return systemTypeLabels[value] ?? plainLabel(value); }
export function stateLabel(value: string): string { return stateLabels[value] ?? plainLabel(value); }
export function observedStateLabel(value: string, freshness?: string): string {
  return value === "running" && freshness === "stale" ? "Running when last checked" : stateLabel(value);
}
export function confidenceLabel(value: string): string { return confidenceLabels[value] ?? plainLabel(value); }
export function freshnessLabel(value: string): string { return freshnessLabels[value] ?? plainLabel(value); }
export function locationTypeLabel(value: string): string { return locationTypeLabels[value] ?? plainLabel(value); }
export function connectionStatusLabel(value: string): string { return connectionStatusLabels[value] ?? plainLabel(value); }
export function changeCategoryLabel(value: string): string { return changeCategoryLabels[value] ?? plainLabel(value); }
export function changeSummaryLabel(value: string): string {
  if (changeSummaryLabels[value]) return changeSummaryLabels[value];
  const states: Record<string, string> = {
    Cached: stateLabels.cached,
    Configured: stateLabels.configured,
    Defined: stateLabels.defined,
    Deployed: stateLabels.deployed,
    Installed: stateLabels.installed,
    Residual: stateLabels.residual,
    Running: stateLabels.running,
  };
  const transition = value.split(" → ");
  return transition.length === 2 && states[transition[0]] && states[transition[1]]
    ? `${states[transition[0]]} → ${states[transition[1]]}`
    : value;
}
export function changeFieldLabel(value: string): string {
  const key = value.replace(/^attributes\./, "");
  return changeFieldLabels[key] ?? plainLabel(key);
}
export function changeValueLabel(path: string, value: unknown): string {
  const key = path.replace(/^attributes\./, "");
  if (value === undefined || value === null || value === "") return "Not observed";
  if (Array.isArray(value)) return value.map((item) => changeValueLabel(key, item)).join(", ");
  if (typeof value === "boolean") {
    if (key === "running_at_scan") return value ? "Running" : "Not running";
    if (key === "current") return value ? "Included" : "Not included";
    return value ? "Yes" : "No";
  }
  if (typeof value === "object") return "More details available";
  return changeValueLabels[String(value)] ?? String(value);
}
