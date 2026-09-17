import { Cloud, Container, GitBranch, Monitor, type LucideIcon } from "lucide-react";
import type { EnvironmentKind } from "../../api";

export type SourceCategory = "code_ci" | "employee_devices" | "cloud_infrastructure" | "saas_identity";
export type DeploymentMethod = "this_computer" | "company" | "command_line" | "mdm";

export type ConnectionSource = {
  kind: EnvironmentKind;
  category: SourceCategory;
  title: string;
  detail: string;
  identifier: string;
  icon: LucideIcon;
  connector: string;
};

export const sourceCategories: Array<{ id: SourceCategory; title: string; detail: string }> = [
  { id: "code_ci", title: "Code & CI", detail: "Repositories and build pipelines" },
  { id: "employee_devices", title: "Employee devices", detail: "macOS, Windows, and Linux computers" },
  { id: "cloud_infrastructure", title: "Cloud & infrastructure", detail: "Cloud accounts and Kubernetes clusters" },
  { id: "saas_identity", title: "SaaS & identity", detail: "Identity and business application signals" },
];

export const environmentCatalog: ConnectionSource[] = [
  { kind: "github_repository", category: "code_ci", title: "GitHub", detail: "Scan repositories through the GitHub App", identifier: "owner/repository (optional)", icon: GitBranch, connector: "github" },
  { kind: "endpoint", category: "employee_devices", title: "Employee devices", detail: "Install once or deploy across your company", identifier: "", icon: Monitor, connector: "endpoint" },
  { kind: "aws_account", category: "cloud_infrastructure", title: "AWS", detail: "Bedrock, AgentCore, and SageMaker", identifier: "12-digit account ID", icon: Cloud, connector: "aws" },
  { kind: "azure_subscription", category: "cloud_infrastructure", title: "Azure", detail: "Foundry, Azure AI, and Azure ML", identifier: "Subscription ID", icon: Cloud, connector: "azure" },
  { kind: "gcp_project", category: "cloud_infrastructure", title: "Google Cloud", detail: "Vertex AI and Agent Registry", identifier: "Project ID", icon: Cloud, connector: "gcp" },
  { kind: "kubernetes_cluster", category: "cloud_infrastructure", title: "Kubernetes", detail: "Read-only cluster collector", identifier: "Optional cluster reference", icon: Container, connector: "kubernetes" },
];

export function sourceEnabled(source: ConnectionSource, connectors: Record<string, boolean>): boolean {
  return connectors[source.connector] === true;
}

export function defaultEnvironmentName(kind: EnvironmentKind): string {
  return kind === "endpoint" ? "Employee device" : environmentCatalog.find((item) => item.kind === kind)?.title ?? "Environment";
}

export function analyticsConnectionType(kind: EnvironmentKind): "aws" | "azure" | "gcp" | "endpoint" | "github" | "kubernetes" {
  return ({ aws_account: "aws", azure_subscription: "azure", gcp_project: "gcp", endpoint: "endpoint", github_repository: "github", kubernetes_cluster: "kubernetes" } as const)[kind];
}
