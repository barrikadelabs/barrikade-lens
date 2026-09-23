import { useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { CheckCircle2, ChevronDown, ChevronRight, X } from "lucide-react";
import { API, type ExposureFinding } from "../../api";
import { captureAnalytics } from "../../analytics";
import { Empty, Failure, FilterBar, InlineError, InlineLoading, Loading, PanelHeading, Select, pretty, relative, useRemote } from "../../ui";
import { analyticsControl } from "../shared/analytics";

export function FindingsPage({ api, revision }: { api: API; revision: number }) {
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
    <FilterBar hideSearch><Select label="Severity" value={filters.severity} onChange={(value) => update("severity", value)} options={{ "": "All severities", critical: "Critical", high: "High", medium: "Medium", low: "Low" }} /><Select label="Last report" value={filters.freshness} onChange={(value) => update("freshness", value)} options={{ all: "All findings", fresh: "Up to date", stale: "Not reporting recently" }} /><Select label="Owner" value={filters.owner_status} onChange={(value) => update("owner_status", value)} options={{ "": "Any owner", unowned: "No owner assigned", owned: "Owner assigned" }} /><Select label="Type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All tools and agents", autonomous_agent: "Autonomous agents", agent_tool: "AI agent tools", model_runtime: "AI model runtimes" }} /></FilterBar>
    <section className="panel data-panel"><PanelHeading title="What needs review" detail="Unresolved findings across your organization, ordered by impact. Older evidence is marked clearly." count={items.length} /><div className="finding-list">{items.map((finding) => <button key={finding.id} className="finding-row" onClick={() => navigate(`/findings/${encodeURIComponent(finding.id)}?${search.toString()}`)}><span className={`severity-pill ${finding.severity}`}>{finding.severity}</span><span><b>{finding.title}</b><small>{finding.root_name} · {finding.evidence_freshness === "stale" ? "Not reporting recently" : "Up to date"} · last observed {relative(finding.evidence_last_seen_at || finding.last_seen_at)}</small></span><span className={finding.effective_ownership?.owned ? "fact good" : "fact quiet"}>{finding.effective_ownership?.owner_name || "No owner assigned"}</span><ChevronRight size={15} /></button>)}{!items.length && <Empty icon={CheckCircle2} title="No findings match" detail="Lens found no unresolved issues for these filters." />}</div>{remote.data?.next_cursor && <button className="load-more" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "findings", interaction: "load_more" } }); setCursor(remote.data!.next_cursor!); }}>Load more findings <ChevronDown size={15} /></button>}</section>
    {findingId && <AccessibleDialog title="Finding details" onClose={() => navigate(`/findings?${search.toString()}`)}>{detail.loading ? <InlineLoading /> : detail.error || !detail.data ? <InlineError text={detail.error || "Finding not found"} /> : <FindingDetail finding={detail.data} navigate={navigate} />}</AccessibleDialog>}
  </div>;
}

function FindingDetail({ finding, navigate }: { finding: ExposureFinding; navigate: ReturnType<typeof useNavigate> }) {
  return <div className="finding-detail"><span className={`severity-pill ${finding.severity}`}>{finding.severity}</span><h2>{finding.title}</h2><h3>What Lens found</h3><p>{finding.explanation}</p><dl><div><dt>AI tool or agent</dt><dd><button className="text-button" onClick={() => navigate(`/systems/${encodeURIComponent(finding.root_entity_id)}`)}>{finding.root_name}</button></dd></div><div><dt>Evidence status</dt><dd>{finding.evidence_freshness === "stale" ? "Location not reporting recently" : "Up to date"}</dd></div><div><dt>Last observed</dt><dd>{relative(finding.evidence_last_seen_at || finding.last_seen_at)}</dd></div><div><dt>Owner</dt><dd>{finding.effective_ownership?.owner_name || "No owner assigned"}</dd></div></dl><h3>Why it matters</h3><ol className="evidence-path">{finding.path.map((step, index) => <li key={`${step.entity_id}-${index}`}><b>{step.name}</b><span>{pretty(step.kind)} · {pretty(step.basis)}</span>{step.edge && <small>{pretty(step.edge)}</small>}</li>)}</ol><div className="next-step"><b>What to check next</b><p>{finding.recommended_next_step}</p></div></div>;
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
