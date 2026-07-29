package hub

import (
	"net/http"
	"slices"
	"time"

	"golang.org/x/oauth2"
)

func (s *Server) oidcConfig(w http.ResponseWriter, r *http.Request) {
	response := map[string]any{"enabled": s.oidcProvider != nil, "development_bootstrap": s.config.DevAdminToken != "", "ard_enabled": !s.config.ARDDisabled}
	if s.oidcProvider != nil {
		response["authorization_endpoint"] = s.oauthConfig.Endpoint.AuthURL
		response["client_id"] = s.oauthConfig.ClientID
		response["redirect_uri"] = s.oauthConfig.RedirectURL
		response["scopes"] = s.oauthConfig.Scopes
	}
	writeJSON(w, 200, response)
}

func (s *Server) oidcExchange(w http.ResponseWriter, r *http.Request) {
	if s.oidcProvider == nil {
		writeError(w, 404, "oidc_not_configured", "OIDC is not configured")
		return
	}
	var request struct {
		Code         string `json:"code"`
		RedirectURI  string `json:"redirect_uri"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		return
	}
	if request.Code == "" || len(request.CodeVerifier) < 43 || request.RedirectURI != s.oauthConfig.RedirectURL {
		writeError(w, 400, "invalid_oidc_exchange", "Code, matching redirect URI, and PKCE verifier are required")
		return
	}
	config := s.oauthConfig
	config.RedirectURL = request.RedirectURI
	token, err := config.Exchange(r.Context(), request.Code, oauth2.SetAuthURLParam("code_verifier", request.CodeVerifier))
	if err != nil {
		writeError(w, 401, "oidc_exchange_failed", "The authorization code could not be exchanged")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		writeError(w, 401, "missing_id_token", "The identity provider did not return an ID token")
		return
	}
	idToken, err := s.oidcVerifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		writeError(w, 401, "invalid_id_token", "The identity token could not be verified")
		return
	}
	var claims struct {
		Subject string   `json:"sub"`
		Email   string   `json:"email"`
		Groups  []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" {
		writeError(w, 401, "invalid_id_token", "The identity token has no subject")
		return
	}
	admin := s.config.OIDCAdminGroup != "" && slices.Contains(claims.Groups, s.config.OIDCAdminGroup)
	access, expires, err := s.auth.issueHumanToken(s.config.DefaultOrganizationID, "oidc:"+claims.Subject, admin)
	if err != nil {
		writeError(w, 500, "internal_error", "Could not create Lens session")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "expires_at": expires.Format(time.RFC3339), "admin": admin})
}
