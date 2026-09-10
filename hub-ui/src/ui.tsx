import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  AlertCircle, Bot, Boxes, BrainCircuit, CheckCircle2, ChevronDown, Cloud, Container,
  Copy, Database, Fingerprint, GitBranch, Link2, Monitor, PackageSearch, PlugZap, Radar,
  RefreshCw, Search, Server, TerminalSquare, UserRound, Workflow, X, type LucideIcon,
} from "lucide-react";
import type { Connection } from "./api";
import { captureAnalytics } from "./analytics";
import barrikadeLogoMark from "./assets/barrikade-logo-mark.svg";

const kindIcons: Record<string, LucideIcon> = {
  endpoint: Monitor, repository: GitBranch, cluster: Container, workload: Container, agent: Bot,
  runtime: TerminalSquare, framework: Boxes, mcp_server: PlugZap, skill: CheckCircle2, model: BrainCircuit,
  model_server: Server, api_service: Database, api_operation: Link2, workflow: Workflow, user: UserRound,
  cloud_environment: Cloud, identity: Fingerprint, knowledge_store: Database,
};

export function CopyBlock({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return <div className="install-command"><code>{value}</code><button onClick={() => navigator.clipboard.writeText(value).then(() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "setup", interaction: "command_copied" } }); setCopied(true); window.setTimeout(() => setCopied(false), 1500); })}><Copy size={15} />{copied ? "Copied" : "Copy"}</button></div>;
}

export function FilterBar({ search, setSearch, hideSearch, children }: { search?: string; setSearch?: (value: string) => void; hideSearch?: boolean; children: ReactNode }) {
  return <section className={hideSearch ? "filter-bar filters-only" : "filter-bar"}>{!hideSearch && <label className="search"><Search size={16} /><input value={search} onChange={(event) => setSearch?.(event.target.value)} placeholder="Search discovered systems" /></label>}<div className="filters">{children}</div></section>;
}

export function Select({ label, value = "", onChange, options }: { label: string; value?: string; onChange: (value: string) => void; options: Record<string, string> }) {
  return <label className="select"><span>{label}</span><select value={value} onChange={(event) => onChange(event.target.value)}>{Object.entries(options).map(([key, name]) => <option value={key} key={key}>{name}</option>)}</select><ChevronDown size={13} /></label>;
}

export function Identity({ kind, name, detail }: { kind: string; name: string; detail: string }) {
  const Icon = kindIcons[kind] ?? PackageSearch;
  return <span className="identity"><i className={`entity-icon ${kind}`}><Icon size={17} /></i><span><b>{name}</b><small>{detail}</small></span></span>;
}

export function TypePill({ value }: { value: string }) { return <span className={`type-pill ${value}`}>{pretty(value)}</span>; }
export function StatePill({ state }: { state: string }) { return <span className={`state-pill ${state}`}><i />{pretty(state)}</span>; }
export function ConfidencePill({ value }: { value: string }) { return <span className={`confidence-pill ${value}`}><i />{pretty(value)}</span>; }
export function Freshness({ value, partial }: { value: string; partial?: boolean }) { return <span className={`freshness ${value}`}><i />{pretty(value)}{partial && <small>Partial scan</small>}</span>; }
export function Fact({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><b>{value}</b></div>; }

export function ConnectionRow({ item }: { item: Connection }) {
  return <div className="connection-row"><Identity kind={item.entity.kind} name={item.entity.name} detail={item.label === "observed_user" ? "Observed user—not authoritative owner" : pretty(item.relationship_kind)} /><ConfidencePill value={item.confidence} /></div>;
}

export function PanelHeading({ title, detail, count, action }: { title: string; detail: string; count?: number; action?: ReactNode }) {
  return <header className="panel-heading"><div><h2>{title}</h2><p>{detail}</p></div>{action ?? (count !== undefined && <span className="panel-count">{count}</span>)}</header>;
}

export function Drawer({ children, onClose }: { children: ReactNode; onClose: () => void }) {
  const ref = useRef<HTMLElement>(null);
  const restore = useRef(document.activeElement as HTMLElement | null);
  useEffect(() => {
    ref.current?.focus();
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") onClose(); if (event.key === "Tab" && ref.current) { const focusable = [...ref.current.querySelectorAll<HTMLElement>('button,a,input,select,textarea,[tabindex]:not([tabindex="-1"])')]; if (!focusable.length) return; const first = focusable[0], last = focusable[focusable.length - 1]; if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); } } };
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("keydown", key); restore.current?.focus(); };
  }, [onClose]);
  return <div className="drawer-overlay" onMouseDown={(event) => { if (event.currentTarget === event.target) onClose(); }}><aside className="drawer" role="dialog" aria-modal="true" aria-label="System details" tabIndex={-1} ref={ref}><button className="drawer-close" aria-label="Close system details" onClick={onClose}><X size={18} /></button><div className="drawer-body">{children}</div></aside></div>;
}

export function Loading() { return <div className="loading"><Radar size={25} /><span>Resolving discovery posture…</span></div>; }
export function InlineLoading() { return <div className="inline-loading"><RefreshCw size={14} /> Loading…</div>; }
export function InlineError({ text }: { text: string }) { return <div className="inline-error"><AlertCircle size={15} />{text}</div>; }
export function Failure({ error, retry }: { error: string; retry: () => void }) { return <div className="failure"><AlertCircle size={24} /><h2>Lens could not load this view</h2><p>{error}</p><button className="button subtle" onClick={retry}>Try again</button></div>; }
export function Empty({ icon: Icon, title, detail }: { icon: LucideIcon; title: string; detail: string }) { return <div className="empty"><Icon size={23} /><b>{title}</b><p>{detail}</p></div>; }
export function Brand() { return <div className="brand"><img className="logo" src={barrikadeLogoMark} alt="" aria-hidden="true" /><span><b>BARRIKADE</b><small>LENS</small></span></div>; }

export function useRemote<T>(factory: () => Promise<T>, dependencies: unknown[]) {
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

export function groupConnections(items: Connection[]) {
  return items.reduce<Record<string, Connection[]>>((groups, item) => {
    const key = item.label === "observed_user" ? "observed_users" : item.entity.kind;
    (groups[key] ??= []).push(item);
    return groups;
  }, {});
}

export function pretty(value: string) { return value.replace(/[._-]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase()); }
export function sum(values: number[]) { return values.reduce((total, value) => total + value, 0); }
export function percent(value: number, total: number) { return total ? Math.round((value / total) * 100) : 0; }
export function formatValue(value: unknown) { if (value === undefined || value === null || value === "") return "Not observed"; if (typeof value === "boolean") return value ? "Yes" : "No"; if (Array.isArray(value)) return value.join(", "); if (typeof value === "object") return "Structured value"; return String(value); }
export function relative(value: string) { const time = new Date(value).getTime(); if (!Number.isFinite(time)) return "Unknown"; const seconds = Math.max(0, Math.floor((Date.now() - time) / 1000)); if (seconds < 60) return "Just now"; if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`; if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`; return `${Math.floor(seconds / 86400)}d ago`; }
