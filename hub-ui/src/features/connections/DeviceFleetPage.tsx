import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertTriangle, ChevronDown, Laptop, Pencil, ShieldOff } from "lucide-react";
import { API, type DeviceFleetPage as DeviceFleetResult, type FleetDevice } from "../../api";
import { Empty, Failure, FilterBar, Identity, InlineError, InlineLoading, Select, pretty, relative, useRemote } from "../../ui";

const statusOptions = {
  "": "All device statuses",
  reporting: "Reporting",
  stale_offline: "Not reporting",
  never_scanned: "Never scanned",
  partial: "Some data is missing",
  failed: "Scan failed",
  revoked: "Access removed",
};

export function DeviceFleetPage({ api, revision }: { api: API; revision: number }) {
  const navigate = useNavigate();
  const session = useRemote(() => api.session(), [api]);
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [policyID, setPolicyID] = useState("");
  const [cursor, setCursor] = useState("");
  const [result, setResult] = useState<DeviceFleetResult>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [mutationError, setMutationError] = useState("");

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setLoading(true); setError("");
      api.deviceFleet({ search, status, policy_id: policyID, cursor, limit: 50 }).then((page) => {
        setResult((current) => cursor && current ? { ...page, items: [...current.items, ...page.items] } : page);
      }).catch((reason) => setError(String(reason))).finally(() => setLoading(false));
    }, search ? 220 : 0);
    return () => window.clearTimeout(timer);
  }, [api, cursor, policyID, revision, search, status]);

  const updateFilter = (setter: (value: string) => void, value: string) => { setCursor(""); setter(value); };
  const canManage = session.data?.role === "owner" || session.data?.role === "admin";
  const mutate = (operation: Promise<unknown>) => {
    setMutationError("");
    operation.then(() => { setCursor(""); setResult(undefined); return api.deviceFleet({ search, status, policy_id: policyID, limit: 50 }); }).then(setResult).catch((reason) => setMutationError(String(reason)));
  };
  const rename = (device: FleetDevice) => {
    const name = window.prompt("Device name shown in Lens", device.name)?.trim();
    if (name && name !== device.name) mutate(api.renameDevice(device.id, name));
  };
  const revoke = (device: FleetDevice) => {
    if (window.confirm(`Remove Lens access from ${device.name}? The scanner will stop reporting immediately. Earlier results will remain available and be marked as out of date.`)) mutate(api.revokeDevice(device.id));
  };
  const policyOptions = Object.fromEntries([["", "All deployment groups"], ...(result?.policies ?? []).map((policy) => [policy.id, policy.name])]);
  const summary = result?.summary;
  const summaryItems = summary ? [
    ["Installed", summary.enrolled, "all known installations"],
    ["Checked", summary.scanned, "completed at least one scan"],
    ["Reporting", summary.reporting, "seen in the last hour"],
    ["Not reporting", summary.stale_offline, "not seen recently"],
    ["Missing data", summary.partial, "latest scan was incomplete"],
    ["Failed", summary.failed, "no complete scan yet"],
    ["Access removed", summary.revoked, "scanner access was removed"],
  ] as const : [];

  if (!result && loading) return <InlineLoading />;
  if (!result && error) return <Failure error={error} retry={() => { setCursor(""); setSearch((value) => value + " "); }} />;
  return <div className="page-stack device-fleet-page">
    <section className="fleet-heading panel"><div><p className="eyebrow">EMPLOYEE DEVICES</p><h2>Managed devices</h2><p>Lens keeps the same protected identity when a device name changes, so updates do not create duplicate devices.</p></div><button className="button subtle" onClick={() => navigate("/connections")}>Back to coverage</button></section>
    <section className="fleet-summary" aria-label="Device status summary">
      {summaryItems.map(([label, value, detail]) => <div key={label}><span>{label}</span><b>{value}</b><small>{detail}</small></div>)}
    </section>
    {result?.policies.length ? <section className="fleet-policies">
      {result.policies.map((policy) => <button key={policy.id} className={policyID === policy.id ? "active" : ""} onClick={() => updateFilter(setPolicyID, policyID === policy.id ? "" : policy.id)}><span><b>{policy.name}</b><small>{pretty(policy.status)}</small></span><strong>{policy.enrolled_count}{policy.expected_device_count === null ? "" : ` / ${policy.expected_device_count}`}</strong><small>{policy.remaining_count} enrollment slots remaining</small></button>)}
    </section> : null}
    <FilterBar search={search} setSearch={(value) => updateFilter(setSearch, value)}>
      <Select label="Device status" value={status} onChange={(value) => updateFilter(setStatus, value)} options={statusOptions} />
      <Select label="Deployment group" value={policyID} onChange={(value) => updateFilter(setPolicyID, value)} options={policyOptions} />
    </FilterBar>
    {mutationError && <InlineError text={mutationError} />}
    <section className="panel data-panel fleet-panel"><div className="table-summary"><span><b>{result?.items.length ?? 0}</b> devices</span><span>Names you add do not replace the device name observed by Lens</span></div>
      <div className="table-scroll"><div className="fleet-row table-head"><span>Device</span><span>Status</span><span>Deployment group</span><span>Lens scanner</span><span>Last seen</span><span>Last complete scan</span><span>Actions</span></div>
        {result?.items.map((device) => <article className="fleet-row" key={device.id}>
          <Identity kind="endpoint" name={device.name} detail={`${device.observed_name}${device.possible_duplicate ? " · duplicate hostname" : ""}`} />
          <span className={`fleet-lifecycle ${device.lifecycle_status}`}><i />{statusOptions[device.lifecycle_status] ?? pretty(device.lifecycle_status)}{device.partial && <small>Some data is missing</small>}</span>
          <span className="stacked"><b>{device.deployment_policy_name ?? "Direct enrollment"}</b><small>{device.reporting_mode === "quick_scan" ? "Quick Scan" : "Continuous"}</small></span>
          <span className="stacked"><b>{device.collector_version ?? "Not reported"}</b><small>{[device.platform, device.architecture].filter(Boolean).join(" · ") || "Platform unknown"}</small></span>
          <span className="observed">{device.last_seen_at ? relative(device.last_seen_at) : "Never"}</span><span className="observed">{device.last_full_at ? relative(device.last_full_at) : "Never"}</span>
          <span className="fleet-actions"><button title="View what Lens found on this device" onClick={() => navigate(`/inventory?view=installations&target_id=${encodeURIComponent(device.id)}`)}>View inventory</button>{canManage && <button title="Rename device" onClick={() => rename(device)}><Pencil size={13} /></button>}{canManage && device.lifecycle_status !== "revoked" && <button className="danger" title="Remove scanner access" onClick={() => revoke(device)}><ShieldOff size={13} /></button>}</span>
        </article>)}
        {!loading && !result?.items.length && <Empty icon={Laptop} title="No devices match this view" detail="Try a broader status, deployment group, or device-name filter." />}
      </div>
      {error && <InlineError text={error} />}{loading && <InlineLoading />}{result?.next_cursor && !loading && <button className="load-more" onClick={() => setCursor(result.next_cursor ?? "")}>Load more devices <ChevronDown size={15} /></button>}
    </section>
    {summary && summary.partial > 0 && <aside className="fleet-integrity-note"><AlertTriangle size={16} /><span><b>Some results are incomplete.</b> Review affected devices before assuming that a missing item is not present.</span></aside>}
  </div>;
}
