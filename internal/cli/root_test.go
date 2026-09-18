package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/managed"
	servicecontrol "github.com/barrikadelabs/barrikade-lens/internal/service"
)

func TestQuickScanUsesPublicHubAndNeverInstallsService(t *testing.T) {
	var output bytes.Buffer
	installed := false
	called := false
	code := ExecuteWith(Dependencies{
		In: os.Stdin, Out: &output, Err: &output,
		QuickScan: func(_ context.Context, hub, enrollmentCode, identityPath string) (managed.QuickScanResult, error) {
			called = true
			if hub != OfficialHubURL || enrollmentCode != "ABCD-1234" || identityPath != "" {
				t.Fatalf("unexpected quick scan inputs: hub=%q code=%q path=%q", hub, enrollmentCode, identityPath)
			}
			return managed.QuickScanResult{JobID: "job", EntityCount: 7, RelationCount: 3}, nil
		},
		InstallService: func(context.Context, string, string) (servicecontrol.Status, error) {
			installed = true
			return servicecontrol.Status{}, nil
		},
	}, []string{"scan", "--enroll", "ABCD-1234"})
	if code != 0 || !called || installed {
		t.Fatalf("quick scan code=%d called=%v installed=%v output=%s", code, called, installed, output.String())
	}
	if !strings.Contains(output.String(), "7 assets") || !strings.Contains(output.String(), "No background service was installed") {
		t.Fatalf("quick scan summary missing: %s", output.String())
	}
}

func TestQuickScanReturnsPartialExitCodeAfterUpload(t *testing.T) {
	var output bytes.Buffer
	code := ExecuteWith(Dependencies{In: os.Stdin, Out: &output, Err: &output, QuickScan: func(context.Context, string, string, string) (managed.QuickScanResult, error) {
		return managed.QuickScanResult{Partial: true}, nil
	}}, []string{"scan", "--enroll", "ABCD-1234", "--hub", "http://localhost:8080"})
	if code != 2 || !strings.Contains(output.String(), "partial results") {
		t.Fatalf("partial quick scan code=%d output=%s", code, output.String())
	}
}

func TestQuickScanUploadFailureReturnsErrorWithoutInstallingService(t *testing.T) {
	var output bytes.Buffer
	installed := false
	code := ExecuteWith(Dependencies{In: os.Stdin, Out: &output, Err: &output,
		QuickScan: func(context.Context, string, string, string) (managed.QuickScanResult, error) {
			return managed.QuickScanResult{}, errors.New("upload unavailable")
		},
		InstallService: func(context.Context, string, string) (servicecontrol.Status, error) {
			installed = true
			return servicecontrol.Status{}, nil
		},
	}, []string{"scan", "--enroll", "ABCD-1234"})
	if code != 1 || installed || !strings.Contains(output.String(), "upload unavailable") {
		t.Fatalf("upload failure code=%d installed=%v output=%s", code, installed, output.String())
	}
}

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

func TestEnrollInstallElevatesBeforeConsumingEnrollmentCode(t *testing.T) {
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { serverCalled = true }))
	defer server.Close()
	var output bytes.Buffer
	var received []string
	code := ExecuteWith(Dependencies{
		In: os.Stdin, Out: &output, Err: &output,
		EnsureInstallPrivileges: func(_ context.Context, args []string) (bool, error) {
			received = append([]string(nil), args...)
			return true, nil
		},
	}, []string{"enroll", "one-time-code", "--hub", server.URL, "--install"})
	if code != 0 {
		t.Fatalf("elevation handoff failed: code=%d output=%s", code, output.String())
	}
	if serverCalled {
		t.Fatal("enrollment code was consumed before the elevated process started")
	}
	if strings.Join(received, "|") != "enroll|one-time-code|--hub|"+server.URL+"|--install" {
		t.Fatalf("elevated invocation did not preserve arguments: %#v", received)
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

func TestEnrollReadsProtectedBootstrapCredentialFromStdinWithoutLoggingIt(t *testing.T) {
	const secret = "ABCDE-FGHIJ"
	server := enrollmentServer(t, nil)
	var output, errors bytes.Buffer
	configuration := filepath.Join(t.TempDir(), "config.json")
	code := ExecuteWith(Dependencies{In: strings.NewReader(secret + "\n"), Out: &output, Err: &errors}, []string{"enroll", "--enrollment-code-stdin", "--hub", server.URL, "--config", configuration})
	if code != 0 {
		t.Fatalf("stdin enrollment failed: code=%d error=%s", code, errors.String())
	}
	if strings.Contains(output.String(), secret) || strings.Contains(errors.String(), secret) {
		t.Fatal("bootstrap credential was printed")
	}
}

func TestEnrollRejectsAmbiguousBootstrapCredentialSources(t *testing.T) {
	var errors bytes.Buffer
	code := ExecuteWith(Dependencies{In: strings.NewReader("stdin-secret"), Out: io.Discard, Err: &errors}, []string{"enroll", "argument-secret", "--enrollment-code-stdin", "--hub", "https://lens.example"})
	if code == 0 || !strings.Contains(errors.String(), "either as an argument") {
		t.Fatalf("expected ambiguous credential error, code=%d error=%s", code, errors.String())
	}
	if strings.Contains(errors.String(), "stdin-secret") || strings.Contains(errors.String(), "argument-secret") {
		t.Fatal("bootstrap credential was printed in the validation error")
	}
}

func TestEnrollRedactsBootstrapCredentialFromHubErrors(t *testing.T) {
	const secret = "private-bootstrap-code"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(writer, `{"error":{"code":"invalid_enrollment","message":"rejected %s"}}`, secret)
	}))
	defer server.Close()
	var errors bytes.Buffer
	code := ExecuteWith(Dependencies{In: strings.NewReader(secret), Out: io.Discard, Err: &errors}, []string{"enroll", "--enrollment-code-stdin", "--hub", server.URL, "--config", filepath.Join(t.TempDir(), "config.json")})
	if code == 0 || strings.Contains(errors.String(), secret) || !strings.Contains(errors.String(), "[redacted]") {
		t.Fatalf("bootstrap credential was not redacted: code=%d error=%s", code, errors.String())
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
