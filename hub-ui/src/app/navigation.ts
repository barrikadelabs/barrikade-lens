import { Bot, Cloud, FileSearch, LayoutDashboard, type LucideIcon } from "lucide-react";

export type Page = "Overview" | "Findings" | "Inventory" | "Connections" | "Changes" | "Evidence" | "Settings";

export const navigation: Array<{ page: Page; icon: LucideIcon; detail: string }> = [
  { page: "Overview", icon: LayoutDashboard, detail: "Organization posture" },
  { page: "Findings", icon: FileSearch, detail: "Prioritized action" },
  { page: "Inventory", icon: Bot, detail: "Known AI systems" },
  { page: "Connections", icon: Cloud, detail: "Coverage and setup" },
];

export const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: { eyebrow: "DISCOVERY", title: "Organization AI posture", detail: "Evidence-backed visibility across connected cloud accounts, endpoints, repositories, and clusters." },
  Findings: { eyebrow: "ATTENTION", title: "Findings", detail: "Workspace-wide priorities ranked by severity, freshness, ownership, and latest observation." },
  Inventory: { eyebrow: "INVENTORY", title: "Organization inventory", detail: "Products grouped across the organization, with every endpoint installation and its evidence one level below." },
  Connections: { eyebrow: "VISIBILITY", title: "Scan environment", detail: "Connect discovery sources, resume setup, and understand reporting coverage." },
  Changes: { eyebrow: "HISTORY", title: "Changes", detail: "Material inventory changes. Routine scan refreshes are suppressed." },
  Evidence: { eyebrow: "EVIDENCE", title: "Evidence graph", detail: "Trace a system to its capabilities, deployment surfaces, observed users, and sanitized evidence." },
  Settings: { eyebrow: "ACCOUNT", title: "Account settings", detail: "Manage your Lens identity and workspace lifecycle." },
};

export const pagePath: Record<Page, string> = { Overview: "/overview", Findings: "/findings", Inventory: "/inventory", Connections: "/connections", Changes: "/changes", Evidence: "/systems/evidence", Settings: "/settings" };

export function pageForPath(path: string): Page {
  if (path.startsWith("/findings")) return "Findings";
  if (path.startsWith("/inventory") || /^\/systems\/[^/]+$/.test(path)) return "Inventory";
  if (path.startsWith("/connections")) return "Connections";
  if (path.startsWith("/changes")) return "Changes";
  if (path.endsWith("/evidence")) return "Evidence";
  if (path.startsWith("/settings")) return "Settings";
  return "Overview";
}
