package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Principal struct {
	OrganizationID string
	SourceID       string
	Subject        string
	Scopes         map[string]bool
	Admin          bool
	Role           string
}
type principalKey struct{}

type Authenticator struct {
	Pool                  *pgxpool.Pool
	JWTSecret             []byte
	DevAdminToken         string
	DefaultOrganizationID string
	Issuer                string
	ClerkVerifier         *oidc.IDTokenVerifier
	ClerkAuthorizedParty  string
}

type collectorClaims struct {
	OrganizationID string   `json:"org"`
	SourceID       string   `json:"source"`
	Scopes         []string `json:"scopes"`
	TokenType      string   `json:"token_type"`
	Admin          bool     `json:"admin,omitempty"`
	Role           string   `json:"role,omitempty"`
	jwt.RegisteredClaims
}

type clerkOrganizationClaims struct {
	ID          string          `json:"id"`
	Role        string          `json:"rol"`
	Permissions claimStringList `json:"per"`
}

type clerkSessionClaims struct {
	AuthorizedParty         string                   `json:"azp"`
	Status                  string                   `json:"sts"`
	NotBefore               int64                    `json:"nbf"`
	OrganizationID          string                   `json:"org_id"`
	OrganizationRole        string                   `json:"org_role"`
	OrganizationPermissions claimStringList          `json:"org_permissions"`
	Organization            *clerkOrganizationClaims `json:"o"`
}

type claimStringList []string

func (s *claimStringList) UnmarshalJSON(value []byte) error {
	var list []string
	if err := json.Unmarshal(value, &list); err == nil {
		*s = list
		return nil
	}
	var compact string
	if err := json.Unmarshal(value, &compact); err != nil {
		return err
	}
	if compact == "" {
		*s = nil
	} else {
		*s = strings.Split(compact, ",")
	}
	return nil
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(writer, http.StatusUnauthorized, "authentication_required", "A bearer token is required")
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if a.DevAdminToken != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(a.DevAdminToken)) == 1 {
			principal := Principal{OrganizationID: a.DefaultOrganizationID, Subject: "local-bootstrap-admin", Admin: true, Role: "owner", Scopes: map[string]bool{"*": true}}
			next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalKey{}, principal)))
			return
		}
		claims := collectorClaims{}
		token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return a.JWTSecret, nil
		}, jwt.WithIssuer(a.Issuer), jwt.WithExpirationRequired())
		if err != nil || !token.Valid || claims.OrganizationID == "" || (claims.TokenType != "human" && claims.SourceID == "") {
			if a.ClerkVerifier != nil {
				if principal, clerkErr := a.authenticateClerk(request.Context(), raw); clerkErr == nil {
					next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalKey{}, principal)))
					return
				}
			}
			principal, serviceErr := a.authenticateServiceAccount(request.Context(), raw)
			if serviceErr != nil {
				writeError(writer, http.StatusUnauthorized, "invalid_token", "The bearer token is invalid, expired, or has no active workspace")
				return
			}
			next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalKey{}, principal)))
			return
		}
		scopes := map[string]bool{}
		for _, scope := range claims.Scopes {
			scopes[scope] = true
		}
		role := claims.Role
		if role == "" && claims.Admin {
			role = "owner"
		}
		if claims.SourceID != "" {
			var active bool
			err = a.Pool.QueryRow(request.Context(), `SELECT revoked_at IS NULL FROM sources WHERE organization_id=$1 AND id=$2`, claims.OrganizationID, claims.SourceID).Scan(&active)
			if err != nil || !active {
				writeError(writer, http.StatusUnauthorized, "source_revoked", "The collector source is no longer active")
				return
			}
		}
		principal := Principal{OrganizationID: claims.OrganizationID, SourceID: claims.SourceID, Subject: claims.Subject, Scopes: scopes, Admin: claims.Admin, Role: role}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalKey{}, principal)))
	})
}

func (a *Authenticator) authenticateClerk(ctx context.Context, raw string) (Principal, error) {
	verified, err := a.ClerkVerifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, err
	}
	var claims clerkSessionClaims
	if err := verified.Claims(&claims); err != nil {
		return Principal{}, err
	}
	if a.ClerkAuthorizedParty != "" && claims.AuthorizedParty != a.ClerkAuthorizedParty {
		return Principal{}, fmt.Errorf("unexpected authorized party")
	}
	if claims.Status != "" && claims.Status != "active" {
		return Principal{}, fmt.Errorf("Clerk session is not active")
	}
	if claims.NotBefore > 0 && time.Now().Add(30*time.Second).Unix() < claims.NotBefore {
		return Principal{}, fmt.Errorf("Clerk session is not active yet")
	}
	organizationID, role, permissions := claims.OrganizationID, claims.OrganizationRole, claims.OrganizationPermissions
	if claims.Organization != nil {
		if organizationID == "" {
			organizationID = claims.Organization.ID
		}
		if role == "" {
			role = claims.Organization.Role
		}
		if len(permissions) == 0 {
			permissions = claims.Organization.Permissions
		}
	}
	if organizationID == "" || verified.Subject == "" {
		return Principal{}, fmt.Errorf("active Clerk organization is required")
	}
	var accountStatus string
	err = a.Pool.QueryRow(ctx, `SELECT status FROM managed_users WHERE user_id=$1`, "clerk:"+verified.Subject).Scan(&accountStatus)
	if err == nil && accountStatus != "active" {
		return Principal{}, fmt.Errorf("managed account is not active")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, fmt.Errorf("validate managed account: %w", err)
	}
	var membershipRole, membershipStatus string
	err = a.Pool.QueryRow(ctx, `SELECT role,status FROM workspace_memberships WHERE organization_id=$1 AND user_id=$2`, organizationID, "clerk:"+verified.Subject).Scan(&membershipRole, &membershipStatus)
	if err == nil && membershipStatus != "active" {
		return Principal{}, fmt.Errorf("workspace membership is not active")
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, fmt.Errorf("validate workspace membership: %w", err)
	}
	if err == nil {
		role = membershipRole
	}
	role = normalizeWorkspaceRole(role)
	scopes := scopesForWorkspaceRole(role)
	_ = permissions // Clerk permissions are identity-provider context; Lens roles authorize application access.
	return Principal{OrganizationID: organizationID, Subject: "clerk:" + verified.Subject, Role: role, Admin: role == "owner" || role == "admin", Scopes: scopes}, nil
}

func normalizeWorkspaceRole(value string) string {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "org:")
	switch value {
	case "owner", "admin", "viewer":
		return value
	case "member":
		return "viewer"
	default:
		return "viewer"
	}
}

func scopesForWorkspaceRole(role string) map[string]bool {
	result := map[string]bool{"inventory:read": true, "environment:read": true, "jobs:read": true}
	if slices.Contains([]string{"owner", "admin"}, role) {
		for _, scope := range []string{"environment:manage", "scan:run", "admin:enrollment", "admin:coverage", "context:write"} {
			result[scope] = true
		}
	}
	if role == "owner" {
		for _, scope := range []string{"workspace:delete", "members:manage", "admin:webhooks", "admin:service_accounts"} {
			result[scope] = true
		}
	}
	return result
}

func principalFrom(ctx context.Context) (Principal, bool) {
	value, ok := ctx.Value(principalKey{}).(Principal)
	return value, ok
}

func requireScope(request *http.Request, scope string) (Principal, error) {
	principal, ok := principalFrom(request.Context())
	if !ok {
		return Principal{}, fmt.Errorf("missing principal")
	}
	if !principal.Scopes["*"] && !principal.Scopes[scope] {
		return Principal{}, fmt.Errorf("scope %s is required", scope)
	}
	return principal, nil
}

func (a *Authenticator) issueAccessToken(orgID, sourceID string, scopes []string) (string, time.Time, error) {
	now := time.Now().UTC()
	expires := now.Add(15 * time.Minute)
	claims := collectorClaims{OrganizationID: orgID, SourceID: sourceID, Scopes: scopes, TokenType: "collector", RegisteredClaims: jwt.RegisteredClaims{Issuer: a.Issuer, Subject: "collector:" + sourceID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires), ID: uuid.NewString()}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.JWTSecret)
	return raw, expires, err
}

func (a *Authenticator) issueHumanToken(orgID, subject string, admin bool) (string, time.Time, error) {
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	scopes := []string{"inventory:read"}
	if admin {
		scopes = append(scopes, "admin:enrollment", "admin:webhooks", "admin:coverage", "admin:service_accounts", "context:write", "environment:read", "environment:manage", "scan:run", "workspace:delete", "members:manage", "jobs:read")
	}
	role := "viewer"
	if admin {
		role = "owner"
	}
	claims := collectorClaims{OrganizationID: orgID, Scopes: scopes, TokenType: "human", Admin: admin, Role: role, RegisteredClaims: jwt.RegisteredClaims{Issuer: a.Issuer, Subject: subject, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires), ID: uuid.NewString()}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.JWTSecret)
	return raw, expires, err
}

func (a *Authenticator) authenticateServiceAccount(ctx context.Context, raw string) (Principal, error) {
	if a.Pool == nil {
		return Principal{}, fmt.Errorf("service accounts are unavailable")
	}
	var id, organizationID, name string
	var scopes []string
	err := a.Pool.QueryRow(ctx, `UPDATE service_accounts SET last_used_at=now() WHERE token_hash=$1 AND revoked_at IS NULL RETURNING id::text,organization_id,name,scopes`, tokenHash(raw)).Scan(&id, &organizationID, &name, &scopes)
	if err != nil {
		return Principal{}, err
	}
	granted := map[string]bool{}
	for _, scope := range scopes {
		granted[scope] = true
	}
	return Principal{OrganizationID: organizationID, Subject: "service-account:" + id + ":" + name, Scopes: granted}, nil
}

func (a *Authenticator) issueRefreshToken(ctx context.Context, orgID, sourceID string, scopes []string) (string, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = a.Pool.Exec(ctx, `INSERT INTO collector_refresh_tokens(token_hash,organization_id,source_id,scopes,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenHash(raw), orgID, sourceID, scopes, time.Now().UTC().Add(90*24*time.Hour))
	return raw, err
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func tokenHash(raw string) []byte { sum := sha256.Sum256([]byte(raw)); return sum[:] }
