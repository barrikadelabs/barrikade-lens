package hub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestWorkspaceRolePermissions(t *testing.T) {
	tests := []struct {
		role    string
		allowed []string
		denied  []string
	}{
		{role: "owner", allowed: []string{"inventory:read", "environment:manage", "scan:run", "workspace:delete", "members:manage", "admin:webhooks"}},
		{role: "admin", allowed: []string{"inventory:read", "environment:manage", "scan:run", "context:write"}, denied: []string{"workspace:delete", "members:manage", "admin:webhooks"}},
		{role: "viewer", allowed: []string{"inventory:read", "environment:read", "jobs:read"}, denied: []string{"environment:manage", "scan:run", "workspace:delete", "members:manage"}},
	}
	for _, test := range tests {
		t.Run(test.role, func(t *testing.T) {
			scopes := scopesForWorkspaceRole(test.role)
			for _, scope := range test.allowed {
				if !scopes[scope] {
					t.Fatalf("%s should allow %s", test.role, scope)
				}
			}
			for _, scope := range test.denied {
				if scopes[scope] {
					t.Fatalf("%s should deny %s", test.role, scope)
				}
			}
		})
	}
}

func TestClerkV2OrganizationPermissionsDecodeCompactClaim(t *testing.T) {
	var claims clerkSessionClaims
	if err := json.Unmarshal([]byte(`{"o":{"id":"org_1","rol":"org:admin","per":"environment:manage,scan:run"}}`), &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Organization == nil || claims.Organization.ID != "org_1" || len(claims.Organization.Permissions) != 2 {
		t.Fatalf("unexpected Clerk organization claims: %+v", claims.Organization)
	}
	if got := normalizeWorkspaceRole(claims.Organization.Role); got != "admin" {
		t.Fatalf("expected admin role, got %s", got)
	}
}

func TestClerkWebhookSignatureVerification(t *testing.T) {
	secret := []byte("webhook-signing-secret")
	payload := "event.123.{}"
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !validSvixSignature(secret, payload, signature) {
		t.Fatal("valid Clerk signature was rejected")
	}
	if validSvixSignature(secret, payload+"tampered", signature) {
		t.Fatal("tampered Clerk payload was accepted")
	}
}
