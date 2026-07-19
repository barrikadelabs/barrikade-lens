export type Entity = {
  id: string;
  kind: string;
  canonical_key?: string;
  name: string;
  confidence: "confirmed" | "likely" | "possible";
  attributes: Record<string, unknown>;
  provenance?: string[];
  current: boolean;
  stale: boolean;
  first_seen_at?: string;
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
  observed_at: string;
};

export type EntityDetail = Entity & { evidence?: Evidence[] };

export type Relationship = {
  id: string;
  kind: string;
  from: string;
  to: string;
  attributes?: Record<string, unknown>;
  confidence: "confirmed" | "likely" | "possible";
  current?: boolean;
  stale?: boolean;
  first_seen_at?: string;
  last_seen_at?: string;
};

export type Source = {
  source_id: string;
  source_type: string;
  name: string;
  platform?: string;
  collector_version?: string;
  last_seen_at?: string;
  last_full_at?: string;
  sequence?: number;
  current_entities: number;
  stale_entities: number;
};

export type Change = {
  id: string;
  event_type: string;
  entity_id: string;
  source_id: string;
  snapshot_id?: string;
  details?: Record<string, unknown>;
  changed_at: string;
};

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

export class API {
  constructor(private token: string) {}

  private async get<T>(path: string): Promise<T> {
    const response = await fetch(path, { headers: { Authorization: `Bearer ${this.token}` } });
    if (!response.ok) throw new Error(`Lens Hub returned ${response.status}`);
    return response.json() as Promise<T>;
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    const response = await fetch(path, {
      method: "POST",
      headers: { Authorization: `Bearer ${this.token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error(`Lens Hub returned ${response.status}`);
    return response.json() as Promise<T>;
  }

  entities(kind = "") {
    return this.get<{ items: Entity[] }>(`/v1/entities?limit=1000${kind ? `&kind=${encodeURIComponent(kind)}` : ""}`);
  }

  entity(id: string) {
    return this.get<EntityDetail>(`/v1/entities/${encodeURIComponent(id)}`);
  }

  relationships() {
    return this.get<{ items: Relationship[] }>("/v1/relationships?limit=1000");
  }

  coverage() {
    return this.get<{ sources: Source[] }>("/v1/coverage");
  }

  changes() {
    return this.get<{ items: Change[] }>("/v1/changes?limit=500");
  }

  enrollment(uses = 1, expires = 600, source_type = "endpoint") {
    return this.post<{ code: string; expires_at: string }>("/v1/admin/enrollment-codes", {
      uses,
      expires_in_seconds: expires,
      source_type,
    });
  }

  async downloadExport(format: "lens" | "ndjson" | "cyclonedx") {
    const response = await fetch(`/v1/exports?format=${format}`, {
      headers: { Authorization: `Bearer ${this.token}` },
    });
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
