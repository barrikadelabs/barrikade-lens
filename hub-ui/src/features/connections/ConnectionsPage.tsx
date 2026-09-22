import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ArrowRight, CheckCircle2, ChevronDown, ChevronRight, CircleDot, Cloud, Container, Copy, GitBranch, Monitor, Plus, RefreshCw, ShieldCheck, SlidersHorizontal, Timer, X } from "lucide-react";
import { API, authConfig, type Environment, type EnvironmentKind, type EnvironmentScan, type GitHubDiscoveryStatus, type Overview, type SetupSession } from "../../api";
import { captureAnalytics } from "../../analytics";
import { connectionStatusLabel, locationTypeLabel } from "../../copy";
import { CopyBlock, Empty, Failure, Freshness, Identity, InlineError, Loading, PanelHeading, pretty, relative, useRemote } from "../../ui";
import { EmployeeDeploymentMethods } from "./EmployeeDeploymentMethods";
import { ScanSourceChooser } from "./ScanSourceChooser";
import { analyticsConnectionType, defaultEnvironmentName, environmentCatalog, type DeploymentMethod } from "./connection-options";

type EndpointMode = "quick_scan" | "continuous";

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
  const [deploymentMethod, setDeploymentMethod] = useState<DeploymentMethod>();
  const [endpointMode, setEndpointMode] = useState<EndpointMode>();
  const [name, setName] = useState("");
  const [externalID, setExternalID] = useState("");
  const [tenantID, setTenantID] = useState("");
	const [projectNumber, setProjectNumber] = useState("");
  const [setup, setSetup] = useState<SetupSession>();
  const [scan, setScan] = useState<EnvironmentScan>();
  const [collectorConnected, setCollectorConnected] = useState(false);
  const [githubStatus, setGitHubStatus] = useState<GitHubDiscoveryStatus>();
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [platform, setPlatform] = useState<"macos" | "windows" | "linux">("macos");
	const selectedPlatformEvent = useRef("");
  const [handoff, setHandoff] = useState<{ id: string; url: string; expires_at: string }>();
  const [connectors, setConnectors] = useState<Record<string, boolean>>({});
	const selected = environmentCatalog.find((item) => item.kind === kind);
	const isEndpointSetup = setup?.kind === "endpoint" || setup?.setup.method === "managed_collector" || setup?.setup.method === "quick_scan";
	const isQuickSetup = setup?.setup.method === "quick_scan";
	const isGitHubSetup = setup?.kind === "github_repository" || kind === "github_repository";
  useEffect(() => { authConfig().then((value) => setConnectors(value.connectors)).catch(() => undefined); }, []);
  useEffect(() => { if (startWizard) setWizard(true); }, [startWizard]);
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
    setKind("endpoint"); setEndpointMode(environment.monitoring_mode || "continuous"); setDeploymentMethod("command_line"); setName(environment.display_name); setWizard(true); setBusy(true);
    api.rotateEndpointCredential(environment.id).then(setSetup).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  }, [api, environmentId, environments.data]);

  useEffect(() => {
    if (!environmentId || !environments.data) return;
    const environment = environments.data.items.find((item) => item.id === environmentId);
    if (!environment || environment.kind !== "github_repository" || environment.connection_status === "disconnected") return;
    setKind("github_repository"); setName(environment.display_name); setWizard(true);
  }, [environmentId, environments.data]);

  useEffect(() => {
    const id = setup?.kind === "github_repository" ? setup.environment_id : kind === "github_repository" ? environmentId : undefined;
    if (!id) return;
    let stopped = false;
    const poll = () => api.githubStatus(id).then((value) => {
      if (stopped) return;
      setGitHubStatus(value);
      if (["ready", "partial"].includes(value.phase)) environments.reload();
    }).catch((reason) => { if (!stopped) setError(String(reason)); });
    void poll();
    const timer = window.setInterval(poll, 2000);
    return () => { stopped = true; window.clearInterval(timer); };
  }, [api, environmentId, kind, setup?.environment_id, setup?.kind]);

  useEffect(() => {
    if (!setup || !scan || ["complete", "partial", "failed", "cancelled"].includes(scan.status)) return;
    const timer = window.setTimeout(() => api.environmentScan(setup.environment_id, scan.id).then(setScan).catch((reason) => setError(String(reason))), 1500);
    return () => window.clearTimeout(timer);
  }, [api, scan, setup]);

  useEffect(() => {
    if (!setup || scan || collectorConnected || ["aws_account", "azure_subscription", "gcp_project"].includes(setup.kind)) return;
    const poll = () => api.environment(setup.environment_id).then((value) => {
      const expectedMode = isQuickSetup ? "quick_scan" : "continuous";
      if (value.connection_status === "connected" && (setup.kind !== "endpoint" || value.monitoring_mode === expectedMode)) {
        setCollectorConnected(true);
        captureAnalytics({ name: "first_collector_connected", properties: { connection_type: "endpoint" } });
        setMessage("Device connected. Lens is preparing the first results.");
        setWizard(false);
        navigate("/connections", { replace: true });
        environments.reload();
      }
    }).catch(() => undefined);
    void poll();
    const timer = window.setInterval(poll, 2000);
    return () => window.clearInterval(timer);
  }, [api, collectorConnected, environments, isQuickSetup, navigate, scan, setup]);

  const canManage = session.data?.role === "owner" || session.data?.role === "admin";
  useEffect(() => {
    if (!session.data || canManage || !wizard) return;
    setWizard(false);
    if (location.pathname !== "/connections") navigate("/connections", { replace: true });
  }, [canManage, navigate, session.data, wizard]);
  const reset = () => { setWizard(false); setKind(undefined); setEndpointMode(undefined); setDeploymentMethod(undefined); setName(""); setExternalID(""); setTenantID(""); setProjectNumber(""); setSetup(undefined); setScan(undefined); setCollectorConnected(false); setGitHubStatus(undefined); setMessage(""); setError(""); setHandoff(undefined); if (location.pathname !== "/connections") navigate("/connections", { replace: true }); };
  const createSetup = () => {
    if (!kind) return;
    setBusy(true); setError("");
    const configuration = kind === "azure_subscription" ? { tenant_id: tenantID } : kind === "gcp_project" ? { project_number: projectNumber } : kind === "endpoint" ? { monitoring_mode: endpointMode || "continuous", deployment_method: deploymentMethod, platform } : {};
    api.createEnvironmentSetup({ kind, display_name: name || defaultEnvironmentName(kind), external_id: externalID || undefined, configuration })
      .then((result) => {
        setSetup(result);
        captureAnalytics({ name: "scan_started", properties: { connection_type: analyticsConnectionType(kind), ...(kind === "endpoint" && deploymentMethod ? { deployment_method: deploymentMethod } : {}) } });
        captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "setup_generated", connection_type: analyticsConnectionType(kind) } });
      }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const verify = () => {
    if (!setup) return;
    captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "verify_requested", connection_type: analyticsConnectionType(setup.kind) } });
    setBusy(true); setError(""); setMessage("");
    api.verifyEnvironment(setup.environment_id).then((result) => {
      setMessage(result.message || "Access confirmed. Lens started the first scan.");
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
  const upgrade = (environment: Environment) => {
    setBusy(true); setError(""); setKind("endpoint"); setEndpointMode("continuous"); setDeploymentMethod("command_line"); setName(environment.display_name);
    api.enableContinuousMonitoring(environment.id).then((result) => { setSetup(result); setWizard(true); }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const viewResults = () => {
    captureAnalytics({ name: "first_results_viewed", properties: { connection_type: setup ? analyticsConnectionType(setup.kind) : "endpoint" } });
    onResults();
  };
  const retryGitHub = () => {
    const id = setup?.environment_id || environmentId;
    if (!id) return;
    setBusy(true); setError("");
    api.reconcileGitHub(id).then(() => api.githubStatus(id)).then(setGitHubStatus).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const reauthorizeGitHub = () => {
    const id = setup?.environment_id || environmentId;
    if (!id) return;
    setBusy(true); setError("");
    api.reauthorizeGitHub(id).then((result) => { setSetup(result); setGitHubStatus(undefined); }).catch((reason) => setError(String(reason))).finally(() => setBusy(false));
  };
  const addEmployeeDevices = () => {
    setSetup(undefined); setGitHubStatus(undefined); setKind("endpoint"); setEndpointMode(undefined); setDeploymentMethod(undefined); setName(defaultEnvironmentName("endpoint")); setError(""); setMessage("");
    navigate("/connections/new", { replace: true });
  };

  if (environments.loading || session.loading) return <Loading />;
  if (environments.error || session.error || !environments.data) return <Failure error={environments.error || session.error} retry={() => { environments.reload(); session.reload(); }} />;
  const activeConnections = environments.data.items.filter((item) => item.connection_status === "connected").length;
  return <div className="page-stack environments-page">
    <section className="panel environment-summary"><div><p className="eyebrow">CONNECTIONS</p><h2>{activeConnections} active {activeConnections === 1 ? "connection" : "connections"}</h2><p>Add or review the devices, repositories, cloud accounts, and clusters you want Lens to check. Lens never changes them.</p></div>{canManage && <button className="button primary" onClick={() => navigate("/connections/new")}><Plus size={16} /> Add a connection</button>}</section>
    {error && !wizard && <InlineError text={error} />}
    <section className="environment-list">
      {environments.data.items.map((environment) => <EnvironmentActivationCard key={environment.id} api={api} environment={environment} revision={revision} canManage={canManage} onResume={() => navigate(`/connections/${environment.id}`)} onUpgrade={() => upgrade(environment)} onScan={() => runScan(environment)} onDisconnect={() => disconnect(environment)} />)}
      {!environments.data.items.length && <div className="empty-state-actions"><Empty icon={ShieldCheck} title="Connect your first location" detail={canManage ? "Start with a device, code repository, cloud account, or cluster." : "Ask a workspace owner or admin to add a connection."} />{canManage && <button className="button primary" onClick={() => navigate("/connections/new")}>Add a connection</button>}</div>}
    </section>
    {scan && <section className={`panel scan-progress ${scan.status}`}><div><span className="scan-spinner"><RefreshCw size={18} /></span><div><p className="eyebrow">SCAN STATUS</p><h2>{connectionStatusLabel(scan.phase || scan.status)}</h2><p>{scan.status === "complete" ? "The scan is complete and your results are ready." : scan.status === "partial" ? "Your results are ready, but Lens could not check every location." : scan.safe_error?.message || "Lens is checking this location. Results already found will remain available if one check fails."}</p></div></div>{["complete", "partial"].includes(scan.status) && <button className="button primary" onClick={viewResults}>View results <ArrowRight size={15} /></button>}</section>}
    {wizard && canManage && <div className="modal-overlay environment-wizard-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) reset(); }}><section className="environment-wizard" role="dialog" aria-modal="true" aria-label="Add a connection"><button className="drawer-close" aria-label="Close connection setup" onClick={reset}><X size={18} /></button><header><p className="eyebrow">ADD A CONNECTION</p><h2>{setup ? "Finish connecting this location" : kind === "endpoint" && !endpointMode ? "Choose how this device reports" : kind === "endpoint" && endpointMode === "continuous" && !deploymentMethod ? "How do you want to install Lens?" : kind ? `Connect ${selected?.title}` : "Choose what to connect"}</h2><p>{setup ? "This read-only setup expires shortly." : kind === "endpoint" && !endpointMode ? "Get results once now, or keep this device’s inventory up to date automatically." : kind === "endpoint" ? "Choose the platform for this device." : "Select a device, repository, cloud account, or cluster for Lens to check."}</p></header>
      {!kind && <ScanSourceChooser connectors={connectors} onCategorySelected={(sourceCategory) => captureAnalytics({ name: "source_category_selected", properties: { source_category: sourceCategory } })} onSelect={(source) => { setKind(source.kind); setName(defaultEnvironmentName(source.kind)); captureAnalytics({ name: "connection_type_selected", properties: { connection_type: analyticsConnectionType(source.kind) } }); }} />}
      {kind === "endpoint" && !setup && !endpointMode && <div className="environment-details"><button className="wizard-back" onClick={() => setKind(undefined)}>← Choose another connection</button><div className="deployment-methods endpoint-modes" role="group" aria-label="Device reporting option"><button onClick={() => setEndpointMode("quick_scan")}><Timer size={19} /><span><b>Quick Scan</b><small>Check once, upload the results, and exit. No administrator access or background service.</small></span></button><button onClick={() => setEndpointMode("continuous")}><RefreshCw size={19} /><span><b>Keep results up to date</b><small>Scan now, then install the Lens scanner to check for changes automatically.</small></span></button></div></div>}
      {kind === "endpoint" && !setup && endpointMode === "continuous" && !deploymentMethod && <div className="environment-details"><button className="wizard-back" onClick={() => setEndpointMode(undefined)}>← Choose another reporting mode</button><EmployeeDeploymentMethods onSelect={(method) => { setDeploymentMethod(method); captureAnalytics({ name: "deployment_method_selected", properties: { deployment_method: method } }); }} /></div>}
      {kind === "endpoint" && !setup && endpointMode && (endpointMode === "quick_scan" || deploymentMethod) && <div className="environment-details"><button className="wizard-back" onClick={() => { if (endpointMode === "quick_scan") setEndpointMode(undefined); else setDeploymentMethod(undefined); setError(""); }}>← Back</button><div className="platform-step"><h3>Choose a platform</h3><p>Lens gives this installation a protected identity so later updates do not create a duplicate device.</p></div><div className="platform-picker" aria-label="Installation platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only by design</b><span>Lens checks installed software, whether it is running, network access, and relevant configuration names. It does not read prompts, outputs, secrets, or file contents.</span></p></div>{error && <InlineError text={error} />}<button className="button primary full" disabled={busy} onClick={createSetup}>{busy ? "Preparing…" : endpointMode === "quick_scan" ? "Create Quick Scan" : "Start scan"}</button></div>}
      {kind === "github_repository" && !setup && !githubStatus && <div className="environment-details"><button className="wizard-back" onClick={() => { setKind(undefined); setError(""); }}>← Choose another connection</button><div className="wizard-boundary"><GitBranch size={18} /><p><b>Check GitHub without installing software</b><span>Choose all repositories or only specific ones. Lens reads repository details and configuration files relevant to AI tools and agents, then starts automatically.</span></p></div><div className="setup-read"><div><h3>Lens will read</h3><span><CheckCircle2 size={14} />Details about selected repositories</span><span><CheckCircle2 size={14} />Configuration files relevant to AI tools and agents</span></div><div><h3>Lens will not read</h3><span><X size={14} />Secret values</span><span><X size={14} />Prompts or model inputs and outputs</span><span><X size={14} />Write or repository administration access</span></div></div>{error && <InlineError text={error} />}<button className="button primary full" disabled={busy} onClick={createSetup}>{busy ? "Preparing…" : "Continue to GitHub"}</button></div>}
      {kind && kind !== "endpoint" && kind !== "github_repository" && !setup && <div className="environment-details"><button className="wizard-back" onClick={() => { setKind(undefined); setError(""); }}>← Choose another connection</button><label>Display name<input value={name} onChange={(event) => setName(event.target.value)} placeholder="Production AI" autoFocus /></label><label>{selected?.identifier}<input value={externalID} onChange={(event) => setExternalID(event.target.value)} placeholder={kind === "aws_account" ? "123456789012" : kind === "azure_subscription" ? "00000000-0000-0000-0000-000000000000" : kind === "gcp_project" ? "my-project-id" : "Optional"} /></label>{kind === "azure_subscription" && <label>Microsoft Entra tenant ID<input value={tenantID} onChange={(event) => setTenantID(event.target.value)} placeholder="00000000-0000-0000-0000-000000000000" /></label>}{kind === "gcp_project" && <label>GCP project number<input value={projectNumber} onChange={(event) => setProjectNumber(event.target.value)} placeholder="123456789012" /></label>}<div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only by design</b><span>Lens checks AI resources and how they connect. It does not run models, read prompts or outputs, retrieve secrets, or change resources.</span></p></div>{error && <InlineError text={error} />}<button className="button primary full" disabled={busy || !name.trim()} onClick={createSetup}>{busy ? "Preparing…" : "Start scan"}</button></div>}
      {setup && <div className="setup-result"><div className="setup-read"><div><h3>Lens will read</h3>{setup.setup.what_lens_reads?.map((item) => <span key={item}><CheckCircle2 size={14} />{item}</span>)}</div><div><h3>Lens will not read</h3>{setup.setup.excluded?.map((item) => <span key={item}><X size={14} />{item}</span>)}</div></div>{isEndpointSetup && <><div className="platform-picker" aria-label="Installation platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><p className="setup-requirements">{isQuickSetup ? "Requires Node.js 18 or newer. It uploads results once, then exits without installing a service." : "Requires Node.js 18 or newer and administrator access to install the background Lens scanner."}</p>{!handoff && !Array.isArray(setup.setup.commands) && setup.setup.commands?.[platform] && <CopyBlock value={setup.setup.commands[platform]} />}{!isQuickSetup && (handoff ? <div className="handoff-result"><b>24-hour IT setup link</b><p>The recipient chooses a platform and generates a command that works once and expires after 15 minutes.</p><CopyBlock value={handoff.url} /><small>Expires {new Date(handoff.expires_at).toLocaleString()}</small></div> : <button className="button subtle full" disabled={busy} onClick={delegate}>{deploymentMethod === "company" || deploymentMethod === "mdm" ? "Create an IT setup link" : "Send setup to IT"}</button>)}</>}{setup.setup.install_url && <a className="button primary full" href={setup.setup.install_url} target="_blank" rel="noreferrer">Open provider setup <ArrowRight size={15} /></a>}{setup.setup.command && <CopyBlock value={setup.setup.command} />}{!isEndpointSetup && setup.setup.commands && (Array.isArray(setup.setup.commands) ? setup.setup.commands : Object.values(setup.setup.commands)).map((command) => <CopyBlock value={command} key={command} />)}{setup.setup.template && <details className="setup-template" open><summary>Setup template <ChevronDown size={13} /></summary><pre>{setup.setup.template}</pre><button className="button subtle" onClick={() => navigator.clipboard.writeText(setup.setup.template || "")}><Copy size={14} /> Copy template</button></details>}{collectorConnected ? <button className="button primary full" onClick={viewResults}>View results <ArrowRight size={15} /></button> : isGitHubSetup ? <p className="form-status">Approve the Lens App in GitHub. Lens starts checking your repositories when you return.</p> : <button className="button primary full" disabled={busy} onClick={verify}>{busy ? "Checking…" : ["aws_account", "azure_subscription", "gcp_project"].includes(setup.kind) ? "I've finished setup — check access" : "Check status"}</button>}{message && <p className="form-status">{message}</p>}{error && <InlineError text={error} />}<small className="setup-expiry">This setup expires {new Date(setup.expires_at).toLocaleTimeString()}. Create a new one from Coverage if it is lost or expires.</small></div>}
      {githubStatus && <GitHubDiscoveryProgress status={githubStatus} busy={busy} onRetry={retryGitHub} onReauthorize={reauthorizeGitHub} onResults={viewResults} onAddDevices={addEmployeeDevices} />}
    </section></div>}
  </div>;
}

function GitHubDiscoveryProgress({ status, busy, onRetry, onReauthorize, onResults, onAddDevices }: { status: GitHubDiscoveryStatus; busy: boolean; onRetry: () => void; onReauthorize: () => void; onResults: () => void; onAddDevices: () => void }) {
  const finished = status.phase === "ready" || status.phase === "partial";
  const recoverable = status.phase === "failed" || status.phase === "revoked";
  const copy = status.phase === "authorizing" ? "Approve the Lens App in GitHub to begin."
    : status.phase === "awaiting_selection" ? "The App is installed, but it cannot see a repository yet. Select repositories in GitHub, then retry."
    : status.phase === "scanning" ? `Checking ${status.selected_repositories} selected repositories. Results appear as each repository finishes.`
    : status.phase === "partial" ? "Your results are ready, but Lens could not check every repository. Retrying will keep the results already found."
    : status.phase === "ready" ? "Lens finished checking your repositories. The AI tools and agents it found are now in your inventory."
    : status.safe_error?.message || "GitHub access needs attention before Lens can continue.";
  return <div className={`setup-result github-discovery ${status.phase}`}>
    <div className="scan-progress"><span className="scan-spinner"><GitBranch size={18} /></span><div><p className="eyebrow">GITHUB</p><h3>{connectionStatusLabel(status.phase)}</h3><p>{copy}</p></div></div>
    {status.selected_repositories > 0 && <div><p>{status.progress.complete + status.progress.failed} of {status.selected_repositories} repositories checked · {status.summary.assets_found} items found</p><progress max="100" value={status.progress.percent}>{status.progress.percent}%</progress></div>}
    {status.phase === "awaiting_selection" && <a className="button subtle full" href="https://github.com/settings/installations" target="_blank" rel="noreferrer">Choose repositories in GitHub <ArrowRight size={15} /></a>}
    {(recoverable || status.phase === "awaiting_selection") && <button className="button primary full" disabled={busy} onClick={status.phase === "revoked" ? onReauthorize : onRetry}>{busy ? "Preparing…" : status.phase === "revoked" ? "Approve GitHub again" : "Try again"}</button>}
    {finished && <><button className="button primary full" onClick={onResults}>View AI inventory <ArrowRight size={15} /></button><button className="button subtle full" onClick={onAddDevices}>Add employee devices</button></>}
  </div>;
}

function EnvironmentActivationCard({ api, environment, revision, canManage, onResume, onUpgrade, onScan, onDisconnect }: { api: API; environment: Environment; revision: number; canManage: boolean; onResume: () => void; onUpgrade: () => void; onScan: () => void; onDisconnect: () => void }) {
  const remote = useRemote(() => api.activation(environment.id), [api, environment.id, revision]);
  const catalog = environmentCatalog.find((item) => item.kind === environment.kind);
  const Icon = catalog?.icon ?? Cloud;
  const fallback = environment.connection_status === "setup_pending" ? "awaiting_install" : environment.last_result_status === "failed" ? "failed" : environment.last_result_status === "partial" ? "partial" : environment.last_result_at ? "ready" : environment.source_id ? "processing" : environment.connection_status;
  const phase = remote.data?.phase ?? fallback;
  const lastResult = remote.data?.last_result_at ?? environment.last_result_at;
  useEffect(() => {
    if (!environment.source_id || !["connected", "processing"].includes(phase)) return;
    const timer = window.setTimeout(() => remote.reload(), 1800);
    return () => window.clearTimeout(timer);
  }, [environment.source_id, phase, remote]);
  const quick = (remote.data?.monitoring_mode || environment.monitoring_mode) === "quick_scan";
  const summary = remote.data?.summary;
  const quickSummary = quick && lastResult && summary ? `Quick Scan found ${summary.assets_found} items across ${summary.systems_found} AI tools and agents${remote.data?.evidence_expires_at ? ` · results expire ${new Date(remote.data.evidence_expires_at).toLocaleString()}` : ""}` : "";
  const github = environment.kind === "github_repository";
  const message = remote.data?.safe_error?.message || environment.last_error_message || quickSummary || (phase === "awaiting_install" ? github ? "GitHub approval is not finished" : "Installation is not finished" : phase === "connected" ? github ? "Repository connected; waiting for the first results" : "Lens scanner connected; waiting for the first results" : phase === "processing" ? "Lens is preparing the first results; you can leave this page" : phase === "stale" ? `Earlier results remain available, but this ${github ? "repository" : "device"} last reported ${relative(remote.data?.last_seen_at || lastResult || "")}` : lastResult ? `Last result ${relative(lastResult)}${phase === "partial" ? " · some data is missing" : ""}` : `Waiting for this ${github ? "repository" : "device"} to report`);
  return <article className="environment-card"><span className="environment-icon"><Icon size={20} /></span><div className="environment-card-copy"><span><b>{environment.display_name}</b><small>{catalog?.title ?? pretty(environment.kind)}{quick ? " · Quick Scan" : ""}{environment.external_id ? ` · ${environment.external_id}` : ""}</small></span><p>{message}</p></div><span className={`connection-status ${phase}`}><i />{connectionStatusLabel(phase)}</span><div className="environment-actions">{canManage && ((environment.kind === "endpoint" && phase === "awaiting_install") || (github && ["setup_pending", "verifying", "auth_error"].includes(environment.connection_status))) && <button className="button subtle" onClick={onResume}>Resume setup</button>}{canManage && remote.data?.can_enable_continuous_monitoring && <button className="button subtle" onClick={onUpgrade}>Keep results up to date</button>}{canManage && environment.connection_status === "connected" && ["aws", "azure", "gcp"].includes(environment.provider || "") && <button className="button subtle" onClick={onScan}><RefreshCw size={14} /> Scan now</button>}{canManage && phase !== "disconnected" && <button className="button quiet" onClick={onDisconnect}>Disconnect</button>}</div></article>;
}

function CoveragePage({ api, revision }: { api: API; revision: number }) {
	const navigate = useNavigate();
  const [targetType, setTargetType] = useState("");
  const overview = useRemote(() => api.overview("7d"), [api, revision]);
  const targets = useRemote(() => api.targets({ target_type: targetType, limit: 100 }), [api, revision, targetType]);
  const [expanded, setExpanded] = useState<string>();
  if (overview.loading || targets.loading) return <Loading />;
  if (overview.error || targets.error || !overview.data || !targets.data) return <Failure error={overview.error || targets.error} retry={() => { overview.reload(); targets.reload(); }} />;
  return <div className="page-stack">
    <section className="coverage-cards">{overview.data.coverage.map((item) => <CoverageCard key={item.target_type} item={item} active={targetType === item.target_type} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "connections", interaction: "filter_changed", control: "target_type" } }); setTargetType((value) => value === item.target_type ? "" : item.target_type); }} />)}</section>
    <section className="panel data-panel">
      <PanelHeading title="Connected locations" detail="Each device, repository, cloud account, or cluster appears once. Open one to see technical scanner details." action={<button className="button subtle" onClick={() => navigate("/connections/devices")}><Monitor size={14} /> Manage devices</button>} />
      <div className="target-table table-scroll"><div className="target-row table-head"><span>Location</span><span>Type</span><span>Last report</span><span>Last complete scan</span><span>Connection quality</span><span /></div>
        {targets.data.items.map((target) => <div className="target-group" key={target.id}>
          <button className="target-row" onClick={() => setExpanded((value) => value === target.id ? undefined : target.id)}>
            <Identity kind={target.target_type === "kubernetes" ? "cluster" : target.target_type} name={target.name} detail={`${target.platform ?? target.target_type}${target.architecture ? ` · ${target.architecture}` : ""}`} />
            <span className="kind-label">{locationTypeLabel(target.target_type)}</span><Freshness value={target.freshness} partial={target.partial} /><span className="observed">{target.last_full_at ? relative(target.last_full_at) : "Never"}</span>
            <span className="diagnostics">{target.possible_duplicate && <i>Possible duplicate</i>}{target.identity_quality === "legacy_identity" && <i>Legacy identity</i>}{!target.possible_duplicate && target.identity_quality === "persistent" && <small>Identity verified</small>}</span>
            {expanded === target.id ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
          </button>
          {expanded === target.id && <div className="collectors"><p>LENS SCANNERS FOR THIS LOCATION</p>{target.collectors.map((collector) => <div className="collector-row" key={collector.source_id}>
            <span><CircleDot size={13} /><b>{collector.name}</b><code>{collector.source_id}</code></span><span>v{collector.collector_version ?? "unknown"}</span><span>Sequence {collector.sequence ?? 0}</span><span className={collector.partial ? "partial" : "complete"}>{collector.partial ? `${collector.error_count} scan errors` : "Complete scan reported"}</span><time>{collector.last_seen_at ? relative(collector.last_seen_at) : "Never"}</time>
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
    api.setBaselines(baselines).then(() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "connections", interaction: "baseline_saved" } }); setStatus("Coverage goals saved"); setEditing(false); onSaved(); }).catch((reason) => setStatus(String(reason)));
  };
  return <section className="panel baseline-panel"><div><p className="eyebrow">COVERAGE GOALS</p><h2>How much should Lens cover?</h2><p>Enter how many devices, repositories, clusters, and cloud accounts you expect. Leave a field blank if you do not know.</p></div>
    {editing ? <div className="baseline-form">{["endpoint", "repository", "kubernetes", "cloud"].map((type) => <label key={type}>{locationTypeLabel(type)}<input type="number" min="0" placeholder="Unknown" value={values[type] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [type]: event.target.value }))} /></label>)}<button className="button primary" onClick={save}>Save coverage goals</button><button className="button subtle" onClick={() => setEditing(false)}>Cancel</button></div> : <button className="button subtle" onClick={() => setEditing(true)}><SlidersHorizontal size={15} /> Set coverage goals</button>}
    {status && <small className="form-status">{status}</small>}
  </section>;
}

function CoverageCard({ item, onClick, active }: { item: Overview["coverage"][number]; onClick?: () => void; active?: boolean }) {
  const Icon = item.target_type === "endpoint" ? Monitor : item.target_type === "repository" ? GitBranch : item.target_type === "cloud" ? Cloud : Container;
  const label = ({ endpoint: "Endpoints", repository: "Repositories", kubernetes: "Kubernetes", cloud: "Cloud environments" } as Record<string, string>)[item.target_type] ?? pretty(item.target_type);
  const status = item.reporting === 0 ? item.collectors ? "Not reporting recently" : "Not connected" : [item.fresh ? `${item.fresh} up to date` : "", item.stale ? `${item.stale} not reporting recently` : "", item.partial ? `${item.partial} missing some data` : ""].filter(Boolean).join(" · ");
  const body = <><span className="coverage-icon"><Icon size={19} /></span><div><p>{label}</p><strong>{item.reporting}</strong><span>{item.population_configured ? `of ${item.expected_count} expected` : "locations reporting"}</span></div><div className={item.stale || item.partial ? "coverage-card-status needs-review" : item.reporting ? "coverage-card-status reporting" : "coverage-card-status quiet"}><b>{status}</b><small>{item.population_configured ? "Coverage goal set" : "Expected total not set"}</small></div></>;
  return onClick ? <button className={active ? "coverage-card active" : "coverage-card"} onClick={onClick}>{body}</button> : <div className="coverage-card">{body}</div>;
}
