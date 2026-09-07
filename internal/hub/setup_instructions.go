package hub

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (s *Server) environmentSetupInstructions(kind, provider, externalID, displayName, token string, configuration map[string]any) map[string]any {
	verifyPath := fmt.Sprintf("/v1/environments/%s/verify", "{environment_id}")
	switch kind {
	case "aws_account":
		externalIDValue, _ := configuration["external_id"].(string)
		template := map[string]any{
			"AWSTemplateFormatVersion": "2010-09-09",
			"Description":              "Barrikade Lens read-only discovery role",
			"Resources": map[string]any{"LensDiscoveryRole": map[string]any{
				"Type": "AWS::IAM::Role",
				"Properties": map[string]any{
					"RoleName":                 "BarrikadeLensDiscovery",
					"AssumeRolePolicyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": map[string]string{"AWS": s.config.AWSBrokerRoleARN}, "Action": "sts:AssumeRole", "Condition": map[string]any{"StringEquals": map[string]string{"sts:ExternalId": externalIDValue}}}}},
					"Policies": []any{map[string]any{"PolicyName": "LensAIInventoryReadOnly", "PolicyDocument": map[string]any{"Version": "2012-10-17", "Statement": []any{
						map[string]any{"Effect": "Allow", "Action": []string{"bedrock:ListAgents", "bedrock:GetAgent", "bedrock:ListAgentAliases", "bedrock:GetAgentAlias", "bedrock:ListAgentActionGroups", "bedrock:GetAgentActionGroup", "bedrock:ListAgentKnowledgeBases", "bedrock:GetKnowledgeBase", "bedrock:ListGuardrails", "bedrock:GetGuardrail"}, "Resource": "*"},
						map[string]any{"Effect": "Allow", "Action": []string{"bedrock-agentcore:ListAgentRuntimes", "bedrock-agentcore:GetAgentRuntime", "bedrock-agentcore:ListAgentRuntimeEndpoints", "bedrock-agentcore:GetAgentRuntimeEndpoint", "bedrock-agentcore:ListGateways", "bedrock-agentcore:GetGateway"}, "Resource": "*"},
						map[string]any{"Effect": "Allow", "Action": []string{"sagemaker:ListEndpoints", "sagemaker:DescribeEndpoint", "sagemaker:DescribeEndpointConfig", "sagemaker:DescribeModel"}, "Resource": "*"},
						map[string]any{"Effect": "Allow", "Action": []string{"ec2:DescribeVpcs", "ec2:DescribeSubnets", "ec2:DescribeSecurityGroups", "ec2:DescribeVpcEndpoints", "ec2:DescribeNetworkInterfaces", "iam:GetRole"}, "Resource": "*"},
					}}}},
				},
			}},
			"Outputs": map[string]any{"RoleArn": map[string]any{"Value": map[string]any{"Fn::GetAtt": []string{"LensDiscoveryRole", "Arn"}}}},
		}
		templateJSON, _ := json.MarshalIndent(template, "", "  ")
		return map[string]any{
			"method": "cloudformation", "template": string(templateJSON), "external_id": externalIDValue,
			"expected_role_arn": fmt.Sprintf("arn:aws:iam::%s:role/BarrikadeLensDiscovery", externalID),
			"what_lens_reads":   []string{"Bedrock agents, aliases, action-group and knowledge-base references, and guardrails", "AgentCore runtimes, endpoints, and gateways", "SageMaker endpoint and model references", "Linked IAM role names and network exposure metadata"},
			"excluded":          []string{"Prompts and model inputs or outputs", "Secret values", "Invocation APIs", "Write operations"}, "verify_path": verifyPath,
		}
	case "azure_subscription":
		roleName := "Barrikade Lens AI Inventory Reader"
		bicep := fmt.Sprintf(`targetScope = 'subscription'
param lensPrincipalId string

resource lensRole 'Microsoft.Authorization/roleDefinitions@2022-04-01' = {
  name: guid(subscription().id, 'barrikade-lens-ai-reader')
  properties: {
    roleName: '%s'
    description: 'Read-only AI resource inventory for Barrikade Lens'
    type: 'CustomRole'
    assignableScopes: [subscription().id]
    permissions: [{
      actions: [
        'Microsoft.CognitiveServices/accounts/read'
        'Microsoft.CognitiveServices/accounts/deployments/read'
		'Microsoft.CognitiveServices/accounts/projects/read'
		'Microsoft.CognitiveServices/accounts/projects/agents/read'
		'Microsoft.CognitiveServices/accounts/projects/applications/read'
        'Microsoft.MachineLearningServices/workspaces/read'
        'Microsoft.MachineLearningServices/workspaces/onlineEndpoints/read'
        'Microsoft.MachineLearningServices/workspaces/onlineEndpoints/deployments/read'
        'Microsoft.Network/virtualNetworks/read'
        'Microsoft.Network/privateEndpoints/read'
        'Microsoft.Authorization/roleAssignments/read'
        'Microsoft.Authorization/roleDefinitions/read'
		'Microsoft.ResourceGraph/resources/read'
		'Microsoft.Resources/subscriptions/read'
      ]
      notActions: []
      dataActions: []
      notDataActions: []
    }]
  }
}

resource lensAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(subscription().id, lensPrincipalId, lensRole.id)
  properties: {
    principalId: lensPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: lensRole.id
  }
}`, roleName)
		return map[string]any{
			"method": "azure_bicep", "subscription_id": externalID, "application_id": s.config.AzureApplicationID,
			"template":        bicep,
			"commands":        []string{fmt.Sprintf("az account set --subscription %s", shellQuote(externalID)), fmt.Sprintf("lensPrincipalId=$(az ad sp create --id %s --query id -o tsv)", shellQuote(s.config.AzureApplicationID)), "az deployment sub create --location eastus --template-file lens.bicep --parameters lensPrincipalId=$lensPrincipalId"},
			"what_lens_reads": []string{"Foundry accounts, projects, agents, and preview-labeled agent applications", "Azure AI Services and Azure OpenAI deployments", "Azure ML online endpoints", "Linked identities and network exposure metadata"},
			"excluded":        []string{"Prompts and model inputs or outputs", "Secret values", "Inference APIs", "Write operations"}, "verify_path": verifyPath,
		}
	case "gcp_project":
		workloadAudience, _ := configuration["workload_identity_audience"].(string)
		terraform := fmt.Sprintf(`variable "project_id" { default = %q }
variable "lens_issuer" { default = %q }
variable "lens_audience" { default = %q }

resource "google_iam_workload_identity_pool" "lens" {
  project                   = var.project_id
  workload_identity_pool_id = "barrikade-lens"
  display_name              = "Barrikade Lens"
}

resource "google_iam_workload_identity_pool_provider" "lens_azure" {
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.lens.workload_identity_pool_id
  workload_identity_pool_provider_id = "lens-azure"
  attribute_mapping = {
    "google.subject" = "assertion.sub"
  }
  oidc {
    issuer_uri        = var.lens_issuer
    allowed_audiences = [var.lens_audience]
  }
}

resource "google_project_iam_member" "lens_viewers" {
  for_each = toset([
    "roles/aiplatform.viewer",
	"roles/cloudasset.viewer",
    "roles/iam.roleViewer",
    "roles/compute.viewer",
	"roles/agentregistry.viewer"
  ])
  project = var.project_id
  role    = each.value
  member  = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.lens.name}/subject/%s"
}

output "lens_workload_identity_audience" {
  value = %q
}`, externalID, s.config.GCPWorkloadIssuer, s.config.GCPAssertionAudience, s.config.ManagedIdentityPrincipalID, workloadAudience)
		return map[string]any{
			"method": "terraform", "project_id": externalID, "template": terraform,
			"commands":        []string{"terraform init", "terraform apply"},
			"what_lens_reads": []string{"Vertex AI Agent Engine reasoning engines", "Preview-labeled Agent Registry endpoints", "Vertex endpoints and model references", "Linked RAG corpora, workload identities, and network exposure metadata"},
			"excluded":        []string{"Prompts and model inputs or outputs", "Secret values", "Prediction APIs", "Write operations"}, "verify_path": verifyPath,
		}
	case "endpoint":
		hub := strings.TrimSuffix(s.config.PublicURL, "/")
		return map[string]any{
			"method": "managed_collector", "enrollment_code": token,
			"commands": map[string]string{
				"macos":   endpointInstallCommand("macos", token, hub),
				"linux":   endpointInstallCommand("linux", token, hub),
				"windows": endpointInstallCommand("windows", token, hub),
			},
			"prerequisites":   []string{"Node.js 18 or newer", "Administrator access to install the background collector"},
			"what_lens_reads": []string{"Installed and running AI tools and runtimes", "Local configuration metadata and network listeners"},
			"excluded":        []string{"Prompt and conversation contents", "Secret values", "Source file bodies", "Write access"},
		}
	case "github_repository":
		if s.config.GitHubAppSlug != "" {
			return map[string]any{"method": "github_app", "install_url": "https://github.com/apps/" + url.PathEscape(s.config.GitHubAppSlug) + "/installations/new?state=" + url.QueryEscape(token), "select_repositories": true, "what_lens_reads": []string{"Selected repository metadata and detector-relevant configuration files"}, "excluded": []string{"Secret values", "Write access", "Repository administration"}}
		}
		return map[string]any{"method": "generic_ci", "enrollment_token": token, "message": "Install the Lens CI collector in the selected repository provider", "what_lens_reads": []string{"Detector-relevant repository configuration"}, "excluded": []string{"Secret values", "Write access"}}
	case "kubernetes_cluster":
		return map[string]any{
			"method": "helm", "enrollment_code": token,
			"command":         fmt.Sprintf("helm upgrade --install lens-k8s oci://ghcr.io/barrikadelabs/charts/lens-k8s --namespace lens-system --create-namespace --set hubURL=%s --set enrollmentCode=%s --set clusterName=%s", shellQuote(strings.TrimSuffix(s.config.PublicURL, "/")), shellQuote(token), shellQuote(displayName)),
			"what_lens_reads": []string{"Workloads, Services, Ingresses, ConfigMaps, namespaces, and CRD definitions"},
			"excluded":        []string{"Kubernetes Secrets", "Pod exec", "Workload mutation", "Application data"},
		}
	default:
		return map[string]any{"method": provider}
	}
}

func teardownInstructions(provider string) []string {
	switch provider {
	case "aws":
		return []string{"Delete the Barrikade Lens CloudFormation stack in the connected AWS account."}
	case "azure":
		return []string{"Remove the Barrikade Lens role assignment and service principal from the subscription tenant."}
	case "gcp":
		return []string{"Destroy the Lens Terraform resources or remove its workload identity provider and IAM bindings."}
	case "github":
		return []string{"Uninstall the Barrikade Lens GitHub App or remove access to the selected repository."}
	case "kubernetes":
		return []string{"Run `helm uninstall lens-k8s --namespace lens-system` and delete the namespace if it is no longer used."}
	default:
		return []string{"Remove the Lens collector from the environment."}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
