import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom";
import { Activity, ChevronDown, ChevronRight, Download, LogOut, Menu, RefreshCw, UserRound, X } from "lucide-react";
import { API, authConfig, type AuthConfig } from "../../api";
import { captureAnalytics, configureAnalytics, semanticPage } from "../../analytics";
import { Brand, Loading, useRemote } from "../../ui";
import { navigationLabel, pageCopy, pageForPath, pagePath, navigation } from "../navigation";
import { OverviewPage } from "../../features/overview/OverviewPage";
import { FindingsPage } from "../../features/findings/FindingsPage";
import { SystemsPage } from "../../features/inventory/SystemsPage";
import { ConnectionsPage } from "../../features/connections/ConnectionsPage";
import { DeviceFleetPage } from "../../features/connections/DeviceFleetPage";
import { ChangesPage } from "../../features/changes/ChangesPage";
import { AccountSettings } from "../../features/settings/AccountSettings";
const EvidenceGraphPage = lazy(() => import("../../EvidenceGraph").then((module) => ({ default: module.EvidenceGraphPage })));

export function Shell({ api, signOut, organizationControl, userControl, selfServe = true, analyticsConfig }: { api: API; signOut: () => void; organizationControl?: ReactNode; userControl?: ReactNode; selfServe?: boolean; analyticsConfig?: AuthConfig["analytics"] }) {
  const location = useLocation();
  const navigate = useNavigate();
  const page = pageForPath(location.pathname);
  const [menuOpen, setMenuOpen] = useState(false);
  const [revision, setRevision] = useState(0);
  const [exposureEnabled, setExposureEnabled] = useState(false);
	const analyticsSession = useRemote(() => api.session(), [api]);
  useEffect(() => { authConfig().then((config) => setExposureEnabled(config.exposure_enabled)).catch(() => setExposureEnabled(false)); }, []);
	useEffect(() => {
		configureAnalytics(analyticsConfig, analyticsSession.data?.analytics);
		const lensPage = semanticPage(location.pathname);
		if (lensPage && analyticsSession.data?.analytics.enabled) captureAnalytics({ name: "lens_page_viewed", properties: { lens_page: lensPage } });
	}, [analyticsConfig, analyticsSession.data?.analytics, location.pathname]);
  const copy = pageCopy[page];
  return <div className="app-shell">
    <aside className={menuOpen ? "sidebar open" : "sidebar"}>
      <div className="sidebar-brand"><Brand /></div>
      <nav className="main-nav">
        {navigation.filter((item) => (item.page !== "Findings" || exposureEnabled) && (item.page !== "Connections" || selfServe)).map(({ page: item, icon: Icon, detail }) => <button key={item} className={page === item ? "active" : ""} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "navigation", interaction: "open", control: "navigation" } }); navigate(pagePath[item]); setMenuOpen(false); }}>
          <Icon size={17} /><span><b>{navigationLabel[item]}</b><small>{detail}</small></span>{page === item && <ChevronRight size={14} />}
        </button>)}
      </nav>
      <div className="sidebar-footer">
        <div className="sidebar-account">
          {(organizationControl || userControl) && <div className="sidebar-account-primary">
            {organizationControl && <div className="sidebar-organization">{organizationControl}</div>}
            {userControl && <div className="sidebar-user">{userControl}</div>}
          </div>}
          <div className="sidebar-footer-actions">
            <button onClick={() => navigate("/settings")}><UserRound size={15} /> Account settings</button>
            <button className="logout" onClick={signOut} aria-label="Sign out" title="Sign out"><LogOut size={16} /></button>
          </div>
        </div>
        <div className="sidebar-legal"><a href="/privacy">Privacy</a><a href="/terms">Terms</a></div>
      </div>
    </aside>
    <main className="main-area">
      <div className="workspace">
        <header className="page-heading">
          <button className="mobile-menu" aria-label={menuOpen ? "Close navigation" : "Open navigation"} onClick={() => setMenuOpen((value) => !value)}>{menuOpen ? <X size={20} /> : <Menu size={20} />}</button>
          <div><p className="eyebrow">{copy.eyebrow}</p><h1>{copy.title}</h1><p>{copy.detail}</p></div>
          <div className="page-actions"><NotificationBell api={api} revision={revision} onOpen={() => navigate("/connections")} /><button className="icon-button" onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: semanticPage(location.pathname) ?? "navigation", interaction: "refresh" } }); setRevision((value) => value + 1); }} title="Refresh"><RefreshCw size={16} /></button><ExportMenu api={api} /></div>
        </header>
        <Routes>
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<OverviewPage api={api} revision={revision} go={(target) => navigate(pagePath[target])} />} />
          <Route path="/findings" element={<FindingsPage api={api} revision={revision} />} />
          <Route path="/findings/:findingId" element={<FindingsPage api={api} revision={revision} />} />
          <Route path="/inventory" element={<SystemsPage api={api} revision={revision} />} />
          <Route path="/systems/:systemId" element={<SystemRoute api={api} revision={revision} />} />
          <Route path="/connections" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} />} />
          <Route path="/connections/new" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} startWizard />} />
          <Route path="/connections/devices" element={<DeviceFleetPage api={api} revision={revision} />} />
          <Route path="/connections/:environmentId" element={<ConnectionsPage api={api} revision={revision} onResults={() => navigate("/overview")} />} />
          <Route path="/systems/:systemId/evidence" element={<EvidenceRoute api={api} revision={revision} />} />
          <Route path="/changes" element={<ChangesPage api={api} revision={revision} />} />
          <Route path="/settings" element={<AccountSettings api={api} onDeleted={async () => signOut()} onAnalyticsChanged={analyticsSession.reload} analyticsAvailable={Boolean(analyticsConfig?.enabled)} />} />
          <Route path="*" element={<Navigate to="/overview" replace />} />
        </Routes>
      </div>
    </main>
    {menuOpen && <button className="sidebar-scrim" onClick={() => setMenuOpen(false)} />}
  </div>;
}
function SystemRoute({ api, revision }: { api: API; revision: number }) { return <SystemsPage api={api} revision={revision} />; }
function EvidenceRoute({ api, revision }: { api: API; revision: number }) { const { systemId = "" } = useParams(); return <Suspense fallback={<Loading />}><EvidenceGraphPage api={api} revision={revision} initialSystemId={systemId} /></Suspense>; }
function ExportMenu({ api }: { api: API }) {
  const [open, setOpen] = useState(false);
  return <div className="export"><button className="button subtle" onClick={() => setOpen((value) => !value)}><Download size={15} /> Export <ChevronDown size={13} /></button>{open && <div>{(["lens", "ndjson", "cyclonedx"] as const).map((format) => <button key={format} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "export", interaction: "open", export_format: format === "lens" ? "json" : format } }); setOpen(false); api.downloadExport(format); }}>{format === "lens" ? "Lens JSON" : format === "ndjson" ? "NDJSON" : "CycloneDX 1.7"}</button>)}</div>}</div>;
}
function NotificationBell({ api, revision, onOpen }: { api: API; revision: number; onOpen: () => void }) {
  const notifications = useRemote(() => api.notifications(), [api, revision]);
  const unread = notifications.data?.items.filter((item) => !item.read_at) ?? [];
  if (!unread.length) return null;
  const latest = unread[0];
  return <button className="notification-button" title={`${unread.length} unread setup notifications`} onClick={() => { captureAnalytics({ name: "lens_interaction", properties: { surface: "notification", interaction: "notification_opened" } }); api.readNotification(latest.id).then(notifications.reload); onOpen(); }}><Activity size={15} /><b>{unread.length}</b></button>;
}
