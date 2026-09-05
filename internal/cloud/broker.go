package cloud

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type StaticBroker struct{ Credentials Credentials }

func (b StaticBroker) Acquire(context.Context, Environment) (Credentials, error) {
	return b.Credentials, nil
}

type ManagedIdentityTokenSource struct {
	Client   HTTPDoer
	ClientID string
	Endpoint string
}

func (s ManagedIdentityTokenSource) Token(ctx context.Context, resource string) (string, time.Time, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := s.Endpoint
	if endpoint == "" {
		endpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
	}
	values := url.Values{"api-version": []string{"2019-08-01"}, "resource": []string{resource}}
	if s.ClientID != "" {
		values.Set("client_id", s.ClientID)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return "", time.Time{}, err
	}
	request.Header.Set("Metadata", "true")
	response, err := client.Do(request)
	if err != nil {
		return "", time.Time{}, &Error{Code: "credential_broker_unavailable", Message: "The Azure managed identity endpoint could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", time.Time{}, providerHTTPError(response)
	}
	var value struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
		ExpiresOn   string `json:"expires_on"`
	}
	if json.NewDecoder(response.Body).Decode(&value) != nil || value.AccessToken == "" {
		return "", time.Time{}, &Error{Code: "credential_broker_error", Message: "Azure managed identity returned an invalid token response"}
	}
	expires := time.Now().Add(45 * time.Minute)
	if seconds, err := strconv.Atoi(value.ExpiresIn); err == nil && seconds > 0 {
		expires = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	return value.AccessToken, expires, nil
}

type AzureFederatedBroker struct {
	Client            HTTPDoer
	TokenSource       ManagedIdentityTokenSource
	ApplicationID     string
	AssertionAudience string
}

func (b AzureFederatedBroker) Acquire(ctx context.Context, environment Environment) (Credentials, error) {
	var configuration map[string]any
	if json.Unmarshal(environment.Configuration, &configuration) != nil {
		return Credentials{}, &Error{Code: "invalid_connection", Message: "Azure connection metadata is invalid"}
	}
	tenantID, _ := configuration["tenant_id"].(string)
	if tenantID == "" || b.ApplicationID == "" {
		return Credentials{}, &Error{Code: "invalid_connection", Message: "Azure tenant and Lens application IDs are required"}
	}
	audience := b.AssertionAudience
	if audience == "" {
		audience = "api://AzureADTokenExchange"
	}
	assertion, _, err := b.TokenSource.Token(ctx, audience)
	if err != nil {
		return Credentials{}, err
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	form := url.Values{
		"client_id": []string{b.ApplicationID}, "scope": []string{"https://management.azure.com/.default"},
		"grant_type": []string{"client_credentials"}, "client_assertion_type": []string{"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": []string{assertion},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://login.microsoftonline.com/"+url.PathEscape(tenantID)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Credentials{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, &Error{Code: "credential_broker_unavailable", Message: "Microsoft Entra token exchange could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Credentials{}, providerHTTPError(response)
	}
	var value struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if json.NewDecoder(response.Body).Decode(&value) != nil || value.AccessToken == "" {
		return Credentials{}, &Error{Code: "credential_broker_error", Message: "Microsoft Entra returned an invalid token response"}
	}
	return Credentials{BearerToken: value.AccessToken, ExpiresAt: time.Now().Add(time.Duration(value.ExpiresIn) * time.Second)}, nil
}

type GCPFederatedBroker struct {
	Client            HTTPDoer
	TokenSource       ManagedIdentityTokenSource
	Audience          string
	AssertionAudience string
}

func (b GCPFederatedBroker) Acquire(ctx context.Context, environment Environment) (Credentials, error) {
	audience := b.Audience
	var configuration map[string]any
	_ = json.Unmarshal(environment.Configuration, &configuration)
	if configured, _ := configuration["workload_identity_audience"].(string); configured != "" {
		audience = configured
	}
	if audience == "" {
		return Credentials{}, &Error{Code: "invalid_connection", Message: "GCP workload identity audience is required"}
	}
	assertionAudience := b.AssertionAudience
	if assertionAudience == "" {
		assertionAudience = audience
	}
	assertion, _, err := b.TokenSource.Token(ctx, assertionAudience)
	if err != nil {
		return Credentials{}, err
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	body := map[string]any{"audience": audience, "grantType": "urn:ietf:params:oauth:grant-type:token-exchange", "requestedTokenType": "urn:ietf:params:oauth:token-type:access_token", "scope": "https://www.googleapis.com/auth/cloud-platform.read-only", "subjectTokenType": "urn:ietf:params:oauth:token-type:jwt", "subjectToken": assertion}
	var value struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := doJSON(ctx, client, http.MethodPost, "https://sts.googleapis.com/v1/token", "", body, &value); err != nil {
		return Credentials{}, err
	}
	if value.AccessToken == "" {
		return Credentials{}, &Error{Code: "credential_broker_error", Message: "Google Security Token Service returned an invalid token response"}
	}
	return Credentials{BearerToken: value.AccessToken, ExpiresAt: time.Now().Add(time.Duration(value.ExpiresIn) * time.Second)}, nil
}

type AWSFederatedBroker struct {
	Client        HTTPDoer
	TokenSource   ManagedIdentityTokenSource
	BrokerRoleARN string
	Audience      string
}

type awsAssumeRoleResponse struct {
	AssumeRoleResult struct {
		Credentials struct {
			AccessKeyID     string    `xml:"AccessKeyId"`
			SecretAccessKey string    `xml:"SecretAccessKey"`
			SessionToken    string    `xml:"SessionToken"`
			Expiration      time.Time `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:"AssumeRoleResult"`
	AssumeRoleWithWebIdentityResult struct {
		Credentials struct {
			AccessKeyID     string    `xml:"AccessKeyId"`
			SecretAccessKey string    `xml:"SecretAccessKey"`
			SessionToken    string    `xml:"SessionToken"`
			Expiration      time.Time `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:"AssumeRoleWithWebIdentityResult"`
}

func (b AWSFederatedBroker) Acquire(ctx context.Context, environment Environment) (Credentials, error) {
	if b.BrokerRoleARN == "" || b.Audience == "" {
		return Credentials{}, &Error{Code: "connector_unavailable", Message: "AWS federation is not configured"}
	}
	assertion, _, err := b.TokenSource.Token(ctx, b.Audience)
	if err != nil {
		return Credentials{}, err
	}
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	first, err := assumeRoleWithWebIdentity(ctx, client, b.BrokerRoleARN, assertion, "lens-broker-"+shortSession(environment.ID))
	if err != nil {
		return Credentials{}, err
	}
	var configuration map[string]any
	if json.Unmarshal(environment.Configuration, &configuration) != nil {
		return Credentials{}, &Error{Code: "invalid_connection", Message: "AWS connection metadata is invalid"}
	}
	externalID, _ := configuration["external_id"].(string)
	roleARN, _ := configuration["role_arn"].(string)
	if roleARN == "" {
		roleARN = "arn:aws:iam::" + environment.ExternalID + ":role/BarrikadeLensDiscovery"
	}
	if externalID == "" {
		return Credentials{}, &Error{Code: "invalid_connection", Message: "AWS external ID is missing"}
	}
	return assumeRole(ctx, client, first, roleARN, externalID, "lens-scan-"+shortSession(environment.ID))
}

func assumeRoleWithWebIdentity(ctx context.Context, client HTTPDoer, roleARN, assertion, session string) (Credentials, error) {
	values := url.Values{"Action": []string{"AssumeRoleWithWebIdentity"}, "Version": []string{"2011-06-15"}, "RoleArn": []string{roleARN}, "RoleSessionName": []string{session}, "WebIdentityToken": []string{assertion}, "DurationSeconds": []string{"3600"}}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://sts.amazonaws.com/", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, &Error{Code: "credential_broker_unavailable", Message: "AWS Security Token Service could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Credentials{}, providerHTTPError(response)
	}
	var value awsAssumeRoleResponse
	if xml.NewDecoder(response.Body).Decode(&value) != nil {
		return Credentials{}, &Error{Code: "credential_broker_error", Message: "AWS Security Token Service returned an invalid response"}
	}
	result := value.AssumeRoleWithWebIdentityResult.Credentials
	if result.AccessKeyID == "" {
		return Credentials{}, &Error{Code: "credential_broker_error", Message: "AWS Security Token Service returned incomplete broker credentials"}
	}
	return Credentials{AccessKeyID: result.AccessKeyID, SecretAccessKey: result.SecretAccessKey, SessionToken: result.SessionToken, ExpiresAt: result.Expiration}, nil
}

func assumeRole(ctx context.Context, client HTTPDoer, source Credentials, roleARN, externalID, session string) (Credentials, error) {
	values := url.Values{"Action": []string{"AssumeRole"}, "Version": []string{"2011-06-15"}, "RoleArn": []string{roleARN}, "RoleSessionName": []string{session}, "ExternalId": []string{externalID}, "DurationSeconds": []string{"3600"}}
	payload := []byte(values.Encode())
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://sts.amazonaws.com/", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := SignAWSRequest(request, payload, "sts", "us-east-1", source, time.Now().UTC()); err != nil {
		return Credentials{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Credentials{}, &Error{Code: "credential_broker_unavailable", Message: "AWS Security Token Service could not be reached", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Credentials{}, providerHTTPError(response)
	}
	var value awsAssumeRoleResponse
	if xml.NewDecoder(response.Body).Decode(&value) != nil || value.AssumeRoleResult.Credentials.AccessKeyID == "" {
		return Credentials{}, &Error{Code: "credential_broker_error", Message: "AWS Security Token Service returned an invalid response"}
	}
	result := value.AssumeRoleResult.Credentials
	return Credentials{AccessKeyID: result.AccessKeyID, SecretAccessKey: result.SecretAccessKey, SessionToken: result.SessionToken, ExpiresAt: result.Expiration}, nil
}

func shortSession(value string) string {
	value = strings.ReplaceAll(value, "-", "")
	if len(value) > 24 {
		return value[:24]
	}
	if value == "" {
		return "environment"
	}
	return value
}
