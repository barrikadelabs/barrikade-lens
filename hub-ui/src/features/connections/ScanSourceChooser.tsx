import { ChevronRight, LockKeyhole } from "lucide-react";
import { environmentCatalog, sourceCategories, sourceEnabled, type ConnectionSource, type SourceCategory } from "./connection-options";

export function ScanSourceChooser({ connectors, onSelect, onCategorySelected }: {
  connectors: Record<string, boolean>;
  onSelect: (source: ConnectionSource) => void;
  onCategorySelected: (category: SourceCategory) => void;
}) {
  return <div className="scan-source-categories">
    {sourceCategories.map((category) => {
      const sources = environmentCatalog.filter((source) => source.category === category.id);
      return <section className="scan-source-category" key={category.id} aria-labelledby={`source-category-${category.id}`}>
        <header><div><h3 id={`source-category-${category.id}`}>{category.title}</h3><p>{category.detail}</p></div></header>
        <div className="environment-catalog">
          {sources.map((source) => {
            const enabled = sourceEnabled(source, connectors);
            const Icon = source.icon;
            return <button key={source.kind} disabled={!enabled} onClick={() => { onCategorySelected(category.id); onSelect(source); }}>
              <Icon size={20} /><span><b>{source.title}</b><small>{source.detail}</small><em>{enabled ? "Available" : "Not available yet"}</em></span>{enabled ? <ChevronRight size={15} /> : <LockKeyhole size={14} />}
            </button>;
          })}
          {!sources.length && <button disabled><LockKeyhole size={18} /><span><b>Coming soon</b><small>More read-only connections will appear here.</small><em>Not available yet</em></span><LockKeyhole size={14} /></button>}
        </div>
      </section>;
    })}
  </div>;
}
