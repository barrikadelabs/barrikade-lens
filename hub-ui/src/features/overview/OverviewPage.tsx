import { useEffect, useRef, useState } from "react";
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
  const trackedFirstResults = useRef(false);
  useEffect(() => {
    const coverageState = overview.data?.executive_summary?.coverage_state;
    if (trackedFirstResults.current || !coverageState || !["ready", "partial", "stale"].includes(coverageState)) return;
    trackedFirstResults.current = true;
    captureAnalytics({ name: "first_results_viewed", properties: {} });
  }, [overview.data?.executive_summary?.coverage_state]);
  if (overview.loading || products.loading) return <Loading />;
  if (overview.error || products.error || !overview.data) return <Failure error={overview.error || products.error} retry={() => { overview.reload(); products.reload(); }} />;
  const data = overview.data;
  const executiveSystems = data.executive_summary?.systems;
  const systems = executiveSystems ? Object.fromEntries(Array.from(new Set([...Object.keys(executiveSystems.fresh_by_type), ...Object.keys(executiveSystems.stale_by_type)])).map((type) => [type, (executiveSystems.fresh_by_type[type] ?? 0) + (executiveSystems.stale_by_type[type] ?? 0)])) : data.footprint.system_types;
  const states = data.footprint.states;
  const totalSystems = sum(Object.values(systems));
  const runningCount = states.running ?? 0;
  const findingCount = data.executive_summary ? data.executive_summary.findings.fresh + data.executive_summary.findings.stale : data.exposure_summary?.total ?? 0;
  const reportingTargets = sum(data.coverage.map((item) => item.reporting));
  const reportingLabel = `${reportingTargets} reporting ${reportingTargets === 1 ? "location" : "locations"}`;
  const attention = [
    ...(data.executive_summary?.top_findings ?? data.exposure_summary?.top_findings ?? []).map((finding) => [`${pretty(finding.severity)} · ${finding.title}`, 1, "See what Lens found and what to check next", "Findings", finding.id]),
    ["No owner assigned", data.executive_summary?.effective_ownership.unowned ?? data.attention.unattributed_systems, "These AI tools and agents have no responsible person or team", "Inventory"],
    ["More information needed", data.attention.possible_only_systems, "Lens needs another reliable signal before it can confirm these results", "Inventory"],
    ["Services are reachable beyond this device", data.attention.non_loopback_services, "Review AI services that other devices may be able to reach", "Inventory"],
    ["Some scan data is missing", data.attention.partial_scans, "Lens could not check every location", "Connections"],
    ["Locations have stopped reporting", data.attention.stale_targets, "Their latest results may be out of date", "Connections"],
    ["Sources disagree", data.attention.fact_conflicts, "Lens found conflicting information about an AI tool or agent", "Inventory"],
  ].filter((item) => Number(item[1]) > 0) as Array<[string, number, string, Page, string?]>;
  const changes = groupChanges(data.changes).slice(0, 4);

  return <div className="page-stack executive-overview">
    <div className="overview-toolbar"><span>Updated {relative(data.generated_at)}</span><div className="window-switch">{["24h", "7d", "30d"].map((item) => <button className={window === item ? "active" : ""} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "overview", interaction: "filter_changed", control: "window" } }); const next = new URLSearchParams(search); item === "7d" ? next.delete("window") : next.set("window", item); setSearch(next, { replace: true }); }} key={item}>{item}</button>)}</div></div>
    {data.executive_summary?.coverage_state === "unassessed" && <section className="panel unassessed-state"><div><p className="eyebrow">START HERE</p><h2>Connect your first location</h2><p>Choose a device, code repository, cloud account, or cluster. Lens will check it without making changes and build your AI inventory.</p></div><button className="button primary" onClick={() => location.assign("/connections/new")}>Add a connection <ArrowRight size={15} /></button></section>}
    {data.executive_summary?.coverage_state === "ready" && data.executive_summary.systems.known === 0 && <section className="panel successful-empty"><CheckCircle2 size={18} /><div><h2>No AI tools or agents found</h2><p>Lens checked the connected device {data.executive_summary.last_successful_evidence_at ? relative(data.executive_summary.last_successful_evidence_at) : "recently"}. Open Coverage to see what Lens checked and confirm that the device is still reporting.</p></div></section>}
    {data.executive_summary && data.executive_summary.coverage_state !== "ready" && <section className={`coverage-limitation ${data.executive_summary.coverage_state}`} role="status"><AlertCircle size={16} /><span><b>This view may be incomplete.</b> Lens includes older results and shows when they were last updated. The latest successful scan {data.executive_summary.last_successful_evidence_at ? `arrived ${relative(data.executive_summary.last_successful_evidence_at)}` : "has not arrived yet"}.</span></section>}
    <section className="exposure-hero">
      <div className="exposure-copy"><span>AI tools and agents found</span><h2><strong>{(data.executive_summary?.systems.known ?? totalSystems).toLocaleString()}</strong> across your organization</h2><p>{data.executive_summary ? `${data.executive_summary.systems.fresh} up to date · ${data.executive_summary.systems.stale} based on locations that are no longer reporting.` : `Results come from ${reportingLabel}. ${runningCount} are running now.`}</p><div className="enrollment-scope">{data.coverage.map((item) => <span key={item.target_type}><b>{pretty(item.target_type)}</b> {item.reporting ? `${item.reporting} reporting${item.stale ? ` · ${item.stale} not reporting recently` : ""}` : "Not connected"}</span>)}</div>{data.exposure_summary && <button className="overview-story-link" onClick={() => go("Findings")}>Open {findingCount} findings <ArrowRight size={14} /></button>}</div>
      <div className="exposure-facts">
        <ExecutiveFact value={runningCount} label="Running now" tone="active" onClick={() => location.assign("/inventory?state=running&confidence=confirmed&freshness=fresh")} />
        <ExecutiveFact value={data.executive_summary?.effective_ownership.unowned ?? data.attention.unattributed_systems ?? 0} label="Missing an owner" tone="attention" onClick={() => location.assign("/inventory?owner_status=unowned")} />
        <ExecutiveFact value={data.executive_summary ? (data.executive_summary.findings.fresh_by_severity.critical ?? 0) + (data.executive_summary.findings.stale_by_severity.critical ?? 0) : data.exposure_summary?.counts.critical ?? 0} label="Critical findings" tone="attention" onClick={() => location.assign("/findings?severity=critical")} />
        <ExecutiveFact value={reportingTargets} label="Locations reporting" tone="good" onClick={() => go("Connections")} />
      </div>
    </section>
    <section className="system-mix-strip">
      <div className="mix-heading"><span>Types of AI found</span><small>AI tools and agents only · related software excluded</small></div>
      <div className="system-breakdown">
        <button onClick={() => location.assign("/inventory?system_type=autonomous_agent")}><Bot size={18} /><span><b>{systems.autonomous_agent ?? 0}</b><small>Autonomous agents</small></span></button>
        <button onClick={() => location.assign("/inventory?system_type=agent_tool")}><TerminalSquare size={18} /><span><b>{systems.agent_tool ?? 0}</b><small>Agent-capable tools</small></span></button>
        <button onClick={() => location.assign("/inventory?system_type=model_runtime")}><BrainCircuit size={18} /><span><b>{systems.model_runtime ?? 0}</b><small>Model runtimes</small></span></button>
      </div>
    </section>
    <div className="executive-primary">
      <section className="panel running-panel"><PanelHeading title="AI tools and agents" detail="See every installation and the device accounts that Lens observed. An observed account is not an assigned owner." action={<button className="text-button" onClick={() => go("Inventory")}>Open AI inventory <ArrowRight size={13} /></button>} />
        {products.data?.items.length ? <div className="running-list">{products.data.items.slice(0, 8).map((item) => <button key={item.id} onClick={() => setSelectedProduct(item)}><span className="running-mark"><CircleDot size={14} /></span><span><b>{item.name}</b><small>{item.installation_count} {item.installation_count === 1 ? "installation" : "installations"} · {item.observed_user_count} observed {item.observed_user_count === 1 ? "account" : "accounts"}</small></span><span><strong>{item.fresh_count} up to date · {item.stale_count} older</strong><small>Last seen {relative(item.last_seen_at)}</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="No AI tools or agents found" detail="Items appear here after a connected location reports its first results." />}
      </section>
      <section className="panel attention-panel"><PanelHeading title="What needs review" detail="Concrete findings, not an opaque risk score" />
        {attention.length ? <div className="attention-list">{attention.map(([label, count, detail, destination, findingID]) => <button key={label} onClick={() => findingID ? location.assign(`/findings/${encodeURIComponent(findingID)}`) : destination === "Inventory" && label === "No owner assigned" ? location.assign("/inventory?owner_status=unowned") : go(destination)}><span className="attention-count active">{count}</span><span><b>{label}</b><small>{detail}</small></span><ChevronRight size={14} /></button>)}</div> : <Empty icon={CheckCircle2} title="Nothing needs review" detail="Lens found no current issues that need your attention." />}
      </section>
    </div>
    <div className="executive-secondary">
      <section className="panel change-panel"><PanelHeading title="Recent changes" detail={`Important changes from the last ${window}; routine updates are grouped together`} action={<button className="text-button" onClick={() => go("Changes")}>View history <ArrowRight size={13} /></button>} /><ChangeList items={changes} /></section>
      <section className="panel evidence-posture"><PanelHeading title="How reliable is this view?" detail="See how current the results are and how strongly Lens could confirm them" action={<button className="text-button" onClick={() => go("Connections")}>View coverage <ArrowRight size={13} /></button>} />{sum(Object.values(states)) ? <StateDistribution values={states} total={sum(Object.values(states))} /> : <p>No recent status observations.</p>}<ConfidenceSummary data={data.data_quality.confidence} /></section>
    </div>
		{selectedProduct && <ProductDrawer item={selectedProduct} onClose={() => setSelectedProduct(undefined)} />}
  </div>;
}
