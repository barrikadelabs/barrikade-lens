package probe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestMetadataOnlyProbeAndSnapshotApplication(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.1.0","info":{"title":"Fixture API"},"paths":{},"secret":"not serialized"}`))
	}))
	defer server.Close()
	result, err := Handshake(context.Background(), server.URL, Config{AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Kind != discovery.KindAPIService {
		t.Fatalf("unexpected result: %#v", result)
	}
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	Apply(&snapshot, result)
	data, _ := json.Marshal(snapshot)
	if strings.Contains(string(data), "not serialized") {
		t.Fatal("probe response body leaked into snapshot")
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeBlocksCredentialsMetadataAndNonAllowlistedHosts(t *testing.T) {
	for _, target := range []string{"http://user:pass@example.com/agent.json", "http://169.254.169.254/latest/meta-data", "http://example.com/openapi.json?api_key=secret"} {
		if _, err := Handshake(context.Background(), target, Config{AllowedHosts: []string{"example.com", "169.254.169.254"}}); err == nil {
			t.Errorf("expected %s to be blocked", target)
		}
	}
}
