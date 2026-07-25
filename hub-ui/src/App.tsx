import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Activity, AlertCircle, ArrowRight, Bot, Boxes, BrainCircuit, CheckCircle2, ChevronDown,
  ChevronRight, CircleDot, Container, Copy, Database, Download, GitBranch, History,
  LayoutDashboard, Link2, LogOut, Menu, Monitor, Network, PackageSearch, PlugZap, Radar,
  RefreshCw, Search, Server, SlidersHorizontal,
  TerminalSquare, UserRound, Workflow, X, type LucideIcon,
} from "lucide-react";
import {
  API, authConfig, exchangeOIDC, type AuthConfig, type Change, type Connection, type Entity,
  type EntityDetail, type Overview, type SystemDetail, type SystemItem, type Target,
} from "./api";

type Page = "Overview" | "Systems" | "Coverage" | "Changes" | "Technical inventory" | "Evidence graph";

const navigation: Array<{ page: Page; icon: LucideIcon; detail: string }> = [
  { page: "Overview", icon: LayoutDashboard, detail: "Discovery posture" },
  { page: "Systems", icon: Bot, detail: "Root systems" },
  { page: "Coverage", icon: Radar, detail: "Targets and freshness" },
  { page: "Changes", icon: History, detail: "Material change" },
  { page: "Technical inventory", icon: Boxes, detail: "Complete entity set" },
  { page: "Evidence graph", icon: Network, detail: "Facts and connections" },
];

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: { eyebrow: "DISCOVERY", title: "Overview", detail: "A current, evidence-backed view of your autonomous system footprint." },
  Systems: { eyebrow: "INVENTORY", title: "Systems", detail: "Agents, agent-capable tools, and model runtimes—without supporting software or cached artifacts." },
  Coverage: { eyebrow: "VISIBILITY", title: "Coverage", detail: "Where Lens is reporting, where data is stale, and where expected population is unknown." },
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
  const [menuOpen, setMenuOpen] = useState(false);
  const [about, setAbout] = useState(false);
  const [revision, setRevision] = useState(0);
  const copy = pageCopy[page];
  return <div className="app-shell">
    <aside className={menuOpen ? "sidebar open" : "sidebar"}>
      <div className="sidebar-brand"><Brand /></div>
      <nav className="main-nav">
        {navigation.map(({ page: item, icon: Icon, detail }) => <button key={item} className={page === item ? "active" : ""} onClick={() => { setPage(item); setMenuOpen(false); }}>
          <Icon size={17} /><span><b>{item}</b><small>{detail}</small></span>{page === item && <ChevronRight size={14} />}
        </button>)}
      </nav>
      <div className="sidebar-footer">
        <button onClick={() => setAbout(true)}><CircleDot size={14} /> Discover only</button>
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
        {page === "Systems" && <SystemsPage api={api} revision={revision} />}
        {page === "Coverage" && <CoveragePage api={api} revision={revision} />}
        {page === "Changes" && <ChangesPage api={api} revision={revision} />}
        {page === "Technical inventory" && <InventoryPage api={api} revision={revision} />}
        {page === "Evidence graph" && <EvidenceGraphPage api={api} revision={revision} />}
      </div>
    </main>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} />}
    {about && <About onClose={() => setAbout(false)} />}
  </div>;
}

function OverviewPage({ api, revision, go }: { api: API; revision: number; go: (page: Page) => void }) {
  const [window, setWindow] = useState("7d");
  const remote = useRemote(() => api.overview(window), [api, revision, window]);
  if (remote.loading) return <Loading />;
  if (remote.error || !remote.data) return <Failure error={remote.error} retry={remote.reload} />;
  const data = remote.data;
  const systems = data.footprint.system_types;
  const states = data.footprint.states;
  const totalSystems = sum(Object.values(systems));
  const attention = [
    ["Non-loopback services", data.attention.non_loopback_services, "Observed beyond a loopback interface", "Systems"],
    ["Possible-only systems", data.attention.possible_only_systems, "Evidence needs corroboration", "Systems"],
    ["Ownership not established", data.attention.unattributed_systems, "No authoritative ownership evidence", "Systems"],
    ["Partial scans", data.attention.partial_scans, "Some locations or detectors were unavailable", "Coverage"],
    ["Stale targets", data.attention.stale_targets, "Outside the freshness threshold", "Coverage"],
    ["Identity diagnostics", data.attention.possible_duplicate_identities, "Distinct identities share a display name", "Coverage"],
    ["Conflicting facts", data.attention.fact_conflicts, "Sources disagree on a material fact", "Systems"],
  ].filter((item) => Number(item[1]) > 0) as Array<[string, number, string, Page]>;
  const reportingTargets = sum(data.coverage.map((item) => item.reporting));
  const staleTargets = sum(data.coverage.map((item) => item.stale));
  const running = states.running ?? 0;

  return <div className="page-stack">
    <div className="overview-toolbar"><span>Updated {relative(data.generated_at)}</span><div className="window-switch">{["24h", "7d", "30d"].map((item) => <button className={window === item ? "active" : ""} onClick={() => setWindow(item)} key={item}>{item}</button>)}</div></div>
    <section className="posture-summary">
      <div className="posture-total"><span>CURRENT FOOTPRINT</span><p><strong>{totalSystems.toLocaleString()}</strong> distinct systems</p><small>{running} running now · {reportingTargets} reporting {reportingTargets === 1 ? "target" : "targets"}{staleTargets ? ` · ${staleTargets} stale` : ""}</small></div>
      <div className="system-breakdown">
        <button onClick={() => go("Systems")}><Bot size={17} /><span><b>{systems.autonomous_agent ?? 0}</b><small>Autonomous agents</small></span></button>
        <button onClick={() => go("Systems")}><TerminalSquare size={17} /><span><b>{systems.agent_tool ?? 0}</b><small>Agent-capable tools</small></span></button>
        <button onClick={() => go("Systems")}><BrainCircuit size={17} /><span><b>{systems.model_runtime ?? 0}</b><small>Model runtimes</small></span></button>
      </div>
    </section>
    <div className="overview-primary">
      <section className="panel state-panel"><PanelHeading title="Operating state" detail="Strongest state observed for each system" />
        <StateDistribution values={states} total={totalSystems} />
        <ConfidenceSummary data={data.data_quality.confidence} />
      </section>
      <section className="panel attention-panel"><PanelHeading title="Review queue" detail="Facts that need context—not a risk score" />
        {attention.length ? <div className="attention-list">{attention.map(([label, count, detail, destination]) => <button key={label} onClick={() => go(destination)}>
          <span className={count ? "attention-count active" : "attention-count"}>{count}</span><span><b>{label}</b><small>{detail}</small></span><ChevronRight size={14} />
        </button>)}</div> : <Empty icon={CheckCircle2} title="Nothing needs review" detail="No stale, partial, conflicting, or possible-only findings in this window." />}
      </section>
    </div>
    <div className="overview-secondary">
      <section className="panel overview-coverage"><PanelHeading title="Coverage" detail="Reporting targets by discovery surface" action={<button className="text-button" onClick={() => go("Coverage")}>Open coverage <ArrowRight size={13} /></button>} />
        <div className="coverage-summary-list">{data.coverage.map((item) => <CoverageSummaryRow item={item} key={item.target_type} onClick={() => go("Coverage")} />)}</div>
      </section>
      <section className="panel change-panel"><PanelHeading title="Meaningful changes" detail={`Material changes in the last ${window}`} action={<button className="text-button" onClick={() => go("Changes")}>View all <ArrowRight size={13} /></button>} />
        <ChangeList items={data.changes.slice(0, 4)} />
      </section>
    </div>
  </div>;
}

function SystemsPage({ api, revision }: { api: API; revision: number }) {
  const [filters, setFilters] = useState<Record<string, string>>({ sort: "last_seen" });
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
    </FilterBar>
    <section className="panel data-panel"><div className="table-summary"><span><b>{items.length}</b> systems</span></div>
      <div className="system-table table-scroll"><div className="system-row table-head"><span>System</span><span>Type</span><span>State</span><span>Target / surface</span><span>Attribution</span><span>Evidence</span><span /></div>
        {items.map((item) => <button className="system-row" key={item.id} onClick={() => setSelected(item.id)}>
          <Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} />
          <TypePill value={item.system_type} /><StatePill state={item.state} /><span className="stacked"><b>{item.target_name ?? "Unresolved target"}</b><small>{pretty(item.surface)}</small></span>
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
  const [enrollment, setEnrollment] = useState<{ code: string; expires_at: string }>();
  const [enrollError, setEnrollError] = useState("");
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
    <section className="panel enrollment-panel"><div><p className="eyebrow">EXPAND COVERAGE</p><h2>Enroll a managed endpoint</h2><p>Generate a single-device code valid for ten minutes. The endpoint creates its own persistent signing identity and keeps it across credential rotation.</p></div>
      <div className="enrollment-action">{enrollment ? <div className="enrollment-code"><span>{enrollment.code}</span><button onClick={() => navigator.clipboard.writeText(enrollment.code)}><Copy size={15} /> Copy</button><small>Expires {new Date(enrollment.expires_at).toLocaleTimeString()}</small></div> : <button className="button primary" onClick={() => { setEnrollError(""); api.enrollment().then(setEnrollment).catch((reason) => setEnrollError(String(reason))); }}>Generate enrollment code</button>}{enrollError && <InlineError text={enrollError} />}</div>
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
  const [filters, setFilters] = useState<Record<string, string>>({ sort: "last_seen" });
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
  </FilterBar>
    <section className="panel data-panel"><div className="table-summary"><span><b>{items.length}</b> entities loaded</span><span>This technical view includes supporting and cached inventory</span></div>
      <div className="inventory-table table-scroll"><div className="inventory-row table-head"><span>Entity</span><span>Graph role</span><span>State</span><span>Surface</span><span>Confidence</span><span>Last observed</span><span /></div>
        {items.map((item) => <button className="inventory-row" key={item.id} onClick={() => setSelected(item.id)}><Identity kind={item.kind} name={item.name} detail={item.canonical_key ?? item.id} /><span className="kind-label">{pretty(item.posture?.system_role ?? item.kind)}</span><StatePill state={item.posture?.state ?? "observed"} /><span>{pretty(item.posture?.surface ?? "unknown")}</span><ConfidencePill value={item.confidence} /><span className="observed">{relative(item.last_seen_at)}</span><ChevronRight size={15} /></button>)}
      </div>
      {remote.loading && <InlineLoading />}{remote.error && <InlineError text={remote.error} />}{remote.data?.next_cursor && !remote.loading && <button className="load-more" onClick={() => setCursor(remote.data!.next_cursor!)}>Load more entities <ChevronDown size={15} /></button>}
    </section>
    {selected && <EntityDrawer api={api} id={selected} onClose={() => setSelected(undefined)} />}
  </div>;
}

function EvidenceGraphPage({ api, revision }: { api: API; revision: number }) {
  const systems = useRemote(() => api.systems({ limit: 50, sort: "name" }), [api, revision]);
  const [selected, setSelected] = useState<string>();
  const detail = useRemote(() => selected ? api.system(selected) : Promise.resolve(undefined), [api, selected]);
  useEffect(() => { if (!selected && systems.data?.items[0]) setSelected(systems.data.items[0].id); }, [selected, systems.data]);
  if (systems.loading) return <Loading />;
  if (systems.error || !systems.data) return <Failure error={systems.error} retry={systems.reload} />;
  return <div className="graph-layout">
    <section className="panel graph-selector"><PanelHeading title="Choose a root system" detail="The graph is fetched on demand, one system at a time" />
      <div className="graph-system-list">{systems.data.items.map((item) => <button className={selected === item.id ? "active" : ""} key={item.id} onClick={() => setSelected(item.id)}><Identity kind={item.kind} name={item.name} detail={pretty(item.system_type)} /><ChevronRight size={14} /></button>)}</div>
    </section>
    <section className="panel graph-canvas">{detail.loading ? <InlineLoading /> : detail.error ? <InlineError text={detail.error} /> : detail.data ? <SystemGraph detail={detail.data} /> : <Empty icon={Network} title="No system selected" detail="Choose a root system to inspect its evidence graph." />}</section>
  </div>;
}

function SystemGraph({ detail }: { detail: SystemDetail }) {
  const groups = groupConnections(detail.connections);
  return <div><PanelHeading title={detail.name} detail={`${pretty(detail.system_type)} · ${pretty(detail.state)} · ${detail.target_name ?? "Unresolved target"}`} action={<ConfidencePill value={detail.confidence} />} />
    <div className="graph-root"><Identity kind={detail.kind} name={detail.name} detail={detail.product_id ?? detail.id} /><span><StatePill state={detail.state} /><span className="network-pill">{pretty(detail.network_scope)}</span></span></div>
    <div className="graph-branches">{Object.entries(groups).map(([label, connections]) => <div className="graph-branch" key={label}><div className="branch-line" /><p>{pretty(label)} <span>{connections.length}</span></p><div className="graph-nodes">{connections.map((connection) => <div className="graph-node" key={connection.relationship_id}><Identity kind={connection.entity.kind} name={connection.entity.name} detail={connection.label === "observed_user" ? "Observed user" : pretty(connection.relationship_kind)} /><ConfidencePill value={connection.confidence} /></div>)}</div></div>)}</div>
    <div className="graph-evidence"><p>EVIDENCE FACTS <span>{detail.evidence.length}</span></p>{detail.evidence.slice(0, 12).map((evidence) => <div key={`${evidence.source_id}:${evidence.id}`}><span><b>{evidence.detector_id}</b><small>{evidence.family} · {evidence.method}{evidence.observations > 1 ? ` · ${evidence.observations} observations` : ""}</small></span><code>{evidence.locator ?? "sanitized locator unavailable"}</code><ConfidencePill value={evidence.specificity === "high" ? "confirmed" : evidence.specificity === "medium" ? "likely" : "possible"} /></div>)}</div>
  </div>;
}

function SystemDrawer({ api, id, onClose }: { api: API; id: string; onClose: () => void }) {
  const remote = useRemote(() => api.system(id), [api, id]);
  return <Drawer onClose={onClose}>{remote.loading ? <Loading /> : remote.error || !remote.data ? <Failure error={remote.error} retry={remote.reload} /> : <SystemDetailView item={remote.data} />}</Drawer>;
}

function SystemDetailView({ item }: { item: SystemDetail }) {
  const groups = groupConnections(item.connections);
  return <><div className="drawer-title"><Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} /><div><TypePill value={item.system_type} /><StatePill state={item.state} /><ConfidencePill value={item.confidence} /></div></div>
    <div className="fact-grid"><Fact label="Target" value={item.target_name ?? "Unresolved"} /><Fact label="Surface" value={pretty(item.surface)} /><Fact label="Network scope" value={pretty(item.network_scope)} /><Fact label="Attribution" value={item.attributed ? "Authoritative" : "Not established"} /><Fact label="First discovered" value={relative(item.first_seen_at)} /><Fact label="Last observed" value={relative(item.last_seen_at)} /></div>
    <section className="drawer-section"><h3>Connected inventory <span>{item.connections.length}</span></h3>{Object.entries(groups).map(([group, values]) => <div className="connection-group" key={group}><p>{pretty(group)}</p>{values.map((connection) => <ConnectionRow item={connection} key={connection.relationship_id} />)}</div>)}</section>
    <section className="drawer-section"><h3>Evidence <span>{item.evidence.length}</span></h3>{item.evidence.map((evidence) => <div className="evidence-row" key={`${evidence.source_id}:${evidence.id}`}><span><b>{evidence.detector_id}</b><small>{evidence.family} · {evidence.specificity}</small></span><code>{evidence.locator ?? "No locator"}</code><time>{relative(evidence.observed_at)}{evidence.observations > 1 ? ` · ${evidence.observations}×` : ""}</time></div>)}</section>
  </>;
}

function EntityDrawer({ api, id, onClose }: { api: API; id: string; onClose: () => void }) {
  const remote = useRemote(() => api.entity(id), [api, id]);
  return <Drawer onClose={onClose}>{remote.loading ? <Loading /> : remote.error || !remote.data ? <Failure error={remote.error} retry={remote.reload} /> : <EntityDetailView item={remote.data} />}</Drawer>;
}

function EntityDetailView({ item }: { item: EntityDetail }) {
  return <><div className="drawer-title"><Identity kind={item.kind} name={item.name} detail={item.canonical_key ?? item.id} /><div><StatePill state={item.posture?.state ?? "observed"} /><ConfidencePill value={item.confidence} /></div></div>
    <div className="fact-grid"><Fact label="Kind" value={pretty(item.kind)} /><Fact label="Graph role" value={pretty(item.posture?.system_role ?? "unknown")} /><Fact label="Surface" value={pretty(item.posture?.surface ?? "unknown")} /><Fact label="Network scope" value={pretty(item.posture?.network_scope ?? "unknown")} /><Fact label="First discovered" value={relative(item.first_seen_at)} /><Fact label="Last observed" value={relative(item.last_seen_at)} /></div>
    <section className="drawer-section"><h3>Sanitized attributes</h3><pre>{JSON.stringify(item.attributes, null, 2)}</pre></section>
    <section className="drawer-section"><h3>Evidence <span>{item.evidence?.length ?? 0}</span></h3>{item.evidence?.map((evidence) => <div className="evidence-row" key={`${evidence.source_id}:${evidence.id}`}><span><b>{evidence.detector_id}</b><small>{evidence.family} · {evidence.specificity}</small></span><code>{evidence.locator ?? "No locator"}</code><time>{relative(evidence.observed_at)}{evidence.observations > 1 ? ` · ${evidence.observations}×` : ""}</time></div>)}</section>
  </>;
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

function ChangeList({ items, expanded = false }: { items: Change[]; expanded?: boolean }) {
  if (!items.length) return <Empty icon={History} title="No material changes" detail="Identical scans and routine refreshes are intentionally suppressed." />;
  return <div className={expanded ? "change-list expanded" : "change-list"}>{items.map((item) => <article key={item.id}><span className={`change-mark ${item.category}`}><Activity size={13} /></span><div><p><b>{item.entity_name ?? "Discovered system"}</b><span className="category-pill">{pretty(item.category)}</span></p><h3>{item.summary || pretty(item.event_type)}</h3><small>{pretty(item.system_type ?? item.surface ?? "inventory")} · {relative(item.changed_at)}</small>{expanded && item.details?.fields && <div className="field-diffs">{item.details.fields.slice(0, 5).map((field) => <span key={field.path}><code>{pretty(field.path.replace("attributes.", ""))}</code><i>{formatValue(field.before)}</i><ArrowRight size={12} /><b>{formatValue(field.after)}</b></span>)}</div>}</div></article>)}</div>;
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
  return <div className="modal-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}><section className="about-modal"><button onClick={onClose}><X size={18} /></button><Brand /><p className="eyebrow">PRODUCT BOUNDARY</p><h2>Lens is the discovery layer.</h2><p>It observes, normalizes, correlates, and reports factual inventory across endpoints, source repositories, and Kubernetes.</p><div className="boundary-grid"><span><CheckCircle2 size={15} /> Discovers systems and capabilities</span><span><CheckCircle2 size={15} /> Preserves sanitized evidence</span><span><CheckCircle2 size={15} /> Reports coverage and change</span><span><X size={15} /> No risk scores or grades</span><span><X size={15} /> No approval or remediation</span><span><X size={15} /> No blocking or governance</span></div><small>Raw Lens JSON remains the canonical discovery contract. Executive posture is a derived projection.</small></section></div>;
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
