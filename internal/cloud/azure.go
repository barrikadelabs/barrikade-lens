package cloud

import (
	"context"
	"net/http"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type AzureResourceGraphDetector struct {
	Client        HTTPDoer
	DetectorID    string
	APIVersion    string
	ResourceTypes []string
	PreviewAPI    bool
}

func (d AzureResourceGraphDetector) ID() string {
	if d.DetectorID != "" {
		return d.DetectorID
	}
	return "azure.resource-graph"
}
func (d AzureResourceGraphDetector) Version() string {
	if d.APIVersion != "" {
		return d.APIVersion
	}
	return "2024-04-01"
}
func (d AzureResourceGraphDetector) Preview() bool { return d.PreviewAPI }

func (d AzureResourceGraphDetector) Detect(ctx context.Context, environment Environment, credentials Credentials) ([]Resource, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	if credentials.BearerToken == "" {
		return nil, &Error{Code: "authentication_failed", Message: "Azure did not return an ARM access token"}
	}
	resourceTypes := d.ResourceTypes
	if len(resourceTypes) == 0 {
		resourceTypes = azureStableResourceTypes
	}
	quoted := make([]string, 0, len(resourceTypes))
	for _, value := range resourceTypes {
		quoted = append(quoted, "'"+strings.ReplaceAll(strings.ToLower(value), "'", "''")+"'")
	}
	query := "Resources | where type in~ (" + strings.Join(quoted, ",") + ") | project id,name,type,location,resourceGroup,subscriptionId,kind,identity,properties | order by id asc"
	endpoint := "https://management.azure.com/providers/Microsoft.ResourceGraph/resources?api-version=" + d.Version()
	items := []Resource{}
	skipToken := ""
	for page := 0; page < 100; page++ {
		body := map[string]any{"subscriptions": []string{environment.ExternalID}, "query": query, "options": map[string]any{"$top": 1000, "resultFormat": "objectArray"}}
		if skipToken != "" {
			body["options"].(map[string]any)["$skipToken"] = skipToken
		}
		var response struct {
			Data      []azureGraphResource `json:"data"`
			SkipToken string               `json:"$skipToken"`
		}
		if err := doJSON(ctx, client, http.MethodPost, endpoint, credentials.BearerToken, body, &response); err != nil {
			return nil, err
		}
		for _, value := range response.Data {
			if value.ID == "" || value.Name == "" {
				continue
			}
			items = append(items, azureResource(value))
		}
		if response.SkipToken == "" {
			return items, nil
		}
		skipToken = response.SkipToken
	}
	return items, &Error{Code: "pagination_limit", Message: "Azure returned more than 100,000 matching AI resources"}
}

type azureGraphResource struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Type          string         `json:"type"`
	Location      string         `json:"location"`
	ResourceGroup string         `json:"resourceGroup"`
	Kind          string         `json:"kind"`
	Identity      map[string]any `json:"identity"`
	Properties    map[string]any `json:"properties"`
}

var azureStableResourceTypes = []string{
	"microsoft.cognitiveservices/accounts",
	"microsoft.cognitiveservices/accounts/deployments",
	"microsoft.cognitiveservices/accounts/projects",
	"microsoft.cognitiveservices/accounts/projects/agents",
	"microsoft.machinelearningservices/workspaces",
	"microsoft.machinelearningservices/workspaces/onlineendpoints",
	"microsoft.machinelearningservices/workspaces/onlineendpoints/deployments",
}

var AzureAgentApplicationResourceTypes = []string{
	"microsoft.cognitiveservices/accounts/projects/applications",
	"microsoft.cognitiveservices/accounts/projects/applications/agentdeployments",
}

func azureResource(value azureGraphResource) Resource {
	resourceType := strings.ToLower(value.Type)
	kind := discovery.KindRuntime
	switch {
	case strings.Contains(resourceType, "/agents") || strings.HasSuffix(resourceType, "/applications"):
		kind = discovery.KindAgent
	case strings.HasSuffix(resourceType, "/deployments"):
		kind = discovery.KindModel
	case strings.HasSuffix(resourceType, "/projects"):
		kind = discovery.KindRuntime
	case strings.HasSuffix(resourceType, "/onlineendpoints"):
		kind = discovery.KindModelServer
	}
	endpoint := nestedString(value.Properties, "baseUrl")
	if endpoint == "" {
		endpoint = nestedString(value.Properties, "scoringUri")
	}
	networkScope := "unknown"
	if strings.EqualFold(nestedString(value.Properties, "publicNetworkAccess"), "disabled") {
		networkScope = "network"
	} else if endpoint != "" {
		networkScope = "external"
	}
	identity := nestedString(value.Identity, "principalId")
	if identity == "" {
		identity = nestedString(value.Properties, "defaultInstanceIdentity", "principalId")
	}
	result := Resource{ProviderID: value.ID, Kind: kind, Name: value.Name, Location: value.Location, Product: azureProduct(resourceType), ResourceType: value.Type, Endpoint: endpoint, NetworkScope: networkScope, LinkedIdentity: identity, Attributes: map[string]any{"resource_group": value.ResourceGroup}}
	if model := nestedString(value.Properties, "model", "name"); model != "" {
		result.References = append(result.References, LinkedResource{ProviderID: value.ID + "/model/" + model, Name: model, Kind: discovery.KindModel, Relation: discovery.RelationshipUses})
	}
	if agents, ok := value.Properties["agents"].([]any); ok {
		for _, raw := range agents {
			item, _ := raw.(map[string]any)
			agentID := nestedString(item, "agentId")
			agentName := nestedString(item, "agentName")
			if agentID == "" {
				agentID = agentName
			}
			if agentID != "" {
				result.References = append(result.References, LinkedResource{ProviderID: value.ID + "/agent/" + agentID, Name: agentName, Kind: discovery.KindAgent, Relation: discovery.RelationshipUses})
			}
		}
	}
	return result
}

func azureProduct(resourceType string) string {
	switch {
	case strings.Contains(resourceType, "/applications"):
		return "Azure AI Foundry Agent Applications"
	case strings.Contains(resourceType, "/agents") || strings.Contains(resourceType, "/projects"):
		return "Azure AI Foundry"
	case strings.Contains(resourceType, "machinelearningservices"):
		return "Azure Machine Learning"
	case strings.Contains(resourceType, "/deployments"):
		return "Azure OpenAI"
	default:
		return "Azure AI Services"
	}
}

func nestedString(value map[string]any, path ...string) string {
	var current any = value
	for _, part := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[part]
	}
	if result, ok := current.(string); ok {
		return result
	}
	return ""
}

func NewAzureAdapter(broker CredentialBroker, client HTTPDoer) Adapter {
	return &ManagedAdapter{Provider: "azure", Broker: broker, Detectors: []Detector{
		AzureResourceGraphDetector{Client: client, DetectorID: "azure.ai-resources", APIVersion: "2024-04-01", ResourceTypes: azureStableResourceTypes},
		AzureResourceGraphDetector{Client: client, DetectorID: "azure.agent-applications", APIVersion: "2026-01-15-preview", ResourceTypes: AzureAgentApplicationResourceTypes, PreviewAPI: true},
	}}
}
