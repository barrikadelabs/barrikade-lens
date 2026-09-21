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
    if (!window.confirm("Delete your Lens account? This signs you out and cannot be undone.")) return;
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
  return <div className="page-stack"><section className="panel settings-panel"><PanelHeading title="Your account" detail={`${session.data.user.id} · ${pretty(session.data.role)}`} /><p>{session.data.can_delete_account ? "You can delete your account. Other members will keep access to the workspace and its data." : "You are the workspace’s only owner. Transfer ownership or delete the workspace before deleting your account."}</p><button className="button subtle" disabled={busy || !session.data.can_delete_account} onClick={removeIdentity}>Delete my account</button></section>{analyticsAvailable && <section className="panel settings-panel"><PanelHeading title="Product analytics" detail="Choose whether to share anonymous usage data that helps Barrikade improve Lens" /><label className="analytics-preference"><input type="checkbox" checked={session.data.analytics.enabled} disabled={busy} onChange={(event) => void updateAnalytics(event.target.checked)} /><span><b>Share anonymous usage data</b><small>Lens can share feature use, performance measurements, limited error details, survey ratings, and a fully masked replay of interactions. It does not share your identity, inventory, commands, or infrastructure details.</small></span></label><details className="analytics-details"><summary>What is and is not shared</summary><p>Lens replaces your user and workspace identifiers with pseudonyms. Session replay masks all text, inputs, and element details and blocks media and network data. Lens never sends names, email addresses, workspace names, AI inventory, supporting evidence, URLs, commands, or infrastructure identifiers. Browser privacy signals disable capture in this browser.</p><p>Turning this off stops future user-linked events and replay in this browser. Anonymous workspace-processing milestones may continue. See the <a href="/privacy">privacy notice</a> or contact Barrikade to request deletion of earlier analytics.</p></details></section>}{session.data.role === "owner" && <section className="panel settings-panel danger-zone"><PanelHeading title="Delete workspace" detail="Immediately deletes connections, AI inventory, findings, supporting details, and member access." /><button className="button quiet" disabled={busy} onClick={removeWorkspace}>Delete workspace</button></section>}{error && <InlineError text={error} />}</div>;
}
