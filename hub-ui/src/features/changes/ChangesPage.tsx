import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { ChevronDown } from "lucide-react";
import { API, type Change } from "../../api";
import { captureAnalytics } from "../../analytics";
import { FilterBar, InlineError, InlineLoading, PanelHeading, Select, useRemote } from "../../ui";
import { analyticsControl } from "../shared/analytics";
import { ChangeList } from "../shared/ChangeList";

export function ChangesPage({ api, revision }: { api: API; revision: number }) {
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
    <Select label="Category" value={filters.category} onChange={(value) => update("category", value)} options={{ "": "All important changes", state: "Status", network_scope: "Network access", attribution: "Owner", capability: "Capability", confidence: "Confidence", identity: "Identity", freshness: "Last report" }} />
    <Select label="Type" value={filters.system_type} onChange={(value) => update("system_type", value)} options={{ "": "All tools and agents", autonomous_agent: "Autonomous agent", agent_tool: "AI agent tool", model_runtime: "AI model runtime" }} />
    <Select label="Location type" value={filters.surface} onChange={(value) => update("surface", value)} options={{ "": "All locations", endpoint: "Device", repository: "Repository", kubernetes: "Cluster", cloud: "Cloud account" }} />
  </FilterBar>
    <section className="panel change-log"><PanelHeading title="Change history" detail="Important changes to AI tools, agents, and their connections. Routine scan updates are hidden." count={items.length} /><ChangeList items={items} expanded />
      {remote.loading && <InlineLoading />}{remote.error && <InlineError text={remote.error} />}{remote.data?.next_cursor && !remote.loading && <button className="load-more" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "changes", interaction: "load_more" } }); setCursor(remote.data!.next_cursor!); }}>Load more changes <ChevronDown size={15} /></button>}
    </section>
  </div>;
}
