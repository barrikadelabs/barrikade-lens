import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { AlertCircle, ArrowRight, Bot, BrainCircuit, CheckCircle2, ChevronRight, CircleDot, TerminalSquare } from "lucide-react";
import { API, type ProductItem } from "../../api";
import { captureAnalytics } from "../../analytics";
import { Empty, Failure, Loading, PanelHeading, pretty, relative, sum, useRemote } from "../../ui";
import type { Page } from "../../app/navigation";
import { ChangeList, groupChanges } from "../shared/ChangeList";
import { ProductDrawer } from "../shared/ProductDrawer";
import { ConfidenceSummary, ExecutiveFact, StateDistribution } from "./OverviewParts";

export function OverviewPage({ api, revision, go }: { api: API; revision: number; go: (page: Page) => void }) {
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
