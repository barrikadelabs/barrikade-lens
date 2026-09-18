export type Confidence = "confirmed" | "likely" | "possible";
export type SystemType = "autonomous_agent" | "agent_tool" | "model_runtime";

export type CoverageSummary = {
  target_type: "endpoint" | "repository" | "kubernetes" | "cloud";
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
  exposure_summary?: ExposureSummary & { top_findings: Array<Pick<ExposureFinding, "id" | "root_entity_id" | "rule_id" | "severity" | "title">> };
  executive_summary?: {
    coverage_state: "unassessed" | "awaiting_install" | "processing" | "ready" | "partial" | "stale" | "failed";
    systems: { known: number; fresh: number; stale: number; fresh_by_type: Record<string, number>; stale_by_type: Record<string, number> };
    findings: { fresh: number; stale: number; fresh_by_severity: Record<string, number>; stale_by_severity: Record<string, number> };
    effective_ownership: { owned: number; unowned: number; unassigned_high_priority_findings: number };
    top_findings: Array<Pick<ExposureFinding, "id" | "root_entity_id" | "root_name" | "severity" | "title" | "recommended_next_step" | "last_seen_at">>;
    last_successful_evidence_at?: string;
  };
};

export type ExposureSummary = { total: number; counts: Record<"critical" | "high" | "medium" | "low", number> };

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
  exposure_summary?: ExposureSummary;
  effective_ownership?: { owned: boolean; basis: "evidence" | "operator" | "none"; owner_name?: string; owner_type?: string };
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

export type ProductInstallation = {
  id: string;
  kind: string;
  name: string;
  target_id?: string;
  target_name?: string;
  target_freshness: "fresh" | "stale" | "never" | "unknown";
  surface: string;
  system_type?: SystemType;
  state: string;
  confidence: Confidence;
  first_seen_at: string;
  last_seen_at: string;
  observed_users: string[];
};

export type ProductItem = {
  id: string;
  name: string;
  system_type?: SystemType;
  product_category?: string;
  installation_count: number;
  fresh_count: number;
  stale_count: number;
  running_count: number;
  observed_user_count: number;
  observed_users: string[];
  last_seen_at: string;
  instances: ProductInstallation[];
};

export type ExposureFinding = {
  id: string;
  root_entity_id: string;
  root_name: string;
  destination_entity_id?: string;
  destination_name?: string;
  rule_id: string;
  rule_version: string;
  severity: "critical" | "high" | "medium" | "low";
  title: string;
  explanation: string;
  recommended_next_step: string;
  path: Array<{ entity_id: string; name: string; kind: string; edge?: string; basis: "observed" | "operator_context" | "catalog_potential" }>;
  evidence_bases: Array<"observed" | "operator_context" | "catalog_potential">;
  first_seen_at: string;
  last_seen_at: string;
  evidence_last_seen_at?: string;
  evidence_freshness?: "fresh" | "stale";
  effective_ownership?: { owned: boolean; owner_name?: string; owner_type?: string };
};

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
  target_type: "endpoint" | "repository" | "kubernetes" | "cloud";
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

export type DeviceFleetSummary = {
  enrolled: number;
  scanned: number;
  reporting: number;
  stale_offline: number;
  partial: number;
  failed: number;
  revoked: number;
};

export type FleetPolicySummary = {
  id: string;
  name: string;
  status: "active" | "revoked";
  expected_device_count: number | null;
  enrolled_count: number;
  remaining_count: number;
};

export type FleetDevice = {
  id: string;
  name: string;
  observed_name: string;
  custom_name?: string;
  platform?: string;
  architecture?: string;
  collector_version?: string;
  reporting_mode: "continuous" | "quick_scan";
  lifecycle_status: "never_scanned" | "reporting" | "stale_offline" | "partial" | "failed" | "revoked";
  freshness: "fresh" | "stale" | "never";
  identity_quality: "persistent" | "legacy_identity";
  possible_duplicate: boolean;
  partial: boolean;
  failed: boolean;
  current: boolean;
  first_seen_at: string;
  last_seen_at?: string;
  last_full_at?: string;
  revoked_at?: string;
  deployment_policy_id?: string;
  deployment_policy_name?: string;
  evidence_url: string;
};

export type DeviceFleetPage = PageResult<FleetDevice> & { summary: DeviceFleetSummary; policies: FleetPolicySummary[] };

export type PageResult<T> = { items: T[]; limit: number; next_cursor?: string };

export type AuthConfig = {
  mode: "clerk" | "oidc" | "development";
  public_url?: string;
  enabled: boolean;
  development_bootstrap: boolean;
  exposure_enabled: boolean;
  self_serve_enabled: boolean;
  clerk_publishable_key?: string;
  connectors: Record<string, boolean>;
  authorization_endpoint?: string;
  client_id?: string;
  redirect_uri?: string;
  scopes?: string[];
	analytics?: { enabled: boolean; host: string; project_token: string; deployment_environment: string };
};

export type Session = {
  user: { id: string };
  workspace: { id: string; name: string };
  role: "owner" | "admin" | "viewer";
  permissions: string[];
  needs_bootstrap: boolean;
  can_delete_account: boolean;
	analytics: { enabled: boolean; user_id: string; workspace_id: string };
};

export type EnvironmentKind = "aws_account" | "azure_subscription" | "gcp_project" | "endpoint" | "github_repository" | "kubernetes_cluster";
export type ConnectionStatus = "setup_pending" | "verifying" | "connected" | "auth_error" | "disconnected";
export type ScanStatus = "queued" | "running" | "ingesting" | "complete" | "partial" | "failed" | "cancelled";

export type Environment = {
  id: string;
  kind: EnvironmentKind;
  provider?: string;
  external_id?: string;
  display_name: string;
  connection_status: ConnectionStatus;
  configuration: Record<string, unknown>;
  target_id?: string;
  source_id?: string;
  schedule_enabled: boolean;
  next_scan_at?: string;
  verified_at?: string;
  disconnected_at?: string;
  purge_after?: string;
  last_error_code?: string;
  last_error_message?: string;
  first_result_at?: string;
  last_result_at?: string;
  last_result_status?: "complete" | "partial" | "failed";
  monitoring_mode: "quick_scan" | "continuous";
  created_at: string;
  updated_at: string;
};

export type Activation = {
  environment_id: string;
  phase: "awaiting_install" | "connected" | "processing" | "ready" | "partial" | "failed" | "stale" | "disconnected";
  connection_status: ConnectionStatus;
  first_result_at?: string;
  last_result_at?: string;
  last_result_status?: "complete" | "partial" | "failed";
  last_seen_at?: string;
  monitoring_mode: "quick_scan" | "continuous";
  evidence_expires_at?: string;
  summary: { assets_found: number; systems_found: number };
  can_enable_continuous_monitoring: boolean;
  safe_error?: { code?: string; message?: string };
};

export type GitHubDiscoveryStatus = {
  environment_id: string;
  phase: "authorizing" | "awaiting_selection" | "scanning" | "partial" | "ready" | "failed" | "revoked" | "disconnected";
  connection_status: ConnectionStatus;
  repository_selection?: "all" | "selected";
  selected_repositories: number;
  progress: { percent: number; pending: number; processing: number; complete: number; failed: number };
  summary: { assets_found: number };
  first_scan_started_at?: string;
  safe_error?: { code?: string; message?: string };
};

export type Notification = { id: string; event_type: string; payload: Record<string, unknown>; read_at?: string; created_at: string };

export type SetupSession = {
  id: string;
  environment_id: string;
  kind: EnvironmentKind;
  expires_at: string;
  token_displayed_once: boolean;
  setup: {
    method: string;
    command?: string;
    commands?: Record<string, string> | string[];
    install_url?: string;
    template?: string;
    what_lens_reads?: string[];
    excluded?: string[];
    [key: string]: unknown;
  };
};

export type EnvironmentScan = {
  id: string;
  environment_id: string;
  status: ScanStatus;
  phase: string;
  trigger: string;
  progress: Record<string, unknown>;
  safe_error?: { code?: string; message?: string };
  created_at: string;
  started_at?: string;
  completed_at?: string;
};
