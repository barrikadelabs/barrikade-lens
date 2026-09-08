package hub

import (
	"strings"
	"testing"
)

func TestAnalyticsPseudonymsAreStableAndDomainSeparated(t *testing.T) {
	config := ProductAnalyticsConfig{IDSalt: []byte("01234567890123456789012345678901")}
	first := config.UserID("clerk:user_123")
	if first != config.UserID("clerk:user_123") {
		t.Fatal("user pseudonym was not deterministic")
	}
	if !strings.HasPrefix(first, "phu_") || !strings.HasPrefix(config.WorkspaceID("user_123"), "phw_") {
		t.Fatal("pseudonyms did not use their domain prefix")
	}
	if first == config.WorkspaceID("clerk:user_123") || first == config.UserID("clerk:user_456") {
		t.Fatal("pseudonyms were not domain/value separated")
	}
	if strings.Contains(first, "user_123") {
		t.Fatal("pseudonym exposed its input")
	}
}

func TestAnalyticsConfigOnlyAllowsManagedSelfServe(t *testing.T) {
	config := ProductAnalyticsConfig{Enabled: true, ProjectToken: "phc_test", Host: "https://eu.i.posthog.com", IDSalt: []byte("01234567890123456789012345678901"), DeploymentEnvironment: "production"}
	if err := config.Validate("clerk", true); err != nil {
		t.Fatalf("valid managed configuration was rejected: %v", err)
	}
	for _, mode := range []string{"development", "oidc"} {
		if err := config.Validate(mode, true); err == nil {
			t.Fatalf("analytics unexpectedly allowed in %s mode", mode)
		}
	}
	if err := config.Validate("clerk", false); err == nil {
		t.Fatal("analytics unexpectedly allowed without self-service")
	}
	config.Host = "http://posthog.invalid"
	if err := config.Validate("clerk", true); err == nil {
		t.Fatal("non-HTTPS ingestion host was accepted")
	}
	config.Host = "https://eu.i.posthog.com"
	config.DeploymentEnvironment = "development"
	if err := config.Validate("clerk", true); err == nil {
		t.Fatal("development analytics were accepted")
	}
}

func TestAnalyticsContractRejectsSensitiveAndUnknownProperties(t *testing.T) {
	valid := ProductEvent{Name: "scan_completed", Properties: map[string]any{"status": "partial", "partial": true, "duration_ms": int64(5), "system_count_bucket": "2-5"}}
	if err := validateProductEvent(valid); err != nil {
		t.Fatalf("valid event was rejected: %v", err)
	}
	fixtures := []ProductEvent{
		{Name: "made_up", Properties: map[string]any{}},
		{Name: "scan_failed", Properties: map[string]any{"error": "dial db.prod.internal:5432"}},
		{Name: "export_generated", Properties: map[string]any{"export_format": "https://customer.example/repository"}},
		{Name: "first_credible_discovery_inspected", Properties: map[string]any{"surface": strings.Repeat("x", 100)}},
	}
	for _, fixture := range fixtures {
		if err := validateProductEvent(fixture); err == nil {
			t.Fatalf("unsafe event was accepted: %#v", fixture)
		}
	}
}

func TestCredibleDiscoveryCountBuckets(t *testing.T) {
	cases := map[int]string{1: "1", 2: "2-5", 5: "2-5", 6: "6-20", 20: "6-20", 21: "21+"}
	for count, expected := range cases {
		if got := systemCountBucket(count); got != expected {
			t.Fatalf("bucket(%d) = %q, want %q", count, got, expected)
		}
	}
}
