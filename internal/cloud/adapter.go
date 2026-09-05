// Package cloud contains the provider boundary used by managed cloud scans.
// Provider implementations return the normal Lens discovery contract and never
// write inventory tables directly.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Environment struct {
	ID             string
	OrganizationID string
	Provider       string
	ExternalID     string
	DisplayName    string
	SourceID       string
	TargetID       string
	Configuration  json.RawMessage
}

// Credentials intentionally has no JSON representation. Implementations keep
// short-lived provider material in memory for only the duration of Verify or Scan.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	BearerToken     string
	ExpiresAt       time.Time
}

type Verification struct {
	Principal   string            `json:"principal"`
	Permissions []string          `json:"permissions"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Progress struct {
	Phase      string         `json:"phase"`
	Completed  int            `json:"completed"`
	Total      int            `json:"total"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type ProgressFunc func(Progress)

// Adapter is the stable credential-broker and detector boundary. Credentials
// are always temporary and a scan returns a normalized Discovery Snapshot.
type Adapter interface {
	Verify(context.Context, Environment) (Verification, error)
	AcquireTemporaryCredentials(context.Context, Environment) (Credentials, error)
	Scan(context.Context, Environment, uint64, ProgressFunc) (discovery.Snapshot, error)
}

type Registry map[string]Adapter

func (r Registry) Adapter(provider string) (Adapter, error) {
	adapter := r[provider]
	if adapter == nil {
		return nil, &Error{Code: "connector_unavailable", Message: fmt.Sprintf("The %s connector is not configured", provider)}
	}
	return adapter, nil
}

// Error is safe to surface to a workspace member. Cause remains server-side.
type Error struct {
	Code       string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

func SafeError(err error) (code, message string, retryable bool, retryAfter time.Duration) {
	var value *Error
	if errors.As(err, &value) {
		return value.Code, value.Message, value.Retryable, value.RetryAfter
	}
	return "provider_error", "The provider request could not be completed", false, 0
}
