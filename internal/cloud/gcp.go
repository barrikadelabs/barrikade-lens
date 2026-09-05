package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type GCPCloudAssetDetector struct{ Client HTTPDoer }

func (GCPCloudAssetDetector) ID() string      { return "gcp.vertex-assets" }
func (GCPCloudAssetDetector) Version() string { return "v1" }
func (GCPCloudAssetDetector) Preview() bool   { return false }

func (d GCPCloudAssetDetector) Detect(ctx context.Context, environment Environment, credentials Credentials) ([]Resource, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	if credentials.BearerToken == "" {
		return nil, &Error{Code: "authentication_failed", Message: "GCP did not return an access token"}
	}
	base := "https://cloudasset.googleapis.com/v1/projects/" + url.PathEscape(environment.ExternalID) + ":searchAllResources"
	query := url.Values{"pageSize": []string{"500"}}
	for _, assetType := range []string{"aiplatform.googleapis.com/Endpoint", "aiplatform.googleapis.com/Model", "aiplatform.googleapis.com/ReasoningEngine", "aiplatform.googleapis.com/RagCorpus"} {
		query.Add("assetTypes", assetType)
	}
	items, pageToken := []Resource{}, ""
	for page := 0; page < 100; page++ {
		if pageToken == "" {
			query.Del("pageToken")
		} else {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			Results       []gcpAsset `json:"results"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := doJSON(ctx, client, http.MethodGet, base+"?"+query.Encode(), credentials.BearerToken, nil, &response); err != nil {
			return nil, err
		}
		for _, value := range response.Results {
			if value.Name == "" {
				continue
			}
			items = append(items, gcpAssetResource(value))
		}
		if response.NextPageToken == "" {
			return items, nil
		}
		pageToken = response.NextPageToken
	}
	return items, &Error{Code: "pagination_limit", Message: "GCP returned more than 50,000 matching AI resources"}
}

type gcpAsset struct {
	Name                 string         `json:"name"`
	DisplayName          string         `json:"displayName"`
	AssetType            string         `json:"assetType"`
	Location             string         `json:"location"`
	AdditionalAttributes map[string]any `json:"additionalAttributes"`
	NetworkTags          []string       `json:"networkTags"`
}

func gcpAssetResource(value gcpAsset) Resource {
	kind, product := discovery.KindRuntime, "Vertex AI"
	switch {
	case strings.HasSuffix(value.AssetType, "/Endpoint"):
		kind = discovery.KindModelServer
	case strings.HasSuffix(value.AssetType, "/Model"):
		kind = discovery.KindModel
	case strings.HasSuffix(value.AssetType, "/ReasoningEngine"):
		kind, product = discovery.KindAgent, "Vertex AI Agent Engine"
	case strings.HasSuffix(value.AssetType, "/RagCorpus"):
		kind, product = discovery.KindKnowledgeStore, "Vertex AI RAG"
	}
	name := value.DisplayName
	if name == "" {
		name = lastResourceSegment(value.Name)
	}
	attributes := map[string]any{}
	if len(value.NetworkTags) > 0 {
		attributes["network_tags"] = value.NetworkTags
	}
	for _, key := range []string{"state", "region", "kmsKey", "network", "serviceAccount"} {
		if item, ok := value.AdditionalAttributes[key]; ok {
			attributes[strings.ToLower(key)] = item
		}
	}
	identity, _ := value.AdditionalAttributes["serviceAccount"].(string)
	return Resource{ProviderID: value.Name, Kind: kind, Name: name, Location: value.Location, Product: product, ResourceType: value.AssetType, LinkedIdentity: identity, NetworkScope: networkScopeFromGCP(value.AdditionalAttributes), Attributes: attributes}
}

type GCPVertexAgentDetector struct{ Client HTTPDoer }

func (GCPVertexAgentDetector) ID() string      { return "gcp.vertex-agent-resources" }
func (GCPVertexAgentDetector) Version() string { return "v1beta1" }
func (GCPVertexAgentDetector) Preview() bool   { return false }

func (d GCPVertexAgentDetector) Detect(ctx context.Context, environment Environment, credentials Credentials) ([]Resource, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	locations, err := gcpLocations(ctx, client, "https://aiplatform.googleapis.com/v1/projects/"+url.PathEscape(environment.ExternalID)+"/locations", credentials.BearerToken)
	if err != nil {
		return nil, err
	}
	items := []Resource{}
	for _, location := range locations {
		parent := "projects/" + environment.ExternalID + "/locations/" + location
		for _, resourceType := range []struct {
			path, field string
			kind        discovery.EntityKind
			product     string
		}{
			{"reasoningEngines", "reasoningEngines", discovery.KindAgent, "Vertex AI Agent Engine"},
			{"ragCorpora", "ragCorpora", discovery.KindKnowledgeStore, "Vertex AI RAG"},
		} {
			pageToken := ""
			for page := 0; page < 100; page++ {
				values := url.Values{"pageSize": []string{"100"}}
				if pageToken != "" {
					values.Set("pageToken", pageToken)
				}
				endpoint := "https://aiplatform.googleapis.com/v1beta1/" + parent + "/" + resourceType.path + "?" + values.Encode()
				var response map[string]any
				if err := doJSON(ctx, client, http.MethodGet, endpoint, credentials.BearerToken, nil, &response); err != nil {
					return items, err
				}
				rawItems, _ := response[resourceType.field].([]any)
				for _, raw := range rawItems {
					value, _ := raw.(map[string]any)
					name := nestedString(value, "name")
					if name == "" {
						continue
					}
					displayName := nestedString(value, "displayName")
					if displayName == "" {
						displayName = lastResourceSegment(name)
					}
					identity := nestedString(value, "spec", "serviceAccount")
					items = append(items, Resource{ProviderID: name, Kind: resourceType.kind, Name: displayName, Location: location, Product: resourceType.product, ResourceType: resourceType.path, LinkedIdentity: identity, NetworkScope: "unknown", Attributes: map[string]any{"state": nestedString(value, "state")}})
				}
				pageToken, _ = response["nextPageToken"].(string)
				if pageToken == "" {
					break
				}
			}
		}
	}
	return items, nil
}

type GCPAgentRegistryDetector struct{ Client HTTPDoer }

func (GCPAgentRegistryDetector) ID() string      { return "gcp.agent-registry" }
func (GCPAgentRegistryDetector) Version() string { return "v1alpha" }
func (GCPAgentRegistryDetector) Preview() bool   { return true }

func (d GCPAgentRegistryDetector) Detect(ctx context.Context, environment Environment, credentials Credentials) ([]Resource, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	base := "https://agentregistry.googleapis.com/v1alpha/projects/" + url.PathEscape(environment.ExternalID)
	locations, err := gcpLocations(ctx, client, base+"/locations", credentials.BearerToken)
	if err != nil {
		return nil, err
	}
	items := []Resource{}
	for _, location := range locations {
		pageToken := ""
		for page := 0; page < 100; page++ {
			values := url.Values{"pageSize": []string{"100"}}
			if pageToken != "" {
				values.Set("pageToken", pageToken)
			}
			endpoint := base + "/locations/" + url.PathEscape(location) + "/endpoints?" + values.Encode()
			var response struct {
				Endpoints []struct {
					Name        string           `json:"name"`
					EndpointID  string           `json:"endpointId"`
					DisplayName string           `json:"displayName"`
					Interfaces  []map[string]any `json:"interfaces"`
					Attributes  map[string]any   `json:"attributes"`
				} `json:"endpoints"`
				NextPageToken string `json:"nextPageToken"`
			}
			if err := doJSON(ctx, client, http.MethodGet, endpoint, credentials.BearerToken, nil, &response); err != nil {
				return items, err
			}
			for _, value := range response.Endpoints {
				name := value.DisplayName
				if name == "" {
					name = lastResourceSegment(value.Name)
				}
				runtimeRef := nestedString(value.Attributes, "agentregistry.googleapis.com/system/RuntimeReference", "uri")
				resource := Resource{ProviderID: value.Name, Kind: discovery.KindAgent, Name: name, Location: location, Product: "Google Agent Registry", ResourceType: "agentregistry.googleapis.com/Endpoint", NetworkScope: registryNetworkScope(value.Interfaces), Attributes: map[string]any{"endpoint_id": value.EndpointID}}
				if runtimeRef != "" {
					resource.References = append(resource.References, LinkedResource{ProviderID: runtimeRef, Name: lastResourceSegment(runtimeRef), Kind: discovery.KindRuntime, Relation: discovery.RelationshipDeployedAs})
				}
				items = append(items, resource)
			}
			if response.NextPageToken == "" {
				break
			}
			pageToken = response.NextPageToken
		}
	}
	return items, nil
}

func gcpLocations(ctx context.Context, client HTTPDoer, endpoint, bearer string) ([]string, error) {
	locations, pageToken := []string{}, ""
	for page := 0; page < 20; page++ {
		values := url.Values{"pageSize": []string{"100"}}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		var response struct {
			Locations []struct {
				LocationID string `json:"locationId"`
				Name       string `json:"name"`
			} `json:"locations"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := doJSON(ctx, client, http.MethodGet, endpoint+"?"+values.Encode(), bearer, nil, &response); err != nil {
			return nil, err
		}
		for _, value := range response.Locations {
			location := value.LocationID
			if location == "" {
				location = lastResourceSegment(value.Name)
			}
			if location != "" {
				locations = append(locations, location)
			}
		}
		if response.NextPageToken == "" {
			return locations, nil
		}
		pageToken = response.NextPageToken
	}
	return locations, &Error{Code: "pagination_limit", Message: "GCP returned too many API locations"}
}

func lastResourceSegment(value string) string {
	value = strings.TrimSuffix(value, "/")
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		return value[index+1:]
	}
	return value
}

func networkScopeFromGCP(attributes map[string]any) string {
	if value, ok := attributes["publicEndpointEnabled"].(bool); ok && value {
		return "external"
	}
	if nestedString(attributes, "network") != "" {
		return "network"
	}
	return "unknown"
}

func registryNetworkScope(interfaces []map[string]any) string {
	for _, item := range interfaces {
		uri := nestedString(item, "url")
		if uri == "" {
			uri = nestedString(item, "uri")
		}
		if strings.HasPrefix(uri, "https://") || strings.HasPrefix(uri, "http://") {
			return "external"
		}
	}
	return "unknown"
}

func NewGCPAdapter(broker CredentialBroker, client HTTPDoer) Adapter {
	return &ManagedAdapter{Provider: "gcp", Broker: broker, Detectors: []Detector{GCPCloudAssetDetector{Client: client}, GCPVertexAgentDetector{Client: client}, GCPAgentRegistryDetector{Client: client}}}
}
