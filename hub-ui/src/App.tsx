import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Activity, AlertCircle, ArrowRight, Bot, Boxes, BrainCircuit, CheckCircle2, ChevronDown,
  ChevronRight, CircleDot, Container, Copy, Database, Download, GitBranch, History,
  FileSearch, Fingerprint, LayoutDashboard, Link2, LogOut, MapPin, Menu, Monitor, Network, PackageSearch, PlugZap, Radar,
  RefreshCw, Search, Server, SlidersHorizontal,
  TerminalSquare, UserRound, Workflow, X, type LucideIcon,
} from "lucide-react";
import {
  API, authConfig, exchangeOIDC, type AuthConfig, type Change, type Connection, type Entity, type Evidence,
  type EntityDetail, type Overview, type SystemDetail, type SystemItem, type Target,
} from "./api";
import { EvidenceGraphPage } from "./EvidenceGraph";
import { InvestigationPage } from "./Investigation";

type Page = "Overview" | "Exposure Map" | "Systems" | "Coverage" | "Changes" | "Technical inventory" | "Evidence graph";

const navigation: Array<{ page: Page; icon: LucideIcon; detail: string }> = [
  { page: "Overview", icon: LayoutDashboard, detail: "Organization posture" },
  { page: "Exposure Map", icon: FileSearch, detail: "Reachability and findings" },
  { page: "Systems", icon: Bot, detail: "Organization systems" },
  { page: "Coverage", icon: Radar, detail: "Enrollment coverage" },
  { page: "Changes", icon: History, detail: "Material change" },
  { page: "Technical inventory", icon: Boxes, detail: "Complete entity set" },
  { page: "Evidence graph", icon: Network, detail: "Facts and connections" },
];

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: { eyebrow: "DISCOVERY", title: "Organization AI posture", detail: "Evidence-backed visibility across enrolled endpoints, repositories, and clusters." },
  "Exposure Map": { eyebrow: "EXPOSURE", title: "Evidence-backed exposure map", detail: "Trace one system through configured connections, credential presence, operator context, and catalogue-derived potential." },
  Systems: { eyebrow: "INVENTORY", title: "Systems", detail: "Agents, agent-capable tools, and model runtimes—without supporting software or cached artifacts." },
  Coverage: { eyebrow: "VISIBILITY", title: "Reporting coverage", detail: "What is enrolled across the organization, where data is stale, and where expected population is unknown." },
  Changes: { eyebrow: "HISTORY", title: "Changes", detail: "Material inventory changes. Routine scan refreshes are suppressed." },
  "Technical inventory": { eyebrow: "TECHNICAL", title: "Technical inventory", detail: "Every discovered entity, including supporting runtimes, cached artifacts, users, APIs, and workloads." },
  "Evidence graph": { eyebrow: "EVIDENCE", title: "Evidence graph", detail: "Trace a system to its capabilities, deployment surfaces, observed users, and sanitized evidence." },
};

const kindIcons: Record<string, LucideIcon> = {
  endpoint: Monitor, repository: GitBranch, cluster: Container, workload: Container, agent: Bot,
  runtime: TerminalSquare, framework: Boxes, mcp_server: PlugZap, skill: CheckCircle2, model: BrainCircuit,
  model_server: Server, api_service: Database, api_operation: Link2, workflow: Workflow, user: UserRound,
};

export function App() {
  const [token, setToken] = useState(() => sessionStorage.getItem("lens-token") ?? "");
  const [authError, setAuthError] = useState("");
  const saveToken = useCallback((value: string) => {
    sessionStorage.setItem("lens-token", value);
    setToken(value);
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const code = params.get("code");
    if (!code) return;
    const verifier = sessionStorage.getItem("lens-pkce-verifier");
    const expected = sessionStorage.getItem("lens-oidc-state");
    const redirect = sessionStorage.getItem("lens-oidc-redirect");
    if (!verifier || !redirect || params.get("state") !== expected) {
      setAuthError("The sign-in state could not be verified.");
      return;
    }
    exchangeOIDC(code, redirect, verifier).then((result) => {
      history.replaceState({}, "", location.pathname);
      saveToken(result.access_token);
    }).catch((error) => setAuthError(String(error)));
  }, [saveToken]);

  if (!token) return <SignIn onToken={saveToken} authError={authError} />;
  return <Shell api={new API(token)} signOut={() => { sessionStorage.removeItem("lens-token"); setToken(""); }} />;
}

function SignIn({ onToken, authError }: { onToken: (token: string) => void; authError: string }) {
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [value, setValue] = useState("");
  const [error, setError] = useState(authError);
  useEffect(() => { authConfig().then(setConfig).catch((reason) => setError(String(reason))); }, []);

  const beginOIDC = async () => {
    if (!config?.authorization_endpoint || !config.client_id || !config.redirect_uri) return;
    const verifier = randomURLSafe(64);
    const state = randomURLSafe(24);
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
    sessionStorage.setItem("lens-pkce-verifier", verifier);
    sessionStorage.setItem("lens-oidc-state", state);
    sessionStorage.setItem("lens-oidc-redirect", config.redirect_uri);
    const target = new URL(config.authorization_endpoint);
    target.search = new URLSearchParams({ response_type: "code", client_id: config.client_id, redirect_uri: config.redirect_uri, scope: (config.scopes ?? ["openid"]).join(" "), state, code_challenge: base64URL(new Uint8Array(digest)), code_challenge_method: "S256" }).toString();
    location.assign(target);
  };

  return <main className="signin">
    <section className="signin-story">
      <Brand />
      <div className="signin-copy">
        <span className="product-kicker"><Radar size={14} /> Autonomous agent discovery</span>
        <h1>Bring the agent footprint into focus.</h1>
        <p>Lens gives security and platform leaders a factual map of autonomous systems, where they run, what they connect to, and the evidence behind every conclusion.</p>
      </div>
    </section>
    <section className="signin-access"><div className="access-card">
      <p className="eyebrow">LENS HUB</p><h2>Open your discovery plane</h2>
      <p className="muted">No scores. No enforcement. Just trustworthy organization-wide discovery posture.</p>
      {config?.enabled && <button className="button primary full" onClick={beginOIDC}>Continue with organization SSO <ArrowRight size={16} /></button>}
      {config?.development_bootstrap && <form onSubmit={(event) => { event.preventDefault(); if (value.trim()) onToken(value.trim()); }}>
        <label>Local bootstrap token<input type="password" value={value} onChange={(event) => setValue(event.target.value)} autoFocus={!config?.enabled} /></label>
        <button className="button primary full">Open Lens Hub <ArrowRight size={16} /></button>
      </form>}
      {error && <p className="error-message">{error}</p>}
    </div></section>
  </main>;
}

function Shell({ api, signOut }: { api: API; signOut: () => void }) {
  const [page, setPage] = useState<Page>("Overview");
  const [graphSystem, setGraphSystem] = useState("");
  const [menuOpen, setMenuOpen] = useState(false);
  const [about, setAbout] = useState(false);
  const [revision, setRevision] = useState(0);
  const [exposureEnabled, setExposureEnabled] = useState(false);
  useEffect(() => { authConfig().then((config) => setExposureEnabled(config.exposure_enabled)).catch(() => setExposureEnabled(false)); }, []);
  const copy = pageCopy[page];
  return <div className="app-shell">
    <aside className={menuOpen ? "sidebar open" : "sidebar"}>
      <div className="sidebar-brand"><Brand /></div>
      <nav className="main-nav">
        {navigation.filter((item) => item.page !== "Exposure Map" || exposureEnabled).map(({ page: item, icon: Icon, detail }) => <button key={item} className={page === item ? "active" : ""} onClick={() => { setPage(item); setMenuOpen(false); }}>
          <Icon size={17} /><span><b>{item}</b><small>{detail}</small></span>{page === item && <ChevronRight size={14} />}
        </button>)}
      </nav>
      <div className="sidebar-footer">
        <button onClick={() => setAbout(true)}><CircleDot size={14} /> {exposureEnabled ? "Discover + assess" : "Discover only"}</button>
        <button className="logout" onClick={signOut} aria-label="Sign out"><LogOut size={16} /></button>
      </div>
    </aside>
    <main className="main-area">
      <div className="workspace">
        <header className="page-heading">
          <button className="mobile-menu" aria-label={menuOpen ? "Close navigation" : "Open navigation"} onClick={() => setMenuOpen((value) => !value)}>{menuOpen ? <X size={20} /> : <Menu size={20} />}</button>
          <div><p className="eyebrow">{copy.eyebrow}</p><h1>{copy.title}</h1><p>{copy.detail}</p></div>
          <div className="page-actions"><button className="icon-button" onClick={() => setRevision((value) => value + 1)} title="Refresh"><RefreshCw size={16} /></button><ExportMenu api={api} /></div>
        </header>
        {page === "Overview" && <OverviewPage api={api} revision={revision} go={setPage} />}
        {page === "Exposure Map" && <InvestigationPage api={api} revision={revision} onOpenGraph={(systemID) => { setGraphSystem(systemID); setPage("Evidence graph"); }} />}
        {page === "Systems" && <SystemsPage api={api} revision={revision} />}
        {page === "Coverage" && <CoveragePage api={api} revision={revision} />}
        {page === "Changes" && <ChangesPage api={api} revision={revision} />}
        {page === "Technical inventory" && <InventoryPage api={api} revision={revision} />}
        {page === "Evidence graph" && <EvidenceGraphPage api={api} revision={revision} initialSystemId={graphSystem} />}
      </div>
    </main>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} />}
    {about && <About onClose={() => setAbout(false)} />}
  </div>;
}

function OverviewPage({ api, revision, go }: { api: API; revision: number; go: (page: Page) => void }) {
  const [window, setWindow] = useState("7d");
  const overview = useRemote(() => api.overview(window), [api, revision, window]);
  const running = useRemote(() => api.systems({ state: "running", confidence: "confirmed", freshness: "fresh", limit: 8 }), [api, revision]);
  if (overview.loading || running.loading) return <Loading />;
  if (overview.error || !overview.data) return <Failure error={overview.error} retry={overview.reload} />;
  const data = overview.data;
  const systems = data.footprint.system_types;
  const states = data.footprint.states;
  const totalSystems = sum(Object.values(systems));
  const runningCount = states.running ?? 0;
  const reportingTargets = sum(data.coverage.map((item) => item.reporting));
  const reportingLabel = `${reportingTargets} reporting ${reportingTargets === 1 ? "target" : "targets"}`;
  const attention = [
    ...(data.exposure_summary?.top_findings ?? []).map((finding) => [`${pretty(finding.severity)} · ${finding.title}`, 1, "Open the complete evidence path and safe next check", "Exposure Map"]),
    ["Ownership is not established", data.attention.unattributed_systems, "These systems have no authoritative business or technical owner", "Systems"],
    ["Evidence needs corroboration", data.attention.possible_only_systems, "A second authoritative signal is needed before governance", "Systems"],
    ["Services are reachable beyond this device", data.attention.non_loopback_services, "Network-accessible AI services need exposure review", "Systems"],
    ["Partial scans", data.attention.partial_scans, "Some locations or detectors were unavailable", "Coverage"],
    ["Stale targets", data.attention.stale_targets, "Outside the freshness threshold", "Coverage"],
    ["Conflicting facts", data.attention.fact_conflicts, "Sources disagree on a material fact", "Systems"],
  ].filter((item) => Number(item[1]) > 0) as Array<[string, number, string, Page]>;
  const changes = groupChanges(data.changes).slice(0, 4);

  return <div className="page-stack executive-overview">
    <div className="overview-toolbar"><span>Updated {relative(data.generated_at)}</span><div className="window-switch">{["24h", "7d", "30d"].map((item) => <button className={window === item ? "active" : ""} onClick={() => setWindow(item)} key={item}>{item}</button>)}</div></div>
    <section className="exposure-hero">
      <div className="exposure-copy"><span>Organization-wide AI exposure</span><h2><strong>{totalSystems.toLocaleString()}</strong> AI systems visible in your organization</h2><p>Current evidence comes from {reportingLabel}. {runningCount} systems are running now{data.exposure_summary ? ` and ${data.exposure_summary.total} explainable exposure findings are current` : ""}.</p><div className="enrollment-scope">{data.coverage.map((item) => <span key={item.target_type}><b>{pretty(item.target_type)}</b> {item.reporting ? `${item.reporting} reporting` : "Not enrolled"}</span>)}</div>{data.exposure_summary && <button className="overview-story-link" onClick={() => go("Exposure Map")}>Open the Exposure Map <ArrowRight size={14} /></button>}</div>
      <div className="exposure-facts">
        <ExecutiveFact value={runningCount} label="Running now" tone="active" />
        <ExecutiveFact value={data.attention.unattributed_systems ?? 0} label="Ownership gaps" tone="attention" />
        <ExecutiveFact value={(data.exposure_summary?.counts.critical ?? 0) + (data.exposure_summary?.counts.high ?? 0)} label="High-priority exposures" tone="attention" />
        <ExecutiveFact value={reportingTargets} label="Reporting targets" tone="good" />
      </div>
    </section>
    <section className="system-mix-strip">
      <div className="mix-heading"><span>Observed system mix</span><small>Root systems only · supporting software excluded</small></div>
      <div className="system-breakdown">
        <button onClick={() => go("Systems")}><Bot size={18} /><span><b>{systems.autonomous_agent ?? 0}</b><small>Autonomous agents</small></span></button>
        <button onClick={() => go("Systems")}><TerminalSquare size={18} /><span><b>{systems.agent_tool ?? 0}</b><small>Agent-capable tools</small></span></button>
        <button onClick={() => go("Systems")}><BrainCircuit size={18} /><span><b>{systems.model_runtime ?? 0}</b><small>Model runtimes</small></span></button>
      </div>
    </section>
    <div className="executive-primary">
      <section className="panel running-panel"><PanelHeading title="Running and confirmed" detail="AI systems active across currently reporting targets" action={<button className="text-button" onClick={() => go("Systems")}>Open systems <ArrowRight size={13} /></button>} />
        {running.data?.items.length ? <div className="running-list">{running.data.items.map((item) => <button key={item.id} onClick={() => go("Systems")}><span className="running-mark"><CircleDot size={14} /></span><span><b>{item.name}</b><small>{pretty(item.system_type)} · {item.network_scope === "loopback" ? "Local access only" : pretty(item.network_scope)}</small></span><span><strong>{pretty(item.state)}</strong><small>{pretty(item.confidence)} evidence</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="No confirmed systems running" detail="No active system has confirmed evidence in the current scan." />}
      </section>
      <section className="panel attention-panel"><PanelHeading title="What needs attention" detail="Concrete findings, not an opaque risk score" />
        {attention.length ? <div className="attention-list">{attention.map(([label, count, detail, destination]) => <button key={label} onClick={() => go(destination)}><span className="attention-count active">{count}</span><span><b>{label}</b><small>{detail}</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="Nothing needs review" detail="No current discovery findings require attention." />}
      </section>
    </div>
    <div className="executive-secondary">
      <section className="panel change-panel"><PanelHeading title="What changed" detail={`Repeated observations grouped over the last ${window}`} action={<button className="text-button" onClick={() => go("Changes")}>View history <ArrowRight size={13} /></button>} /><ChangeList items={changes} /></section>
      <section className="panel evidence-posture"><PanelHeading title="Evidence posture" detail="How current and conclusive this view is" action={<button className="text-button" onClick={() => go("Coverage")}>Technical coverage <ArrowRight size={13} /></button>} /><StateDistribution values={states} total={totalSystems} /><ConfidenceSummary data={data.data_quality.confidence} /></section>
    </div>
  </div>;
}

function SystemsPage({ api, revision }: { api: API; revision: number }) {
  const [filters, setFilters] = useState<Record<string, string>>({ sort: "last_seen", freshness: "fresh" });
  const [cursor, setCursor] = useState("");
  const [items, setItems] = useState<SystemItem[]>([]);
  const [next, setNext] = useState("");
  const [selected, setSelected] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setLoading(true); setError("");
      api.systems({ ...filters, cursor }).then((result) => { setItems((current) => cursor ? [...current, ...result.items] : result.items); setNext(result.next_cursor ?? ""); }).catch((reason) => setError(String(reason))).finally(() => setLoading(false));
    }, filters.search ? 220 : 0);
    return () => clearTimeout(timer);
  }, [api, revision, filters, cursor]);

  const update = (key: string, value: string) => { setCursor(""); setItems([]); setFilters((current) => ({ ...current, [key]: value })); };
  return <div className="page-stack">
    <FilterBar search={filters.search ?? ""} setSearch={(value) => update("search", value)}>
      <Select label="System type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All root systems", autonomous_agent: "Autonomous agents", agent_tool: "Agent-capable tools", model_runtime: "Model runtimes" }} />
      <Select label="State" value={filters.state} onChange={(value) => update("state", value)} options={{ "": "Any state", running: "Running", deployed: "Deployed", defined: "Defined", configured: "Configured", installed: "Installed", residual: "Residual", cached: "Cached" }} />
      <Select label="Confidence" value={filters.confidence} onChange={(value) => update("confidence", value)} options={{ "": "Any confidence", confirmed: "Confirmed", likely: "Likely", possible: "Possible" }} />
      <Select label="Network" value={filters.network_scope} onChange={(value) => update("network_scope", value)} options={{ "": "Any scope", external: "External", network: "Network", loopback: "Loopback", none: "None", unknown: "Unknown" }} />
      <Select label="Reporting" value={filters.freshness} onChange={(value) => update("freshness", value)} options={{ fresh: "Fresh targets", stale: "Stale targets", all: "Fresh and stale" }} />
    </FilterBar>
    <section className="panel data-panel"><div className="table-summary"><span><b>{items.length}</b> {filters.freshness === "stale" ? "stale" : filters.freshness === "all" ? "fresh and stale" : "fresh"} systems</span>{filters.freshness === "fresh" && <span>Older identities remain available through Reporting filters and Coverage diagnostics</span>}</div>
      <div className="system-table table-scroll"><div className="system-row table-head"><span>System</span><span>Type</span><span>State</span><span>Target / surface</span><span>Attribution</span><span>Evidence</span><span /></div>
        {items.map((item) => <button className="system-row" key={item.id} onClick={() => setSelected(item.id)}>
          <Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} />
          <TypePill value={item.system_type} /><StatePill state={item.state} /><span className="stacked"><b>{item.target_name ?? "Unresolved target"}</b><small>{pretty(item.surface)}{item.target_freshness ? ` · ${pretty(item.target_freshness)}` : ""}</small></span>
          <span className={item.attributed ? "fact good" : "fact quiet"}>{item.attributed ? "Attributed" : "Unattributed"}</span><ConfidencePill value={item.confidence} /><ChevronRight size={15} />
        </button>)}
        {!loading && !items.length && <Empty icon={Bot} title="No systems match this view" detail="Supporting runtimes and cached artifacts are intentionally excluded from the executive systems view." />}
      </div>
      {error && <InlineError text={error} />}{loading && <InlineLoading />}{next && !loading && <button className="load-more" onClick={() => setCursor(next)}>Load more systems <ChevronDown size={15} /></button>}
    </section>
    {selected && <SystemDrawer api={api} id={selected} onClose={() => setSelected(undefined)} />}
  </div>;
}

function CoveragePage({ api, revision }: { api: API; revision: number }) {
  const [targetType, setTargetType] = useState("");
  const overview = useRemote(() => api.overview("7d"), [api, revision]);
  const targets = useRemote(() => api.targets({ target_type: targetType, limit: 100 }), [api, revision, targetType]);
  const [expanded, setExpanded] = useState<string>();
  const [enrollment, setEnrollment] = useState<{ code: string; expires_at: string; hub_url?: string; collector_version?: string }>();
  const [enrollError, setEnrollError] = useState("");
  const [enrollmentPlatform, setEnrollmentPlatform] = useState<"macos" | "windows">("macos");
  const [commandCopied, setCommandCopied] = useState(false);
  const generateEnrollment = () => {
    setEnrollError("");
    setCommandCopied(false);
    api.enrollment().then(setEnrollment).catch((reason) => setEnrollError(String(reason)));
  };
  const enrollmentHub = enrollment?.hub_url || location.origin;
  const collectorVersion = enrollment?.collector_version && !enrollment.collector_version.endsWith("-dev") ? enrollment.collector_version : "latest";
  const collectorPackage = `barrikade-lens@${collectorVersion}`;
  const enrollmentCommand = enrollmentPlatform === "macos"
    ? `sudo -E "$(command -v npx)" --yes --no-audit --no-fund ${collectorPackage} enroll ${enrollment?.code ?? "CODE"} --hub '${enrollmentHub}' --config '/Library/Application Support/Barrikade/Lens/config.json' --install`
    : `npx --yes --no-audit --no-fund ${collectorPackage} enroll ${enrollment?.code ?? "CODE"} --hub '${enrollmentHub}' --install`;
  const copyEnrollmentCommand = () => navigator.clipboard.writeText(enrollmentCommand).then(() => {
    setCommandCopied(true);
    window.setTimeout(() => setCommandCopied(false), 1800);
  }).catch((reason) => setEnrollError(`Could not copy the command: ${String(reason)}`));
  if (overview.loading || targets.loading) return <Loading />;
  if (overview.error || targets.error || !overview.data || !targets.data) return <Failure error={overview.error || targets.error} retry={() => { overview.reload(); targets.reload(); }} />;
  return <div className="page-stack">
    <section className="coverage-cards">{overview.data.coverage.map((item) => <CoverageCard key={item.target_type} item={item} active={targetType === item.target_type} onClick={() => setTargetType((value) => value === item.target_type ? "" : item.target_type)} />)}</section>
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
    <section className="panel enrollment-panel">
      <div className="enrollment-head"><div><p className="eyebrow">EXPAND COVERAGE</p><h2>Enroll a managed endpoint</h2><p>Generate a private, single-device command that expires in ten minutes. It enrolls the endpoint and starts continuous discovery in one step.</p></div>
        {!enrollment && <button className="button primary" onClick={generateEnrollment}>Generate install command</button>}
      </div>
      {enrollment && <div className="enrollment-setup">
        <div className="platform-tabs" aria-label="Endpoint platform">
          <button className={enrollmentPlatform === "macos" ? "active" : ""} onClick={() => { setEnrollmentPlatform("macos"); setCommandCopied(false); }}>macOS</button>
          <button className={enrollmentPlatform === "windows" ? "active" : ""} onClick={() => { setEnrollmentPlatform("windows"); setCommandCopied(false); }}>Windows</button>
        </div>
        <p className="install-instruction">{enrollmentPlatform === "macos" ? "Open Terminal on the Mac. Paste this command and enter the administrator password when prompted. Requires Node.js 18+." : "Open PowerShell as Administrator on the Windows device, then paste this command. Requires Node.js 18+."}</p>
        <div className="install-command"><code>{enrollmentCommand}</code><button onClick={copyEnrollmentCommand}><Copy size={15} /> {commandCopied ? "Copied" : "Copy command"}</button></div>
        <div className="enrollment-meta"><span>One use · expires {new Date(enrollment.expires_at).toLocaleTimeString()}</span><button onClick={generateEnrollment}>Generate a new command</button></div>
      </div>}
      {enrollError && <InlineError text={enrollError} />}
    </section>
  </div>;
}

function ChangesPage({ api, revision }: { api: API; revision: number }) {
  const [filters, setFilters] = useState<Record<string, string>>({ window: "7d", system_role: "system" });
  const [cursor, setCursor] = useState("");
  const remote = useRemote(() => api.changes({ ...filters, cursor }), [api, revision, filters, cursor]);
  const [items, setItems] = useState<Change[]>([]);
  useEffect(() => { if (remote.data) setItems((current) => cursor ? [...current, ...remote.data!.items] : remote.data!.items); }, [remote.data, cursor]);
  const update = (key: string, value: string) => { setCursor(""); setItems([]); setFilters((current) => ({ ...current, [key]: value })); };
  return <div className="page-stack"><FilterBar hideSearch>
    <Select label="Window" value={filters.window} onChange={(value) => update("window", value)} options={{ "24h": "Last 24 hours", "7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days" }} />
    <Select label="Category" value={filters.category} onChange={(value) => update("category", value)} options={{ "": "All material changes", state: "State", network_scope: "Network scope", attribution: "Attribution", capability: "Capability", confidence: "Confidence", identity: "Identity", freshness: "Freshness" }} />
    <Select label="System type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All systems", autonomous_agent: "Autonomous agent", agent_tool: "Agent-capable tool", model_runtime: "Model runtime" }} />
    <Select label="Surface" value={filters.surface} onChange={(value) => update("surface", value)} options={{ "": "All surfaces", endpoint: "Endpoint", repository: "Repository", kubernetes: "Kubernetes" }} />
  </FilterBar>
    <section className="panel change-log"><PanelHeading title="System change history" detail="Changes to root systems and their connected capabilities; routine re-observation is suppressed" count={items.length} /><ChangeList items={items} expanded />
      {remote.loading && <InlineLoading />}{remote.error && <InlineError text={remote.error} />}{remote.data?.next_cursor && !remote.loading && <button className="load-more" onClick={() => setCursor(remote.data!.next_cursor!)}>Load more changes <ChevronDown size={15} /></button>}
    </section>
  </div>;
}

function InventoryPage({ api, revision }: { api: API; revision: number }) {
  const [filters, setFilters] = useState<Record<string, string>>({ sort: "last_seen", freshness: "fresh" });
  const [cursor, setCursor] = useState("");
  const remote = useRemote(() => api.entities({ ...filters, cursor }), [api, revision, filters, cursor]);
  const [items, setItems] = useState<Entity[]>([]);
  const [selected, setSelected] = useState<string>();
  useEffect(() => { if (remote.data) setItems((current) => cursor ? [...current, ...remote.data!.items] : remote.data!.items); }, [remote.data, cursor]);
  const update = (key: string, value: string) => { setCursor(""); setItems([]); setFilters((current) => ({ ...current, [key]: value })); };
  return <div className="page-stack"><FilterBar search={filters.search ?? ""} setSearch={(value) => update("search", value)}>
    <Select label="Entity type" value={filters.kind} onChange={(value) => update("kind", value)} options={{ "": "All entity types", agent: "Agent", runtime: "Runtime", mcp_server: "MCP server", skill: "Skill", model: "Model", model_server: "Model server", framework: "Framework", repository: "Repository", workload: "Workload", api_service: "API service", workflow: "Workflow", user: "User" }} />
    <Select label="Role" value={filters.system_role} onChange={(value) => update("system_role", value)} options={{ "": "Any graph role", system: "Root system", component: "Component", supporting: "Supporting runtime", artifact: "Artifact", target: "Discovery target" }} />
    <Select label="State" value={filters.state} onChange={(value) => update("state", value)} options={{ "": "Any state", running: "Running", deployed: "Deployed", defined: "Defined", configured: "Configured", installed: "Installed", residual: "Residual", cached: "Cached", observed: "Observed" }} />
    <Select label="Confidence" value={filters.confidence} onChange={(value) => update("confidence", value)} options={{ "": "Any confidence", confirmed: "Confirmed", likely: "Likely", possible: "Possible" }} />
    <Select label="Reporting" value={filters.freshness} onChange={(value) => update("freshness", value)} options={{ fresh: "Fresh targets", stale: "Stale targets", all: "Fresh and stale" }} />
  </FilterBar>
    <section className="panel data-panel"><div className="table-summary"><span><b>{items.length}</b> {filters.freshness === "stale" ? "stale" : filters.freshness === "all" ? "fresh and stale" : "fresh"} entities loaded</span><span>Supporting and cached inventory is included; older identities are available with the Reporting filter</span></div>
      <div className="inventory-table table-scroll"><div className="inventory-row table-head"><span>Entity</span><span>Graph role</span><span>State</span><span>Target / surface</span><span>Confidence</span><span>Last observed</span><span /></div>
        {items.map((item) => <button className="inventory-row" key={item.id} onClick={() => setSelected(item.id)}><Identity kind={item.kind} name={item.name} detail={item.canonical_key ?? item.id} /><span className="kind-label">{pretty(item.posture?.system_role ?? item.kind)}</span><StatePill state={item.posture?.state ?? "observed"} /><span className="stacked"><b>{item.posture?.target_name ?? "Shared inventory"}</b><small>{pretty(item.posture?.surface ?? "unknown")} · {pretty(item.posture?.target_freshness ?? "unknown")}</small></span><ConfidencePill value={item.confidence} /><span className="observed">{relative(item.last_seen_at)}</span><ChevronRight size={15} /></button>)}
      </div>
      {remote.loading && <InlineLoading />}{remote.error && <InlineError text={remote.error} />}{remote.data?.next_cursor && !remote.loading && <button className="load-more" onClick={() => setCursor(remote.data!.next_cursor!)}>Load more entities <ChevronDown size={15} /></button>}
    </section>
    {selected && <EntityDrawer api={api} id={selected} onClose={() => setSelected(undefined)} />}
  </div>;
}

function SystemDrawer({ api, id, onClose }: { api: API; id: string; onClose: () => void }) {
  const remote = useRemote(() => api.system(id), [api, id]);
  return <Drawer onClose={onClose}>{remote.loading ? <Loading /> : remote.error || !remote.data ? <Failure error={remote.error} retry={remote.reload} /> : <SystemDetailView item={remote.data} />}</Drawer>;
}

function SystemDetailView({ item }: { item: SystemDetail }) {
  const groups = groupConnections(item.connections);
  return <><div className="drawer-title"><Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} /><div><TypePill value={item.system_type} /><StatePill state={item.state} /><ConfidencePill value={item.confidence} /></div></div>
    <div className="fact-grid"><Fact label="Target" value={item.target_name ?? "Unresolved"} /><Fact label="Reporting" value={pretty(item.target_freshness ?? "unknown")} /><Fact label="Network scope" value={pretty(item.network_scope)} /><Fact label="Attribution" value={item.attributed ? "Authoritative" : "Not established"} /><Fact label="First discovered" value={relative(item.first_seen_at)} /><Fact label="Last observed" value={relative(item.last_seen_at)} /></div>
    <section className="drawer-section"><h3>Connected inventory <span>{item.connections.length}</span></h3>{Object.entries(groups).map(([group, values]) => <div className="connection-group" key={group}><p>{pretty(group)}</p>{values.map((connection) => <ConnectionRow item={connection} key={connection.relationship_id} />)}</div>)}</section>
    <EvidenceSection items={item.evidence} />
  </>;
}

function EntityDrawer({ api, id, onClose }: { api: API; id: string; onClose: () => void }) {
  const remote = useRemote(() => api.entity(id), [api, id]);
  return <Drawer onClose={onClose}>{remote.loading ? <Loading /> : remote.error || !remote.data ? <Failure error={remote.error} retry={remote.reload} /> : <EntityDetailView item={remote.data} />}</Drawer>;
}

function EntityDetailView({ item }: { item: EntityDetail }) {
  return <><div className="drawer-title"><Identity kind={item.kind} name={item.name} detail={item.canonical_key ?? item.id} /><div><StatePill state={item.posture?.state ?? "observed"} /><ConfidencePill value={item.confidence} /></div></div>
    <div className="fact-grid"><Fact label="Kind" value={pretty(item.kind)} /><Fact label="Graph role" value={pretty(item.posture?.system_role ?? "unknown")} /><Fact label="Target" value={item.posture?.target_name ?? "Shared inventory"} /><Fact label="Reporting" value={pretty(item.posture?.target_freshness ?? "unknown")} /><Fact label="Surface" value={pretty(item.posture?.surface ?? "unknown")} /><Fact label="Network scope" value={pretty(item.posture?.network_scope ?? "unknown")} /><Fact label="First discovered" value={relative(item.first_seen_at)} /><Fact label="Last observed" value={relative(item.last_seen_at)} /></div>
    <EvidenceSection items={item.evidence ?? []} />
    <details className="technical-attributes"><summary>Technical attributes <ChevronDown size={13} /></summary><pre>{JSON.stringify(item.attributes, null, 2)}</pre></details>
  </>;
}

function EvidenceSection({ items }: { items: Evidence[] }) {
  return <section className="drawer-section evidence-section"><div className="drawer-section-heading"><h3>Evidence <span>{items.length}</span></h3><small>Open a finding to see why Lens linked it and what to investigate.</small></div>
    {items.length ? <div className="evidence-cards">{items.map((evidence) => <EvidenceCard item={evidence} key={`${evidence.source_id}:${evidence.id}`} />)}</div> : <Empty icon={FileSearch} title="No retained evidence" detail="This entity has no evidence observations in the current retention window." />}
  </section>;
}

function EvidenceCard({ item }: { item: Evidence }) {
  const title = item.title ?? `${pretty(item.family)} evidence`;
  const summary = item.summary ?? `${pretty(item.method)} evidence was observed by ${pretty(item.detector_id)}.`;
  const location = item.location ?? (item.locator?.startsWith("sha256:") || item.locator?.startsWith("path_hash:") ? "Protected endpoint location" : item.locator ?? "Location not retained");
  return <details className="evidence-card"><summary>
    <span className="evidence-card-icon"><FileSearch size={16} /></span>
    <span className="evidence-card-copy"><b>{title}</b><p>{summary}</p><small><MapPin size={11} /> {location}<i />{item.target_name ?? item.source_name ?? pretty(item.source_type ?? "discovery source")}<i />{relative(item.observed_at)}</small></span>
    <ConfidencePill value={item.specificity === "high" ? "confirmed" : item.specificity === "medium" ? "likely" : "possible"} /><ChevronDown className="evidence-chevron" size={15} />
  </summary><div className="evidence-card-body">
    {item.subject && <div className="evidence-subject"><span><FileSearch size={13} /> EXACT RESOURCE</span><div><b>{item.subject.name}</b><small>{pretty(item.subject.entity_kind)} · {pretty(item.subject.confidence)} evidence</small></div></div>}
    {!!item.matched_facts?.length && <div className="evidence-facts"><span>DISCOVERED DETAILS</span><div>{item.matched_facts.map((fact) => <p key={fact.label}><small>{fact.label}</small><b>{fact.value}</b></p>)}</div></div>}
    <div className="evidence-explanations"><article><span>WHY LENS CONNECTED THIS</span><p>{item.why_it_matched ?? `The ${pretty(item.detector_id)} detector recorded ${pretty(item.specificity)}-specificity evidence.`}</p></article><article><span>INVESTIGATE NEXT</span><p>{item.investigation_hint ?? `Review this ${pretty(item.family)} observation on ${item.target_name ?? "the reporting target"}.`}</p></article></div>
    <div className="evidence-provenance"><Fact label="Target" value={item.target_name ?? "Unresolved"} /><Fact label="Target freshness" value={pretty(item.target_freshness ?? "unknown")} /><Fact label="Collector" value={item.source_name ?? item.source_id} /><Fact label="Detector" value={`${item.detector_id} v${item.detector_version}`} /><Fact label="Method" value={pretty(item.method)} /><Fact label="Observations" value={String(item.observations)} /></div>
    {!!item.related_entities?.length && <div className="evidence-related"><span>ALSO SUPPORTED BY THIS OBSERVATION</span>{item.related_entities.map((entity) => <div key={entity.entity_id}><b>{entity.name}</b><small>{pretty(entity.entity_kind)} · {pretty(entity.confidence)}</small></div>)}</div>}
    {!!item.integrity && <details className="evidence-integrity"><summary><Fingerprint size={13} /> Integrity references <ChevronDown size={12} /></summary><div>{item.integrity.locator_reference && <code><span>Locator reference</span>{item.integrity.locator_reference}</code>}{item.integrity.content_hash && <code><span>Content hash</span>{item.integrity.content_hash}</code>}</div><p>Hashes prove which sanitized artifact Lens observed. They are integrity metadata, not the finding itself.</p></details>}
  </div></details>;
}

function CoverageBaseline({ api, coverage, onSaved }: { api: API; coverage: Overview["coverage"]; onSaved: () => void }) {
  const initial = Object.fromEntries(coverage.map((item) => [item.target_type, item.expected_count === null ? "" : String(item.expected_count)]));
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [editing, setEditing] = useState(false);
  const [status, setStatus] = useState("");
  const save = () => {
    const baselines = ["endpoint", "repository", "kubernetes"].map((target_type) => ({ target_type, expected_count: values[target_type] === "" ? null : Number(values[target_type]) }));
    api.setBaselines(baselines).then(() => { setStatus("Coverage baseline saved"); setEditing(false); onSaved(); }).catch((reason) => setStatus(String(reason)));
  };
  return <section className="panel baseline-panel"><div><p className="eyebrow">EXPECTED POPULATION</p><h2>Coverage denominator</h2><p>Optional manual baselines let Lens compare reporting targets with a known population. Blank values remain explicitly unknown.</p></div>
    {editing ? <div className="baseline-form">{["endpoint", "repository", "kubernetes"].map((type) => <label key={type}>{pretty(type)}<input type="number" min="0" placeholder="Unknown" value={values[type] ?? ""} onChange={(event) => setValues((current) => ({ ...current, [type]: event.target.value }))} /></label>)}<button className="button primary" onClick={save}>Save baselines</button><button className="button subtle" onClick={() => setEditing(false)}>Cancel</button></div> : <button className="button subtle" onClick={() => setEditing(true)}><SlidersHorizontal size={15} /> Configure baselines</button>}
    {status && <small className="form-status">{status}</small>}
  </section>;
}

function CoverageCard({ item, onClick, active }: { item: Overview["coverage"][number]; onClick?: () => void; active?: boolean }) {
  const Icon = item.target_type === "endpoint" ? Monitor : item.target_type === "repository" ? GitBranch : Container;
  const label = ({ endpoint: "Endpoints", repository: "Repositories", kubernetes: "Kubernetes" } as Record<string, string>)[item.target_type] ?? pretty(item.target_type);
  const status = item.reporting === 0 ? "Not reporting" : [item.fresh ? `${item.fresh} fresh` : "", item.stale ? `${item.stale} stale` : "", item.partial ? `${item.partial} partial` : ""].filter(Boolean).join(" · ");
  const body = <><span className="coverage-icon"><Icon size={19} /></span><div><p>{label}</p><strong>{item.reporting}</strong><span>{item.population_configured ? `of ${item.expected_count} expected` : "reporting targets"}</span></div><div className={item.stale || item.partial ? "coverage-card-status needs-review" : item.reporting ? "coverage-card-status reporting" : "coverage-card-status quiet"}><b>{status}</b><small>{item.population_configured ? "Manual baseline" : "Expected population unknown"}</small></div></>;
  return onClick ? <button className={active ? "coverage-card active" : "coverage-card"} onClick={onClick}>{body}</button> : <div className="coverage-card">{body}</div>;
}

function CoverageSummaryRow({ item, onClick }: { item: Overview["coverage"][number]; onClick: () => void }) {
  const Icon = item.target_type === "endpoint" ? Monitor : item.target_type === "repository" ? GitBranch : Container;
  const label = ({ endpoint: "Endpoints", repository: "Repositories", kubernetes: "Kubernetes" } as Record<string, string>)[item.target_type] ?? pretty(item.target_type);
  const statusText = item.reporting === 0 ? "Not reporting" : `${item.reporting} reporting${item.stale ? ` · ${item.stale} stale` : ""}${item.partial ? ` · ${item.partial} partial` : ""}`;
  const baseline = item.population_configured ? `${item.expected_count} expected` : "Expected population unknown";
  return <button className="coverage-summary-row" onClick={onClick}><span className="coverage-summary-icon"><Icon size={16} /></span><span><b>{label}</b><small>{baseline}</small></span><span className={item.stale || item.partial ? "coverage-status needs-review" : item.reporting ? "coverage-status reporting" : "coverage-status quiet"}>{statusText}</span><ChevronRight size={14} /></button>;
}

type GroupedChange = Change & { occurrences?: number };

function groupChanges(items: Change[]): GroupedChange[] {
  const groups = new Map<string, GroupedChange>();
  for (const item of items) {
    const key = `${item.entity_id}|${item.category}|${item.summary}`;
    const existing = groups.get(key);
    if (existing) existing.occurrences = (existing.occurrences ?? 1) + 1;
    else groups.set(key, { ...item, occurrences: 1 });
  }
  return [...groups.values()].sort((left, right) => Date.parse(right.changed_at) - Date.parse(left.changed_at));
}

function ExecutiveFact({ value, label, tone = "neutral" }: { value: number; label: string; tone?: string }) {
  return <div className={`executive-fact ${tone}`}><strong>{value}</strong><span>{label}</span></div>;
}

function ChangeList({ items, expanded = false }: { items: GroupedChange[]; expanded?: boolean }) {
  if (!items.length) return <Empty icon={History} title="No material changes" detail="Identical scans and routine refreshes are intentionally suppressed." />;
  return <div className={expanded ? "change-list expanded" : "change-list"}>{items.map((item) => <article key={item.id}><span className={`change-mark ${item.category}`}><Activity size={13} /></span><div><p><b>{item.entity_name ?? "Discovered system"}</b><span className="category-pill">{pretty(item.category)}</span></p><h3>{item.summary || pretty(item.event_type)}{(item.occurrences ?? 1) > 1 ? ` · ${item.occurrences} observations` : ""}</h3><small>{pretty(item.system_type ?? item.surface ?? "inventory")} · latest {relative(item.changed_at)}</small>{expanded && item.details?.fields && <div className="field-diffs">{item.details.fields.slice(0, 5).map((field) => <span key={field.path}><code>{pretty(field.path.replace("attributes.", ""))}</code><i>{formatValue(field.before)}</i><ArrowRight size={12} /><b>{formatValue(field.after)}</b></span>)}</div>}</div></article>)}</div>;
}

function ConfidenceSummary({ data }: { data: Record<string, number> }) {
  return <div className="confidence-summary"><span>Evidence confidence</span><div><b><i className="confirmed" />{data.confirmed ?? 0} confirmed</b><b><i className="likely" />{data.likely ?? 0} likely</b><b><i className="possible" />{data.possible ?? 0} possible</b></div></div>;
}

function StateDistribution({ values, total }: { values: Record<string, number>; total: number }) {
  const order = ["running", "deployed", "defined", "configured", "installed", "residual", "cached", "observed"];
  const observed = order.filter((state) => (values[state] ?? 0) > 0);
  return <div className="state-distribution"><div className="state-bar">{observed.map((state) => <i key={state} className={state} style={{ width: `${percent(values[state], total)}%` }} title={`${pretty(state)} ${values[state]}`} />)}</div><div className="state-legend">{observed.map((state) => <div key={state}><span><i className={state} />{pretty(state)}</span><b>{values[state]}</b><small>{percent(values[state], total)}%</small></div>)}</div></div>;
}

function FilterBar({ search, setSearch, hideSearch, children }: { search?: string; setSearch?: (value: string) => void; hideSearch?: boolean; children: ReactNode }) {
  return <section className={hideSearch ? "filter-bar filters-only" : "filter-bar"}>{!hideSearch && <label className="search"><Search size={16} /><input value={search} onChange={(event) => setSearch?.(event.target.value)} placeholder="Search discovered systems" /></label>}<div className="filters">{children}</div></section>;
}

function Select({ label, value = "", onChange, options }: { label: string; value?: string; onChange: (value: string) => void; options: Record<string, string> }) {
  return <label className="select"><span>{label}</span><select value={value} onChange={(event) => onChange(event.target.value)}>{Object.entries(options).map(([key, name]) => <option value={key} key={key}>{name}</option>)}</select><ChevronDown size={13} /></label>;
}

function Identity({ kind, name, detail }: { kind: string; name: string; detail: string }) {
  const Icon = kindIcons[kind] ?? PackageSearch;
  return <span className="identity"><i className={`entity-icon ${kind}`}><Icon size={17} /></i><span><b>{name}</b><small>{detail}</small></span></span>;
}

function TypePill({ value }: { value: string }) { return <span className={`type-pill ${value}`}>{pretty(value)}</span>; }
function StatePill({ state }: { state: string }) { return <span className={`state-pill ${state}`}><i />{pretty(state)}</span>; }
function ConfidencePill({ value }: { value: string }) { return <span className={`confidence-pill ${value}`}><i />{pretty(value)}</span>; }
function Freshness({ value, partial }: { value: string; partial?: boolean }) { return <span className={`freshness ${value}`}><i />{pretty(value)}{partial && <small>Partial scan</small>}</span>; }
function Fact({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><b>{value}</b></div>; }

function ConnectionRow({ item }: { item: Connection }) {
  return <div className="connection-row"><Identity kind={item.entity.kind} name={item.entity.name} detail={item.label === "observed_user" ? "Observed user—not authoritative owner" : pretty(item.relationship_kind)} /><ConfidencePill value={item.confidence} /></div>;
}

function PanelHeading({ title, detail, count, action }: { title: string; detail: string; count?: number; action?: ReactNode }) {
  return <header className="panel-heading"><div><h2>{title}</h2><p>{detail}</p></div>{action ?? (count !== undefined && <span className="panel-count">{count}</span>)}</header>;
}

function Drawer({ children, onClose }: { children: ReactNode; onClose: () => void }) {
  return <div className="drawer-overlay" onMouseDown={(event) => { if (event.currentTarget === event.target) onClose(); }}><aside className="drawer"><button className="drawer-close" onClick={onClose}><X size={18} /></button><div className="drawer-body">{children}</div></aside></div>;
}

function ExportMenu({ api }: { api: API }) {
  const [open, setOpen] = useState(false);
  return <div className="export"><button className="button subtle" onClick={() => setOpen((value) => !value)}><Download size={15} /> Export <ChevronDown size={13} /></button>{open && <div>{(["lens", "ndjson", "cyclonedx"] as const).map((format) => <button key={format} onClick={() => { setOpen(false); api.downloadExport(format); }}>{format === "lens" ? "Lens JSON" : format === "ndjson" ? "NDJSON" : "CycloneDX 1.7"}</button>)}</div>}</div>;
}

function About({ onClose }: { onClose: () => void }) {
  return <div className="modal-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}><section className="about-modal"><button onClick={onClose}><X size={18} /></button><Brand /><p className="eyebrow">PRODUCT BOUNDARY</p><h2>Lens discovers and assesses exposure.</h2><p>It observes, normalizes, correlates, and reports factual inventory, operator context, and clearly labelled catalogue potential.</p><div className="boundary-grid"><span><CheckCircle2 size={15} /> Discovers systems and connections</span><span><CheckCircle2 size={15} /> Preserves sanitized evidence</span><span><CheckCircle2 size={15} /> Explains categorical findings</span><span><X size={15} /> No composite risk score</span><span><X size={15} /> No authorization verification or invocation</span><span><X size={15} /> No remediation or enforcement</span></div><small>Discovery Snapshot 1.1 remains unchanged. Exposure is a Hub-side derived projection.</small></section></div>;
}

function Loading() { return <div className="loading"><Radar size={25} /><span>Resolving discovery posture…</span></div>; }
function InlineLoading() { return <div className="inline-loading"><RefreshCw size={14} /> Loading…</div>; }
function InlineError({ text }: { text: string }) { return <div className="inline-error"><AlertCircle size={15} />{text}</div>; }
function Failure({ error, retry }: { error: string; retry: () => void }) { return <div className="failure"><AlertCircle size={24} /><h2>Lens could not load this view</h2><p>{error}</p><button className="button subtle" onClick={retry}>Try again</button></div>; }
function Empty({ icon: Icon, title, detail }: { icon: LucideIcon; title: string; detail: string }) { return <div className="empty"><Icon size={23} /><b>{title}</b><p>{detail}</p></div>; }

function Brand() { return <div className="brand"><span className="logo"><i /><i /><i /></span><span><b>BARRIKADE</b><small>LENS</small></span></div>; }

function useRemote<T>(factory: () => Promise<T>, dependencies: unknown[]) {
  const [data, setData] = useState<T>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    let active = true;
    setLoading(true); setError("");
    factory().then((value) => { if (active) setData(value); }).catch((reason) => { if (active) setError(String(reason)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
    // The caller owns a stable API instance or explicitly lists its dependencies.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...dependencies, revision]);
  return { data, loading, error, reload: () => setRevision((value) => value + 1) };
}

function groupConnections(items: Connection[]) {
  return items.reduce<Record<string, Connection[]>>((groups, item) => {
    const key = item.label === "observed_user" ? "observed_users" : item.entity.kind;
    (groups[key] ??= []).push(item);
    return groups;
  }, {});
}

function pretty(value: string) { return value.replace(/[._-]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase()); }
function sum(values: number[]) { return values.reduce((total, value) => total + value, 0); }
function percent(value: number, total: number) { return total ? Math.round((value / total) * 100) : 0; }
function formatValue(value: unknown) { if (value === undefined || value === null || value === "") return "Not observed"; if (typeof value === "boolean") return value ? "Yes" : "No"; if (Array.isArray(value)) return value.join(", "); if (typeof value === "object") return "Structured value"; return String(value); }
function relative(value: string) { const time = new Date(value).getTime(); if (!Number.isFinite(time)) return "Unknown"; const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000)); if (seconds < 60) return "Just now"; if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`; if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`; return `${Math.floor(seconds / 86400)}d ago`; }
function randomURLSafe(length: number) { const bytes = crypto.getRandomValues(new Uint8Array(length)); return base64URL(bytes).slice(0, length); }
function base64URL(bytes: Uint8Array) { let value = ""; bytes.forEach((byte) => { value += String.fromCharCode(byte); }); return btoa(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); }
