package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestMCPHandshakeListsToolsWithoutCallingThem(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var message struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			t.Error(err)
			return
		}
		methods = append(methods, message.Method)
		w.Header().Set("MCP-Session-Id", "private-session")
		switch message.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"CRM","instructions":"private prompt"}}}`)
		case "notifications/initialized":
			if r.Header.Get("MCP-Protocol-Version") != "2025-11-25" || r.Header.Get("MCP-Session-Id") != "private-session" {
				t.Error("negotiated headers were missing")
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("MCP-Protocol-Version") != "2025-11-25" || r.Header.Get("MCP-Session-Id") != "private-session" {
				t.Error("negotiated headers were missing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"search_records\",\"description\":\"private instructions\",\"inputSchema\":{\"secret\":\"never store\"}}]}}\n\n", message.ID)
		default:
			t.Errorf("unexpected MCP method %q", message.Method)
		}
	}))
	defer server.Close()
	result, err := MCPHandshake(context.Background(), server.URL+"/mcp", Config{AllowedHosts: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "initialize,notifications/initialized,tools/list" || len(result.Tools) != 1 || result.Tools[0] != "search_records" {
		t.Fatalf("unexpected protocol exchange: %v %#v", methods, result)
	}
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	Apply(&snapshot, result)
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Relationships) != 1 || snapshot.Relationships[0].ObservationState != discovery.ObservationObserved {
		t.Fatalf("observed tool edge missing: %#v", snapshot.Relationships)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, secret := range []string{"private-session", "private prompt", "private instructions", "never store"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("MCP snapshot retained %q", secret)
		}
	}
}

func TestMCPHandshakeRequiresExplicitAllowedHostAndBounds(t *testing.T) {
	if _, err := MCPHandshake(context.Background(), "http://user:pass@example.test/mcp?token=secret", Config{AllowedHosts: []string{"example.test"}}); err == nil {
		t.Fatal("credential-bearing MCP URL accepted")
	}
	if _, err := MCPHandshake(context.Background(), "http://169.254.169.254/mcp", Config{AllowedHosts: []string{"169.254.169.254"}}); err == nil {
		t.Fatal("metadata endpoint accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, strings.Repeat("x", 2048))
	}))
	defer server.Close()
	if _, err := MCPHandshake(context.Background(), server.URL, Config{AllowedHosts: []string{"127.0.0.1"}, MaxBytes: 256}); err == nil {
		t.Fatal("oversized response accepted")
	}
}

func TestMCPObservationConvergesWithConfiguredServer(t *testing.T) {
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "1", Mode: "test"})
	endpoint := "https://api.example.test/mcp"
	canonical := "mcp-endpoint:" + endpoint
	serverID := discovery.StableID("org", discovery.KindMCPServer, canonical)
	snapshot.Entities = append(snapshot.Entities, discovery.Entity{ID: serverID, Kind: discovery.KindMCPServer, CanonicalKey: canonical, Name: "Configured CRM", Attributes: map[string]any{"transport": "http"}, Confidence: discovery.ConfidencePossible})
	result := Result{Kind: discovery.KindMCPServer, Name: "Observed CRM", Endpoint: endpoint, Host: "api.example.test", ContentHash: "sha256:fixture", Tools: []string{"search_records"}, Attributes: map[string]any{"transport": "streamable_http"}}
	Apply(&snapshot, result)
	Apply(&snapshot, result)
	if len(snapshot.Entities) != 2 || len(snapshot.Relationships) != 1 || len(snapshot.Evidence) != 1 {
		t.Fatalf("configured and observed MCP metadata diverged or duplicated: entities=%d edges=%d evidence=%d", len(snapshot.Entities), len(snapshot.Relationships), len(snapshot.Evidence))
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
}
