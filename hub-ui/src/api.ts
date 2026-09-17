import type { Activation, AuthConfig, Change, ConnectionStatus, Environment, EnvironmentKind, EnvironmentScan, ExposureFinding, Notification, Overview, PageResult, ProductItem, ScanStatus, Session, SetupSession, SystemDetail, SystemItem, Target } from "./api-types";
export type * from "./api-types";

export async function authConfig() {
  const response = await fetch("/v1/auth/config");
  if (!response.ok) throw new Error("Could not load authentication configuration");
  return response.json() as Promise<AuthConfig>;
}

export async function exchangeOIDC(code: string, redirect_uri: string, code_verifier: string) {
  const response = await fetch("/v1/auth/exchange", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code, redirect_uri, code_verifier }),
  });
  if (!response.ok) throw new Error("OIDC sign-in could not be completed");
  return response.json() as Promise<{ access_token: string }>;
}

function queryPath(path: string, values: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") query.set(key, String(value));
  });
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}

export class API {
  constructor(private tokenSource: string | (() => Promise<string | null>)) {}

  private async token() {
    const value = typeof this.tokenSource === "string" ? this.tokenSource : await this.tokenSource();
    if (!value) throw new Error("Your Lens session has expired. Sign in again.");
    return value;
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set("Authorization", `Bearer ${await this.token()}`);
    if (init?.body) headers.set("Content-Type", "application/json");
    const response = await fetch(path, { ...init, headers });
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { error?: { message?: string } } | null;
      throw new Error(body?.error?.message ?? `Lens Hub returned ${response.status}`);
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  session() { return this.request<Session>("/v1/session"); }

  updateAnalytics(enabled: boolean) {
	return this.request<{ enabled: boolean }>("/v1/session/analytics", { method: "PATCH", body: JSON.stringify({ enabled }) });
  }

  bootstrapWorkspace(name: string) {
    return this.request<{ id: string; name: string; role: string; created: boolean }>("/v1/workspaces/bootstrap", { method: "POST", body: JSON.stringify({ name }) });
  }

  environments() { return this.request<{ items: Environment[] }>("/v1/environments"); }

  environment(id: string) { return this.request<Environment>(`/v1/environments/${encodeURIComponent(id)}`); }

  activation(id: string) { return this.request<Activation>(`/v1/environments/${encodeURIComponent(id)}/activation`); }

  createEnvironmentSetup(input: { kind: EnvironmentKind; display_name: string; external_id?: string; configuration?: Record<string, unknown> }) {
    return this.request<SetupSession>("/v1/environments/setup-sessions", { method: "POST", body: JSON.stringify(input) });
  }

  verifyEnvironment(id: string) {
    return this.request<{ environment_id: string; connection_status: ConnectionStatus; verification?: { principal: string; permissions: string[] }; scan?: { id: string; status: ScanStatus }; message?: string }>(`/v1/environments/${encodeURIComponent(id)}/verify`, { method: "POST" });
  }

  scanEnvironment(id: string) {
    return this.request<{ id: string; status: ScanStatus; coalesced: boolean }>(`/v1/environments/${encodeURIComponent(id)}/scans`, { method: "POST" });
  }

  environmentScan(environmentID: string, scanID: string) {
    return this.request<EnvironmentScan>(`/v1/environments/${encodeURIComponent(environmentID)}/scans/${encodeURIComponent(scanID)}`);
  }

  disconnectEnvironment(id: string) {
    return this.request<{ id: string; connection_status: "disconnected"; purge_after_days: number; teardown: string[] }>(`/v1/environments/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  rotateEndpointCredential(id: string) {
    return this.request<SetupSession>(`/v1/environments/${encodeURIComponent(id)}/enrollment-credentials`, { method: "POST" });
  }

  createEndpointHandoff(id: string) {
    return this.request<{ id: string; environment_id: string; url: string; expires_at: string; token_displayed_once: true }>(`/v1/environments/${encodeURIComponent(id)}/handoffs`, { method: "POST" });
  }

  exposures(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<ExposureFinding>>(queryPath("/v1/exposures", { limit: 50, ...filters }));
  }

  exposure(id: string) { return this.request<ExposureFinding>(`/v1/exposures/${encodeURIComponent(id)}`); }

  notifications() { return this.request<{ items: Notification[] }>("/v1/notifications"); }

  readNotification(id: string) { return this.request<void>(`/v1/notifications/${encodeURIComponent(id)}`, { method: "PATCH" }); }

  deleteAccount() { return this.request<void>("/v1/account", { method: "DELETE" }); }

  deleteWorkspace(organizationID: string) { return this.request<void>("/v1/workspaces/current", { method: "DELETE", body: JSON.stringify({ confirmation: organizationID }) }); }

  overview(window = "7d") {
    return this.request<Overview>(queryPath("/v1/overview", { window }));
  }

  products() {
    return this.request<{ items: ProductItem[] }>("/v1/products");
  }

  systems(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<SystemItem>>(queryPath("/v1/systems", { limit: 50, ...filters }));
  }

  system(id: string) {
    return this.request<SystemDetail>(`/v1/systems/${encodeURIComponent(id)}`);
  }

  targets(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<Target>>(queryPath("/v1/targets", { limit: 50, ...filters }));
  }

  changes(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<Change>>(queryPath("/v1/changes", { limit: 50, window: "7d", ...filters }));
  }

  setBaselines(baselines: Array<{ target_type: string; expected_count: number | null }>) {
    return this.request("/v1/admin/coverage/baselines", { method: "PUT", body: JSON.stringify({ baselines }) });
  }

  async downloadExport(format: "lens" | "ndjson" | "cyclonedx") {
    const response = await fetch(`/v1/exports?format=${format}`, { headers: { Authorization: `Bearer ${await this.token()}` } });
    if (!response.ok) throw new Error(`Lens Hub returned ${response.status}`);
    const blob = await response.blob();
    const href = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `barrikade-lens-inventory.${format === "ndjson" ? "ndjson" : "json"}`;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(href);
  }
}
