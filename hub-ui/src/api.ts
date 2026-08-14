export type Confidence = "confirmed" | "likely" | "possible";
export type SystemType = "autonomous_agent" | "agent_tool" | "model_runtime";

export type CoverageSummary = {
  target_type: "endpoint" | "repository" | "kubernetes";
  reporting: number;
  fresh: number;
  stale: number;
  partial: number;
  collectors: number;
  expected_count: number | null;
  population_configured: boolean;
};

export type Change = {
  id: string;
  event_type: string;
  entity_id: string;
  entity_name?: string;
  source_id?: string;
  target_id?: string;
  snapshot_id?: string;
  category: string;
  summary: string;
  system_type?: SystemType;
  surface?: string;
  details?: { fields?: Array<{ path: string; before?: unknown; after?: unknown }>; [key: string]: unknown };
  changed_at: string;
};

export type Overview = {
  window: string;
  generated_at: string;
  coverage: CoverageSummary[];
  footprint: {
    system_types: Record<string, number>;
    states: Record<string, number>;
    surfaces: Record<string, number>;
  };
  attention: Record<string, number>;
  changes: Change[];
  data_quality: {
    confidence: Record<string, number>;
    confidence_note: string;
    coverage_note: string;
  };
};

export type SystemItem = {
  id: string;
  kind: string;
  name: string;
  attributes: Record<string, unknown>;
  target_id?: string;
  target_name?: string;
  target_freshness?: "fresh" | "stale" | "never" | "unknown";
  surface: string;
  system_type: SystemType;
  product_id?: string;
  product_category?: string;
  state: string;
  network_scope: string;
  attributed: boolean;
  confidence: Confidence;
  first_seen_at: string;
  last_seen_at: string;
};

export type Evidence = {
  id: string;
  source_id: string;
  detector_id: string;
  detector_version: string;
  method: string;
  family: string;
  specificity: string;
  locator?: string;
  content_hash?: string;
  title?: string;
  summary?: string;
  location?: string;
  locator_kind?: "protected_path" | "network_listener" | "endpoint" | "repository_path" | "resource_reference" | "unavailable";
  source_name?: string;
  source_type?: string;
  target_id?: string;
  target_name?: string;
  target_type?: string;
  target_freshness?: "fresh" | "stale" | "never";
  matched_facts?: Array<{ label: string; value: string }>;
  subject?: { entity_id: string; entity_kind: string; name: string; confidence: Confidence };
  related_entities?: Array<{
    entity_id: string;
    entity_kind: string;
    name: string;
    confidence: Confidence;
    matched_facts?: Array<{ label: string; value: string }>;
  }>;
  why_it_matched?: string;
  investigation_hint?: string;
  integrity?: { locator_reference?: string; content_hash?: string };
  observed_at: string;
  observations: number;
};

export type Connection = {
  relationship_id: string;
  relationship_kind: string;
  label: string;
  direction: "outgoing" | "incoming";
  confidence: Confidence;
  attributes: Record<string, unknown>;
  entity: { id: string; kind: string; name: string; attributes: Record<string, unknown> };
};

export type SystemDetail = SystemItem & { connections: Connection[]; evidence: Evidence[] };

export type Collector = {
  source_id: string;
  source_type: string;
  name: string;
  collector_version?: string;
  last_seen_at?: string;
  last_full_at?: string;
  sequence?: number;
  partial: boolean;
  error_count: number;
  coverage?: Record<string, unknown>;
  revoked_at?: string;
};

export type Target = {
  id: string;
  target_type: "endpoint" | "repository" | "kubernetes";
  identity_quality: "persistent" | "legacy_identity";
  name: string;
  platform?: string;
  architecture?: string;
  first_seen_at: string;
  last_seen_at?: string;
  last_full_at?: string;
  current: boolean;
  freshness: "fresh" | "stale" | "never";
  partial?: boolean;
  possible_duplicate?: boolean;
  collectors: Collector[];
};

export type Entity = {
  id: string;
  kind: string;
  canonical_key?: string;
  name: string;
  confidence: Confidence;
  attributes: Record<string, unknown>;
  provenance?: string[];
  current: boolean;
  stale: boolean;
  first_seen_at: string;
  last_seen_at: string;
  posture?: {
    target_id?: string;
    target_name?: string;
    target_freshness?: "fresh" | "stale" | "never" | "unknown";
    surface?: string;
    system_role?: string;
    system_type?: string;
    state?: string;
    network_scope?: string;
    attributed?: boolean;
    product_id?: string;
    product_category?: string;
  };
};

export type EntityDetail = Entity & { evidence?: Evidence[] };

export type Relationship = {
  id: string;
  kind: string;
  from: string;
  to: string;
  from_name: string;
  to_name: string;
  attributes?: Record<string, unknown>;
  confidence: Confidence;
  last_seen_at: string;
};

export type PageResult<T> = { items: T[]; limit: number; next_cursor?: string };

export type AuthConfig = {
  enabled: boolean;
  development_bootstrap: boolean;
  authorization_endpoint?: string;
  client_id?: string;
  redirect_uri?: string;
  scopes?: string[];
};

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
  constructor(private token: string) {}

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set("Authorization", `Bearer ${this.token}`);
    if (init?.body) headers.set("Content-Type", "application/json");
    const response = await fetch(path, { ...init, headers });
    if (!response.ok) {
      const body = await response.json().catch(() => null) as { error?: { message?: string } } | null;
      throw new Error(body?.error?.message ?? `Lens Hub returned ${response.status}`);
    }
    return response.json() as Promise<T>;
  }

  overview(window = "7d") {
    return this.request<Overview>(queryPath("/v1/overview", { window }));
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

  target(id: string) {
    return this.request<Target>(`/v1/targets/${encodeURIComponent(id)}`);
  }

  entities(filters: Record<string, string | number | boolean | undefined> = {}) {
    return this.request<PageResult<Entity>>(queryPath("/v1/entities", { limit: 50, ...filters }));
  }

  entity(id: string) {
    return this.request<EntityDetail>(`/v1/entities/${encodeURIComponent(id)}`);
  }

  relationships(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<Relationship>>(queryPath("/v1/relationships", { limit: 75, ...filters }));
  }

  changes(filters: Record<string, string | number | undefined> = {}) {
    return this.request<PageResult<Change>>(queryPath("/v1/changes", { limit: 50, window: "7d", ...filters }));
  }

  coverage() {
    return this.request<{ target_types: CoverageSummary[]; collectors: { active: number } }>("/v1/coverage");
  }

  setBaselines(baselines: Array<{ target_type: string; expected_count: number | null }>) {
    return this.request("/v1/admin/coverage/baselines", { method: "PUT", body: JSON.stringify({ baselines }) });
  }

  enrollment(uses = 1, expires = 600, source_type = "endpoint") {
    return this.request<{ code: string; expires_at: string }>("/v1/admin/enrollment-codes", {
      method: "POST",
      body: JSON.stringify({ uses, expires_in_seconds: expires, source_type }),
    });
  }

  async downloadExport(format: "lens" | "ndjson" | "cyclonedx") {
    const response = await fetch(`/v1/exports?format=${format}`, { headers: { Authorization: `Bearer ${this.token}` } });
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
