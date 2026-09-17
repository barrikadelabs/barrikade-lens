import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Bot, ChevronDown, ChevronRight, FileSearch, Fingerprint, MapPin, Monitor, Network, PackageSearch } from "lucide-react";
import { API, type Evidence, type ProductItem, type SystemDetail, type SystemItem } from "../../api";
import { captureAnalytics } from "../../analytics";
import { ConfidencePill, ConnectionRow, Drawer, Empty, Fact, Failure, FilterBar, Identity, InlineError, InlineLoading, Loading, Select, StatePill, TypePill, groupConnections, pretty, relative, useRemote } from "../../ui";
import { analyticsControl } from "../shared/analytics";
import { ProductDrawer } from "../shared/ProductDrawer";

export function SystemsPage({ api, revision }: { api: API; revision: number }) {
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
