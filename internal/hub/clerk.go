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
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
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
	if err = applyClerkLifecycleEvent(r.Context(), tx, envelope.Type, envelope.Data); err != nil {
		writeError(w, 422, "invalid_webhook", "Clerk lifecycle payload could not be applied")
		return
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
	switch eventType {
	case "organization.deleted":
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &value) != nil || value.ID == "" {
			return errInvalidWebhook
		}
		_, err := tx.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, value.ID)
		return err
	case "organizationMembership.created", "organizationMembership.updated", "organizationMembership.deleted":
		var value struct {
			Role         string `json:"role"`
			Organization struct {
				ID string `json:"id"`
			} `json:"organization"`
			PublicUserData struct {
				UserID string `json:"user_id"`
			} `json:"public_user_data"`
		}
		if json.Unmarshal(data, &value) != nil || value.Organization.ID == "" || value.PublicUserData.UserID == "" {
			return errInvalidWebhook
		}
		status := "active"
		if eventType == "organizationMembership.deleted" {
			status = "revoked"
		}
		_, err := tx.Exec(ctx, `INSERT INTO workspace_memberships(organization_id,user_id,role,status) VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,user_id) DO UPDATE SET role=EXCLUDED.role,status=EXCLUDED.status,updated_at=now()`, value.Organization.ID, "clerk:"+value.PublicUserData.UserID, normalizeWorkspaceRole(value.Role), status)
		return err
	case "user.deleted":
		var value struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &value) != nil || value.ID == "" {
			return errInvalidWebhook
		}
		userID := "clerk:" + value.ID
		_, err := tx.Exec(ctx, `INSERT INTO managed_users(user_id,status) VALUES($1,'deleted') ON CONFLICT(user_id) DO UPDATE SET status='deleted',updated_at=now()`, userID)
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
		_, err := tx.Exec(ctx, `INSERT INTO managed_users(user_id,status) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET status=EXCLUDED.status,updated_at=now()`, "clerk:"+value.ID, status)
		return err
	}
	return nil
}

var errInvalidWebhook = &webhookPayloadError{}

type webhookPayloadError struct{}

func (*webhookPayloadError) Error() string { return "invalid webhook payload" }
