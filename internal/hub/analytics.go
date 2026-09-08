package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const analyticsSchemaVersion = 1

type ProductAnalyticsConfig struct {
	Enabled               bool
	ProjectToken          string
	Host                  string
	IDSalt                []byte
	DeploymentEnvironment string
}

func (c ProductAnalyticsConfig) Validate(authMode string, selfServe bool) error {
	if !c.Enabled {
		return nil
	}
	if authMode != "clerk" || !selfServe {
		return fmt.Errorf("PostHog analytics require Clerk auth and self-service mode")
	}
	if c.ProjectToken == "" {
		return fmt.Errorf("LENS_POSTHOG_PROJECT_TOKEN is required when PostHog analytics are enabled")
	}
	if len(c.IDSalt) < 32 {
		return fmt.Errorf("LENS_POSTHOG_ID_SALT must contain at least 32 bytes")
	}
	if c.DeploymentEnvironment != "staging" && c.DeploymentEnvironment != "production" {
		return fmt.Errorf("LENS_DEPLOYMENT_ENVIRONMENT must be staging or production when PostHog analytics are enabled")
	}
	parsed, err := url.Parse(c.Host)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("LENS_POSTHOG_HOST must be an HTTPS origin")
	}
	return nil
}

func pseudonymousID(salt []byte, domain, raw, prefix string) string {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(raw))
	return prefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:18])
}

func (c ProductAnalyticsConfig) UserID(raw string) string {
	return pseudonymousID(c.IDSalt, "lens-posthog-user-v1", raw, "phu_")
}

func (c ProductAnalyticsConfig) WorkspaceID(raw string) string {
	return pseudonymousID(c.IDSalt, "lens-posthog-workspace-v1", raw, "phw_")
}

type ProductEvent struct {
	OrganizationID string
	ActorID        string
	Name           string
	Properties     map[string]any
	DedupeKey      string
}

var productEventProperties = map[string]map[string]bool{
	"signup_completed":                   {},
	"workspace_created":                  {},
	"connection_setup_started":           {"connection_type": true, "lifecycle_phase": true},
	"environment_enrolled":               {"connection_type": true, "lifecycle_phase": true},
	"connection_removed":                 {"connection_type": true, "lifecycle_phase": true},
	"scan_received":                      {"connection_type": true, "lifecycle_phase": true},
	"scan_completed":                     {"status": true, "partial": true, "duration_ms": true, "queue_ms": true, "system_count_bucket": true},
	"scan_failed":                        {"failure": true, "duration_ms": true, "queue_ms": true},
	"first_credible_discovery_completed": {"status": true, "partial": true, "duration_ms": true, "queue_ms": true, "system_count_bucket": true},
	"first_credible_discovery_inspected": {"surface": true},
	"export_generated":                   {"export_format": true},
}

var safeStringValues = map[string]map[string]bool{
	"connection_type":     {"aws": true, "azure": true, "gcp": true, "endpoint": true, "github": true, "kubernetes": true},
	"lifecycle_phase":     {"setup": true, "enrolled": true, "removed": true, "received": true},
	"status":              {"complete": true, "partial": true},
	"failure":             {"validation": true, "normalization": true, "provider": true, "timeout": true, "internal": true},
	"system_count_bucket": {"1": true, "2-5": true, "6-20": true, "21+": true},
	"surface":             {"inventory": true, "system": true, "finding": true, "evidence": true, "changes": true},
	"export_format":       {"json": true, "csv": true, "cyclonedx": true},
}

func validateProductEvent(event ProductEvent) error {
	allowed, ok := productEventProperties[event.Name]
	if !ok {
		return fmt.Errorf("unknown analytics event %q", event.Name)
	}
	for key, value := range event.Properties {
		if !allowed[key] {
			return fmt.Errorf("property %q is not allowed for %s", key, event.Name)
		}
		switch typed := value.(type) {
		case string:
			if len(typed) > 32 || !safeStringValues[key][typed] {
				return fmt.Errorf("property %q contains an unsafe value", key)
			}
		case bool:
			if key != "partial" {
				return fmt.Errorf("boolean property %q is not allowed", key)
			}
		case int, int32, int64, float64:
			if key != "duration_ms" && key != "queue_ms" {
				return fmt.Errorf("numeric property %q is not allowed", key)
			}
		default:
			return fmt.Errorf("property %q has an unsupported type", key)
		}
	}
	return nil
}

func recordProductEvent(ctx context.Context, db database, config ProductAnalyticsConfig, event ProductEvent) error {
	if err := validateProductEvent(event); err != nil {
		return err
	}
	if event.Properties == nil {
		event.Properties = map[string]any{}
	}
	properties, err := json.Marshal(event.Properties)
	if err != nil {
		return err
	}
	status := any(nil)
	nextAttempt := any(nil)
	if config.Enabled {
		status = "pending"
		nextAttempt = "now"
	}
	insert := func(target database) error {
		if config.Enabled && event.ActorID != "" {
			var optedOut bool
			if preferenceErr := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM managed_users WHERE user_id=$1 AND analytics_opted_out_at IS NOT NULL)`, event.ActorID).Scan(&optedOut); preferenceErr != nil {
				return preferenceErr
			}
			if optedOut {
				return nil
			}
		}
		query := `INSERT INTO product_events(id,organization_id,actor_id,event_type,properties,dedupe_key,posthog_status,posthog_next_attempt_at)
		VALUES($1,NULLIF($2,''),NULLIF($3,''),$4,$5,NULLIF($6,''),$7,CASE WHEN $8::text='now' THEN now() ELSE NULL END) ON CONFLICT DO NOTHING`
		_, insertErr := target.Exec(ctx, query, uuid.New(), event.OrganizationID, event.ActorID, event.Name, properties, event.DedupeKey, status, nextAttempt)
		return insertErr
	}
	// A savepoint keeps an analytics constraint or availability failure from
	// aborting the Lens transaction that produced the event.
	if starter, ok := db.(interface {
		Begin(context.Context) (pgx.Tx, error)
	}); ok {
		nested, beginErr := starter.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		defer nested.Rollback(ctx)
		if err := insert(nested); err != nil {
			return err
		}
		return nested.Commit(ctx)
	}
	return insert(db)
}

func connectionType(kind, provider string) string {
	if provider == "repository" {
		return "github"
	}
	if provider != "" {
		return strings.TrimSpace(provider)
	}
	if kind == "kubernetes_cluster" {
		return "kubernetes"
	}
	if kind == "github_repository" {
		return "github"
	}
	if kind == "repository" {
		return "github"
	}
	return strings.TrimSuffix(kind, "_account")
}

func systemCountBucket(count int) string {
	switch {
	case count <= 1:
		return "1"
	case count <= 5:
		return "2-5"
	case count <= 20:
		return "6-20"
	default:
		return "21+"
	}
}

func failureCategory(code string) string {
	code = strings.ToLower(code)
	switch {
	case strings.Contains(code, "valid"):
		return "validation"
	case strings.Contains(code, "normal"):
		return "normalization"
	case strings.Contains(code, "timeout"), strings.Contains(code, "deadline"):
		return "timeout"
	case strings.Contains(code, "auth"), strings.Contains(code, "permission"), strings.Contains(code, "provider"), strings.Contains(code, "connector"):
		return "provider"
	default:
		return "internal"
	}
}
