import { percent, pretty } from "../../ui";

export function ExecutiveFact({ value, label, tone = "neutral", onClick }: { value: number; label: string; tone?: string; onClick: () => void }) {
  return <button className={`executive-fact ${tone}`} onClick={onClick}><strong>{value}</strong><span>{label}</span></button>;
}
export function ConfidenceSummary({ data }: { data: Record<string, number> }) {
  return <div className="confidence-summary"><span>How sure Lens is</span><div><b><i className="confirmed" />{data.confirmed ?? 0} confirmed</b><b><i className="likely" />{data.likely ?? 0} likely</b><b><i className="possible" />{data.possible ?? 0} possible</b></div></div>;
}

export function StateDistribution({ values, total }: { values: Record<string, number>; total: number }) {
  const order = ["running", "deployed", "defined", "configured", "installed", "residual", "cached", "observed"];
  const observed = order.filter((state) => (values[state] ?? 0) > 0);
  return <div className="state-distribution"><div className="state-bar">{observed.map((state) => <i key={state} className={state} style={{ width: `${percent(values[state], total)}%` }} title={`${pretty(state)} ${values[state]}`} />)}</div><div className="state-legend">{observed.map((state) => <div key={state}><span><i className={state} />{pretty(state)}</span><b>{values[state]}</b><small>{percent(values[state], total)}%</small></div>)}</div></div>;
}
