import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ClerkProvider, OrganizationSwitcher, SignIn as ClerkSignIn, SignUp as ClerkSignUp, UserButton,
  useAuth, useClerk, useOrganization, useOrganizationList,
} from "@clerk/react";
import {
  Activity, AlertCircle, ArrowRight, Bot, BrainCircuit, CheckCircle2, ChevronDown, ChevronRight,
  CircleDot, Cloud, Container, Copy, Download, FileSearch, Fingerprint, GitBranch, History,
  LayoutDashboard, LogOut, MapPin, Menu, Monitor, Network, PackageSearch, Plus, Radar, RefreshCw,
  ShieldCheck, SlidersHorizontal, TerminalSquare, UserRound, X, type LucideIcon,
} from "lucide-react";
import {
  API, authConfig, exchangeOIDC, type AuthConfig, type Change, type Evidence,
  type Environment, type EnvironmentKind, type EnvironmentScan, type ExposureFinding, type Overview, type ProductItem, type SetupSession, type SystemDetail, type SystemItem,
} from "./api";
import { captureAnalytics, configureAnalytics, resetAnalytics, semanticPage } from "./analytics";
import { DesignPartnerTerms, PrivacyNotice } from "./Legal";
import {
  Brand, ConfidencePill, ConnectionRow, CopyBlock, Drawer, Empty, Fact, Failure, FilterBar,
  Freshness, Identity, InlineError, InlineLoading, Loading, PanelHeading, Select, StatePill,
  TypePill, formatValue, groupConnections, percent, pretty, relative, sum, useRemote,
} from "./ui";
const EvidenceGraphPage = lazy(() => import("./EvidenceGraph").then((module) => ({ default: module.EvidenceGraphPage })));

type Page = "Overview" | "Findings" | "Inventory" | "Connections" | "Changes" | "Evidence" | "Settings";

const navigation: Array<{ page: Page; icon: LucideIcon; detail: string }> = [
  { page: "Overview", icon: LayoutDashboard, detail: "Organization posture" },
  { page: "Findings", icon: FileSearch, detail: "Prioritized action" },
  { page: "Inventory", icon: Bot, detail: "Known AI systems" },
  { page: "Connections", icon: Cloud, detail: "Coverage and setup" },
];

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: { eyebrow: "DISCOVERY", title: "Organization AI posture", detail: "Evidence-backed visibility across connected cloud accounts, endpoints, repositories, and clusters." },
  Findings: { eyebrow: "ATTENTION", title: "Findings", detail: "Workspace-wide priorities ranked by severity, freshness, ownership, and latest observation." },
  Inventory: { eyebrow: "INVENTORY", title: "Organization inventory", detail: "Products grouped across the organization, with every endpoint installation and its evidence one level below." },
  Connections: { eyebrow: "VISIBILITY", title: "Connections", detail: "Connect endpoints, resume setup, and understand reporting coverage." },
  Changes: { eyebrow: "HISTORY", title: "Changes", detail: "Material inventory changes. Routine scan refreshes are suppressed." },
  Evidence: { eyebrow: "EVIDENCE", title: "Evidence graph", detail: "Trace a system to its capabilities, deployment surfaces, observed users, and sanitized evidence." },
  Settings: { eyebrow: "ACCOUNT", title: "Account settings", detail: "Manage your Lens identity and workspace lifecycle." },
};

const pagePath: Record<Page, string> = { Overview: "/overview", Findings: "/findings", Inventory: "/inventory", Connections: "/connections", Changes: "/changes", Evidence: "/systems/evidence", Settings: "/settings" };

function pageForPath(path: string): Page {
  if (path.startsWith("/findings")) return "Findings";
  if (path.startsWith("/inventory") || /^\/systems\/[^/]+$/.test(path)) return "Inventory";
  if (path.startsWith("/connections")) return "Connections";
  if (path.startsWith("/changes")) return "Changes";
  if (path.endsWith("/evidence")) return "Evidence";
  if (path.startsWith("/settings")) return "Settings";
  return "Overview";
}

export function App() {
  return <BrowserRouter><Application /></BrowserRouter>;
}

function Application() {
  if (location.pathname === "/install") return <PublicEndpointInstall />;
  if (location.pathname === "/privacy") return <PrivacyNotice />;
  if (location.pathname === "/terms") return <DesignPartnerTerms />;
  const [config, setConfig] = useState<AuthConfig>();
  const [configurationError, setConfigurationError] = useState("");
  useEffect(() => { authConfig().then(setConfig).catch((reason) => setConfigurationError(String(reason))); }, []);
  if (configurationError) return <Failure error={configurationError} retry={() => location.reload()} />;
  if (!config) return <Loading />;
  if (location.protocol === "https:" && config.public_url) {
    const canonical = new URL(`${location.pathname}${location.search}${location.hash}`, config.public_url);
    if (canonical.origin !== location.origin) {
      location.replace(canonical.toString());
      return <Loading />;
    }
  }
  if (config.mode === "clerk" && config.clerk_publishable_key) {
    return <ClerkProvider publishableKey={config.clerk_publishable_key}><ClerkApplication config={config} /></ClerkProvider>;
  }
  return <LegacyApplication config={config} />;
}

function LegacyApplication({ config }: { config: AuthConfig }) {
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

  if (!token) return <LegacySignIn config={config} onToken={saveToken} authError={authError} />;
  return <Shell api={new API(token)} signOut={() => { resetAnalytics(); sessionStorage.removeItem("lens-token"); setToken(""); }} selfServe={config.self_serve_enabled} />;
}

function ClerkApplication({ config }: { config: AuthConfig }) {
  const { isLoaded, isSignedIn, getToken } = useAuth();
  const { signOut } = useClerk();
  const { organization } = useOrganization();
  const memberships = useOrganizationList({ userMemberships: { infinite: true } });
  const [bootstrapped, setBootstrapped] = useState("");
  const [workspaceName, setWorkspaceName] = useState("");
  const [error, setError] = useState("");
  const api = useMemo(() => new API(() => getToken()), [getToken]);

  useEffect(() => {
    if (!isLoaded || !isSignedIn || organization || !memberships.isLoaded || bootstrapped === "creating") return;
    setBootstrapped("creating");
    const existing = memberships.userMemberships.data?.[0]?.organization;
    if (!existing) { setBootstrapped("needs-workspace"); return; }
    Promise.resolve(memberships.setActive?.({ organization: existing.id })).catch((reason) => { setError(String(reason)); setBootstrapped(""); });
  }, [bootstrapped, isLoaded, isSignedIn, memberships, organization]);

  useEffect(() => {
    if (!organization || bootstrapped === organization.id) return;
    api.bootstrapWorkspace(organization.name).then(() => setBootstrapped(organization.id)).catch((reason) => setError(String(reason)));
  }, [api, bootstrapped, organization]);

  if (!isLoaded) return <Loading />;
  if (!isSignedIn) return <ManagedSignIn />;
  if (error) return <Failure error={error} retry={() => { setError(""); setBootstrapped(""); }} />;
  if (!organization && bootstrapped === "needs-workspace") return <main className="signin"><section className="signin-story"><Brand /><div className="signin-copy"><span className="product-kicker"><Radar size={14} /> Set up Lens</span><h1>Name your security workspace.</h1><p>This name identifies the organization whose endpoint footprint Lens will assess.</p></div></section><section className="signin-access"><form className="access-card" onSubmit={(event) => { event.preventDefault(); const value = workspaceName.trim(); if (!value) return; setBootstrapped("creating"); Promise.resolve(memberships.createOrganization?.({ name: value })).then((created) => created && memberships.setActive?.({ organization: created.id })).catch((reason) => { setError(String(reason)); setBootstrapped("needs-workspace"); }); }}><p className="eyebrow">WORKSPACE</p><h2>Organization name</h2><label>Name<input value={workspaceName} maxLength={128} required autoFocus onChange={(event) => setWorkspaceName(event.target.value)} placeholder="Acme Security" /></label><button className="button primary full">Create workspace <ArrowRight size={16} /></button></form></section></main>;
  if (!organization || bootstrapped !== organization.id) return <Loading />;
  const organizationControl = <OrganizationSwitcher hidePersonal organizationProfileMode="modal" afterCreateOrganizationUrl="/" afterSelectOrganizationUrl="/" />;
  const userControl = <UserButton userProfileMode="modal" />;
  return <Shell api={api} signOut={() => { resetAnalytics(); return signOut(); }} organizationControl={organizationControl} userControl={userControl} selfServe={config.self_serve_enabled} analyticsConfig={config.analytics} />;
}

function ManagedSignIn() {
  const [signUp, setSignUp] = useState(() => location.hash.includes("sign-up"));
  useEffect(() => {
    const changed = () => setSignUp(location.hash.includes("sign-up"));
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, []);
  const appearance = { variables: { colorPrimary: "#ff6b00", colorBackground: "#131313", colorText: "#ffffff", colorInputBackground: "#0c0c0c", colorInputText: "#ffffff" } };
  return <main className="signin managed-signin">
    <section className="signin-story"><Brand /><div className="signin-copy"><span className="product-kicker"><Radar size={14} /> Autonomous agent discovery</span><h1>Bring the agent footprint into focus.</h1><p>Start free, connect the environments you choose, and see evidence-backed results without a score or write access.</p></div></section>
    <section className="signin-access"><div className="managed-auth"><div className="managed-auth-tabs"><button className={!signUp ? "active" : ""} onClick={() => { location.hash = "sign-in"; setSignUp(false); }}>Sign in</button><button className={signUp ? "active" : ""} onClick={() => { location.hash = "sign-up"; setSignUp(true); }}>Create account</button></div>{signUp ? <ClerkSignUp routing="hash" signInUrl="#sign-in" appearance={appearance} /> : <ClerkSignIn routing="hash" signUpUrl="#sign-up" appearance={appearance} />}<p className="auth-legal">{signUp ? <>By creating an account, you agree to the <a href="/terms">design partner terms</a> and acknowledge the <a href="/privacy">Lens privacy notice</a>.</> : <>Managed Lens is governed by the <a href="/terms">design partner terms</a> and <a href="/privacy">privacy notice</a>.</>}</p></div></section>
  </main>;
}

function PublicEndpointInstall() {
  const [token] = useState(() => decodeURIComponent(location.hash.replace(/^#token=/, "")));
  const [platform, setPlatform] = useState<"macos" | "windows" | "linux" | "">("");
  const [result, setResult] = useState<{ workspace_name: string; environment_name: string; command: string; expires_at: string; prerequisites: string[]; what_lens_reads: string[]; excluded: string[] }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { history.replaceState({}, "", "/install"); }, []);
  const generate = async () => {
    if (!token || !platform) return;
    setBusy(true); setError("");
    try {
      const response = await fetch("/v1/public/endpoint-handoffs/resolve", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ token, platform }) });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body?.error?.message || "This setup link is unavailable");
      setResult(body);
    } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
  };
  return <main className="public-install"><header><Brand /><span>Delegated endpoint setup</span></header><section className="public-install-card"><p className="eyebrow">BARRIKADE LENS</p><h1>{result ? `Install for ${result.environment_name}` : "Choose this endpoint's platform"}</h1><p>{result ? `${result.workspace_name} has authorized a read-only Lens installation.` : "The command is generated only after you select a platform. It expires in 15 minutes and can be used once."}</p>{!result && <><div className="platform-picker" aria-label="Endpoint platform">{(["macos", "windows", "linux"] as const).map((value) => <button key={value} aria-pressed={platform === value} className={platform === value ? "active" : ""} onClick={() => setPlatform(value)}>{value === "macos" ? "macOS" : pretty(value)}</button>)}</div><button className="button primary full" disabled={!platform || busy || !token} onClick={generate}>{busy ? "Generating…" : "Generate single-use command"}</button></>}{result && <><div className="wizard-boundary"><ShieldCheck size={18} /><p><b>Read-only discovery</b><span>Lens inventories software, runtime state, network listeners, and configuration references. It excludes prompts, outputs, secret values, and file contents.</span></p></div><CopyBlock value={result.command} /><div className="setup-read"><div><h3>Requirements</h3>{result.prerequisites.map((item) => <span key={item}><CheckCircle2 size={14} />{item}</span>)}</div><div><h3>Credential</h3><span><CheckCircle2 size={14} />Single use</span><span><CheckCircle2 size={14} />Expires {new Date(result.expires_at).toLocaleTimeString()}</span></div></div></>}{error && <InlineError text={error} />}</section></main>;
}

function LegacySignIn({ config, onToken, authError }: { config: AuthConfig; onToken: (token: string) => void; authError: string }) {
  const [value, setValue] = useState("");
  const error = authError;

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

function Shell({ api, signOut, organizationControl, userControl, selfServe = true, analyticsConfig }: { api: API; signOut: () => void; organizationControl?: ReactNode; userControl?: ReactNode; selfServe?: boolean; analyticsConfig?: AuthConfig["analytics"] }) {
  const location = useLocation();
  const navigate = useNavigate();
  const page = pageForPath(location.pathname);
  const [menuOpen, setMenuOpen] = useState(false);
  const [revision, setRevision] = useState(0);
  const [exposureEnabled, setExposureEnabled] = useState(false);
	const analyticsSession = useRemote(() => api.session(), [api]);
  useEffect(() => { authConfig().then((config) => setExposureEnabled(config.exposure_enabled)).catch(() => setExposureEnabled(false)); }, []);
	useEffect(() => {
		configureAnalytics(analyticsConfig, analyticsSession.data?.analytics);
		const lensPage = semanticPage(location.pathname);
		if (lensPage && analyticsSession.data?.analytics.enabled) captureAnalytics({ name: "lens_page_viewed", properties: { lens_page: lensPage } });
	}, [analyticsConfig, analyticsSession.data?.analytics, location.pathname]);
  const copy = pageCopy[page];
  return <div className="app-shell">
    <aside className={menuOpen ? "sidebar open" : "sidebar"}>
      <div className="sidebar-brand"><Brand /></div>
      <nav className="main-nav">
        {navigation.filter((item) => (item.page !== "Findings" || exposureEnabled) && (item.page !== "Connections" || selfServe)).map(({ page: item, icon: Icon, detail }) => <button key={item} className={page === item ? "active" : ""} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "navigation", interaction: "open", control: "navigation" } }); navigate(pagePath[item]); setMenuOpen(false); }}>
          <Icon size={17} /><span><b>{item}</b><small>{detail}</small></span>{page === item && <ChevronRight size={14} />}
        </button>)}
      </nav>
      <div className="sidebar-footer">
        <div className="sidebar-account">
          {(organizationControl || userControl) && <div className="sidebar-account-primary">
            {organizationControl && <div className="sidebar-organization">{organizationControl}</div>}
            {userControl && <div className="sidebar-user">{userControl}</div>}
          </div>}
          <div className="sidebar-footer-actions">
            <button onClick={() => navigate("/settings")}><UserRound size={15} /> Account settings</button>
            <button className="logout" onClick={signOut} aria-label="Sign out" title="Sign out"><LogOut size={16} /></button>
          </div>
        </div>
        <div className="sidebar-legal"><a href="/privacy">Privacy</a><a href="/terms">Terms</a></div>
      </div>
    </aside>
    <main className="main-area">
      <div className="workspace">
        <header className="page-heading">
          <button className="mobile-menu" aria-label={menuOpen ? "Close navigation" : "Open navigation"} onClick={() => setMenuOpen((value) => !value)}>{menuOpen ? <X size={20} /> : <Menu size={20} />}</button>
          <div><p className="eyebrow">{copy.eyebrow}</p><h1>{copy.title}</h1><p>{copy.detail}</p></div>
          <div className="page-actions"><NotificationBell api={api} revision={revision} onOpen={() => navigate("/connections")} /><button className="icon-button" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: semanticPage(location.pathname) ?? "navigation", interaction: "refresh" } }); setRevision((value) => value + 1); }} title="Refresh"><RefreshCw size={16} /></button><ExportMenu api={api} /></div>
        </header>
        <Routes>
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<OverviewPage api={api} revision={revision} go={(target) => navigate(pagePath[target])} />} />
          <Route path="/findings" element={<FindingsPage api={api} revision={revision} />} />
          <Route path="/findings/:findingId" element={<FindingsPage api={api} revision={revision} />} />
          <Route path="/inventory" element={<SystemsPage api={api} revision={revision} />} />
          <Route path="/systems/:systemId" element={<SystemRoute api={api} revision={revision} />} />
          <Route path="/connections" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} />} />
          <Route path="/connections/new" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} startWizard />} />
          <Route path="/connections/:environmentId" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} />} />
          <Route path="/systems/:systemId/evidence" element={<EvidenceRoute api={api} revision={revision} />} />
          <Route path="/changes" element={<ChangesPage api={api} revision={revision} />} />
          <Route path="/settings" element={<AccountSettings api={api} onDeleted={async () => signOut()} onAnalyticsChanged={analyticsSession.reload} analyticsAvailable={Boolean(analyticsConfig?.enabled)} />} />
          <Route path="*" element={<Navigate to="/overview" replace />} />
        </Routes>
      </div>
    </main>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} />}
  </div>;
}

function OverviewPage({ api, revision, go }: { api: API; revision: number; go: (page: Page) => void }) {
  const [search, setSearch] = useSearchParams();
	const [selectedProduct, setSelectedProduct] = useState<ProductItem>();
  const window = search.get("window") || "7d";
  const overview = useRemote(() => api.overview(window), [api, revision, window]);
  const products = useRemote(() => api.products(), [api, revision]);
  if (overview.loading || products.loading) return <Loading />;
  if (overview.error || products.error || !overview.data) return <Failure error={overview.error || products.error} retry={() => { overview.reload(); products.reload(); }} />;
  const data = overview.data;
  const systems = data.footprint.system_types;
  const states = data.footprint.states;
  const totalSystems = sum(Object.values(systems));
  const runningCount = states.running ?? 0;
  const reportingTargets = sum(data.coverage.map((item) => item.reporting));
  const reportingLabel = `${reportingTargets} reporting ${reportingTargets === 1 ? "target" : "targets"}`;
  const attention = [
    ...(data.executive_summary?.top_findings ?? data.exposure_summary?.top_findings ?? []).map((finding) => [`${pretty(finding.severity)} · ${finding.title}`, 1, "Open the complete evidence path and safe next check", "Findings", finding.id]),
    ["Ownership is not established", data.executive_summary?.effective_ownership.unowned ?? data.attention.unattributed_systems, "These systems have no authoritative business or technical owner", "Inventory"],
    ["Evidence needs corroboration", data.attention.possible_only_systems, "A second authoritative signal is needed before governance", "Inventory"],
    ["Services are reachable beyond this device", data.attention.non_loopback_services, "Network-accessible AI services need exposure review", "Inventory"],
    ["Partial scans", data.attention.partial_scans, "Some locations or detectors were unavailable", "Connections"],
    ["Stale targets", data.attention.stale_targets, "Outside the freshness threshold", "Connections"],
    ["Conflicting facts", data.attention.fact_conflicts, "Sources disagree on a material fact", "Inventory"],
  ].filter((item) => Number(item[1]) > 0) as Array<[string, number, string, Page, string?]>;
  const changes = groupChanges(data.changes).slice(0, 4);

  return <div className="page-stack executive-overview">
    <div className="overview-toolbar"><span>Updated {relative(data.generated_at)}</span><div className="window-switch">{["24h", "7d", "30d"].map((item) => <button className={window === item ? "active" : ""} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "overview", interaction: "filter_changed", control: "window" } }); const next = new URLSearchParams(search); item === "7d" ? next.delete("window") : next.set("window", item); setSearch(next, { replace: true }); }} key={item}>{item}</button>)}</div></div>
    {data.executive_summary?.coverage_state === "unassessed" && <section className="panel unassessed-state"><div><p className="eyebrow">START HERE</p><h2>Your organization is unassessed</h2><p>Install the read-only endpoint collector here, or create a 24-hour handoff for IT. Results appear after normalization completes.</p></div><button className="button primary" onClick={() => go("Connections")}>Connect endpoint <ArrowRight size={15} /></button></section>}
    {data.executive_summary?.coverage_state === "ready" && data.executive_summary.systems.known === 0 && <section className="panel successful-empty"><CheckCircle2 size={18} /><div><h2>No AI systems found in the checked scope</h2><p>Lens completed the latest endpoint scan {data.executive_summary.last_successful_evidence_at ? relative(data.executive_summary.last_successful_evidence_at) : "recently"}. Open Connections to review the reporting endpoint and coverage.</p></div></section>}
    {data.executive_summary && data.executive_summary.coverage_state !== "ready" && <section className={`coverage-limitation ${data.executive_summary.coverage_state}`} role="status"><AlertCircle size={16} /><span><b>{pretty(data.executive_summary.coverage_state)} coverage.</b> Executive conclusions include retained evidence and show its age. Last successful evidence {data.executive_summary.last_successful_evidence_at ? relative(data.executive_summary.last_successful_evidence_at) : "has not arrived yet"}.</span></section>}
    <section className="exposure-hero">
      <div className="exposure-copy"><span>What can Lens see?</span><h2><strong>{(data.executive_summary?.systems.known ?? totalSystems).toLocaleString()}</strong> known AI systems</h2><p>{data.executive_summary ? `${data.executive_summary.systems.fresh} currently reporting · ${data.executive_summary.systems.stale} retained from stale evidence.` : `Current evidence comes from ${reportingLabel}. ${runningCount} systems are running now.`}</p><div className="enrollment-scope">{data.coverage.map((item) => <span key={item.target_type}><b>{pretty(item.target_type)}</b> {item.reporting ? `${item.reporting} reporting${item.stale ? ` · ${item.stale} stale` : ""}` : "Not enrolled"}</span>)}</div>{data.exposure_summary && <button className="overview-story-link" onClick={() => go("Findings")}>Open findings <ArrowRight size={14} /></button>}</div>
      <div className="exposure-facts">
        <ExecutiveFact value={runningCount} label="Running now" tone="active" onClick={() => location.assign("/inventory?state=running&confidence=confirmed&freshness=fresh")} />
        <ExecutiveFact value={data.executive_summary?.effective_ownership.unowned ?? data.attention.unattributed_systems ?? 0} label="Ownership gaps" tone="attention" onClick={() => location.assign("/inventory?owner_status=unowned")} />
        <ExecutiveFact value={data.executive_summary ? (data.executive_summary.findings.fresh_by_severity.critical ?? 0) + (data.executive_summary.findings.stale_by_severity.critical ?? 0) : data.exposure_summary?.counts.critical ?? 0} label="Critical findings" tone="attention" onClick={() => location.assign("/findings?severity=critical")} />
        <ExecutiveFact value={reportingTargets} label="Reporting targets" tone="good" onClick={() => go("Connections")} />
      </div>
    </section>
    <section className="system-mix-strip">
      <div className="mix-heading"><span>Observed system mix</span><small>Root systems only · supporting software excluded</small></div>
      <div className="system-breakdown">
        <button onClick={() => location.assign("/inventory?system_type=autonomous_agent")}><Bot size={18} /><span><b>{systems.autonomous_agent ?? 0}</b><small>Autonomous agents</small></span></button>
        <button onClick={() => location.assign("/inventory?system_type=agent_tool")}><TerminalSquare size={18} /><span><b>{systems.agent_tool ?? 0}</b><small>Agent-capable tools</small></span></button>
        <button onClick={() => location.assign("/inventory?system_type=model_runtime")}><BrainCircuit size={18} /><span><b>{systems.model_runtime ?? 0}</b><small>Model runtimes</small></span></button>
      </div>
    </section>
    <div className="executive-primary">
      <section className="panel running-panel"><PanelHeading title="Who uses it?" detail="Products across every installation; observed accounts are not business owners" action={<button className="text-button" onClick={() => go("Inventory")}>Open inventory <ArrowRight size={13} /></button>} />
        {products.data?.items.length ? <div className="running-list">{products.data.items.slice(0, 8).map((item) => <button key={item.id} onClick={() => setSelectedProduct(item)}><span className="running-mark"><CircleDot size={14} /></span><span><b>{item.name}</b><small>{item.installation_count} {item.installation_count === 1 ? "installation" : "installations"} · {item.observed_user_count} observed {item.observed_user_count === 1 ? "user" : "users"}</small></span><span><strong>{item.fresh_count} fresh · {item.stale_count} stale</strong><small>Last observed {relative(item.last_seen_at)}</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="No products discovered" detail="Products appear here after a connected target reports system evidence." />}
      </section>
      <section className="panel attention-panel"><PanelHeading title="What needs attention" detail="Concrete findings, not an opaque risk score" />
        {attention.length ? <div className="attention-list">{attention.map(([label, count, detail, destination, findingID]) => <button key={label} onClick={() => findingID ? location.assign(`/findings/${encodeURIComponent(findingID)}`) : destination === "Inventory" && label.startsWith("Ownership") ? location.assign("/inventory?owner_status=unowned") : go(destination)}><span className="attention-count active">{count}</span><span><b>{label}</b><small>{detail}</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="Nothing needs review" detail="No current discovery findings require attention." />}
      </section>
    </div>
    <div className="executive-secondary">
      <section className="panel change-panel"><PanelHeading title="What changed" detail={`Repeated observations grouped over the last ${window}`} action={<button className="text-button" onClick={() => go("Changes")}>View history <ArrowRight size={13} /></button>} /><ChangeList items={changes} /></section>
      <section className="panel evidence-posture"><PanelHeading title="Evidence posture" detail="How current and conclusive this view is" action={<button className="text-button" onClick={() => go("Connections")}>Coverage details <ArrowRight size={13} /></button>} /><StateDistribution values={states} total={totalSystems} /><ConfidenceSummary data={data.data_quality.confidence} /></section>
    </div>
		{selectedProduct && <ProductDrawer item={selectedProduct} onClose={() => setSelectedProduct(undefined)} />}
  </div>;
}

function ProductDrawer({ item, onClose }: { item: ProductItem; onClose: () => void }) {
	return <Drawer onClose={onClose}>
		<div className="drawer-title"><Identity kind="runtime" name={item.name} detail={item.id} /><div>{item.system_type && <TypePill value={item.system_type} />}</div></div>
		<div className="fact-grid"><Fact label="Installations" value={String(item.installation_count)} /><Fact label="Observed users" value={String(item.observed_user_count)} /><Fact label="Fresh evidence" value={String(item.fresh_count)} /><Fact label="Stale evidence retained" value={String(item.stale_count)} /></div>
		<section className="drawer-section"><h3>Observed users <span>{item.observed_user_count}</span></h3><p>{item.observed_users.length ? item.observed_users.join(", ") : "No OS account was observed."}</p><small>Counts retain target-scoped identities even when separate endpoints use the same local account name. Usage does not establish a business or technical owner.</small></section>
		<section className="drawer-section"><h3>Installations <span>{item.instances.length}</span></h3><div className="running-list">{item.instances.map((instance) => <button key={instance.id} onClick={() => location.assign(`/systems/${encodeURIComponent(instance.id)}`)}><span className="running-mark"><Monitor size={14} /></span><span><b>{instance.target_name ?? "Unresolved target"}</b><small>{instance.observed_users.length ? `Observed user: ${instance.observed_users.join(", ")}` : "No observed user"}</small></span><span><strong>{pretty(instance.target_freshness)}</strong><small>{pretty(instance.state)} · {relative(instance.last_seen_at)}</small></span><ChevronRight size={14} /></button>)}</div></section>
	</Drawer>;
}

function FindingsPage({ api, revision }: { api: API; revision: number }) {
  const navigate = useNavigate();
  const { findingId } = useParams();
  const [search, setSearch] = useSearchParams();
  const [cursor, setCursor] = useState("");
  const filters = { severity: search.get("severity") ?? "", freshness: search.get("freshness") ?? "all", owner_status: search.get("owner_status") ?? "", system_type: search.get("system_type") ?? "" };
  const remote = useRemote(() => api.exposures({ ...filters, cursor }), [api, revision, cursor, search.toString()]);
  const detail = useRemote(() => findingId ? api.exposure(findingId) : Promise.resolve(undefined), [api, findingId]);
  const [items, setItems] = useState<ExposureFinding[]>([]);
	const inspectedFinding = useRef("");
  useEffect(() => { if (remote.data) setItems((current) => cursor ? [...current, ...remote.data!.items] : remote.data!.items); }, [remote.data, cursor]);
	useEffect(() => {
		if (!detail.data || inspectedFinding.current === detail.data.id) return;
		inspectedFinding.current = detail.data.id;
		captureAnalytics({ name: "finding_opened", properties: { severity: detail.data.severity, freshness: detail.data.evidence_freshness, owner_state: detail.data.effective_ownership?.owned ? "owned" : "unowned" } });
	}, [detail.data]);
  const update = (key: string, value: string) => { captureAnalytics({ name: "lens_interaction", properties: { surface: "findings", interaction: "filter_changed", control: analyticsControl(key) } }); const next = new URLSearchParams(search); value && value !== "all" ? next.set(key, value) : next.delete(key); setCursor(""); setItems([]); setSearch(next); };
  if (remote.loading && !items.length) return <Loading />;
  if (remote.error) return <Failure error={remote.error} retry={remote.reload} />;
  return <div className="page-stack">
    <FilterBar hideSearch><Select label="Severity" value={filters.severity} onChange={(value) => update("severity", value)} options={{ "": "All severities", critical: "Critical", high: "High", medium: "Medium", low: "Low" }} /><Select label="Evidence" value={filters.freshness} onChange={(value) => update("freshness", value)} options={{ all: "Fresh and stale", fresh: "Fresh", stale: "Stale" }} /><Select label="Ownership" value={filters.owner_status} onChange={(value) => update("owner_status", value)} options={{ "": "Any owner", unowned: "Owner missing", owned: "Owned" }} /><Select label="System type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All systems", autonomous_agent: "Autonomous agents", agent_tool: "Agent tools", model_runtime: "Model runtimes" }} /></FilterBar>
    <section className="panel data-panel"><PanelHeading title="What needs attention" detail="Ranked across the entire workspace; stale findings remain visible with their evidence age." count={items.length} /><div className="finding-list">{items.map((finding) => <button key={finding.id} className="finding-row" onClick={() => navigate(`/findings/${encodeURIComponent(finding.id)}?${search.toString()}`)}><span className={`severity-pill ${finding.severity}`}>{finding.severity}</span><span><b>{finding.title}</b><small>{finding.root_name} · evidence {finding.evidence_last_seen_at ? relative(finding.evidence_last_seen_at) : relative(finding.last_seen_at)}</small></span><span className={finding.effective_ownership?.owned ? "fact good" : "fact quiet"}>{finding.effective_ownership?.owner_name || "Owner missing"}</span><ChevronRight size={15} /></button>)}{!items.length && <Empty icon={CheckCircle2} title="No findings match this view" detail="Lens found no current evidence-backed findings for these filters." />}</div>{remote.data?.next_cursor && <button className="load-more" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "findings", interaction: "load_more" } }); setCursor(remote.data!.next_cursor!); }}>Load more findings <ChevronDown size={15} /></button>}</section>
    {findingId && <AccessibleDialog title="Finding details" onClose={() => navigate(`/findings?${search.toString()}`)}>{detail.loading ? <InlineLoading /> : detail.error || !detail.data ? <InlineError text={detail.error || "Finding not found"} /> : <FindingDetail finding={detail.data} navigate={navigate} />}</AccessibleDialog>}
  </div>;
}

function FindingDetail({ finding, navigate }: { finding: ExposureFinding; navigate: ReturnType<typeof useNavigate> }) {
  return <div className="finding-detail"><span className={`severity-pill ${finding.severity}`}>{finding.severity}</span><h2>{finding.title}</h2><p>{finding.explanation}</p><dl><div><dt>Affected system</dt><dd><button className="text-button" onClick={() => navigate(`/systems/${encodeURIComponent(finding.root_entity_id)}`)}>{finding.root_name}</button></dd></div><div><dt>Evidence</dt><dd>{finding.evidence_freshness ? pretty(finding.evidence_freshness) : "Current"} · {relative(finding.evidence_last_seen_at || finding.last_seen_at)}</dd></div><div><dt>Owner</dt><dd>{finding.effective_ownership?.owner_name || "Not established"}</dd></div></dl><h3>Why Lens raised this</h3><ol className="evidence-path">{finding.path.map((step, index) => <li key={`${step.entity_id}-${index}`}><b>{step.name}</b><span>{pretty(step.kind)} · {pretty(step.basis)}</span>{step.edge && <small>{pretty(step.edge)}</small>}</li>)}</ol><div className="next-step"><b>Recommended next step</b><p>{finding.recommended_next_step}</p></div></div>;
}

function AccessibleDialog({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLElement>(null);
  const restore = useRef(document.activeElement as HTMLElement | null);
  useEffect(() => {
    ref.current?.focus();
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); if (event.key === "Tab" && ref.current) { const focusable = [...ref.current.querySelectorAll<HTMLElement>('button,a,input,select,textarea,[tabindex]:not([tabindex="-1"])')]; if (!focusable.length) return; const first = focusable[0], last = focusable[focusable.length - 1]; if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); } } };
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("keydown", key); restore.current?.focus(); };
  }, [onClose]);
  return <div className="modal-overlay" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}><section className="detail-drawer" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} ref={ref}><button className="drawer-close" aria-label="Close dialog" onClick={onClose}><X size={18} /></button>{children}</section></div>;
}

function SystemsPage({ api, revision }: { api: API; revision: number }) {
  const navigate = useNavigate();
  const { systemId } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const hasInstallationFilter = ["freshness", "system_type", "state", "confidence", "network_scope", "owner_status"].some((key) => searchParams.has(key));
  const [inventoryView, setInventoryView] = useState<"products" | "installations">(() => systemId || searchParams.get("view") === "installations" || hasInstallationFilter ? "installations" : "products");
  const [productSearch, setProductSearch] = useState("");
  const [productType, setProductType] = useState("");
  const [productReach, setProductReach] = useState("");
  const [productActivity, setProductActivity] = useState("");
  const [selectedProduct, setSelectedProduct] = useState<ProductItem>();
  const [filters, setFilters] = useState<Record<string, string>>(() => ({ sort: "last_seen", freshness: searchParams.get("freshness") || "all", search: searchParams.get("search") || "", system_type: searchParams.get("system_type") || "", state: searchParams.get("state") || "", confidence: searchParams.get("confidence") || "", network_scope: searchParams.get("network_scope") || "", owner_status: searchParams.get("owner_status") || "" }));
  const [cursor, setCursor] = useState("");
  const [items, setItems] = useState<SystemItem[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
	const viewedInventory = useRef(false);
	const lastTrackedSearch = useRef("");
	const productInventory = useRemote(() => api.products(), [api, revision]);
	const overview = useRemote(() => api.overview("7d"), [api, revision]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setLoading(true); setError("");
      api.systems({ ...filters, cursor }).then((result) => { setItems((current) => cursor ? [...current, ...result.items] : result.items); setNext(result.next_cursor ?? ""); }).catch((reason) => setError(String(reason))).finally(() => setLoading(false));
    }, filters.search ? 220 : 0);
    return () => clearTimeout(timer);
  }, [api, revision, filters, cursor]);
	useEffect(() => {
		if (!loading && !error && !viewedInventory.current) {
			viewedInventory.current = true;
			captureAnalytics({ name: "inventory_viewed", properties: {} });
		}
	}, [error, loading]);
	useEffect(() => {
		if (!loading && !error && filters.search && filters.search !== lastTrackedSearch.current) {
			lastTrackedSearch.current = filters.search;
			captureAnalytics({ name: "lens_interaction", properties: { surface: "inventory", interaction: "search_used" } });
		}
	}, [error, filters.search, loading]);

  const update = (key: string, value: string) => { if (key !== "search") captureAnalytics({ name: "lens_interaction", properties: { surface: "inventory", interaction: "filter_changed", control: analyticsControl(key) } }); setCursor(""); setItems([]); setFilters((current) => ({ ...current, [key]: value })); const next = new URLSearchParams(searchParams); value && value !== "all" ? next.set(key, value) : next.delete(key); setSearchParams(next, { replace: true }); };
  const switchView = (view: "products" | "installations") => {
    setInventoryView(view);
    captureAnalytics({ name: "lens_interaction", properties: { surface: "inventory", interaction: "filter_changed", control: "inventory_scope" } });
    const next = new URLSearchParams(searchParams);
    view === "products" ? next.delete("view") : next.set("view", "installations");
    setSearchParams(next, { replace: true });
  };
  const products = productInventory.data?.items ?? [];
  const reportingEndpoints = overview.data?.coverage.find((item) => item.target_type === "endpoint")?.reporting ?? 0;
  const productItems = products.filter((item) => {
    const endpointCount = new Set(item.instances.map((instance) => instance.target_id).filter(Boolean)).size;
    return (!productSearch || item.name.toLowerCase().includes(productSearch.toLowerCase()))
      && (!productType || item.system_type === productType)
      && (!productReach || (productReach === "broad" ? endpointCount > 1 : endpointCount <= 1))
      && (!productActivity || (productActivity === "running" ? item.running_count > 0 : item.running_count === 0));
  });
  const installationCount = products.reduce((total, item) => total + item.installation_count, 0);
  const runningCount = products.reduce((total, item) => total + item.running_count, 0);
  const staleCount = products.reduce((total, item) => total + item.stale_count, 0);
  return <div className="page-stack">
    <section className="inventory-viewbar">
      <div><p className="eyebrow">SCOPE</p><h2>{inventoryView === "products" ? "Organization products" : "Endpoint installations"}</h2><p>{inventoryView === "products" ? "One row per product, regardless of how many endpoints report it." : "Every target-scoped system observation, retained for investigation and evidence review."}</p></div>
      <div className="inventory-view-switch" role="group" aria-label="Inventory scope">
        <button className={inventoryView === "products" ? "active" : ""} onClick={() => switchView("products")}><PackageSearch size={15} /> Products</button>
        <button className={inventoryView === "installations" ? "active" : ""} onClick={() => switchView("installations")}><Monitor size={15} /> Installations</button>
      </div>
    </section>
    {inventoryView === "products" ? <>
      <section className="product-inventory-summary">
        <div><span>Products</span><b>{products.length}</b><small>unique organization-wide</small></div>
        <div><span>Installations</span><b>{installationCount}</b><small>across every endpoint</small></div>
        <div><span>Running now</span><b className="good">{runningCount}</b><small>confirmed active state</small></div>
        <div><span>Reporting endpoints</span><b className="good">{reportingEndpoints || "—"}</b><small>{staleCount ? `${staleCount} stale installations` : "all evidence current"}</small></div>
      </section>
      <FilterBar search={productSearch} setSearch={setProductSearch}>
        <Select label="Product type" value={productType} onChange={setProductType} options={{ "": "All products", autonomous_agent: "Autonomous agents", agent_tool: "Agent-capable tools", model_runtime: "Model runtimes" }} />
        <Select label="Endpoint reach" value={productReach} onChange={setProductReach} options={{ "": "Any reach", broad: "Multiple endpoints", single: "Single endpoint" }} />
        <Select label="Activity" value={productActivity} onChange={setProductActivity} options={{ "": "Any activity", running: "Running somewhere", quiet: "Not running" }} />
      </FilterBar>
      <section className="panel data-panel product-inventory-panel"><div className="table-summary"><span><b>{productItems.length}</b> organization products</span><span>Open a product to see its endpoint installations and observed accounts</span></div>
        <div className="table-scroll"><div className="product-inventory-row table-head"><span>Product</span><span>Endpoint reach</span><span>Activity</span><span>Observed users</span><span>Evidence</span><span>Last observed</span><span /></div>
          {productItems.map((item) => {
            const targets = new Set(item.instances.map((instance) => instance.target_id).filter(Boolean)).size;
            const confirmed = item.instances.some((instance) => instance.confidence === "confirmed");
            const likely = item.instances.some((instance) => instance.confidence === "likely");
            const confidence: "confirmed" | "likely" | "possible" = confirmed ? "confirmed" : likely ? "likely" : "possible";
            return <button className="product-inventory-row" key={item.id} onClick={() => setSelectedProduct(item)}>
              <Identity kind={item.system_type === "model_runtime" ? "model_server" : "agent"} name={item.name} detail={pretty(item.system_type ?? item.product_category ?? "discovered product")} />
              <span className="product-reach"><b>{targets} of {reportingEndpoints || Math.max(targets, 1)}</b><small>reporting endpoints</small><i><em style={{ width: `${Math.min(100, (targets / Math.max(reportingEndpoints, targets, 1)) * 100)}%` }} /></i></span>
              <span className="stacked"><b className={item.running_count ? "good" : ""}>{item.running_count ? `${item.running_count} running` : "Not running"}</b><small>{item.installation_count} {item.installation_count === 1 ? "installation" : "installations"}</small></span>
              <span className="stacked"><b>{item.observed_user_count}</b><small>observed {item.observed_user_count === 1 ? "account" : "accounts"}</small></span>
              <ConfidencePill value={confidence} /><span className="observed">{relative(item.last_seen_at)}</span><ChevronRight size={15} />
            </button>;
          })}
          {!productInventory.loading && !productItems.length && <Empty icon={PackageSearch} title="No products match this view" detail="Try a broader search or filter. Endpoint-level observations remain available under Installations." />}
        </div>
        {productInventory.error && <InlineError text={productInventory.error} />}{productInventory.loading && <InlineLoading />}
      </section>
    </> : <>
      <FilterBar search={filters.search ?? ""} setSearch={(value) => update("search", value)}>
        <Select label="System type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All root systems", autonomous_agent: "Autonomous agents", agent_tool: "Agent-capable tools", model_runtime: "Model runtimes" }} />
        <Select label="State" value={filters.state} onChange={(value) => update("state", value)} options={{ "": "Any state", running: "Running", deployed: "Deployed", defined: "Defined", configured: "Configured", installed: "Installed", residual: "Residual", cached: "Cached" }} />
        <Select label="Confidence" value={filters.confidence} onChange={(value) => update("confidence", value)} options={{ "": "Any confidence", confirmed: "Confirmed", likely: "Likely", possible: "Possible" }} />
        <Select label="Ownership" value={filters.owner_status} onChange={(value) => update("owner_status", value)} options={{ "": "Any owner", owned: "Owned", unowned: "Owner missing" }} />
        <Select label="Network" value={filters.network_scope} onChange={(value) => update("network_scope", value)} options={{ "": "Any scope", external: "External", network: "Network", loopback: "Loopback", none: "None", unknown: "Unknown" }} />
        <Select label="Reporting" value={filters.freshness} onChange={(value) => update("freshness", value)} options={{ fresh: "Fresh targets", stale: "Stale targets", all: "Fresh and stale" }} />
      </FilterBar>
      <section className="panel data-panel"><div className="table-summary"><span><b>{items.length}</b> {filters.freshness === "stale" ? "stale" : filters.freshness === "all" ? "fresh and stale" : "fresh"} installations</span>{filters.freshness === "fresh" && <span>Older identities remain available through Reporting filters and Coverage diagnostics</span>}</div>
        <div className="system-table table-scroll"><div className="system-row table-head"><span>System</span><span>Type</span><span>State</span><span>Target / surface</span><span>Attribution</span><span>Evidence</span><span /></div>
          {items.map((item) => <button className="system-row" key={item.id} onClick={() => navigate(`/systems/${encodeURIComponent(item.id)}?${searchParams.toString()}`)}>
            <Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} />
            <TypePill value={item.system_type} /><StatePill state={item.state} /><span className="stacked"><b>{item.target_name ?? "Unresolved target"}</b><small>{pretty(item.surface)}{item.target_freshness ? ` · ${pretty(item.target_freshness)}` : ""}</small></span>
            <span className={item.effective_ownership?.owned ? "fact good" : "fact quiet"}>{item.effective_ownership?.owner_name || (item.effective_ownership?.owned ? "Owned" : "Owner missing")}</span><ConfidencePill value={item.confidence} /><ChevronRight size={15} />
          </button>)}
          {!loading && !items.length && <Empty icon={Bot} title="No installations match this view" detail="Supporting runtimes and cached artifacts are intentionally excluded from the executive systems view." />}
        </div>
        {error && <InlineError text={error} />}{loading && <InlineLoading />}{next && !loading && <button className="load-more" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "inventory", interaction: "load_more" } }); setCursor(next); }}>Load more installations <ChevronDown size={15} /></button>}
      </section>
    </>}
    {selectedProduct && <ProductDrawer item={selectedProduct} onClose={() => setSelectedProduct(undefined)} />}
    {systemId && <SystemDrawer api={api} id={systemId} onClose={() => navigate(`/inventory?view=installations&${searchParams.toString()}`)} />}
  </div>;
}

function SystemRoute({ api, revision }: { api: API; revision: number }) { return <SystemsPage api={api} revision={revision} />; }
function EvidenceRoute({ api, revision }: { api: API; revision: number }) { const { systemId = "" } = useParams(); return <Suspense fallback={<Loading />}><EvidenceGraphPage api={api} revision={revision} initialSystemId={systemId} /></Suspense>; }

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

function analyticsControl(key: string): string {
	return ({ owner_status: "ownership", network_scope: "network", system_type: "system_type", target_type: "target_type" } as Record<string, string>)[key] ?? key;
}

function ConnectionsPage({ api, revision, onResults, startWizard = false }: { api: API; revision: number; onResults: () => void; startWizard?: boolean }) {
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

function ChangesPage({ api, revision }: { api: API; revision: number }) {
  const [search, setSearch] = useSearchParams();
  const filters: Record<string, string> = { window: search.get("window") || "7d", system_role: "system", category: search.get("category") || "", system_type: search.get("system_type") || "", surface: search.get("surface") || "" };
  const [cursor, setCursor] = useState("");
  const remote = useRemote(() => api.changes({ ...filters, cursor }), [api, revision, search.toString(), cursor]);
  const [items, setItems] = useState<Change[]>([]);
	const viewedChanges = useRef(false);
  useEffect(() => { if (remote.data) setItems((current) => cursor ? [...current, ...remote.data!.items] : remote.data!.items); }, [remote.data, cursor]);
	useEffect(() => { if (remote.data && !viewedChanges.current) { viewedChanges.current = true; captureAnalytics({ name: "changes_viewed", properties: {} }); } }, [remote.data]);
  const update = (key: string, value: string) => { captureAnalytics({ name: "lens_interaction", properties: { surface: "changes", interaction: "filter_changed", control: analyticsControl(key) } }); const next = new URLSearchParams(search); value && !(key === "window" && value === "7d") ? next.set(key, value) : next.delete(key); setCursor(""); setItems([]); setSearch(next, { replace: true }); };
  return <div className="page-stack"><FilterBar hideSearch>
    <Select label="Window" value={filters.window} onChange={(value) => update("window", value)} options={{ "24h": "Last 24 hours", "7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days" }} />
    <Select label="Category" value={filters.category} onChange={(value) => update("category", value)} options={{ "": "All material changes", state: "State", network_scope: "Network scope", attribution: "Attribution", capability: "Capability", confidence: "Confidence", identity: "Identity", freshness: "Freshness" }} />
    <Select label="System type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All systems", autonomous_agent: "Autonomous agent", agent_tool: "Agent-capable tool", model_runtime: "Model runtime" }} />
    <Select label="Surface" value={filters.surface} onChange={(value) => update("surface", value)} options={{ "": "All surfaces", endpoint: "Endpoint", repository: "Repository", kubernetes: "Kubernetes", cloud: "Cloud" }} />
  </FilterBar>
    <section className="panel change-log"><PanelHeading title="System change history" detail="Changes to root systems and their connected capabilities; routine re-observation is suppressed" count={items.length} /><ChangeList items={items} expanded />
      {remote.loading && <InlineLoading />}{remote.error && <InlineError text={remote.error} />}{remote.data?.next_cursor && !remote.loading && <button className="load-more" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "changes", interaction: "load_more" } }); setCursor(remote.data!.next_cursor!); }}>Load more changes <ChevronDown size={15} /></button>}
    </section>
  </div>;
}

function SystemDrawer({ api, id, onClose }: { api: API; id: string; onClose: () => void }) {
  const remote = useRemote(() => api.system(id), [api, id]);
	const opened = useRef("");
	useEffect(() => {
		if (!remote.data || opened.current === remote.data.id) return;
		opened.current = remote.data.id;
		captureAnalytics({ name: "system_opened", properties: { system_kind: remote.data.system_type, confidence: remote.data.confidence, freshness: remote.data.target_freshness === "fresh" || remote.data.target_freshness === "stale" ? remote.data.target_freshness : undefined, owner_state: remote.data.effective_ownership?.owned ? "owned" : "unowned" } });
	}, [remote.data]);
  return <Drawer onClose={onClose}>{remote.loading ? <Loading /> : remote.error || !remote.data ? <Failure error={remote.error} retry={remote.reload} /> : <SystemDetailView item={remote.data} />}</Drawer>;
}

function SystemDetailView({ item }: { item: SystemDetail }) {
  const navigate = useNavigate();
  const groups = groupConnections(item.connections);
  return <><div className="drawer-title"><Identity kind={item.kind} name={item.name} detail={item.product_id ?? item.id} /><div><TypePill value={item.system_type} /><StatePill state={item.state} /><ConfidencePill value={item.confidence} /></div></div>
    <div className="fact-grid"><Fact label="Target" value={item.target_name ?? "Unresolved"} /><Fact label="Reporting" value={pretty(item.target_freshness ?? "unknown")} /><Fact label="Network scope" value={pretty(item.network_scope)} /><Fact label="Effective owner" value={item.effective_ownership?.owner_name || (item.effective_ownership?.owned ? "Evidence-backed" : "Not established")} /><Fact label="First discovered" value={relative(item.first_seen_at)} /><Fact label="Last observed" value={relative(item.last_seen_at)} /></div>
    <button className="button subtle" onClick={() => navigate(`/systems/${encodeURIComponent(item.id)}/evidence`)}><Network size={14} /> Open evidence graph</button>
    <section className="drawer-section"><h3>Connected inventory <span>{item.connections.length}</span></h3>{Object.entries(groups).map(([group, values]) => <div className="connection-group" key={group}><p>{pretty(group)}</p>{values.map((connection) => <ConnectionRow item={connection} key={connection.relationship_id} />)}</div>)}</section>
    <EvidenceSection items={item.evidence} />
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

function ExecutiveFact({ value, label, tone = "neutral", onClick }: { value: number; label: string; tone?: string; onClick: () => void }) {
  return <button className={`executive-fact ${tone}`} onClick={onClick}><strong>{value}</strong><span>{label}</span></button>;
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

function ExportMenu({ api }: { api: API }) {
  const [open, setOpen] = useState(false);
  return <div className="export"><button className="button subtle" onClick={() => setOpen((value) => !value)}><Download size={15} /> Export <ChevronDown size={13} /></button>{open && <div>{(["lens", "ndjson", "cyclonedx"] as const).map((format) => <button key={format} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "export", interaction: "open", export_format: format === "lens" ? "json" : format } }); setOpen(false); api.downloadExport(format); }}>{format === "lens" ? "Lens JSON" : format === "ndjson" ? "NDJSON" : "CycloneDX 1.7"}</button>)}</div>}</div>;
}

function AccountSettings({ api, onDeleted, onAnalyticsChanged, analyticsAvailable }: { api: API; onDeleted: () => Promise<void>; onAnalyticsChanged: () => void; analyticsAvailable: boolean }) {
  const session = useRemote(() => api.session(), [api]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (session.loading) return <Loading />;
  if (session.error || !session.data) return <Failure error={session.error} retry={session.reload} />;
  const removeIdentity = async () => {
    if (!window.confirm("Delete your Lens identity? This signs you out and cannot be undone.")) return;
    setBusy(true); setError("");
    try { await api.deleteAccount(); await onDeleted(); } catch (reason) { setError(String(reason)); setBusy(false); }
  };
  const removeWorkspace = async () => {
    if (!window.confirm(`Delete ${session.data!.workspace.name} and all Lens data immediately?`)) return;
    setBusy(true); setError("");
    try { await api.deleteWorkspace(session.data!.workspace.id); await onDeleted(); } catch (reason) { setError(String(reason)); setBusy(false); }
  };
	const updateAnalytics = async (enabled: boolean) => {
		setBusy(true); setError("");
		try { await api.updateAnalytics(enabled); if (!enabled) resetAnalytics(); session.reload(); onAnalyticsChanged(); } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
	};
  return <div className="page-stack"><section className="panel settings-panel"><PanelHeading title="Your account" detail={`${session.data.user.id} · ${pretty(session.data.role)}`} /><p>{session.data.can_delete_account ? "You can delete your identity. Workspace evidence and settings remain available to other members." : "You are the workspace's sole owner. Transfer ownership or delete the workspace before deleting your identity."}</p><button className="button subtle" disabled={busy || !session.data.can_delete_account} onClick={removeIdentity}>Delete my identity</button></section>{analyticsAvailable && <section className="panel settings-panel"><PanelHeading title="Product analytics" detail="Help Barrikade improve managed Lens" /><label className="analytics-preference"><input type="checkbox" checked={session.data.analytics.enabled} disabled={busy} onChange={(event) => void updateAnalytics(event.target.checked)} /><span><b>Share privacy-minimized product diagnostics</b><small>Lens sends pseudonymous activation and feature-use events, numeric performance, sanitized error classes, survey ratings, flag exposure, and total-privacy session replay. Replay masks all text, inputs, and attributes and blocks media and network data. Lens never sends names, email addresses, workspace names, inventory, evidence, URLs, commands, or infrastructure identifiers. Browser privacy signals disable capture on this browser.</small></span></label><p className="muted">Turning this off affects future events and immediately stops replay in this browser. Actorless workspace-processing milestones can continue. See the <a href="/privacy">privacy notice</a> or contact Barrikade to request erasure of previously collected pseudonymous analytics.</p></section>}{session.data.role === "owner" && <section className="panel settings-panel danger-zone"><PanelHeading title="Delete workspace" detail="Immediately deletes connections, inventory, findings, evidence, and member access." /><button className="button quiet" disabled={busy} onClick={removeWorkspace}>Delete workspace</button></section>}{error && <InlineError text={error} />}</div>;
}

function NotificationBell({ api, revision, onOpen }: { api: API; revision: number; onOpen: () => void }) {
  const notifications = useRemote(() => api.notifications(), [api, revision]);
  const unread = notifications.data?.items.filter((item) => !item.read_at) ?? [];
  if (!unread.length) return null;
  const latest = unread[0];
  return <button className="notification-button" title={`${unread.length} unread setup notifications`} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "notification", interaction: "notification_opened" } }); api.readNotification(latest.id).then(notifications.reload); onOpen(); }}><Activity size={15} /><b>{unread.length}</b></button>;
}

function randomURLSafe(length: number) { const bytes = crypto.getRandomValues(new Uint8Array(length)); return base64URL(bytes).slice(0, length); }
function base64URL(bytes: Uint8Array) { let value = ""; bytes.forEach((byte) => { value += String.fromCharCode(byte); }); return btoa(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); }
