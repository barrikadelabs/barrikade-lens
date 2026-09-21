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

export function plainLabel(value: string): string {
  return value.replace(/[._-]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function systemTypeLabel(value: string): string { return systemTypeLabels[value] ?? plainLabel(value); }
export function stateLabel(value: string): string { return stateLabels[value] ?? plainLabel(value); }
export function confidenceLabel(value: string): string { return confidenceLabels[value] ?? plainLabel(value); }
export function freshnessLabel(value: string): string { return freshnessLabels[value] ?? plainLabel(value); }
export function locationTypeLabel(value: string): string { return locationTypeLabels[value] ?? plainLabel(value); }
export function connectionStatusLabel(value: string): string { return connectionStatusLabels[value] ?? plainLabel(value); }
