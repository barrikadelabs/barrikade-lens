import { ChevronRight, Monitor } from "lucide-react";
import { type ProductItem } from "../../api";
import { Drawer, Fact, Identity, TypePill, pretty, relative } from "../../ui";

export function ProductDrawer({ item, onClose }: { item: ProductItem; onClose: () => void }) {
	return <Drawer onClose={onClose}>
		<div className="drawer-title"><Identity kind="runtime" name={item.name} detail={item.id} /><div>{item.system_type && <TypePill value={item.system_type} />}</div></div>
		<div className="fact-grid"><Fact label="Installations" value={String(item.installation_count)} /><Fact label="Observed users" value={String(item.observed_user_count)} /><Fact label="Fresh evidence" value={String(item.fresh_count)} /><Fact label="Stale evidence retained" value={String(item.stale_count)} /></div>
		<section className="drawer-section"><h3>Observed users <span>{item.observed_user_count}</span></h3><p>{item.observed_users.length ? item.observed_users.join(", ") : "No OS account was observed."}</p><small>Counts retain target-scoped identities even when separate endpoints use the same local account name. Usage does not establish a business or technical owner.</small></section>
		<section className="drawer-section"><h3>Installations <span>{item.instances.length}</span></h3><div className="running-list">{item.instances.map((instance) => <button key={instance.id} onClick={() => location.assign(`/systems/${encodeURIComponent(instance.id)}`)}><span className="running-mark"><Monitor size={14} /></span><span><b>{instance.target_name ?? "Unresolved target"}</b><small>{instance.observed_users.length ? `Observed user: ${instance.observed_users.join(", ")}` : "No observed user"}</small></span><span><strong>{pretty(instance.target_freshness)}</strong><small>{pretty(instance.state)} · {relative(instance.last_seen_at)}</small></span><ChevronRight size={14} /></button>)}</div></section>
	</Drawer>;
}
