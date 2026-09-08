package hubclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
