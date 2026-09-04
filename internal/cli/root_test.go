package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	servicecontrol "github.com/barrikadelabs/barrikade-lens/internal/service"
)

func TestEnrollInstallCompletesOnboardingInOneCommand(t *testing.T) {
	server := enrollmentServer(t, nil)
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "lens", "config.json")
	var output bytes.Buffer
	installed := false
	collected := false
	code := ExecuteWith(Dependencies{
		In: os.Stdin, Out: &output, Err: &output,
		CollectOnce: func(_ context.Context, path string) error {
			if path != configPath {
				t.Fatalf("collector config path=%q want %q", path, configPath)
			}
			collected = true
			return nil
		},
		InstallService: func(_ context.Context, executable, path string) (servicecontrol.Status, error) {
			if !collected {
				t.Fatal("service installed before the initial snapshot was uploaded")
			}
			installed = true
			if executable != "" {
				t.Fatalf("expected the current executable, got %q", executable)
			}
			if path != configPath {
				t.Fatalf("service config path=%q want %q", path, configPath)
			}
			if _, err := lensconfig.Load(path); err != nil {
				t.Fatalf("service installer received an unreadable config: %v", err)
			}
			return servicecontrol.Status{State: servicecontrol.StateRunning}, nil
		},
	}, []string{"enroll", "ABCDE-FGHIJ", "--hub", server.URL, "--config", configPath, "--install"})
	if code != 0 || !collected || !installed {
		t.Fatalf("one-command enrollment failed: code=%d collected=%v installed=%v output=%s", code, collected, installed, output.String())
	}
	for _, message := range []string{"Enrolled source:test", "Initial discovery snapshot uploaded", "Collector service: running", "Onboarding complete"} {
		if !strings.Contains(output.String(), message) {
			t.Fatalf("output omitted %q: %s", message, output.String())
		}
	}
}

func TestEnrollInstallUploadsBeforeStartingService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	uploaded := false
	server := enrollmentServer(t, &uploaded)
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "lens", "config.json")
	var output bytes.Buffer
	code := ExecuteWith(Dependencies{
		In: os.Stdin, Out: &output, Err: &output,
		InstallService: func(_ context.Context, _, _ string) (servicecontrol.Status, error) {
			if !uploaded {
				t.Fatal("service installed before the Hub accepted the initial snapshot")
			}
			return servicecontrol.Status{State: servicecontrol.StateRunning}, nil
		},
	}, []string{"enroll", "ABCDE-FGHIJ", "--hub", server.URL, "--config", configPath, "--install"})
	if code != 0 || !uploaded {
		t.Fatalf("initial upload failed: code=%d uploaded=%v output=%s", code, uploaded, output.String())
	}
	cfg, err := lensconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sequence != 1 {
		t.Fatalf("initial upload sequence=%d want 1", cfg.Sequence)
	}
}

func TestEnrollWithoutInstallPreservesManualServiceFlow(t *testing.T) {
	server := enrollmentServer(t, nil)
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "lens", "config.json")
	var output bytes.Buffer
	code := ExecuteWith(Dependencies{In: os.Stdin, Out: &output, Err: &output}, []string{"enroll", "ABCDE-FGHIJ", "--hub", server.URL, "--config", configPath})
	if code != 0 {
		t.Fatalf("enrollment failed: code=%d output=%s", code, output.String())
	}
	if !strings.Contains(output.String(), "barrikade-lens service install") {
		t.Fatalf("manual service guidance was omitted: %s", output.String())
	}
}

func enrollmentServer(t *testing.T, uploaded *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/v1/discovery/snapshots" {
			if request.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("snapshot omitted collector authorization")
			}
			if uploaded != nil {
				*uploaded = true
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"job:test","status":"pending"}`))
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/enrollment/exchange" {
			http.NotFound(writer, request)
			return
		}
		var payload struct {
			Code              string `json:"code"`
			IdentityPublicKey string `json:"identity_public_key"`
			IdentityProof     string `json:"identity_proof"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode enrollment request: %v", err)
		}
		if payload.Code != "ABCDE-FGHIJ" || payload.IdentityPublicKey == "" || payload.IdentityProof == "" {
			t.Errorf("incomplete enrollment request: %+v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"organization_id":"org_test","source_id":"source:test","target_id":"endpoint:test","access_token":"access","refresh_token":"refresh"}`))
	}))
}
