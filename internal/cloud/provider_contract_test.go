package cloud

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string, headers map[string]string) *http.Response {
	value := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
	for key, item := range headers {
		value.Header.Set(key, item)
	}
	return value
}

func TestAzureDetectorPaginates(t *testing.T) {
	calls := 0
	client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected request: %s", request.URL)
		}
		if calls == 1 {
			return jsonResponse(200, `{"data":[{"id":"/one","name":"one","type":"microsoft.cognitiveservices/accounts","location":"westeurope"}],"$skipToken":"next"}`, nil), nil
		}
		return jsonResponse(200, `{"data":[{"id":"/two","name":"two","type":"microsoft.machinelearningservices/workspaces/onlineendpoints","location":"westeurope"}]}`, nil), nil
	})
	resources, err := (AzureResourceGraphDetector{Client: client}).Detect(t.Context(), Environment{ExternalID: "00000000-0000-0000-0000-000000000000"}, Credentials{BearerToken: "token"})
	if err != nil || calls != 2 || len(resources) != 2 {
		t.Fatalf("pagination failed calls=%d resources=%d err=%v", calls, len(resources), err)
	}
}

func TestProviderRetryAfterAndMalformedResponses(t *testing.T) {
	throttled := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(429, `{}`, map[string]string{"Retry-After": "17"}), nil
	})
	err := doJSON(t.Context(), throttled, http.MethodGet, "https://example.test", "token", nil, &map[string]any{})
	code, _, retryable, delay := SafeError(err)
	if code != "provider_throttled" || !retryable || delay != 17*time.Second {
		t.Fatalf("unexpected throttle mapping: %s %v %s", code, retryable, delay)
	}
	malformed := roundTripFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{`, nil), nil })
	err = doJSON(t.Context(), malformed, http.MethodGet, "https://example.test", "token", nil, &map[string]any{})
	code, _, _, _ = SafeError(err)
	if code != "malformed_provider_response" {
		t.Fatalf("unexpected malformed mapping: %v", err)
	}
}

func TestAWSSigningUsesTemporarySessionCredential(t *testing.T) {
	request, _ := http.NewRequest(http.MethodGet, "https://bedrock-agent.us-east-1.amazonaws.com/agents/?maxResults=100", nil)
	err := SignAWSRequest(request, nil, "bedrock", "us-east-1", Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "session"}, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") || request.Header.Get("X-Amz-Security-Token") != "session" {
		t.Fatalf("request was not signed with session credentials")
	}
}

func TestAWSDetectorDiscoversAgentCoreEndpointsAndSageMakerModels(t *testing.T) {
	client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") == "" || request.Header.Get("X-Amz-Security-Token") != "session" {
			t.Fatalf("unsigned AWS request: %s", request.URL)
		}
		switch request.Header.Get("X-Amz-Target") {
		case "SageMaker.ListEndpoints":
			return jsonResponse(200, `{"Endpoints":[{"EndpointArn":"arn:aws:sagemaker:eu-west-1:123456789012:endpoint/prod","EndpointName":"prod","EndpointStatus":"InService"}]}`, nil), nil
		case "SageMaker.DescribeEndpoint":
			return jsonResponse(200, `{"EndpointArn":"arn:aws:sagemaker:eu-west-1:123456789012:endpoint/prod","EndpointName":"prod","EndpointStatus":"InService","EndpointConfigName":"prod-config"}`, nil), nil
		case "SageMaker.DescribeEndpointConfig":
			return jsonResponse(200, `{"ProductionVariants":[{"VariantName":"main","ModelName":"agent-model"}]}`, nil), nil
		case "SageMaker.DescribeModel":
			return jsonResponse(200, `{"ModelArn":"arn:aws:sagemaker:eu-west-1:123456789012:model/agent-model","ModelName":"agent-model","ExecutionRoleArn":"arn:aws:iam::123456789012:role/model","EnableNetworkIsolation":true}`, nil), nil
		}
		path := request.URL.Path
		switch {
		case path == "/runtimes/":
			if request.Method != http.MethodPost {
				t.Fatalf("ListAgentRuntimes must use POST, got %s", request.Method)
			}
			return jsonResponse(200, `{"agentRuntimes":[{"agentRuntimeArn":"arn:aws:bedrock-agentcore:eu-west-1:123456789012:runtime/runtime-a","agentRuntimeId":"runtime-a-1234567890","agentRuntimeName":"runtime-a","status":"READY"}]}`, nil), nil
		case strings.Contains(path, "/runtime-endpoints/"):
			if request.Method != http.MethodPost {
				t.Fatalf("ListAgentRuntimeEndpoints must use POST, got %s", request.Method)
			}
			return jsonResponse(200, `{"runtimeEndpoints":[{"agentRuntimeEndpointArn":"arn:aws:bedrock-agentcore:eu-west-1:123456789012:runtime/runtime-a/runtime-endpoint/PROD","name":"PROD","status":"READY","liveVersion":"2"}]}`, nil), nil
		case strings.HasPrefix(path, "/agents/"):
			return jsonResponse(200, `{"agentSummaries":[],"agentAliasSummaries":[],"agentKnowledgeBaseSummaries":[],"actionGroupSummaries":[]}`, nil), nil
		case path == "/guardrails/":
			return jsonResponse(200, `{"guardrails":[]}`, nil), nil
		case path == "/gateways/":
			return jsonResponse(200, `{"items":[]}`, nil), nil
		default:
			t.Fatalf("unexpected AWS request: %s %s", request.Method, request.URL)
			return nil, nil
		}
	})
	resources, err := (AWSDetector{Client: client, Regions: []string{"eu-west-1"}}).Detect(t.Context(), Environment{ExternalID: "123456789012"}, Credentials{AccessKeyID: "AKID", SecretAccessKey: "secret", SessionToken: "session"})
	if err != nil {
		t.Fatal(err)
	}
	var runtimeEndpoints, modelReferences, models int
	for _, resource := range resources {
		if resource.Kind == discovery.KindRuntime && resource.Product == "Amazon Bedrock AgentCore" {
			for _, reference := range resource.References {
				if reference.Kind == discovery.KindEndpoint {
					runtimeEndpoints++
				}
			}
		}
		if resource.Kind == discovery.KindModelServer {
			modelReferences += len(resource.References)
		}
		if resource.Kind == discovery.KindModel {
			models++
		}
	}
	if runtimeEndpoints != 1 || modelReferences != 1 || models != 1 {
		t.Fatalf("missing linked AWS resources: endpoints=%d references=%d models=%d", runtimeEndpoints, modelReferences, models)
	}
}

func TestGCPAgentRegistryIncludesGlobalAndPaginates(t *testing.T) {
	calls := 0
	client := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/locations") {
			return jsonResponse(200, `{"locations":[{"locationId":"global"}]}`, nil), nil
		}
		if strings.Contains(request.URL.Path, "/locations/global/endpoints") {
			calls++
			if request.URL.Query().Get("pageToken") == "" {
				return jsonResponse(200, `{"endpoints":[{"name":"projects/p/locations/global/endpoints/a","displayName":"Agent A"}],"nextPageToken":"next"}`, nil), nil
			}
			return jsonResponse(200, `{"endpoints":[{"name":"projects/p/locations/global/endpoints/b","displayName":"Agent B"}]}`, nil), nil
		}
		t.Fatalf("unexpected GCP request: %s", request.URL)
		return nil, nil
	})
	resources, err := (GCPAgentRegistryDetector{Client: client}).Detect(t.Context(), Environment{ExternalID: "project-id"}, Credentials{BearerToken: "token"})
	if err != nil || calls != 2 || len(resources) != 2 {
		t.Fatalf("global pagination failed calls=%d resources=%d err=%v", calls, len(resources), err)
	}
}
