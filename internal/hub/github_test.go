package hub

import (
	"testing"

	"github.com/barrikadelabs/barrikade-lens/internal/githubapp"
)

func TestGitHubConnectorRequiresHealthyDeploymentConfiguration(t *testing.T) {
	server := &Server{config: Config{GitHubConnectorEnabled: true, GitHubClient: &githubapp.Client{}, GitHubAppSlug: "lens", GitHubWebhookSecret: []byte("secret")}}
	if !server.githubConnectorHealthy() {
		t.Fatal("expected complete GitHub App configuration to enable the connector")
	}
	server.config.GitHubWebhookSecret = nil
	if server.githubConnectorHealthy() {
		t.Fatal("expected a missing webhook secret to disable the connector")
	}
}

func TestGitHubScannerPermissionsAreReadOnlyAndMinimal(t *testing.T) {
	if !validGitHubScannerPermissions(map[string]string{"contents": "read", "metadata": "read"}) {
		t.Fatal("expected scanner permissions to be accepted")
	}
	for _, permissions := range []map[string]string{
		{},
		{"contents": "write", "metadata": "read"},
		{"contents": "read", "issues": "read"},
	} {
		if validGitHubScannerPermissions(permissions) {
			t.Fatalf("expected permissions to be rejected: %#v", permissions)
		}
	}
}
