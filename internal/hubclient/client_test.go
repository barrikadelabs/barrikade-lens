package hubclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

func TestEnrollQuickUsesTransientCredentialsAndUploadDoesNotPersistConfig(t *testing.T) {
	uploads := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/enrollment/exchange":
			var payload EnrollmentRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.EnrollmentMode != "quick_scan" || payload.IdentityPublicKey == "" || payload.IdentityProof == "" {
				t.Fatalf("unexpected quick enrollment: %+v", payload)
			}
			_, _ = writer.Write([]byte(`{"organization_id":"org","source_id":"source","target_id":"target","access_token":"short-lived"}`))
		case "/v1/discovery/snapshots":
			uploads++
			if request.Header.Get("Authorization") != "Bearer short-lived" {
				t.Fatalf("missing transient bearer token")
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"job","status":"pending"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "collector.json")
	client := New("test")
	cfg, err := client.EnrollQuick(context.Background(), server.URL, "ABCD-1234", configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshToken != "" {
		t.Fatal("quick enrollment unexpectedly returned a refresh token")
	}
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "test", Mode: "quick_scan"})
	snapshot.TargetID = "target"
	if _, err = client.UploadTransient(context.Background(), cfg, snapshot); err != nil {
		t.Fatal(err)
	}
	if uploads != 1 {
		t.Fatalf("uploads=%d want exactly one", uploads)
	}
	if _, err = os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("collector config was persisted: %v", err)
	}
}

func TestDoJSONSurfacesSafeHubError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":{"code":"environment_exists","message":"This environment is already connected"}}`))
	}))
	defer server.Close()

	err := New("test").doJSON(context.Background(), http.MethodPost, server.URL, "", map[string]string{}, nil)
	if err == nil || err.Error() != "Lens Hub returned environment_exists (HTTP 409): This environment is already connected" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoJSONRejectsUnsafeHubErrorText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"code":"bad code","message":"terminal\u001b[31m injection"}}`))
	}))
	defer server.Close()

	err := New("test").doJSON(context.Background(), http.MethodPost, server.URL, "", map[string]string{}, nil)
	if err == nil || err.Error() != "Hub request failed with HTTP 500" || strings.Contains(err.Error(), "terminal") {
		t.Fatalf("unsafe response was surfaced: %v", err)
	}
}

func TestUploadReusesPersistentCredentialAndStoresCanonicalHub(t *testing.T) {
	refreshes, uploads := 0, 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/discovery/snapshots":
			uploads++
			if request.Header.Get("Authorization") != "Bearer fresh-access" {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(`{"error":{"code":"invalid_token","message":"Access token expired"}}`))
				return
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"job","status":"pending"}`))
		case "/v1/collector/token":
			refreshes++
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["refresh_token"] != "persistent-installation" {
				t.Fatalf("unexpected refresh payload: %v err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"hub_url":"` + server.URL + `","access_token":"fresh-access","access_token_expires_at":"2099-01-01T00:00:00Z","refresh_token":"persistent-installation"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := lensconfig.Config{ConfigVersion: 2, HubURL: server.URL, OrganizationID: "org", SourceID: "source", TargetID: "target", AccessToken: "expired-access", RefreshToken: "persistent-installation"}
	if err := lensconfig.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	snapshot := discovery.NewSnapshot("org", "source", discovery.SourceEndpoint, discovery.Collector{ID: "test", Name: "test", Version: "test", Mode: "managed"})
	snapshot.TargetID = "target"
	if _, err := New("test").Upload(context.Background(), configPath, &cfg, snapshot); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || uploads != 2 || cfg.RefreshToken != "persistent-installation" || cfg.HubURL != server.URL {
		t.Fatalf("unexpected refresh state refreshes=%d uploads=%d cfg=%+v", refreshes, uploads, cfg)
	}
	stored, err := lensconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "fresh-access" || stored.RefreshToken != "persistent-installation" || stored.HubURL != server.URL {
		t.Fatalf("refreshed configuration was not stored: %+v", stored)
	}
}
