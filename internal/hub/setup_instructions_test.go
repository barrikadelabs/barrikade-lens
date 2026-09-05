package hub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAWSSetupManifestContainsOnlyInventoryReads(t *testing.T) {
	server := &Server{config: Config{AWSBrokerRoleARN: "arn:aws:iam::111111111111:role/LensBroker"}}
	setup := server.environmentSetupInstructions("aws_account", "aws", "123456789012", "Production", "single-use", map[string]any{"external_id": "0123456789abcdef0123456789abcdef"})
	template, _ := setup["template"].(string)
	if template == "" || !strings.Contains(template, "0123456789abcdef0123456789abcdef") || !strings.Contains(template, server.config.AWSBrokerRoleARN) {
		t.Fatalf("generated trust does not bind broker and external ID: %s", template)
	}
	var document any
	if err := json.Unmarshal([]byte(template), &document); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Create", "Update", "Delete", "Put", "Invoke", "SecretValue", "GetObject"} {
		if strings.Contains(template, `"`+forbidden) || strings.Contains(template, `:`+forbidden) {
			t.Fatalf("runtime permission manifest contains forbidden operation %q", forbidden)
		}
	}
}

func TestGeneratedSetupsStatePrivacyBoundary(t *testing.T) {
	server := &Server{config: Config{PublicURL: "https://lens.example", AzureApplicationID: "app", GCPWorkloadIssuer: "https://issuer", GCPAssertionAudience: "api://exchange", ManagedIdentityPrincipalID: "principal"}}
	for _, test := range []struct {
		kind, provider string
		configuration  map[string]any
	}{
		{"azure_subscription", "azure", map[string]any{}},
		{"gcp_project", "gcp", map[string]any{"workload_identity_audience": "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/lens/providers/azure"}},
		{"endpoint", "endpoint", map[string]any{}},
		{"kubernetes_cluster", "kubernetes", map[string]any{}},
	} {
		setup := server.environmentSetupInstructions(test.kind, test.provider, "00000000-0000-0000-0000-000000000000", "Production", "single-use", test.configuration)
		excluded, _ := setup["excluded"].([]string)
		joined := strings.ToLower(strings.Join(excluded, " "))
		if !strings.Contains(joined, "secret") || !strings.Contains(joined, "write") && !strings.Contains(joined, "mutation") {
			t.Fatalf("%s setup does not communicate read-only privacy boundary: %#v", test.kind, setup)
		}
	}
}
