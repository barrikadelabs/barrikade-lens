import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  Activity,
  ArrowRight,
  Bot,
  Boxes,
  BrainCircuit,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Clock3,
  Container,
  Copy,
  Cpu,
  Database,
  Download,
  GitBranch,
  History,
  KeyRound,
  Layers3,
  LayoutDashboard,
  Link2,
  LogOut,
  Menu,
  Monitor,
  Network,
  PackageSearch,
  PlugZap,
  Radar,
  RadioTower,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  TerminalSquare,
  UserRound,
  Workflow,
  Wrench,
  X,
  type LucideIcon,
} from "lucide-react";
import {
  API,
  type AuthConfig,
  type Change,
  type Entity,
  type EntityDetail,
  type Relationship,
  type Source,
  authConfig,
  exchangeOIDC,
} from "./api";

type Page = "Overview" | "Inventory" | "Topology" | "Changes" | "Sources";

const navigation: Array<{ page: Page; label: string; icon: LucideIcon; description: string }> = [
  { page: "Overview", label: "Overview", icon: LayoutDashboard, description: "Fleet discovery at a glance" },
  { page: "Inventory", label: "Inventory", icon: Boxes, description: "Every discovered entity" },
  { page: "Topology", label: "Relationships", icon: Network, description: "How the inventory connects" },
  { page: "Changes", label: "Changes", icon: History, description: "What changed and when" },
  { page: "Sources", label: "Sources", icon: RadioTower, description: "Coverage and enrollment" },
];

const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: {
    eyebrow: "DISCOVERY COMMAND CENTER",
    title: "Your autonomous agent footprint",
    detail: "A factual, evidence-backed view across endpoints, source repositories, and Kubernetes.",
  },
  Inventory: {
    eyebrow: "CURRENT INVENTORY",
    title: "Everything Lens can see",
    detail: "Filter discovered agents, runtimes, models, tools, APIs, and deployment surfaces.",
  },
  Topology: {
    eyebrow: "EVIDENCE GRAPH",
    title: "See how the pieces connect",
    detail: "Trace where agents run, what they use, and which capabilities they expose.",
  },
  Changes: {
    eyebrow: "OBSERVATION HISTORY",
    title: "What changed across the fleet",
    detail: "Discovered, updated, stale, and removed inventory over the retention window.",
  },
  Sources: {
    eyebrow: "COVERAGE MANAGEMENT",
    title: "Bring the whole organization into view",
    detail: "Track source freshness and enroll endpoints with short-lived, scoped credentials.",
  },
};

const kindConfig: Record<string, { label: string; icon: LucideIcon; family: string }> = {
  endpoint: { label: "Endpoint", icon: Monitor, family: "source" },
  user: { label: "User", icon: UserRound, family: "source" },
  repository: { label: "Repository", icon: GitBranch, family: "source" },
  cluster: { label: "Cluster", icon: Container, family: "source" },
  workload: { label: "Workload", icon: Container, family: "delivery" },
  agent: { label: "Agent", icon: Bot, family: "agent" },
  runtime: { label: "Runtime", icon: TerminalSquare, family: "agent" },
  framework: { label: "Framework", icon: Layers3, family: "agent" },
  mcp_server: { label: "MCP server", icon: PlugZap, family: "capability" },
  skill: { label: "Skill", icon: Sparkles, family: "capability" },
  model: { label: "Model", icon: BrainCircuit, family: "ai" },
  model_server: { label: "Model server", icon: Server, family: "ai" },
  tool: { label: "Tool", icon: Wrench, family: "capability" },
  api_service: { label: "API service", icon: Database, family: "delivery" },
  api_operation: { label: "API operation", icon: Link2, family: "delivery" },
  workflow: { label: "Workflow", icon: Workflow, family: "delivery" },
  credential_reference: { label: "Credential reference", icon: KeyRound, family: "capability" },
};

export function App() {
  const [token, setToken] = useState(() => sessionStorage.getItem("lens-token") ?? "");
  const [page, setPage] = useState<Page>("Overview");
  const [inventoryKind, setInventoryKind] = useState("all");
  const [authError, setAuthError] = useState("");
  const api = useMemo(() => new API(token), [token]);

  const saveToken = useCallback((value: string) => {
    sessionStorage.setItem("lens-token", value);
    setToken(value);
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const code = params.get("code");
    const state = params.get("state");
    if (!code) return;
    const verifier = sessionStorage.getItem("lens-pkce-verifier");
    const expected = sessionStorage.getItem("lens-oidc-state");
    const redirect = sessionStorage.getItem("lens-oidc-redirect");
    if (!verifier || !redirect || state !== expected) {
      setAuthError("The sign-in state could not be verified.");
      return;
    }
    exchangeOIDC(code, redirect, verifier)
      .then((result) => {
        history.replaceState({}, "", location.pathname);
        sessionStorage.removeItem("lens-pkce-verifier");
        sessionStorage.removeItem("lens-oidc-state");
        saveToken(result.access_token);
      })
      .catch((error) => setAuthError(String(error)));
  }, [saveToken]);

  if (!token) return <SignIn onToken={saveToken} authError={authError} />;

  const openInventory = (kind = "all") => {
    setInventoryKind(kind);
    setPage("Inventory");
  };

  return (
    <Shell
      page={page}
      setPage={setPage}
      api={api}
      inventoryKind={inventoryKind}
      openInventory={openInventory}
      signOut={() => {
        sessionStorage.removeItem("lens-token");
        setToken("");
      }}
    />
  );
}

function SignIn({ onToken, authError }: { onToken: (value: string) => void; authError: string }) {
  const [value, setValue] = useState("");
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [error, setError] = useState(authError);

  useEffect(() => {
    authConfig().then(setConfig).catch((reason) => setError(String(reason)));
  }, []);

  const beginOIDC = async () => {
    if (!config?.authorization_endpoint || !config.client_id || !config.redirect_uri) return;
    const verifier = randomURLSafe(64);
    const state = randomURLSafe(24);
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
    const challenge = base64URL(new Uint8Array(digest));
    sessionStorage.setItem("lens-pkce-verifier", verifier);
    sessionStorage.setItem("lens-oidc-state", state);
    sessionStorage.setItem("lens-oidc-redirect", config.redirect_uri);
    const target = new URL(config.authorization_endpoint);
    target.search = new URLSearchParams({
      response_type: "code",
      client_id: config.client_id,
      redirect_uri: config.redirect_uri,
      scope: (config.scopes ?? ["openid"]).join(" "),
      state,
      code_challenge: challenge,
      code_challenge_method: "S256",
    }).toString();
    location.assign(target);
  };

  return (
    <main className="signin">
      <div className="signin-glow signin-glow-one" />
      <div className="signin-glow signin-glow-two" />
      <section className="signin-story">
        <Brand lockup="Lens" />
        <div className="signin-copy">
          <div className="product-kicker"><Radar size={14} /> Autonomous agent discovery</div>
          <h1>Bring your entire agent footprint into focus.</h1>
          <p>
            Lens discovers agents, runtimes, models, MCP servers, APIs, repositories, and deployments—then connects every finding to the evidence behind it.
          </p>
        </div>
      </section>
      <section className="signin-access">
        <div className="access-card">
          <p className="eyebrow">LENS HUB</p>
          <h2>Open your discovery plane</h2>
          <p className="muted">Sign in to view your organization’s current inventory and source coverage.</p>
          {config?.enabled && (
            <button className="button primary full" onClick={beginOIDC}>
              Continue with organization SSO <ArrowRight size={16} />
            </button>
          )}
          {config?.development_bootstrap && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                if (value.trim()) onToken(value.trim());
              }}
            >
              <label>
                Local bootstrap token
                <input
                  value={value}
                  onChange={(event) => setValue(event.target.value)}
                  type="password"
                  placeholder="Enter development token"
                  autoFocus={!config?.enabled}
                />
              </label>
              <button className="button primary full">Open Lens Hub <ArrowRight size={16} /></button>
            </form>
          )}
          {error && <p className="error-message">{error}</p>}
          {/* <div className="access-note">
            <ShieldCheck size={16} />
            <span>Production access uses OIDC Authorization Code with PKCE. Bootstrap access is for local quickstarts only.</span>
          </div> */}
        </div>
      </section>
    </main>
  );
}

function Shell({
  page,
  setPage,
  api,
  inventoryKind,
  openInventory,
  signOut,
}: {
  page: Page;
  setPage: (page: Page) => void;
  api: API;
  inventoryKind: string;
  openInventory: (kind?: string) => void;
  signOut: () => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const data = useData(api);
  const copy = pageCopy[page];
  const latest = newestTimestamp(data.sources.map((source) => source.last_seen_at));

  const navigate = (next: Page) => {
    setPage(next);
    setMenuOpen(false);
  };

  return (
    <div className="app-shell">
      <aside className={menuOpen ? "sidebar open" : "sidebar"}>
        <div className="sidebar-brand"><Brand lockup="Lens" /></div>
        <nav className="main-nav" aria-label="Lens Hub navigation">
          <p className="nav-heading">DISCOVERY</p>
          {navigation.map((item) => {
            const Icon = item.icon;
            return (
              <button className={page === item.page ? "active" : ""} key={item.page} onClick={() => navigate(item.page)}>
                <Icon size={17} />
                <span><b>{item.label}</b><small>{item.description}</small></span>
                {page === item.page && <ChevronRight size={14} />}
              </button>
            );
          })}
        </nav>
        <div className="lifecycle-card">
          <div className="lifecycle-card-title"><CircleDot size={14} /><span>Barrikade lifecycle</span></div>
          <div className="lifecycle-steps">
            <span className="active"><i />Discover</span>
            <span><i />Register</span>
            <span><i />Protect</span>
            <span><i />Govern</span>
          </div>
          <p>Lens is the discovery layer. It observes and reports; it does not approve, block, or enforce.</p>
        </div>
        <div className="sidebar-footer">
          <div className="hub-state"><i /><span><b>Lens Hub</b><small>Connected</small></span></div>
          <button onClick={signOut} aria-label="Sign out"><LogOut size={16} /></button>
        </div>
      </aside>

      <main className="main-area">
        <header className="topbar">
          <button className="mobile-menu" onClick={() => setMenuOpen((current) => !current)} aria-label="Toggle navigation">
            {menuOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
          <div className="topbar-status"><span className="status-dot" /> Inventory live</div>
          <div className="topbar-actions">
            <span className="last-sync">Last observation {latest ? relative(latest) : "pending"}</span>
            <button className="icon-button" onClick={data.refresh} aria-label="Refresh inventory" title="Refresh inventory">
              <RefreshCw size={16} className={data.loading ? "spinning" : ""} />
            </button>
            <ExportMenu api={api} />
          </div>
        </header>

        <div className="workspace">
          <section className="page-heading">
            <div>
              <p className="eyebrow">{copy.eyebrow}</p>
              <h1>{copy.title}</h1>
              <p>{copy.detail}</p>
            </div>
            {page === "Overview" && (
              <button className="button primary" onClick={() => openInventory()}>
                Explore inventory <ArrowRight size={15} />
              </button>
            )}
          </section>

          {data.loading && !data.loaded ? (
            <LoadingState />
          ) : data.error ? (
            <ErrorState detail={data.error} retry={data.refresh} />
          ) : (
            <PageContent
              page={page}
              api={api}
              entities={data.entities}
              relationships={data.relationships}
              sources={data.sources}
              changes={data.changes}
              initialInventoryKind={inventoryKind}
              openInventory={openInventory}
            />
          )}
        </div>
      </main>
      {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} aria-label="Close navigation" />}
    </div>
  );
}

function useData(api: API) {
  const [entities, setEntities] = useState<Entity[]>([]);
  const [relationships, setRelationships] = useState<Relationship[]>([]);
  const [sources, setSources] = useState<Source[]>([]);
  const [changes, setChanges] = useState<Change[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    Promise.all([api.entities(), api.relationships(), api.coverage(), api.changes()])
      .then(([entityResult, relationshipResult, sourceResult, changeResult]) => {
        if (cancelled) return;
        setEntities(entityResult.items);
        setRelationships(relationshipResult.items);
        setSources(sourceResult.sources);
        setChanges(changeResult.items);
        setLoaded(true);
      })
      .catch((reason) => !cancelled && setError(String(reason)))
      .finally(() => !cancelled && setLoading(false));
    return () => { cancelled = true; };
  }, [api, revision]);

  return {
    entities,
    relationships,
    sources,
    changes,
    error,
    loading,
    loaded,
    refresh: () => setRevision((current) => current + 1),
  };
}

function PageContent({
  page,
  api,
  entities,
  relationships,
  sources,
  changes,
  initialInventoryKind,
  openInventory,
}: {
  page: Page;
  api: API;
  entities: Entity[];
  relationships: Relationship[];
  sources: Source[];
  changes: Change[];
  initialInventoryKind: string;
  openInventory: (kind?: string) => void;
}) {
  if (page === "Overview") return <Overview entities={entities} sources={sources} changes={changes} openInventory={openInventory} />;
  if (page === "Inventory") return <Inventory entities={entities} relationships={relationships} api={api} initialKind={initialInventoryKind} />;
  if (page === "Topology") return <Topology entities={entities} relationships={relationships} openInventory={openInventory} />;
  if (page === "Changes") return <Changes items={changes} entities={entities} sources={sources} />;
  return <Sources items={sources} api={api} />;
}

function Overview({
  entities,
  sources,
  changes,
  openInventory,
}: {
  entities: Entity[];
  sources: Source[];
  changes: Change[];
  openInventory: (kind?: string) => void;
}) {
  const counts = useMemo(() => countBy(entities, "kind"), [entities]);
  const confidence = useMemo(() => countBy(entities, "confidence"), [entities]);
  const reportingSources = sources.filter((source) => source.last_seen_at).length;
  const staleEntities = sources.reduce((total, source) => total + (source.stale_entities ?? 0), 0);
  const categories = [
    { label: "Agents & runtimes", detail: "Agent definitions, execution surfaces, and frameworks", kinds: ["agent", "runtime", "framework"], icon: Bot, color: "orange" },
    { label: "MCP, skills & tools", detail: "Configured capabilities and tool servers", kinds: ["mcp_server", "skill", "tool"], icon: PlugZap, color: "purple" },
    { label: "Models & inference", detail: "Model inventory and serving endpoints", kinds: ["model", "model_server"], icon: BrainCircuit, color: "blue" },
    { label: "Code & delivery", detail: "Repositories, workloads, workflows, and APIs", kinds: ["repository", "workload", "workflow", "api_service", "api_operation"], icon: GitBranch, color: "green" },
  ];

  return (
    <div className="page-stack">
      <section className="kpi-grid">
        <Kpi icon={PackageSearch} label="Current entities" value={entities.length} detail={`${Object.keys(counts).length} entity types`} />
        <Kpi icon={CheckCircle2} label="Confirmed findings" value={confidence.confirmed ?? 0} detail="Authoritative or corroborated" />
        <Kpi icon={RadioTower} label="Sources reporting" value={`${reportingSources}/${sources.length}`} detail={sources.length ? "Across the organization" : "Enroll your first source"} />
        <Kpi icon={Clock3} label="Stale entities" value={staleEntities} detail="Awaiting source reconciliation" tone={staleEntities ? "attention" : "default"} />
      </section>

      <section className="discovery-hero panel">
        <div className="discovery-hero-copy">
          <p className="eyebrow">DISCOVERED SURFACES</p>
          <h2>Your inventory, organized by how teams use it</h2>
          <p>Start broad, then drill into the exact evidence, posture, and relationships behind every finding.</p>
        </div>
        <div className="discovery-categories">
          {categories.map((category) => {
            const Icon = category.icon;
            const total = category.kinds.reduce((sum, kind) => sum + (counts[kind] ?? 0), 0);
            return (
              <button key={category.label} onClick={() => openInventory(category.kinds[0])}>
                <span className={`category-icon ${category.color}`}><Icon size={19} /></span>
                <span><b>{category.label}</b><small>{category.detail}</small></span>
                <strong>{total}</strong><ChevronRight size={15} />
              </button>
            );
          })}
        </div>
      </section>

      <div className="overview-grid">
        <section className="panel coverage-panel">
          <PanelTitle title="Fleet coverage" detail="Freshness and inventory contributed by each discovery source" action={<span className="panel-count">{sources.length} sources</span>} />
          <div className="source-table">
            {sources.length ? sources.slice(0, 10).map((source) => <SourceRow key={source.source_id} source={source} />) : (
              <EmptyState icon={RadioTower} title="No managed sources yet" detail="Enroll an endpoint to start building an organization-wide inventory." />
            )}
          </div>
        </section>

        <section className="panel confidence-panel">
          <PanelTitle title="Evidence confidence" detail="How strongly each entity is supported" />
          <ConfidenceBreakdown values={confidence} total={entities.length} />
          <div className="confidence-note">
            <ShieldCheck size={17} />
            <p><b>Confidence is not a risk score.</b><span>It describes the strength and independence of the discovery evidence.</span></p>
          </div>
        </section>

        <section className="panel recent-panel">
          <PanelTitle title="Recent changes" detail="Latest inventory observations" action={<Activity size={16} />} />
          <ChangePreview changes={changes.slice(0, 7)} entities={entities} />
        </section>

        <section className="panel boundary-panel">
          <div className="boundary-mark"><Radar size={22} /></div>
          <p className="eyebrow">LENS PRODUCT BOUNDARY</p>
          <h2>Discovery that hands off cleanly</h2>
          <p>Lens finds and describes your autonomous agent footprint. Open APIs and exports let any registration or control plane consume the inventory.</p>
          <div className="boundary-list">
            <span><Check size={14} /> Evidence-backed facts</span>
            <span><Check size={14} /> Sanitized metadata</span>
            <span><Check size={14} /> Interoperable graph</span>
          </div>
        </section>
      </div>
    </div>
  );
}

function Inventory({
  entities,
  relationships,
  api,
  initialKind,
}: {
  entities: Entity[];
  relationships: Relationship[];
  api: API;
  initialKind: string;
}) {
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState(initialKind);
  const [confidence, setConfidence] = useState("all");
  const [posture, setPosture] = useState("all");
  const [selected, setSelected] = useState<EntityDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const kinds = useMemo(() => [...new Set(entities.map((entity) => entity.kind))].sort(), [entities]);

  useEffect(() => setKind(initialKind), [initialKind]);

  const shown = useMemo(() => entities.filter((entity) => {
    const search = query.trim().toLowerCase();
    const matchesSearch = !search || `${entity.name} ${entity.kind} ${Object.keys(entity.attributes).join(" ")}`.toLowerCase().includes(search);
    const matchesKind = kind === "all" || entity.kind === kind;
    const matchesConfidence = confidence === "all" || entity.confidence === confidence;
    const matchesPosture = posture === "all" || Boolean(entity.attributes[posture]);
    return matchesSearch && matchesKind && matchesConfidence && matchesPosture;
  }), [entities, query, kind, confidence, posture]);

  const open = (entity: Entity) => {
    setDetailError("");
    setDetailLoading(true);
    api.entity(entity.id)
      .then(setSelected)
      .catch((reason) => setDetailError(String(reason)))
      .finally(() => setDetailLoading(false));
  };

  const clearFilters = () => {
    setQuery("");
    setKind("all");
    setConfidence("all");
    setPosture("all");
  };

  return (
    <div className="page-stack inventory-page">
      <section className="panel inventory-panel">
        <div className="inventory-toolbar">
          <label className="search-box">
            <Search size={16} />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search names, types, and posture…" />
          </label>
          <label className="select-box"><span>Type</span><select value={kind} onChange={(event) => setKind(event.target.value)}><option value="all">All types</option>{kinds.map((item) => <option value={item} key={item}>{kindLabel(item)}</option>)}</select><ChevronDown size={14} /></label>
          <label className="select-box"><span>Confidence</span><select value={confidence} onChange={(event) => setConfidence(event.target.value)}><option value="all">All confidence</option><option value="confirmed">Confirmed</option><option value="likely">Likely</option><option value="possible">Possible</option></select><ChevronDown size={14} /></label>
          <label className="select-box"><span>Posture</span><select value={posture} onChange={(event) => setPosture(event.target.value)}><option value="all">Any posture</option><option value="installed">Installed</option><option value="configured">Configured</option><option value="running_at_scan">Running at scan</option><option value="state_present">State present</option><option value="cached">Cached</option></select><ChevronDown size={14} /></label>
        </div>

        <div className="inventory-summary">
          <span><SlidersHorizontal size={14} /> Showing <b>{shown.length}</b> of {entities.length} entities</span>
          {(query || kind !== "all" || confidence !== "all" || posture !== "all") && <button onClick={clearFilters}>Clear filters <X size={13} /></button>}
        </div>

        <div className="entity-table" role="table" aria-label="Discovery inventory">
          <div className="entity-row entity-head" role="row">
            <span>Entity</span><span>Type</span><span>Factual posture</span><span>Confidence</span><span>Last observed</span><span />
          </div>
          {shown.map((entity) => (
            <button className="entity-row" role="row" key={entity.id} onClick={() => open(entity)}>
              <span className="entity-identity"><KindIcon kind={entity.kind} /><span><b>{entity.name}</b><small>{compactIdentifier(entity)}</small></span></span>
              <span><span className="kind-badge">{kindLabel(entity.kind)}</span></span>
              <span className="posture-list">{postureFacts(entity).length ? postureFacts(entity).slice(0, 3).map((fact) => <i key={fact}>{fact}</i>) : <i className="quiet">Observed</i>}</span>
              <span><ConfidencePill value={entity.confidence} /></span>
              <span className="observed-time">{relative(entity.last_seen_at)}</span>
              <span><ChevronRight size={15} /></span>
            </button>
          ))}
          {!shown.length && <EmptyState icon={Search} title="No entities match these filters" detail="Clear a filter or search for another name or type." />}
        </div>
      </section>

      {detailLoading && <div className="drawer-loading"><RefreshCw className="spinning" size={20} /></div>}
      {detailError && <div className="toast-error">{detailError}<button onClick={() => setDetailError("")}><X size={14} /></button></div>}
      {selected && <EntityDrawer item={selected} entities={entities} relationships={relationships} close={() => setSelected(null)} />}
    </div>
  );
}

function EntityDrawer({
  item,
  entities,
  relationships,
  close,
}: {
  item: EntityDetail;
  entities: Entity[];
  relationships: Relationship[];
  close: () => void;
}) {
  const names = useMemo(() => new Map(entities.map((entity) => [entity.id, entity])), [entities]);
  const related = relationships.filter((relationship) => relationship.from === item.id || relationship.to === item.id);
  const attributes = Object.entries(item.attributes).sort(([left], [right]) => attributeRank(left) - attributeRank(right) || left.localeCompare(right));

  useEffect(() => {
    const listener = (event: KeyboardEvent) => event.key === "Escape" && close();
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [close]);

  return (
    <div className="drawer-overlay" onMouseDown={close}>
      <aside className="entity-drawer" onMouseDown={(event) => event.stopPropagation()} aria-label={`${item.name} details`}>
        <header className="drawer-header">
          <div className="drawer-heading">
            <KindIcon kind={item.kind} large />
            <div><p className="eyebrow">EVIDENCE-BACKED FINDING</p><h2>{item.name}</h2><div><span className="kind-badge">{kindLabel(item.kind)}</span><ConfidencePill value={item.confidence} />{item.stale && <span className="stale-pill">Stale</span>}</div></div>
          </div>
          <button className="icon-button" onClick={close} aria-label="Close details"><X size={18} /></button>
        </header>

        <div className="drawer-body">
          <section className="drawer-section">
            <PanelTitle title="Factual posture" detail="Sanitized attributes reported by discovery collectors" />
            {attributes.length ? <dl className="facts-grid">{attributes.map(([key, value]) => <div key={key}><dt>{humanize(key)}</dt><dd>{displayValue(value)}</dd></div>)}</dl> : <EmptyState icon={CircleDot} title="No posture attributes" detail="This entity is represented by identity and relationship evidence." compact />}
          </section>

          {item.canonical_key && (
            <section className="drawer-section identity-section">
              <PanelTitle title="Stable identity" detail="Deterministic identity used for reconciliation" />
              <div className="copy-field"><code>{item.canonical_key}</code><CopyButton value={item.canonical_key} /></div>
            </section>
          )}

          <section className="drawer-section">
            <PanelTitle title="Relationships" detail={`${related.length} current connections in the discovery graph`} />
            <div className="related-list">
              {related.slice(0, 20).map((relationship) => {
                const outbound = relationship.from === item.id;
                const other = names.get(outbound ? relationship.to : relationship.from);
                return (
                  <div key={relationship.id}>
                    <KindIcon kind={other?.kind ?? "unknown"} />
                    <span><small>{outbound ? humanize(relationship.kind) : `Is ${humanize(relationship.kind)} by`}</small><b>{other?.name ?? "Unknown entity"}</b></span>
                    <ConfidencePill value={relationship.confidence} />
                  </div>
                );
              })}
              {!related.length && <EmptyState icon={Network} title="No current relationships" detail="This finding is currently an independent inventory node." compact />}
            </div>
          </section>

          <section className="drawer-section">
            <PanelTitle title="Evidence trail" detail={`${item.evidence?.length ?? 0} retained observations`} />
            <div className="evidence-cards">
              {item.evidence?.length ? item.evidence.map((evidence) => (
                <article key={evidence.id}>
                  <div><span className="method-pill">{humanize(evidence.method)}</span><time>{formatDate(evidence.observed_at)}</time></div>
                  <h3>{evidence.detector_id}</h3>
                  <p>{humanize(evidence.family)} · {evidence.specificity} specificity · detector {evidence.detector_version}</p>
                  {evidence.locator && <code>{evidence.locator}</code>}
                </article>
              )) : <EmptyState icon={Radar} title="No retained observation" detail="Catalog-derived capability entities may not include collector evidence." compact />}
            </div>
          </section>

          <section className="drawer-section provenance-section">
            <span>First seen <b>{item.first_seen_at ? formatDate(item.first_seen_at) : "Unknown"}</b></span>
            <span>Last observed <b>{formatDate(item.last_seen_at)}</b></span>
          </section>
        </div>
      </aside>
    </div>
  );
}

function Topology({
  entities,
  relationships,
  openInventory,
}: {
  entities: Entity[];
  relationships: Relationship[];
  openInventory: (kind?: string) => void;
}) {
  const [type, setType] = useState("all");
  const [query, setQuery] = useState("");
  const entityMap = useMemo(() => new Map(entities.map((entity) => [entity.id, entity])), [entities]);
  const types = useMemo(() => [...new Set(relationships.map((relationship) => relationship.kind))].sort(), [relationships]);
  const filtered = relationships.filter((relationship) => {
    const from = entityMap.get(relationship.from);
    const to = entityMap.get(relationship.to);
    const search = query.trim().toLowerCase();
    return (type === "all" || relationship.kind === type) && (!search || `${from?.name} ${to?.name} ${relationship.kind}`.toLowerCase().includes(search));
  });
  const pairs = useMemo(() => {
    const values = new Map<string, number>();
    relationships.forEach((relationship) => {
      const from = entityMap.get(relationship.from)?.kind ?? "unknown";
      const to = entityMap.get(relationship.to)?.kind ?? "unknown";
      const key = `${from}|${relationship.kind}|${to}`;
      values.set(key, (values.get(key) ?? 0) + 1);
    });
    return [...values.entries()].sort((left, right) => right[1] - left[1]).slice(0, 8);
  }, [relationships, entityMap]);

  return (
    <div className="page-stack topology-page">
      <section className="topology-kpis">
        <div><Network size={18} /><span><b>{relationships.length}</b><small>Current relationships</small></span></div>
        <div><Boxes size={18} /><span><b>{entities.length}</b><small>Connected inventory nodes</small></span></div>
        <div><Layers3 size={18} /><span><b>{types.length}</b><small>Relationship types</small></span></div>
      </section>

      <section className="panel topology-shape">
        <PanelTitle title="Topology shape" detail="Most common paths through the discovery graph" />
        <div className="topology-pairs">
          {pairs.map(([key, count]) => {
            const [from, relationship, to] = key.split("|");
            return (
              <button key={key} onClick={() => setType(relationship)}>
                <span><KindIcon kind={from} /><b>{kindLabel(from)}</b></span>
                <span className="relationship-arrow"><small>{humanize(relationship)}</small><ArrowRight size={16} /></span>
                <span><KindIcon kind={to} /><b>{kindLabel(to)}</b></span>
                <strong>{count}</strong>
              </button>
            );
          })}
          {!pairs.length && <EmptyState icon={Network} title="No relationships yet" detail="Relationships appear as sources contribute connected inventory." />}
        </div>
      </section>

      <section className="panel relationship-explorer">
        <PanelTitle title="Relationship explorer" detail="Inspect the exact entity-to-entity connections" action={<span className="panel-count">{filtered.length} edges</span>} />
        <div className="relationship-toolbar">
          <label className="search-box"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search connected entities…" /></label>
          <label className="select-box"><span>Relationship</span><select value={type} onChange={(event) => setType(event.target.value)}><option value="all">All relationships</option>{types.map((item) => <option value={item} key={item}>{humanize(item)}</option>)}</select><ChevronDown size={14} /></label>
        </div>
        <div className="relationship-list">
          {filtered.slice(0, 120).map((relationship) => {
            const from = entityMap.get(relationship.from);
            const to = entityMap.get(relationship.to);
            return (
              <article key={relationship.id}>
                <button onClick={() => openInventory(from?.kind)}><KindIcon kind={from?.kind ?? "unknown"} /><span><small>{kindLabel(from?.kind ?? "unknown")}</small><b>{from?.name ?? "Unknown"}</b></span></button>
                <div><span>{humanize(relationship.kind)}</span><ArrowRight size={17} /><ConfidencePill value={relationship.confidence} /></div>
                <button onClick={() => openInventory(to?.kind)}><KindIcon kind={to?.kind ?? "unknown"} /><span><small>{kindLabel(to?.kind ?? "unknown")}</small><b>{to?.name ?? "Unknown"}</b></span></button>
              </article>
            );
          })}
          {!filtered.length && <EmptyState icon={Search} title="No relationships match" detail="Try another entity name or relationship type." />}
        </div>
      </section>
    </div>
  );
}

function Changes({ items, entities, sources }: { items: Change[]; entities: Entity[]; sources: Source[] }) {
  const [eventType, setEventType] = useState("all");
  const [query, setQuery] = useState("");
  const names = useMemo(() => new Map(entities.map((entity) => [entity.id, entity])), [entities]);
  const sourceNames = useMemo(() => new Map(sources.map((source) => [source.source_id, source.name])), [sources]);
  const events = useMemo(() => countBy(items, "event_type"), [items]);
  const shown = items.filter((item) => {
    const entity = names.get(item.entity_id);
    const search = query.trim().toLowerCase();
    return (eventType === "all" || item.event_type === eventType) && (!search || `${entity?.name} ${item.event_type} ${sourceNames.get(item.source_id)}`.toLowerCase().includes(search));
  });

  return (
    <div className="page-stack changes-page">
      <section className="change-summary">
        {[
          ["entity.discovered", "Discovered", events["entity.discovered"] ?? 0],
          ["entity.updated", "Updated", events["entity.updated"] ?? 0],
          ["entity.stale", "Stale", events["entity.stale"] ?? 0],
          ["entity.removed", "Removed", events["entity.removed"] ?? 0],
        ].map(([value, label, count]) => <button className={eventType === value ? "active" : ""} onClick={() => setEventType(eventType === value ? "all" : String(value))} key={String(value)}><span className={`event-dot ${String(value).split(".")[1]}`} /><b>{Number(count).toLocaleString()}</b><small>{label}</small></button>)}
      </section>
      <section className="panel change-log">
        <PanelTitle title="Change history" detail="Normalized inventory events from every discovery source" action={<span className="panel-count">90-day history</span>} />
        <div className="change-toolbar">
          <label className="search-box"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search entities or sources…" /></label>
          {(query || eventType !== "all") && <button className="text-button" onClick={() => { setQuery(""); setEventType("all"); }}>Clear filters <X size={13} /></button>}
        </div>
        <div className="timeline">
          {shown.map((item) => {
            const entity = names.get(item.entity_id);
            const event = item.event_type.replace("entity.", "");
            return (
              <article key={item.id}>
                <div className={`timeline-marker ${event}`}><span /></div>
                <div className="timeline-content">
                  <div><span className={`event-pill ${event}`}>{event}</span><time>{formatDate(item.changed_at)}</time></div>
                  <h3>{entity?.name ?? shortID(item.entity_id)}</h3>
                  <p>{kindLabel(entity?.kind ?? "entity")} · observed by <b>{sourceNames.get(item.source_id) ?? shortID(item.source_id)}</b></p>
                </div>
              </article>
            );
          })}
          {!shown.length && <EmptyState icon={History} title="No changes match" detail="Change events will appear as sources reconcile inventory." />}
        </div>
      </section>
    </div>
  );
}

function Sources({ items, api }: { items: Source[]; api: API }) {
  const [code, setCode] = useState<{ code: string; expires_at: string } | null>(null);
  const [error, setError] = useState("");
  const current = items.reduce((total, source) => total + source.current_entities, 0);
  const stale = items.reduce((total, source) => total + source.stale_entities, 0);

  const create = () => {
    setError("");
    api.enrollment().then(setCode).catch((reason) => setError(String(reason)));
  };

  return (
    <div className="page-stack sources-page">
      <section className="source-kpis">
        <Kpi icon={RadioTower} label="Enrolled sources" value={items.length} detail={`${items.filter((item) => item.last_seen_at).length} have reported`} />
        <Kpi icon={Boxes} label="Contributed entities" value={current} detail="Current source observations" />
        <Kpi icon={Clock3} label="Stale observations" value={stale} detail="Pending reconciliation" tone={stale ? "attention" : "default"} />
      </section>

      <section className="panel source-inventory">
        <PanelTitle title="Discovery sources" detail="Endpoint, repository, and Kubernetes coverage" action={<span className="panel-count">{items.length} enrolled</span>} />
        <div className="source-table full">
          <div className="source-row source-head"><span>Source</span><span>Type</span><span>Inventory</span><span>Collector</span><span>Last full scan</span><span>Status</span></div>
          {items.map((source) => <SourceRow key={source.source_id} source={source} expanded />)}
          {!items.length && <EmptyState icon={RadioTower} title="No sources enrolled" detail="Use the enrollment card below to connect the first endpoint." />}
        </div>
      </section>

      <div className="enrollment-grid">
        <section className="panel enrollment-card featured">
          <div className="enrollment-icon"><Monitor size={21} /></div>
          <p className="eyebrow">QUICK ENROLLMENT</p>
          <h2>Connect one endpoint</h2>
          <p>Generate a single-use code that expires in ten minutes. No signup or package customization required.</p>
          {code ? (
            <div className="enrollment-result">
              <div className="enrollment-code"><span>{code.code}</span><CopyButton value={code.code} /></div>
              <div className="command-box"><code>npx barrikade-lens enroll {code.code} --hub {location.origin}</code><CopyButton value={`npx barrikade-lens enroll ${code.code} --hub ${location.origin}`} /></div>
              <span className="expiry"><Clock3 size={13} /> Expires {relative(code.expires_at, true)}</span>
              <button className="text-button" onClick={create}>Generate another code <RefreshCw size={13} /></button>
            </div>
          ) : <button className="button primary" onClick={create}>Generate enrollment code <ArrowRight size={15} /></button>}
          {error && <p className="error-message">{error}</p>}
        </section>

        <FleetProfile api={api} />
      </div>
    </div>
  );
}

function FleetProfile({ api }: { api: API }) {
  const [uses, setUses] = useState(100);
  const [hours, setHours] = useState(24);
  const [profile, setProfile] = useState<{ code: string; expires_at: string } | null>(null);
  const [error, setError] = useState("");

  const create = () => {
    setError("");
    api.enrollment(Math.max(1, uses), Math.max(1, hours) * 3600, "endpoint")
      .then(setProfile)
      .catch((reason) => setError(String(reason)));
  };

  const profileText = profile ? `BARRIKADE_LENS_ENROLLMENT_CODE=${profile.code}\nBARRIKADE_LENS_HUB=${location.origin}` : "";

  return (
    <section className="panel enrollment-card">
      <div className="enrollment-icon secondary"><Layers3 size={21} /></div>
      <p className="eyebrow">FLEET ROLLOUT</p>
      <h2>Create a deployment profile</h2>
      <p>Issue an expiring, use-limited bootstrap profile for Jamf, Intune, Fleet, or Linux package automation.</p>
      {profile ? (
        <div className="enrollment-result">
          <div className="enrollment-code"><span>{profile.code}</span><CopyButton value={profile.code} /></div>
          <div className="command-box multi"><code>{profileText}</code><CopyButton value={profileText} /></div>
          <span className="expiry"><Clock3 size={13} /> {uses} uses · expires {formatDate(profile.expires_at)}</span>
          <button className="text-button" onClick={() => setProfile(null)}>Create another profile <RefreshCw size={13} /></button>
        </div>
      ) : (
        <>
          <div className="profile-fields">
            <label>Maximum devices<input type="number" min="1" max="10000" value={uses} onChange={(event) => setUses(Number(event.target.value))} /></label>
            <label>Expires in hours<input type="number" min="1" max="24" value={hours} onChange={(event) => setHours(Number(event.target.value))} /></label>
          </div>
          <button className="button secondary" onClick={create}>Create fleet profile <ArrowRight size={15} /></button>
        </>
      )}
      {error && <p className="error-message">{error}</p>}
    </section>
  );
}

function SourceRow({ source, expanded = false }: { source: Source; expanded?: boolean }) {
  const state = sourceState(source);
  return (
    <div className={`source-row ${expanded ? "expanded" : ""}`}>
      <span className="source-name"><SourceIcon type={source.source_type} /><span><b>{source.name}</b><small>{source.platform || source.source_id}</small></span></span>
      {expanded && <span><span className="kind-badge">{humanize(source.source_type)}</span></span>}
      <span className="source-count"><b>{source.current_entities}</b><small>{source.stale_entities ? `${source.stale_entities} stale` : "current entities"}</small></span>
      {expanded && <span className="collector-version">{source.collector_version || "—"}</span>}
      {expanded && <span className="last-full">{source.last_full_at ? relative(source.last_full_at) : "Awaiting scan"}</span>}
      <span className={`source-state ${state.tone}`}><i />{state.label}</span>
      {!expanded && <time>{source.last_seen_at ? relative(source.last_seen_at) : "Never reported"}</time>}
    </div>
  );
}

function ChangePreview({ changes, entities }: { changes: Change[]; entities: Entity[] }) {
  const names = new Map(entities.map((entity) => [entity.id, entity]));
  return (
    <div className="change-preview">
      {changes.map((change) => {
        const event = change.event_type.replace("entity.", "");
        return <div key={change.id}><span className={`event-dot ${event}`} /><span><b>{names.get(change.entity_id)?.name ?? shortID(change.entity_id)}</b><small>{event} · {relative(change.changed_at)}</small></span></div>;
      })}
      {!changes.length && <EmptyState icon={History} title="No changes observed yet" detail="Changes appear after the first inventory reconciliation." compact />}
    </div>
  );
}

function ConfidenceBreakdown({ values, total }: { values: Record<string, number>; total: number }) {
  const rows = [
    ["confirmed", "Confirmed", "Authoritative descriptor or independent evidence"],
    ["likely", "Likely", "One high-specificity evidence family"],
    ["possible", "Possible", "State or lower-specificity evidence only"],
  ];
  return (
    <div className="confidence-breakdown">
      <div className="confidence-bar" aria-label="Confidence distribution">
        {rows.map(([value]) => <span className={value} style={{ width: `${total ? ((values[value] ?? 0) / total) * 100 : 0}%` }} key={value} />)}
      </div>
      {rows.map(([value, label, detail]) => (
        <div className="confidence-row" key={value}>
          <span className={`confidence-swatch ${value}`} />
          <span><b>{label}</b><small>{detail}</small></span>
          <strong>{values[value] ?? 0}</strong>
        </div>
      ))}
    </div>
  );
}

function ExportMenu({ api }: { api: API }) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const download = (format: "lens" | "ndjson" | "cyclonedx") => {
    setOpen(false);
    api.downloadExport(format).catch((reason) => setError(String(reason)));
  };
  return (
    <div className="export-menu">
      <button className="button subtle" onClick={() => setOpen((current) => !current)}><Download size={15} /> Export <ChevronDown size={13} /></button>
      {open && <div className="export-popover"><button onClick={() => download("lens")}><b>Lens JSON</b><small>Canonical discovery graph</small></button><button onClick={() => download("ndjson")}><b>NDJSON</b><small>Streaming and forwarding</small></button><button onClick={() => download("cyclonedx")}><b>CycloneDX 1.7</b><small>Software and AI BOM</small></button></div>}
      {error && <div className="export-error">{error}</div>}
    </div>
  );
}

function Brand({ lockup }: { lockup?: string }) {
  return (
    <div className="brand-lockup">
      <LogoMark />
      <span><b>BARRIKADE</b>{lockup && <small>{lockup}</small>}</span>
    </div>
  );
}

function LogoMark() {
  return <span className="logo-mark" aria-hidden="true"><i className="logo-left" /><i className="logo-slash" /><i className="logo-right" /></span>;
}

function Kpi({ icon: Icon, label, value, detail, tone = "default" }: { icon: LucideIcon; label: string; value: number | string; detail: string; tone?: "default" | "attention" }) {
  return <div className={`kpi-card ${tone}`}><span className="kpi-icon"><Icon size={18} /></span><div><span>{label}</span><b>{typeof value === "number" ? value.toLocaleString() : value}</b><small>{detail}</small></div></div>;
}

function PanelTitle({ title, detail, action }: { title: string; detail: string; action?: ReactNode }) {
  return <div className="panel-title"><div><h2>{title}</h2><p>{detail}</p></div>{action && <div className="panel-action">{action}</div>}</div>;
}

function ConfidencePill({ value }: { value: string }) {
  return <span className={`confidence-pill ${value}`}><i />{value}</span>;
}

function KindIcon({ kind, large = false }: { kind: string; large?: boolean }) {
  const Icon = kindConfig[kind]?.icon ?? CircleDot;
  return <span className={`kind-icon ${kindConfig[kind]?.family ?? "source"} ${large ? "large" : ""}`}><Icon size={large ? 21 : 16} /></span>;
}

function SourceIcon({ type }: { type: string }) {
  const Icon = type === "repository" ? GitBranch : type === "kubernetes" ? Container : Monitor;
  return <span className={`source-icon ${type}`}><Icon size={16} /></span>;
}

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  return <button className="copy-button" onClick={copy} aria-label="Copy to clipboard">{copied ? <Check size={14} /> : <Copy size={14} />}</button>;
}

function EmptyState({ icon: Icon, title, detail, compact = false }: { icon: LucideIcon; title: string; detail: string; compact?: boolean }) {
  return <div className={`empty-state ${compact ? "compact" : ""}`}><Icon size={compact ? 18 : 24} /><b>{title}</b><span>{detail}</span></div>;
}

function LoadingState() {
  return <div className="loading-grid"><div /><div /><div /><div className="wide" /><div className="wide" /></div>;
}

function ErrorState({ detail, retry }: { detail: string; retry: () => void }) {
  return <section className="panel error-state"><span><X size={20} /></span><h2>Lens Hub is unavailable</h2><p>{detail}</p><button className="button secondary" onClick={retry}><RefreshCw size={15} /> Try again</button></section>;
}

function countBy<T extends Record<string, unknown>>(items: T[], key: keyof T) {
  return items.reduce<Record<string, number>>((result, item) => {
    const value = String(item[key]);
    result[value] = (result[value] ?? 0) + 1;
    return result;
  }, {});
}

function kindLabel(kind: string) {
  return kindConfig[kind]?.label ?? humanize(kind);
}

function humanize(value: string) {
  return value.replaceAll("_", " ").replaceAll("-", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function postureFacts(entity: Entity) {
  const labels: Array<[string, string]> = [
    ["running_at_scan", "Running"], ["installed", "Installed"], ["configured", "Configured"],
    ["enabled", "Enabled"], ["cached", "Cached"], ["state_present", "State present"],
    ["deployment_reference", "Declared"], ["agent_card", "Agent card"], ["listener_process_verified", "Socket verified"],
  ];
  return labels.filter(([key]) => entity.attributes[key] === true).map(([, label]) => label);
}

function compactIdentifier(entity: Entity) {
  if (typeof entity.attributes.host === "string") return entity.attributes.host;
  if (typeof entity.attributes.locator === "string") return entity.attributes.locator;
  if (typeof entity.attributes.repository_url === "string") return entity.attributes.repository_url;
  return shortID(entity.id);
}

function attributeRank(key: string) {
  const priority = ["running_at_scan", "installed", "configured", "enabled", "state_present", "cached", "transport", "binding", "port", "host"];
  const index = priority.indexOf(key);
  return index < 0 ? 100 : index;
}

function displayValue(value: unknown): string {
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (Array.isArray(value)) return value.map(String).join(", ");
  if (value && typeof value === "object") return Object.entries(value as Record<string, unknown>).map(([key, item]) => `${humanize(key)}: ${String(item)}`).join(" · ");
  return String(value);
}

function sourceState(source: Source) {
  if (!source.last_seen_at) return { label: "Awaiting scan", tone: "pending" };
  if (source.stale_entities > 0) return { label: "Has stale data", tone: "attention" };
  return { label: "Reporting", tone: "healthy" };
}

function shortID(value: string) {
  const plain = value.replace("urn:lens:", "");
  return plain.length > 18 ? `${plain.slice(0, 10)}…${plain.slice(-6)}` : plain;
}

function relative(value: string, future = false) {
  const seconds = Math.round((Date.now() - new Date(value).getTime()) / 1000);
  const absolute = Math.abs(seconds);
  const suffix = future || seconds < 0 ? "from now" : "ago";
  if (absolute < 60) return `${absolute}s ${suffix}`;
  if (absolute < 3600) return `${Math.floor(absolute / 60)}m ${suffix}`;
  if (absolute < 86400) return `${Math.floor(absolute / 3600)}h ${suffix}`;
  return `${Math.floor(absolute / 86400)}d ${suffix}`;
}

function formatDate(value: string) {
  return new Date(value).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
}

function newestTimestamp(values: Array<string | undefined>) {
  const valid = values.filter(Boolean) as string[];
  return valid.sort((left, right) => new Date(right).getTime() - new Date(left).getTime())[0];
}

function base64URL(data: Uint8Array) {
  let binary = "";
  data.forEach((byte) => { binary += String.fromCharCode(byte); });
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/g, "");
}

function randomURLSafe(length: number) {
  const data = new Uint8Array(length);
  crypto.getRandomValues(data);
  return base64URL(data);
}
