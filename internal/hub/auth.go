package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Principal struct {
	OrganizationID string
	SourceID       string
	Subject        string
	Scopes         map[string]bool
	Admin          bool
}
type principalKey struct{}

type Authenticator struct {
	Pool                  *pgxpool.Pool
	JWTSecret             []byte
	DevAdminToken         string
	DefaultOrganizationID string
	Issuer                string
}

type collectorClaims struct {
	OrganizationID string   `json:"org"`
	SourceID       string   `json:"source"`
	Scopes         []string `json:"scopes"`
	TokenType      string   `json:"token_type"`
	Admin          bool     `json:"admin,omitempty"`
	jwt.RegisteredClaims
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
			principal := Principal{OrganizationID: a.DefaultOrganizationID, Subject: "local-bootstrap-admin", Admin: true, Scopes: map[string]bool{"*": true}}
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
			writeError(writer, http.StatusUnauthorized, "invalid_token", "The collector token is invalid or expired")
			return
		}
		scopes := map[string]bool{}
		for _, scope := range claims.Scopes {
			scopes[scope] = true
		}
		principal := Principal{OrganizationID: claims.OrganizationID, SourceID: claims.SourceID, Subject: claims.Subject, Scopes: scopes, Admin: claims.Admin}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalKey{}, principal)))
	})
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
	if !principal.Admin && !principal.Scopes[scope] {
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
		scopes = append(scopes, "admin:enrollment", "admin:webhooks", "admin:coverage")
	}
	claims := collectorClaims{OrganizationID: orgID, Scopes: scopes, TokenType: "human", Admin: admin, RegisteredClaims: jwt.RegisteredClaims{Issuer: a.Issuer, Subject: subject, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires), ID: uuid.NewString()}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.JWTSecret)
	return raw, expires, err
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
