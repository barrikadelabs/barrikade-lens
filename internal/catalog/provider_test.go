package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactIndexMatchingAndLazyFetch(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "example", "apis.json")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Fixture","include":[{"name":"example.com:main@1 - Example","url":"example/apis.json"}]}`
	entry := `{"aid":"example.com:main-1","name":"Example","apis":[{"aid":"example.com:main-1","name":"Example API","description":"Example capabilities","baseURL":"https://api.example.com/v1","version":"1","properties":[]}]}`
	if err := os.WriteFile(filepath.Join(root, "apis.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &FileProvider{ManifestPath: filepath.Join(root, "apis.json")}
	index, err := provider.Refresh(context.Background(), State{})
	if err != nil {
		t.Fatal(err)
	}
	matches := provider.Match(index, "api.example.com", "")
	if len(matches) != 1 || matches[0].Confidence != "confirmed" || !matches[0].Exact {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	document, err := provider.Fetch(context.Background(), matches[0].Entry, State{})
	if err != nil {
		t.Fatal(err)
	}
	if document.API.Host != "api.example.com" {
		t.Fatalf("unexpected API: %#v", document.API)
	}
}

func TestCatalogHTTPBlocksCredentialsParametersAndPrivateTargets(t *testing.T) {
	provider := &OAKProvider{}
	for _, target := range []string{
		"https://user:pass@example.com/apis.json",
		"https://example.com/apis.json?token=secret",
		"https://169.254.169.254/latest/meta-data",
	} {
		if _, _, _, err := provider.fetchHTTP(context.Background(), target, State{}); err == nil {
			t.Fatalf("expected catalog URL to be blocked: %s", target)
		}
	}
	_, err := restrictedCatalogDialer()(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("private address was not blocked: %v", err)
	}
}

func TestUmbrellaCatalogSelectsTheDiscoveredHost(t *testing.T) {
	root := t.TempDir()
	entryPath := filepath.Join(root, "vendor", "apis.json")
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Fixture","include":[{"name":"example.com:main@1 - Vendor","url":"vendor/apis.json"}]}`
	entry := `{"apis":[{"aid":"wrong","name":"Wrong","baseURL":"https://first.example.com"},{"aid":"right","name":"Right","baseURL":"https://agents.example.com/v1"}]}`
	if err := os.WriteFile(filepath.Join(root, "apis.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &FileProvider{ManifestPath: filepath.Join(root, "apis.json")}
	index, err := provider.Refresh(context.Background(), State{})
	if err != nil {
		t.Fatal(err)
	}
	matches := provider.Match(index, "agents.example.com", "")
	if len(matches) == 0 {
		t.Fatal("expected provider-domain match")
	}
	document, err := provider.Fetch(context.Background(), matches[0].Entry, State{})
	if err != nil {
		t.Fatal(err)
	}
	if document.API.ID != "right" {
		t.Fatalf("selected wrong umbrella API: %#v", document.API)
	}
}

func TestAmbiguousProvidersAndVersionsNeverAutoLink(t *testing.T) {
	provider := &OAKProvider{}
	for _, providerID := range []string{"amazonaws.com", "googleapis.com", "github.com", "anthropic.com"} {
		index := Index{Entries: []Entry{
			{ID: providerID + "-v1", Name: providerID + ":main@v1", ProviderID: providerID, APIFamily: "main", Version: "v1"},
			{ID: providerID + "-v2", Name: providerID + ":main@v2", ProviderID: providerID, APIFamily: "main", Version: "v2"},
		}}
		matches := provider.Match(index, "api."+providerID, "")
		if len(matches) != 2 {
			t.Fatalf("%s: expected two suggestions, got %#v", providerID, matches)
		}
		for _, match := range matches {
			if match.Exact || match.Confidence == "confirmed" {
				t.Fatalf("%s collision auto-linked: %#v", providerID, match)
			}
		}
		versioned := provider.Match(index, "", "main@v2")
		if len(versioned) != 1 || !versioned[0].Exact || versioned[0].Entry.Version != "v2" {
			t.Fatalf("%s explicit family/version did not disambiguate: %#v", providerID, versioned)
		}
	}
}

func TestRealCompactIndexWhenRepositoryIsAvailable(t *testing.T) {
	path := filepath.Clean("../../../jentic-public-apis/apis/openapi/apis.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("public catalog checkout is not attached")
	}
	provider := &FileProvider{ManifestPath: path}
	index, err := provider.Refresh(context.Background(), State{})
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Entries) < 6000 {
		t.Fatalf("expected compact public index, got %d entries", len(index.Entries))
	}
	matches := provider.Match(index, "api.1password.com", "")
	if len(matches) == 0 {
		t.Fatal("expected provider-domain match")
	}
}
