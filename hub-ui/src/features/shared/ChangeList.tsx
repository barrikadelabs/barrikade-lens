import { Activity, ArrowRight, History } from "lucide-react";
import { type Change } from "../../api";
import { Empty, formatValue, pretty, relative } from "../../ui";

type GroupedChange = Change & { occurrences?: number };

export function groupChanges(items: Change[]): GroupedChange[] {
  const groups = new Map<string, GroupedChange>();
  for (const item of items) {
    const key = `${item.entity_id}|${item.category}|${item.summary}`;
    const existing = groups.get(key);
    if (existing) existing.occurrences = (existing.occurrences ?? 1) + 1;
    else groups.set(key, { ...item, occurrences: 1 });
  }
  return [...groups.values()].sort((left, right) => Date.parse(right.changed_at) - Date.parse(left.changed_at));
}
export function ChangeList({ items, expanded = false }: { items: GroupedChange[]; expanded?: boolean }) {
  if (!items.length) return <Empty icon={History} title="No important changes" detail="Lens hides identical scans and routine updates." />;
  return <div className={expanded ? "change-list expanded" : "change-list"}>{items.map((item) => <article key={item.id}><span className={`change-mark ${item.category}`}><Activity size={13} /></span><div><p><b>{item.entity_name ?? "AI tool or agent"}</b><span className="category-pill">{pretty(item.category)}</span></p><h3>{item.summary || pretty(item.event_type)}{(item.occurrences ?? 1) > 1 ? ` · seen ${item.occurrences} times` : ""}</h3><small>{pretty(item.system_type ?? item.surface ?? "inventory")} · latest {relative(item.changed_at)}</small>{expanded && item.details?.fields && <div className="field-diffs">{item.details.fields.slice(0, 5).map((field) => <span key={field.path}><code>{pretty(field.path.replace("attributes.", ""))}</code><i>{formatValue(field.before)}</i><ArrowRight size={12} /><b>{formatValue(field.after)}</b></span>)}</div>}</div></article>)}</div>;
}
