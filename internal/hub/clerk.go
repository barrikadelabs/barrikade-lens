package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) clerkWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("svix-id")
	timestamp := r.Header.Get("svix-timestamp")
	signatures := r.Header.Get("svix-signature")
	if id == "" || timestamp == "" || signatures == "" {
		writeError(w, 400, "invalid_webhook", "Required Clerk webhook headers are missing")
		return
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(unix, 0)).Abs() > 5*time.Minute {
		writeError(w, 401, "invalid_signature", "Clerk webhook timestamp is invalid")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "invalid_webhook", "Webhook body could not be read")
		return
	}
	secret, err := decodeClerkWebhookSecret(s.config.ClerkWebhookSecret)
	if err != nil || !validSvixSignature(secret, id+"."+timestamp+"."+string(body), signatures) {
		writeError(w, 401, "invalid_signature", "Clerk webhook signature is invalid")
		return
	}
	var envelope struct {
		Type      string          `json:"type"`
		Timestamp int64           `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Type == "" {
		writeError(w, 400, "invalid_webhook", "Clerk webhook payload is malformed")
		return
	}
	tx, err := s.begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not process webhook")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `INSERT INTO clerk_webhook_deliveries(event_id,event_type) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, envelope.Type)
	if err != nil {
		writeError(w, 500, "database_error", "Could not record webhook")
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, 200, map[string]string{"status": "duplicate"})
		return
	}
	eventAt := time.Unix(unix, 0).UTC()
	if envelope.Timestamp > 0 {
		stamp := envelope.Timestamp
		if stamp > 1_000_000_000_000 {
			stamp /= 1000
		}
		eventAt = time.Unix(stamp, 0).UTC()
	}
	if err = applyClerkLifecycleEventAt(r.Context(), tx, envelope.Type, envelope.Data, eventAt); err != nil {
		writeError(w, 422, "invalid_webhook", "Clerk lifecycle payload could not be applied")
		return
	}
	if envelope.Type == "user.created" {
		var user struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(envelope.Data, &user) != nil || user.ID == "" {
			writeError(w, 422, "invalid_webhook", "Clerk lifecycle payload could not be applied")
			return
		}
		if analyticsErr := recordProductEvent(r.Context(), tx, s.config.ProductAnalytics, ProductEvent{ActorID: "clerk:" + user.ID, Name: "signup_completed", DedupeKey: id}); analyticsErr != nil {
			s.config.Logger.Warn("analytics event was not recorded", "event", "signup_completed", "error", analyticsErr)
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "Could not commit webhook")
		return
	}
	writeJSON(w, 202, map[string]string{"status": "accepted"})
}

func decodeClerkWebhookSecret(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "whsec_")
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func validSvixSignature(secret []byte, signed string, header string) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signed))
	expected := mac.Sum(nil)
	for _, candidate := range strings.Fields(header) {
		parts := strings.SplitN(candidate, ",", 2)
		if len(parts) != 2 || parts[0] != "v1" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err == nil && subtle.ConstantTimeCompare(expected, decoded) == 1 {
			return true
		}
	}
	return false
}

func applyClerkLifecycleEvent(ctx context.Context, tx pgx.Tx, eventType string, data json.RawMessage) error {
	return applyClerkLifecycleEventAt(ctx, tx, eventType, data, time.Now().UTC())
}

func applyClerkLifecycleEventAt(ctx context.Context, tx pgx.Tx, eventType string, data json.RawMessage, eventAt time.Time) error {
	switch eventType {
	case "organization.created", "organization.updated", "organization.deleted":
		var value struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &value) != nil || value.ID == "" {
			return errInvalidWebhook
		}
		status := "active"
		if eventType == "organization.deleted" {
			status = "deleted"
		}
		tag, err := tx.Exec(ctx, `INSERT INTO clerk_organization_state(organization_id,status,provider_updated_at) VALUES($1,$2,$3)
			ON CONFLICT(organization_id) DO UPDATE SET status=EXCLUDED.status,provider_updated_at=EXCLUDED.provider_updated_at,updated_at=now()
			WHERE clerk_organization_state.provider_updated_at<=EXCLUDED.provider_updated_at`, value.ID, status, eventAt)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if status == "deleted" {
			_, err = tx.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, value.ID)
			return err
		}
		name := strings.TrimSpace(value.Name)
		if name == "" {
			name = "Lens workspace"
		}
		_, err = tx.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name`, value.ID, name)
		return err
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		var value struct {
			Role         string `json:"role"`
			Organization struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"organization"`
			PublicUserData struct {
				UserID string `json:"user_id"`
			} `json:"public_user_data"`
		}
		if json.Unmarshal(data, &value) != nil || value.Organization.ID == "" || value.PublicUserData.UserID == "" {
			return errInvalidWebhook
		}
		var organizationStatus string
		var organizationUpdated time.Time
		stateErr := tx.QueryRow(ctx, `SELECT status,provider_updated_at FROM clerk_organization_state WHERE organization_id=$1`, value.Organization.ID).Scan(&organizationStatus, &organizationUpdated)
		if stateErr == nil && organizationStatus == "deleted" && !eventAt.After(organizationUpdated) {
			return nil
		}
		if stateErr != nil && stateErr != pgx.ErrNoRows {
			return stateErr
		}
		name := strings.TrimSpace(value.Organization.Name)
		if name == "" {
			name = "Lens workspace"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, value.Organization.ID, name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO clerk_organization_state(organization_id,status,provider_updated_at) VALUES($1,'active',$2)
			ON CONFLICT(organization_id) DO UPDATE SET status='active',provider_updated_at=EXCLUDED.provider_updated_at,updated_at=now()
			WHERE clerk_organization_state.provider_updated_at<=EXCLUDED.provider_updated_at`, value.Organization.ID, eventAt); err != nil {
			return err
		}
		status := "active"
		if eventType == "organizationMembership.deleted" {
			status = "revoked"
		}
		_, err := tx.Exec(ctx, `INSERT INTO workspace_memberships(organization_id,user_id,role,status,provider_updated_at) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(organization_id,user_id) DO UPDATE SET
				role=CASE WHEN workspace_memberships.role='owner' AND EXCLUDED.role<>'owner' THEN 'owner' ELSE EXCLUDED.role END,
				status=EXCLUDED.status,provider_updated_at=EXCLUDED.provider_updated_at,updated_at=now()
			WHERE workspace_memberships.provider_updated_at IS NULL OR workspace_memberships.provider_updated_at<=EXCLUDED.provider_updated_at`, value.Organization.ID, "clerk:"+value.PublicUserData.UserID, normalizeWorkspaceRole(value.Role), status, eventAt)
		return err
	case "user.deleted":
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &value) != nil || value.ID == "" {
			return errInvalidWebhook
		}
		userID := "clerk:" + value.ID
		_, err := tx.Exec(ctx, `INSERT INTO managed_users(user_id,status,provider_updated_at) VALUES($1,'deleted',$2) ON CONFLICT(user_id) DO UPDATE SET status='deleted',provider_updated_at=EXCLUDED.provider_updated_at,updated_at=now() WHERE managed_users.provider_updated_at IS NULL OR managed_users.provider_updated_at<=EXCLUDED.provider_updated_at`, userID, eventAt)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE workspace_memberships SET status='deleted',updated_at=now() WHERE user_id=$1`, userID)
		}
		return err
	case "user.created", "user.updated":
		var value struct {
			ID     string `json:"id"`
			Banned bool   `json:"banned"`
			Locked bool   `json:"locked"`
		}
		if json.Unmarshal(data, &value) != nil || value.ID == "" {
			return errInvalidWebhook
		}
		status := "active"
		if value.Banned || value.Locked {
			status = "revoked"
		}
		_, err := tx.Exec(ctx, `INSERT INTO managed_users(user_id,status,provider_updated_at) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET status=EXCLUDED.status,provider_updated_at=EXCLUDED.provider_updated_at,updated_at=now() WHERE managed_users.provider_updated_at IS NULL OR managed_users.provider_updated_at<=EXCLUDED.provider_updated_at`, "clerk:"+value.ID, status, eventAt)
		return err
	}
	return nil
}

var errInvalidWebhook = &webhookPayloadError{}

type webhookPayloadError struct{}

func (*webhookPayloadError) Error() string { return "invalid webhook payload" }
