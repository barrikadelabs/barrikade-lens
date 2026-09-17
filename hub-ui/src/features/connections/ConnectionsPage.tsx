import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowRight, CheckCircle2, ChevronDown, ChevronRight, CircleDot, Cloud, Container, Copy, GitBranch, Monitor, Plus, RefreshCw, ShieldCheck, SlidersHorizontal, X, type LucideIcon } from "lucide-react";
import { API, authConfig, type Environment, type EnvironmentKind, type EnvironmentScan, type Overview, type SetupSession } from "../../api";
import { captureAnalytics } from "../../analytics";
import { CopyBlock, Empty, Failure, Freshness, Identity, InlineError, Loading, PanelHeading, pretty, relative, useRemote } from "../../ui";

const environmentCatalog: Array<{ kind: EnvironmentKind; title: string; detail: string; identifier: string; icon: LucideIcon; connector: string }> = [
  { kind: "aws_account", title: "AWS account", detail: "Bedrock, AgentCore, and SageMaker", identifier: "12-digit account ID", icon: Cloud, connector: "aws" },
  { kind: "azure_subscription", title: "Azure subscription", detail: "Foundry, Azure AI, and Azure ML", identifier: "Subscription ID", icon: Cloud, connector: "azure" },
  { kind: "gcp_project", title: "GCP project", detail: "Vertex AI and Agent Registry", identifier: "Project ID", icon: Cloud, connector: "gcp" },
  { kind: "endpoint", title: "Endpoint", detail: "macOS, Windows, or Linux", identifier: "Optional device reference", icon: Monitor, connector: "endpoint" },
  { kind: "github_repository", title: "Repository", detail: "GitHub App or generic CI", identifier: "owner/repository (optional)", icon: GitBranch, connector: "github" },
  { kind: "kubernetes_cluster", title: "Kubernetes", detail: "Read-only cluster collector", identifier: "Optional cluster reference", icon: Container, connector: "kubernetes" },
];

function analyticsConnectionType(kind: EnvironmentKind): "aws" | "azure" | "gcp" | "endpoint" | "github" | "kubernetes" {
	return ({ aws_account: "aws", azure_subscription: "azure", gcp_project: "gcp", endpoint: "endpoint", github_repository: "github", kubernetes_cluster: "kubernetes" } as const)[kind];
}

export function ConnectionsPage({ api, revision, onResults, startWizard = false }: { api: API; revision: number; onResults: () => void; startWizard?: boolean }) {
  return <div className="page-stack"><EnvironmentsPage api={api} revision={revision} onResults={onResults} startWizard={startWizard} /><CoveragePage api={api} revision={revision} /></div>;
}

function EnvironmentsPage({ api, revision, onResults, startWizard = false }: { api: API; revision: number; onResults: () => void; startWizard?: boolean }) {
  const navigate = useNavigate();
  const { environmentId } = useParams();
  const environments = useRemote(() => api.environments(), [api, revision]);
  const session = useRemote(() => api.session(), [api]);
  const [wizard, setWizard] = useState(startWizard);
  const [kind, setKind] = useState<EnvironmentKind>();
  const [name, setName] = useState("");
  const [externalID, setExternalID] = useState("");
  const [tenantID, setTenantID] = useState("");
	const [projectNumber, setProjectNumber] = useState("");
  const [setup, setSetup] = useState<SetupSession>();
  const [scan, setScan] = useState<EnvironmentScan>();
  const [collectorConnected, setCollectorConnected] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [platform, setPlatform] = useState<"macos" | "windows" | "linux">("macos");
	const selectedPlatformEvent = useRef("");
  const [handoff, setHandoff] = useState<{ id: string; url: string; expires_at: string }>();
  const [connectors, setConnectors] = useState<Record<string, boolean>>({});
	const selected = environmentCatalog.find((item) => item.kind === kind);
	const isEndpointSetup = setup?.kind === "endpoint" || setup?.setup.method === "managed_collector";
  useEffect(() => { authConfig().then((value) => setConnectors(value.connectors)).catch(() => undefined); }, []);
  useEffect(() => { if (startWizard && connectors.endpoint && !kind) { setWizard(true); setKind("endpoint"); setName("Endpoint"); captureAnalytics({ name: "connection_type_selected", properties: { connection_type: "endpoint" } }); } }, [connectors.endpoint, kind, startWizard]);
	useEffect(() => {
		if (!setup || !isEndpointSetup) return;
		const key = `${setup.id}:${platform}`;
		if (selectedPlatformEvent.current === key) return;
		selectedPlatformEvent.current = key;
		captureAnalytics({ name: "install_platform_selected", properties: { platform } });
	}, [isEndpointSetup, platform, setup]);
  useEffect(() => {
    if (!environmentId || !environments.data) return;
    const environment = environments.data.items.find((item) => item.id === environmentId);
    if (!environment || environment.kind !== "endpoint" || environment.connection_status !== "setup_pending") return;
    setKind("endpoint"); setName(environment.display_name); setWizard(true); setBusy(true);
    api.rotateEndpointCredential(environment.id).then(setSetup).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  }, [api, environmentId, environments.data]);

  useEffect(() => {
    if (!setup || !scan || ["complete", "partial", "failed", "cancelled"].includes(scan.status)) return;
    const timer = window.setTimeout(() => api.environmentScan(setup.environment_id, scan.id).then(setScan).catch((reason) => setError(String(reason))), 1500);
    return () => window.clearTimeout(timer);
  }, [api, scan, setup]);

  useEffect(() => {
    if (!setup || scan || collectorConnected || ["aws_account", "azure_subscription", "gcp_project"].includes(setup.kind)) return;
    const poll = () => api.environment(setup.environment_id).then((value) => {
      if (value.connection_status === "connected") {
        setCollectorConnected(true);
        setMessage("Environment connected. Lens is ingesting the first collector snapshot.");
        environments.reload();
      }
    }).catch(() => undefined);
    void poll();
    const timer = window.setInterval(poll, 2000);
    return () => window.clearInterval(timer);
  }, [api, collectorConnected, environments, scan, setup]);

  const canManage = session.data?.role === "owner" || session.data?.role === "admin";
  const reset = () => { setWizard(false); setKind(undefined); setName(""); setExternalID(""); setTenantID(""); setProjectNumber(""); setSetup(undefined); setScan(undefined); setCollectorConnected(false); setMessage(""); setError(""); setHandoff(undefined); if (location.pathname !== "/connections") navigate("/connections", { replace: true }); };
  const createSetup = () => {
    if (!kind) return;
    setBusy(true); setError("");
    const configuration = kind === "azure_subscription" ? { tenant_id: tenantID } : kind === "gcp_project" ? { project_number: projectNumber } : {};
    api.createEnvironmentSetup({ kind, display_name: name, external_id: externalID || undefined, configuration })
      .then((result) => { setSetup(result); captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "setup_generated", connection_type: analyticsConnectionType(kind) } }); }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const verify = () => {
    if (!setup) return;
    captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "verify_requested", connection_type: analyticsConnectionType(setup.kind) } });
    setBusy(true); setError(""); setMessage("");
    api.verifyEnvironment(setup.environment_id).then((result) => {
      setMessage(result.message || "Access verified. Lens started the first scan.");
      if (result.scan) setScan({ ...result.scan, environment_id: setup.environment_id, trigger: "first_scan", phase: "queued", progress: {}, created_at: new Date().toISOString() });
      environments.reload();
    }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const delegate = () => {
    if (!setup) return;
    setBusy(true); setError("");
    api.createEndpointHandoff(setup.environment_id).then((result) => { setHandoff(result); captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "handoff_created", connection_type: "endpoint" } }); }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const runScan = (environment: Environment) => {
    setError("");
    captureAnalytics({ name: "lens_interaction", properties: { surface: "connections", interaction: "scan_requested", connection_type: analyticsConnectionType(environment.kind) } });
    api.scanEnvironment(environment.id).then((result) => {
      setSetup({ id: "manual", environment_id: environment.id, kind: environment.kind, expires_at: "", token_displayed_once: false, setup: { method: "manual" } });
      setScan({ ...result, environment_id: environment.id, trigger: "manual", phase: "queued", progress: {}, created_at: new Date().toISOString() });
    }).catch((reason) => setError(String(reason)));
  };
  const disconnect = (environment: Environment) => {
    if (!window.confirm(`Disconnect ${environment.display_name}? Lens access stops immediately; historical results are retained for 90 days.`)) return;
    api.disconnectEnvironment(environment.id).then(() => environments.reload()).catch((reason) => setError(String(reason)));
  };

  if (environments.loading || session.loading) return <Loading />;
  if (environments.error || session.error || !environments.data) return <Failure error={environments.error || session.error} retry={() => { environments.reload(); session.reload(); }} />;
  return <div className="page-stack environments-page">
    <section className="panel environment-summary"><div><p className="eyebrow">ENDPOINT BETA</p><h2>{environments.data.items.filter((item) => item.connection_status === "connected").length} connected endpoints</h2><p>Endpoint count is unlimited. Each installation is read-only and produces durable status from install through first results.</p></div>{canManage && <button className="button primary" onClick={() => navigate("/connections/new")}><Plus size={16} /> Connect endpoint</button>}</section>
    {error && <InlineError text={error} />}
    <section className="environment-list">
      {environments.data.items.map((environment) => <EnvironmentActivationCard key={environment.id} api={api} environment={environment} revision={revision} canManage={canManage} onResume={() => navigate(`/connections/${environment.id}`)} onScan={() => runScan(environment)} onDisconnect={() => disconnect(environment)} />)}
      {!environments.data.items.length && <div className="empty-state-actions"><Empty icon={ShieldCheck} title="Your organization is unassessed" detail={canManage ? "Connect this endpoint directly or create a 24-hour setup handoff for IT." : "Ask a workspace owner or admin to connect an endpoint."} />{canManage && <button className="button primary" onClick={() => navigate("/connections/new")}>Connect endpoint</button>}</div>}
    </section>
    {scan && <section className={`panel scan-progress ${scan.status}`}><div><span className="scan-spinner"><RefreshCw size={18} /></span><div><p className="eyebrow">SCAN STATUS</p><h2>{pretty(scan.phase || scan.status)}</h2><p>{scan.status === "complete" ? "Discovery is complete and results are ready." : scan.status === "partial" ? "Useful results are ready; some detectors or locations could not be read." : scan.safe_error?.message || "Lens is collecting inventory and coverage. Partial results remain visible if one detector fails."}</p></div></div>{["complete", "partial"].includes(scan.status) && <button className="button primary" onClick={onResults}>View results <ArrowRight size={15} /></button>}</section>}
    {wizard && <div className="modal-overlay environment-wizard-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) reset(); }}><section className="environment-wizard"><button className="drawer-close" onClick={reset}><X size={18} /></button><header><p className="eyebrow">ADD ENVIRONMENT</p><h2>{setup ? "Complete provider setup" : kind ? `Connect ${selected?.title}` : "What do you want Lens to scan?"}</h2><p>{setup ? "The generated setup is least-privilege and expires shortly. Lens stores no long-lived cloud keys." : "Every verified environment scans immediately and refreshes daily."}</p></header>
      {!kind && <div className="environment-catalog">{environmentCatalog.filter(({ connector }) => connectors[connector] === true).map(({ kind: value, title, detail, icon: Icon }) => <button key={value} onClick={() => { setKind(value); setName(title); captureAnalytics({ name: "connection_type_selected", properties: { connection_type: analyticsConnectionType(value) } }); }}><Icon size={20} /><span><b>{title}</b><small>{detail}</small></span><ChevronRight size={15} /></button>)}</div>}
      {kind && !setup && <div className="environment-details"><button className="wizard-back" onClick={() => setKind(undefined)}>← Choose another type</button><label>Display name<input value={name} onChange={(event) => setName(event.target.value)} placeholder="Production AI" autoFocus /></label><label>{selected?.identifier}<input value={externalID} onChange={(event) => setExternalID(event.target.value)} placeholder={kind === "aws_account" ? "123456789012" : kind === "azure_subscription" ? "00000000-0000-0000-0000-000000000000" : kind === "gcp_project" ? "my-project-id" : "Optional"} /></label>{kind === "azure_subscription" && <label>Microsoft Entra tenant ID<input value={tenantID} onChange={(event) => setTenantID(event.target.value)} placeholder="00000000-0000-0000-0000-000000000000" /></label>}{kind === "gcp_project" && <label>GCP project number<input value={projectNumber} onChange={(event) => setProjectNumber(event.target.value)} placeholder="123456789012" /></label>}<div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only by design</b><span>Lens inventories resources and relationships. It does not invoke models, read prompts or outputs, retrieve secrets, or remediate resources.</span></p></div><button className="button primary full" disabled={busy || !name.trim()} onClick={createSetup}>{busy ? "Preparing…" : "Generate least-privilege setup"}</button></div>}
      {setup && <div className="setup-result"><div className="setup-read"><div><h3>Lens will read</h3>{setup.setup.what_lens_reads?.map((item) => <span key={item}><CheckCircle2 size={14} />{item}</span>)}</div><div><h3>Lens will not read</h3>{setup.setup.excluded?.map((item) => <span key={item}><X size={14} />{item}</span>)}</div></div>{isEndpointSetup && <><div className="platform-picker" aria-label="Installation platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><p className="setup-requirements">Requires Node.js 18+ and administrator access to install the background collector.</p>{!handoff && !Array.isArray(setup.setup.commands) && setup.setup.commands?.[platform] && <CopyBlock value={setup.setup.commands[platform]} />}{handoff ? <div className="handoff-result"><b>24-hour IT handoff</b><p>The recipient chooses their platform and generates a single-use 15-minute command.</p><CopyBlock value={handoff.url} /><small>Expires {new Date(handoff.expires_at).toLocaleString()}</small></div> : <button className="button subtle full" disabled={busy} onClick={delegate}>Delegate installation to IT</button>}</>}{setup.setup.install_url && <a className="button primary full" href={setup.setup.install_url} target="_blank" rel="noreferrer">Open provider setup <ArrowRight size={15} /></a>}{setup.setup.command && <CopyBlock value={setup.setup.command} />}{!isEndpointSetup && setup.setup.commands && (Array.isArray(setup.setup.commands) ? setup.setup.commands : Object.values(setup.setup.commands)).map((command) => <CopyBlock value={command} key={command} />)}{setup.setup.template && <details className="setup-template" open><summary>Generated setup template <ChevronDown size={13} /></summary><pre>{setup.setup.template}</pre><button className="button subtle" onClick={() => navigator.clipboard.writeText(setup.setup.template || "")}><Copy size={14} /> Copy template</button></details>}{collectorConnected ? <button className="button primary full" onClick={onResults}>View results <ArrowRight size={15} /></button> : <button className="button primary full" disabled={busy} onClick={verify}>{busy ? "Checking…" : ["aws_account", "azure_subscription", "gcp_project"].includes(setup.kind) ? "I've completed setup — verify access" : "Check installation status"}</button>}{message && <p className="form-status">{message}</p>}{error && <InlineError text={error} />}<small className="setup-expiry">This command expires {new Date(setup.expires_at).toLocaleTimeString()}. Rotate it from Connections if it is lost or expires.</small></div>}
    </section></div>}
  </div>;
}

function EnvironmentActivationCard({ api, environment, revision, canManage, onResume, onScan, onDisconnect }: { api: API; environment: Environment; revision: number; canManage: boolean; onResume: () => void; onScan: () => void; onDisconnect: () => void }) {
  const remote = useRemote(() => api.activation(environment.id), [api, environment.id, revision]);
  const catalog = environmentCatalog.find((item) => item.kind === environment.kind);
  const Icon = catalog?.icon ?? Cloud;
  const fallback = environment.connection_status === "setup_pending" ? "awaiting_install" : environment.last_result_status === "failed" ? "failed" : environment.last_result_status === "partial" ? "partial" : environment.last_result_at ? "ready" : environment.source_id ? "processing" : environment.connection_status;
  const phase = remote.data?.phase ?? fallback;
  const lastResult = remote.data?.last_result_at ?? environment.last_result_at;
  const message = remote.data?.safe_error?.message || environment.last_error_message || (phase === "awaiting_install" ? "Installation has not completed" : phase === "connected" ? "Collector connected; awaiting the first snapshot" : phase === "processing" ? "First results are being normalized; you can leave this page" : phase === "stale" ? `Retained results are visible, but this endpoint last reported ${relative(remote.data?.last_seen_at || lastResult || "")}` : lastResult ? `Last result ${relative(lastResult)}${phase === "partial" ? " · partial coverage" : ""}` : "Waiting for endpoint evidence");
  return <article className="environment-card"><span className="environment-icon"><Icon size={20} /></span><div className="environment-card-copy"><span><b>{environment.display_name}</b><small>{catalog?.title ?? pretty(environment.kind)}{environment.external_id ? ` · ${environment.external_id}` : ""}</small></span><p>{message}</p></div><span className={`connection-status ${phase}`}><i />{pretty(phase)}</span><div className="environment-actions">{canManage && environment.kind === "endpoint" && phase === "awaiting_install" && <button className="button subtle" onClick={onResume}>Resume setup</button>}{canManage && environment.connection_status === "connected" && ["aws", "azure", "gcp"].includes(environment.provider || "") && <button className="button subtle" onClick={onScan}><RefreshCw size={14} /> Scan now</button>}{canManage && phase !== "disconnected" && <button className="button quiet" onClick={onDisconnect}>Disconnect</button>}</div></article>;
}

function CoveragePage({ api, revision }: { api: API; revision: number }) {
  const [targetType, setTargetType] = useState("");
  const overview = useRemote(() => api.overview("7d"), [api, revision]);
  const targets = useRemote(() => api.targets({ target_type: targetType, limit: 100 }), [api, revision, targetType]);
  const [expanded, setExpanded] = useState<string>();
  if (overview.loading || targets.loading) return <Loading />;
  if (overview.error || targets.error || !overview.data || !targets.data) return <Failure error={overview.error || targets.error} retry={() => { overview.reload(); targets.reload(); }} />;
  return <div className="page-stack">
    <section className="coverage-cards">{overview.data.coverage.map((item) => <CoverageCard key={item.target_type} item={item} active={targetType === item.target_type} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "connections", interaction: "filter_changed", control: "target_type" } }); setTargetType((value) => value === item.target_type ? "" : item.target_type); }} />)}</section>
    <section className="panel data-panel">
      <PanelHeading title="Unique discovery targets" detail="One row per endpoint installation, repository, or cluster. Collector credentials are nested below the target." count={targets.data.items.length} />
      <div className="target-table table-scroll"><div className="target-row table-head"><span>Target</span><span>Surface</span><span>Freshness</span><span>Last full scan</span><span>Data quality</span><span /></div>
        {targets.data.items.map((target) => <div className="target-group" key={target.id}>
          <button className="target-row" onClick={() => setExpanded((value) => value === target.id ? undefined : target.id)}>
            <Identity kind={target.target_type === "kubernetes" ? "cluster" : target.target_type} name={target.name} detail={`${target.platform ?? target.target_type}${target.architecture ? ` · ${target.architecture}` : ""}`} />
            <span className="kind-label">{pretty(target.target_type)}</span><Freshness value={target.freshness} partial={target.partial} /><span className="observed">{target.last_full_at ? relative(target.last_full_at) : "Never"}</span>
            <span className="diagnostics">{target.possible_duplicate && <i>Possible duplicate</i>}{target.identity_quality === "legacy_identity" && <i>Legacy identity</i>}{!target.possible_duplicate && target.identity_quality === "persistent" && <small>Identity verified</small>}</span>
            {expanded === target.id ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
          </button>
          {expanded === target.id && <div className="collectors"><p>COLLECTORS FOR THIS TARGET</p>{target.collectors.map((collector) => <div className="collector-row" key={collector.source_id}>
            <span><CircleDot size={13} /><b>{collector.name}</b><code>{collector.source_id}</code></span><span>v{collector.collector_version ?? "unknown"}</span><span>Sequence {collector.sequence ?? 0}</span><span className={collector.partial ? "partial" : "complete"}>{collector.partial ? `${collector.error_count} scan errors` : "Full coverage reported"}</span><time>{collector.last_seen_at ? relative(collector.last_seen_at) : "Never"}</time>
          </div>)}</div>}
        </div>)}
      </div>
    </section>
    <CoverageBaseline api={api} coverage={overview.data.coverage} onSaved={() => overview.reload()} />
  </div>;
}
function CoverageBaseline({ api, coverage, onSaved }: { api: API; coverage: Overview["coverage"]; onSaved: () => void }) {
  const initial = Object.fromEntries(coverage.map((item) => [item.target_type, item.expected_count === null ? "" : String(item.expected_count)]));
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [editing, setEditing] = useState(false);
  const [status, setStatus] = useState("");
  const save = () => {
    const baselines = ["endpoint", "repository", "kubernetes", "cloud"].map((target_type) => ({ target_type, expected_count: values[target_type] === "" ? null : Number(values[target_type]) }));
    api.setBaselines(baselines).then(() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "connections", interaction: "baseline_saved" } }); setStatus("Coverage baseline saved"); setEditing(false); onSaved(); }).catch((reason) => setStatus(String(reason)));
  };
  return <section className="panel baseline-panel"><div><p className="eyebrow">EXPECTED POPULATION</p><h2>Coverage denominator</h2><p>Optional manual baselines let Lens compare reporting targets with a known population. Blank values remain explicitly unknown.</p></div>
    {editing ? <div className="baseline-form">{["endpoint", "repository", "kubernetes", "cloud"].map((type) => <label key={type}>{pretty(type)}<input type="number" min="0" placeholder="Unknown" value={values[type] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [type]: event.target.value }))} /></label>)}<button className="button primary" onClick={save}>Save baselines</button><button className="button subtle" onClick={() => setEditing(false)}>Cancel</button></div> : <button className="button subtle" onClick={() => setEditing(true)}><SlidersHorizontal size={15} /> Configure baselines</button>}
    {status && <small className="form-status">{status}</small>}
  </section>;
}

function CoverageCard({ item, onClick, active }: { item: Overview["coverage"][number]; onClick?: () => void; active?: boolean }) {
  const Icon = item.target_type === "endpoint" ? Monitor : item.target_type === "repository" ? GitBranch : item.target_type === "cloud" ? Cloud : Container;
  const label = ({ endpoint: "Endpoints", repository: "Repositories", kubernetes: "Kubernetes", cloud: "Cloud environments" } as Record<string, string>)[item.target_type] ?? pretty(item.target_type);
  const status = item.reporting === 0 ? "Not reporting" : [item.fresh ? `${item.fresh} fresh` : "", item.stale ? `${item.stale} stale` : "", item.partial ? `${item.partial} partial` : ""].filter(Boolean).join(" · ");
  const body = <><span className="coverage-icon"><Icon size={19} /></span><div><p>{label}</p><strong>{item.reporting}</strong><span>{item.population_configured ? `of ${item.expected_count} expected` : "reporting targets"}</span></div><div className={item.stale || item.partial ? "coverage-card-status needs-review" : item.reporting ? "coverage-card-status reporting" : "coverage-card-status quiet"}><b>{status}</b><small>{item.population_configured ? "Manual baseline" : "Expected population unknown"}</small></div></>;
  return onClick ? <button className={active ? "coverage-card active" : "coverage-card"} onClick={onClick}>{body}</button> : <div className="coverage-card">{body}</div>;
}
