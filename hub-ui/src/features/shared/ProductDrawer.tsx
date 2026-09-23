import { ChevronRight, Monitor } from "lucide-react";
import { type ProductItem } from "../../api";
import { observedStateLabel } from "../../copy";
import { Drawer, Fact, Identity, TypePill, relative } from "../../ui";

export function ProductDrawer({ item, onClose }: { item: ProductItem; onClose: () => void }) {
	return <Drawer onClose={onClose}>
		<div className="drawer-title"><Identity kind="runtime" name={item.name} detail={item.id} /><div>{item.system_type && <TypePill value={item.system_type} />}</div></div>
			<div className="fact-grid"><Fact label="Installations" value={String(item.installation_count)} /><Fact label="Observed accounts" value={String(item.observed_user_count)} /><Fact label="Up-to-date results" value={String(item.fresh_count)} /><Fact label="Older results" value={String(item.stale_count)} /></div>
			<section className="drawer-section"><h3>Observed accounts <span>{item.observed_user_count}</span></h3><p>{item.observed_users.length ? item.observed_users.join(", ") : "No device account was observed."}</p><small>The same account name on two devices is counted twice. Seeing an account in use does not mean that person or team owns this tool or agent.</small></section>
			<section className="drawer-section"><h3>Installations <span>{item.instances.length}</span></h3><div className="running-list">{item.instances.map((instance) => <button key={instance.id} onClick={() => location.assign(`/systems/${encodeURIComponent(instance.id)}`)}><span className="running-mark"><Monitor size={14} /></span><span><b>{instance.target_name ?? "Location unresolved"}</b><small>{instance.observed_users.length ? `Observed account: ${instance.observed_users.join(", ")}` : "No observed account"}</small></span><span><strong>{instance.target_freshness === "fresh" ? "Up to date" : "Not reporting recently"}</strong><small>{observedStateLabel(instance.state, instance.target_freshness)} · last seen {relative(instance.last_seen_at)}</small></span><ChevronRight size={14} /></button>)}</div></section>
	</Drawer>;
}
