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
	server := enrollmentServer(t)
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "lens", "config.json")
	var output bytes.Buffer
	installed := false
	code := ExecuteWith(Dependencies{
		In: os.Stdin, Out: &output, Err: &output,
		InstallService: func(_ context.Context, executable, path string) (servicecontrol.Status, error) {
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
	if code != 0 || !installed {
		t.Fatalf("one-command enrollment failed: code=%d installed=%v output=%s", code, installed, output.String())
	}
	for _, message := range []string{"Enrolled source:test", "Collector service: running", "Onboarding complete"} {
		if !strings.Contains(output.String(), message) {
			t.Fatalf("output omitted %q: %s", message, output.String())
		}
	}
}

func TestEnrollWithoutInstallPreservesManualServiceFlow(t *testing.T) {
	server := enrollmentServer(t)
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

func enrollmentServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
