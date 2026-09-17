import { useState } from "react";
import { API } from "../../api";
import { resetAnalytics } from "../../analytics";
import { Failure, InlineError, Loading, PanelHeading, pretty, useRemote } from "../../ui";

export function AccountSettings({ api, onDeleted, onAnalyticsChanged, analyticsAvailable }: { api: API; onDeleted: () => Promise<void>; onAnalyticsChanged: () => void; analyticsAvailable: boolean }) {
  const session = useRemote(() => api.session(), [api]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (session.loading) return <Loading />;
  if (session.error || !session.data) return <Failure error={session.error} retry={session.reload} />;
  const removeIdentity = async () => {
    if (!window.confirm("Delete your Lens identity? This signs you out and cannot be undone.")) return;
    setBusy(true); setError("");
    try { await api.deleteAccount(); await onDeleted(); } catch (reason) { setError(String(reason)); setBusy(false); }
  };
  const removeWorkspace = async () => {
    if (!window.confirm(`Delete ${session.data!.workspace.name} and all Lens data immediately?`)) return;
    setBusy(true); setError("");
    try { await api.deleteWorkspace(session.data!.workspace.id); await onDeleted(); } catch (reason) { setError(String(reason)); setBusy(false); }
  };
	const updateAnalytics = async (enabled: boolean) => {
		setBusy(true); setError("");
		try { await api.updateAnalytics(enabled); if (!enabled) resetAnalytics(); session.reload(); onAnalyticsChanged(); } catch (reason) { setError(String(reason)); } finally { setBusy(false); }
	};
  return <div className="page-stack"><section className="panel settings-panel"><PanelHeading title="Your account" detail={`${session.data.user.id} · ${pretty(session.data.role)}`} /><p>{session.data.can_delete_account ? "You can delete your identity. Workspace evidence and settings remain available to other members." : "You are the workspace's sole owner. Transfer ownership or delete the workspace before deleting your identity."}</p><button className="button subtle" disabled={busy || !session.data.can_delete_account} onClick={removeIdentity}>Delete my identity</button></section>{analyticsAvailable && <section className="panel settings-panel"><PanelHeading title="Product analytics" detail="Help Barrikade improve managed Lens" /><label className="analytics-preference"><input type="checkbox" checked={session.data.analytics.enabled} disabled={busy} onChange={(event) => void updateAnalytics(event.target.checked)} /><span><b>Share privacy-minimized product diagnostics</b><small>Lens sends pseudonymous activation and feature-use events, numeric performance, sanitized error classes, survey ratings, flag exposure, and total-privacy session replay. Replay masks all text, inputs, and attributes and blocks media and network data. Lens never sends names, email addresses, workspace names, inventory, evidence, URLs, commands, or infrastructure identifiers. Browser privacy signals disable capture on this browser.</small></span></label><p className="muted">Turning this off affects future events and immediately stops replay in this browser. Actorless workspace-processing milestones can continue. See the <a href="/privacy">privacy notice</a> or contact Barrikade to request erasure of previously collected pseudonymous analytics.</p></section>}{session.data.role === "owner" && <section className="panel settings-panel danger-zone"><PanelHeading title="Delete workspace" detail="Immediately deletes connections, inventory, findings, evidence, and member access." /><button className="button quiet" disabled={busy} onClick={removeWorkspace}>Delete workspace</button></section>}{error && <InlineError text={error} />}</div>;
}
