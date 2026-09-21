import { Bot, Cloud, FileSearch, LayoutDashboard, type LucideIcon } from "lucide-react";

export type Page = "Overview" | "Findings" | "Inventory" | "Connections" | "Changes" | "Evidence" | "Settings";

export const navigation: Array<{ page: Page; icon: LucideIcon; detail: string }> = [
  { page: "Overview", icon: LayoutDashboard, detail: "AI activity at a glance" },
  { page: "Findings", icon: FileSearch, detail: "What needs review" },
  { page: "Inventory", icon: Bot, detail: "AI tools and agents" },
  { page: "Connections", icon: Cloud, detail: "What Lens can check" },
];

export const navigationLabel: Record<Page, string> = {
  Overview: "Overview",
  Findings: "Findings",
  Inventory: "AI inventory",
  Connections: "Coverage",
  Changes: "Changes",
  Evidence: "How Lens knows",
  Settings: "Account settings",
};

export const pageCopy: Record<Page, { eyebrow: string; title: string; detail: string }> = {
  Overview: { eyebrow: "OVERVIEW", title: "Your organization’s AI activity", detail: "See which AI tools and agents Lens found, where they run, and what needs review." },
  Findings: { eyebrow: "REVIEW", title: "Findings", detail: "Potential issues that may need review, ordered by impact and how recently Lens observed them." },
  Inventory: { eyebrow: "AI INVENTORY", title: "AI inventory", detail: "Explore the AI tools and agents Lens found, including every installation and its supporting details." },
  Connections: { eyebrow: "COVERAGE", title: "Coverage", detail: "Choose what Lens checks and see which connected devices, repositories, cloud accounts, and clusters are reporting." },
  Changes: { eyebrow: "HISTORY", title: "Changes", detail: "Important changes to AI tools and agents. Routine scan updates are hidden." },
  Evidence: { eyebrow: "SUPPORTING DETAILS", title: "How Lens knows", detail: "See how Lens connected an AI tool or agent to related software, accounts, and observations." },
  Settings: { eyebrow: "ACCOUNT", title: "Account settings", detail: "Manage your account, workspace, and privacy preferences." },
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
