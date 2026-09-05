package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type AWSDetector struct {
	Client  HTTPDoer
	Regions []string
}

func (AWSDetector) ID() string      { return "aws.ai-control-planes" }
func (AWSDetector) Version() string { return "2026-09-01" }
func (AWSDetector) Preview() bool   { return false }

var defaultAWSRegions = []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "ca-central-1", "eu-central-1", "eu-west-1", "eu-west-2", "eu-west-3", "eu-north-1", "ap-northeast-1", "ap-northeast-2", "ap-south-1", "ap-southeast-1", "ap-southeast-2", "sa-east-1"}

func (d AWSDetector) Detect(ctx context.Context, environment Environment, credentials Credentials) ([]Resource, error) {
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return nil, &Error{Code: "authentication_failed", Message: "AWS did not return temporary role credentials"}
	}
	regions := d.Regions
	if len(regions) == 0 {
		regions = defaultAWSRegions
	}
	items := []Resource{}
	var firstError error
	for _, region := range regions {
		regionItems, err := d.detectRegion(ctx, client, region, environment.ExternalID, credentials)
		items = append(items, regionItems...)
		if err != nil && firstError == nil {
			firstError = err
		}
	}
	return items, firstError
}

func (d AWSDetector) detectRegion(ctx context.Context, client HTTPDoer, region, accountID string, credentials Credentials) ([]Resource, error) {
	items := []Resource{}
	var firstError error
	agents, err := awsRESTPages(ctx, client, credentials, "bedrock", region, "bedrock-agent."+region+".amazonaws.com", "/agents/", "agentSummaries")
	if err != nil {
		firstError = err
	}
	for _, value := range agents {
		agentID, name := mapString(value, "agentId"), mapString(value, "agentName")
		if agentID == "" {
			continue
		}
		if name == "" {
			name = agentID
		}
		resource := Resource{ProviderID: awsIdentifier(value, "agentArn", region, "bedrock-agent", agentID), Kind: discovery.KindAgent, Name: name, Location: region, Product: "Amazon Bedrock Agents", ResourceType: "AWS::Bedrock::Agent", LinkedIdentity: mapString(value, "agentResourceRoleArn"), NetworkScope: "none", Attributes: map[string]any{"status": mapString(value, "agentStatus")}}
		aliases, aliasErr := awsRESTPages(ctx, client, credentials, "bedrock", region, "bedrock-agent."+region+".amazonaws.com", "/agents/"+url.PathEscape(agentID)+"/agentaliases/", "agentAliasSummaries")
		if aliasErr != nil && firstError == nil {
			firstError = aliasErr
		}
		for _, alias := range aliases {
			aliasID := mapString(alias, "agentAliasId")
			if aliasID != "" {
				resource.References = append(resource.References, LinkedResource{ProviderID: resource.ProviderID + "/alias/" + aliasID, Name: firstNonEmpty(mapString(alias, "agentAliasName"), aliasID), Kind: discovery.KindRuntime, Relation: discovery.RelationshipDeployedAs, Attributes: map[string]any{"status": mapString(alias, "agentAliasStatus")}})
			}
		}
		knowledgeBases, knowledgeErr := awsRESTPages(ctx, client, credentials, "bedrock", region, "bedrock-agent."+region+".amazonaws.com", "/agents/"+url.PathEscape(agentID)+"/agentversions/DRAFT/knowledgebases/", "agentKnowledgeBaseSummaries")
		if knowledgeErr != nil && firstError == nil {
			firstError = knowledgeErr
		}
		for _, knowledge := range knowledgeBases {
			knowledgeID := mapString(knowledge, "knowledgeBaseId")
			if knowledgeID != "" {
				resource.KnowledgeStores = append(resource.KnowledgeStores, LinkedResource{ProviderID: awsIdentifier(knowledge, "knowledgeBaseArn", region, "bedrock-knowledge-base", knowledgeID), Name: firstNonEmpty(mapString(knowledge, "description"), knowledgeID), Kind: discovery.KindKnowledgeStore})
			}
		}
		actions, actionErr := awsRESTPages(ctx, client, credentials, "bedrock", region, "bedrock-agent."+region+".amazonaws.com", "/agents/"+url.PathEscape(agentID)+"/agentversions/DRAFT/actiongroups/", "actionGroupSummaries")
		if actionErr != nil && firstError == nil {
			firstError = actionErr
		}
		for _, action := range actions {
			actionID := mapString(action, "actionGroupId")
			if actionID != "" {
				resource.References = append(resource.References, LinkedResource{ProviderID: resource.ProviderID + "/action-group/" + actionID, Name: firstNonEmpty(mapString(action, "actionGroupName"), actionID), Kind: discovery.KindTool, Relation: discovery.RelationshipProvides})
			}
		}
		items = append(items, resource)
	}
	guardrails, err := awsRESTPages(ctx, client, credentials, "bedrock", region, "bedrock."+region+".amazonaws.com", "/guardrails/", "guardrails")
	if err != nil && firstError == nil {
		firstError = err
	}
	for _, value := range guardrails {
		id := mapString(value, "id")
		if id == "" {
			continue
		}
		items = append(items, Resource{ProviderID: awsIdentifier(value, "arn", region, "bedrock-guardrail", id), Kind: discovery.KindRuntime, Name: firstNonEmpty(mapString(value, "name"), id), Location: region, Product: "Amazon Bedrock Guardrails", ResourceType: "AWS::Bedrock::Guardrail", NetworkScope: "none", Attributes: map[string]any{"status": mapString(value, "status")}})
	}
	runtimes, err := awsRESTPagesMethod(ctx, client, credentials, "bedrock-agentcore", region, "bedrock-agentcore-control."+region+".amazonaws.com", http.MethodPost, "/runtimes/", "agentRuntimes")
	if err != nil && firstError == nil {
		firstError = err
	}
	for _, value := range runtimes {
		runtimeID := mapString(value, "agentRuntimeId")
		id := firstNonEmpty(mapString(value, "agentRuntimeArn"), runtimeID)
		if id == "" {
			continue
		}
		runtime := Resource{ProviderID: id, Kind: discovery.KindRuntime, Name: firstNonEmpty(mapString(value, "agentRuntimeName"), lastResourceSegment(id)), Location: region, Product: "Amazon Bedrock AgentCore", ResourceType: "AWS::BedrockAgentCore::Runtime", LinkedIdentity: firstNonEmpty(mapString(value, "roleArn"), nestedString(value, "workloadIdentityDetails", "workloadIdentityArn")), NetworkScope: "unknown", Attributes: map[string]any{"status": mapString(value, "status")}}
		if runtimeID != "" {
			endpoints, endpointErr := awsRESTPagesMethod(ctx, client, credentials, "bedrock-agentcore", region, "bedrock-agentcore-control."+region+".amazonaws.com", http.MethodPost, "/runtimes/"+url.PathEscape(runtimeID)+"/runtime-endpoints/", "runtimeEndpoints")
			if endpointErr != nil && firstError == nil {
				firstError = endpointErr
			}
			for _, endpoint := range endpoints {
				endpointID := firstNonEmpty(mapString(endpoint, "agentRuntimeEndpointArn"), mapString(endpoint, "id"))
				if endpointID == "" {
					continue
				}
				runtime.References = append(runtime.References, LinkedResource{ProviderID: endpointID, Name: firstNonEmpty(mapString(endpoint, "name"), lastResourceSegment(endpointID)), Kind: discovery.KindEndpoint, Relation: discovery.RelationshipDeployedAs, Attributes: map[string]any{"status": mapString(endpoint, "status"), "live_version": mapString(endpoint, "liveVersion"), "target_version": mapString(endpoint, "targetVersion")}})
			}
		}
		items = append(items, runtime)
	}
	gateways, err := awsRESTPages(ctx, client, credentials, "bedrock-agentcore", region, "bedrock-agentcore-control."+region+".amazonaws.com", "/gateways/", "items")
	if err != nil && firstError == nil {
		firstError = err
	}
	for _, value := range gateways {
		id := firstNonEmpty(mapString(value, "gatewayArn"), mapString(value, "gatewayId"))
		if id == "" {
			continue
		}
		items = append(items, Resource{ProviderID: id, Kind: discovery.KindAPIService, Name: firstNonEmpty(mapString(value, "name"), lastResourceSegment(id)), Location: region, Product: "Amazon Bedrock AgentCore Gateway", ResourceType: "AWS::BedrockAgentCore::Gateway", LinkedIdentity: mapString(value, "roleArn"), Endpoint: mapString(value, "gatewayUrl"), NetworkScope: "external", Attributes: map[string]any{"status": mapString(value, "status")}})
	}
	endpoints, err := awsJSONPages(ctx, client, credentials, "sagemaker", region, "api.sagemaker."+region+".amazonaws.com", "SageMaker.ListEndpoints", "Endpoints")
	if err != nil && firstError == nil {
		firstError = err
	}
	for _, value := range endpoints {
		arn := mapString(value, "EndpointArn")
		name := mapString(value, "EndpointName")
		if arn == "" {
			arn = "arn:aws:sagemaker:" + region + ":" + accountID + ":endpoint/" + name
		}
		if name == "" {
			continue
		}
		endpointResource := Resource{ProviderID: arn, Kind: discovery.KindModelServer, Name: name, Location: region, Product: "Amazon SageMaker", ResourceType: "AWS::SageMaker::Endpoint", NetworkScope: "unknown", Attributes: map[string]any{"status": mapString(value, "EndpointStatus")}}
		described, describeErr := awsJSONCall(ctx, client, credentials, "sagemaker", region, "api.sagemaker."+region+".amazonaws.com", "SageMaker.DescribeEndpoint", map[string]any{"EndpointName": name})
		if describeErr != nil && firstError == nil {
			firstError = describeErr
		}
		if described != nil {
			endpointResource.ProviderID = firstNonEmpty(mapString(described, "EndpointArn"), endpointResource.ProviderID)
			endpointResource.Attributes["status"] = firstNonEmpty(mapString(described, "EndpointStatus"), mapString(value, "EndpointStatus"))
			configName := mapString(described, "EndpointConfigName")
			if configName != "" {
				endpointResource.Attributes["endpoint_config"] = configName
				config, configErr := awsJSONCall(ctx, client, credentials, "sagemaker", region, "api.sagemaker."+region+".amazonaws.com", "SageMaker.DescribeEndpointConfig", map[string]any{"EndpointConfigName": configName})
				if configErr != nil && firstError == nil {
					firstError = configErr
				}
				variants := append(mapItems(config["ProductionVariants"]), mapItems(config["ShadowProductionVariants"])...)
				seenModels := map[string]bool{}
				for _, variant := range variants {
					modelName := mapString(variant, "ModelName")
					if modelName == "" || seenModels[modelName] {
						continue
					}
					seenModels[modelName] = true
					modelARN := "arn:aws:sagemaker:" + region + ":" + accountID + ":model/" + modelName
					endpointResource.References = append(endpointResource.References, LinkedResource{ProviderID: modelARN, Name: modelName, Kind: discovery.KindModel, Relation: discovery.RelationshipUses})
					model, modelErr := awsJSONCall(ctx, client, credentials, "sagemaker", region, "api.sagemaker."+region+".amazonaws.com", "SageMaker.DescribeModel", map[string]any{"ModelName": modelName})
					if modelErr != nil && firstError == nil {
						firstError = modelErr
					}
					if model != nil {
						modelAttributes := map[string]any{"network_isolation": model["EnableNetworkIsolation"]}
						if vpc, ok := model["VpcConfig"].(map[string]any); ok {
							modelAttributes["vpc_subnets"] = vpc["Subnets"]
							modelAttributes["security_groups"] = vpc["SecurityGroupIds"]
						}
						items = append(items, Resource{ProviderID: firstNonEmpty(mapString(model, "ModelArn"), modelARN), Kind: discovery.KindModel, Name: modelName, Location: region, Product: "Amazon SageMaker", ResourceType: "AWS::SageMaker::Model", LinkedIdentity: mapString(model, "ExecutionRoleArn"), NetworkScope: "none", Attributes: modelAttributes})
					}
				}
			}
		}
		items = append(items, endpointResource)
	}
	return items, firstError
}

func awsRESTPages(ctx context.Context, client HTTPDoer, credentials Credentials, service, region, host, path, field string) ([]map[string]any, error) {
	return awsRESTPagesMethod(ctx, client, credentials, service, region, host, http.MethodGet, path, field)
}

func awsRESTPagesMethod(ctx context.Context, client HTTPDoer, credentials Credentials, service, region, host, method, path, field string) ([]map[string]any, error) {
	items, token := []map[string]any{}, ""
	for page := 0; page < 100; page++ {
		values := url.Values{"maxResults": []string{"100"}}
		if token != "" {
			values.Set("nextToken", token)
		}
		endpoint := "https://" + host + path + "?" + values.Encode()
		var response map[string]any
		if err := awsJSONRequest(ctx, client, credentials, service, region, method, endpoint, nil, nil, &response); err != nil {
			return items, err
		}
		items = append(items, mapItems(response[field])...)
		token = mapString(response, "nextToken")
		if token == "" {
			return items, nil
		}
	}
	return items, &Error{Code: "pagination_limit", Message: "AWS returned too many resources from one detector"}
}

func awsJSONCall(ctx context.Context, client HTTPDoer, credentials Credentials, service, region, host, target string, body map[string]any) (map[string]any, error) {
	response := map[string]any{}
	headers := map[string]string{"X-Amz-Target": target, "Content-Type": "application/x-amz-json-1.1"}
	if err := awsJSONRequest(ctx, client, credentials, service, region, http.MethodPost, "https://"+host+"/", headers, body, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func awsJSONPages(ctx context.Context, client HTTPDoer, credentials Credentials, service, region, host, target, field string) ([]map[string]any, error) {
	items, token := []map[string]any{}, ""
	for page := 0; page < 100; page++ {
		body := map[string]any{"MaxResults": 100}
		if token != "" {
			body["NextToken"] = token
		}
		var response map[string]any
		headers := map[string]string{"X-Amz-Target": target, "Content-Type": "application/x-amz-json-1.1"}
		if err := awsJSONRequest(ctx, client, credentials, service, region, http.MethodPost, "https://"+host+"/", headers, body, &response); err != nil {
			return items, err
		}
		items = append(items, mapItems(response[field])...)
		token = mapString(response, "NextToken")
		if token == "" {
			return items, nil
		}
	}
	return items, &Error{Code: "pagination_limit", Message: "AWS returned too many resources from one detector"}
}

func awsJSONRequest(ctx context.Context, client HTTPDoer, credentials Credentials, service, region, method, endpoint string, headers map[string]string, body any, target any) error {
	payload := []byte{}
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if err := SignAWSRequest(request, payload, service, region, credentials, time.Now().UTC()); err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return &Error{Code: "provider_unavailable", Message: "AWS could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return providerHTTPError(response)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(target); err != nil {
		return &Error{Code: "malformed_provider_response", Message: "AWS returned an unreadable response", Cause: err}
	}
	return nil
}

func SignAWSRequest(request *http.Request, payload []byte, service, region string, credentials Credentials, now time.Time) error {
	if credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return fmt.Errorf("AWS credentials are incomplete")
	}
	amzDate, date := now.Format("20060102T150405Z"), now.Format("20060102")
	payloadDigest := sha256.Sum256(payload)
	request.Header.Set("X-Amz-Date", amzDate)
	request.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(payloadDigest[:]))
	if credentials.SessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", credentials.SessionToken)
	}
	headers := map[string]string{"host": request.URL.Host}
	for key, values := range request.Header {
		lower := strings.ToLower(key)
		if lower == "authorization" {
			continue
		}
		cleaned := make([]string, len(values))
		for index, value := range values {
			cleaned[index] = strings.Join(strings.Fields(value), " ")
		}
		headers[lower] = strings.Join(cleaned, ",")
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonicalHeaders := ""
	for _, key := range keys {
		canonicalHeaders += key + ":" + headers[key] + "\n"
	}
	signedHeaders := strings.Join(keys, ";")
	canonicalQuery := request.URL.Query().Encode()
	canonicalRequest := strings.Join([]string{request.Method, request.URL.EscapedPath(), canonicalQuery, canonicalHeaders, signedHeaders, hex.EncodeToString(payloadDigest[:])}, "\n")
	requestDigest := sha256.Sum256([]byte(canonicalRequest))
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(requestDigest[:])
	kDate := hmacSHA256([]byte("AWS4"+credentials.SecretAccessKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+credentials.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
func mapItems(value any) []map[string]any {
	raw, _ := value.([]any)
	result := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}
func mapString(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func awsIdentifier(value map[string]any, arnKey, region, resourceType, id string) string {
	if arn := mapString(value, arnKey); arn != "" {
		return arn
	}
	return "aws:" + region + ":" + resourceType + ":" + id
}

func NewAWSAdapter(broker CredentialBroker, client HTTPDoer, regions []string) Adapter {
	return &ManagedAdapter{Provider: "aws", Broker: broker, Detectors: []Detector{AWSDetector{Client: client, Regions: regions}}}
}
