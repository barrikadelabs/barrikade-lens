package hubclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	lensconfig "github.com/barrikadelabs/barrikade-lens/internal/config"
	"github.com/barrikadelabs/barrikade-lens/internal/identity"
	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Client struct {
	HTTP    *http.Client
	Version string
}

type EnrollmentRequest struct {
	Code             string `json:"code"`
	Hostname         string `json:"hostname"`
	Platform         string `json:"platform"`
	Architecture     string `json:"architecture"`
	CollectorVersion string `json:"collector_version"`
	IdentityPublicKey string `json:"identity_public_key"`
	IdentityProof     string `json:"identity_proof"`
}
type EnrollmentResponse struct {
	HubURL               string `json:"hub_url"`
	OrganizationID       string `json:"organization_id"`
	SourceID             string `json:"source_id"`
	TargetID             string `json:"target_id"`
	Sequence             uint64 `json:"sequence"`
	AccessToken          string `json:"access_token"`
	AccessTokenExpiresAt string `json:"access_token_expires_at"`
	RefreshToken         string `json:"refresh_token"`
}
type IngestionJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func New(version string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Version: version}
}

func (c *Client) Enroll(ctx context.Context, hubURL, code, configPath string) (lensconfig.Config, error) {
	base, err := validateHubURL(hubURL)
	if err != nil {
		return lensconfig.Config{}, err
	}
	hostname, _ := os.Hostname()
	if configPath == "" {
		configPath, err = lensconfig.Path()
		if err != nil {
			return lensconfig.Config{}, err
		}
	}
	state, err := identity.LoadOrCreate(identity.Path(filepath.Clean(configPath)), base)
	if err != nil {
		return lensconfig.Config{}, err
	}
	proof, err := state.Sign(code, hostname, runtime.GOOS, runtime.GOARCH, c.Version)
	if err != nil {
		return lensconfig.Config{}, err
	}
	request := EnrollmentRequest{Code: strings.TrimSpace(code), Hostname: hostname, Platform: runtime.GOOS, Architecture: runtime.GOARCH, CollectorVersion: c.Version, IdentityPublicKey: state.PublicKey, IdentityProof: proof}
	var response EnrollmentResponse
	if err := c.doJSON(ctx, http.MethodPost, base+"/v1/enrollment/exchange", "", request, &response); err != nil {
		return lensconfig.Config{}, err
	}
	if response.OrganizationID == "" || response.SourceID == "" || response.TargetID == "" || response.RefreshToken == "" {
		return lensconfig.Config{}, fmt.Errorf("Hub returned an incomplete enrollment response")
	}
	if response.HubURL == "" {
		response.HubURL = base
	}
	return lensconfig.Config{ConfigVersion: 2, HubURL: response.HubURL, OrganizationID: response.OrganizationID, SourceID: response.SourceID, TargetID: response.TargetID, AccessToken: response.AccessToken, AccessTokenExpiresAt: response.AccessTokenExpiresAt, RefreshToken: response.RefreshToken, Sequence: response.Sequence}, nil
}

func (c *Client) Upload(ctx context.Context, configPath string, cfg *lensconfig.Config, snapshot discovery.Snapshot) (IngestionJob, error) {
	base, err := validateHubURL(cfg.HubURL)
	if err != nil {
		return IngestionJob{}, err
	}
	var job IngestionJob
	err = c.doJSON(ctx, http.MethodPost, base+"/v1/discovery/snapshots", cfg.AccessToken, snapshot, &job)
	if unauthorized, ok := err.(httpError); ok && unauthorized.status == http.StatusUnauthorized && cfg.RefreshToken != "" {
		if refreshErr := c.refresh(ctx, base, configPath, cfg); refreshErr != nil {
			return IngestionJob{}, refreshErr
		}
		err = c.doJSON(ctx, http.MethodPost, base+"/v1/discovery/snapshots", cfg.AccessToken, snapshot, &job)
	}
	return job, err
}

func (c *Client) refresh(ctx context.Context, base, configPath string, cfg *lensconfig.Config) error {
	var response struct {
		AccessToken          string `json:"access_token"`
		AccessTokenExpiresAt string `json:"access_token_expires_at"`
		RefreshToken         string `json:"refresh_token"`
	}
	if err := c.doJSON(ctx, http.MethodPost, base+"/v1/collector/token", "", map[string]string{"refresh_token": cfg.RefreshToken}, &response); err != nil {
		return err
	}
	if response.AccessToken == "" || response.RefreshToken == "" {
		return fmt.Errorf("Hub returned incomplete rotated credentials")
	}
	cfg.AccessToken, cfg.AccessTokenExpiresAt, cfg.RefreshToken = response.AccessToken, response.AccessTokenExpiresAt, response.RefreshToken
	return lensconfig.Save(configPath, *cfg)
}

func (c *Client) doJSON(ctx context.Context, method, endpoint, bearer string, body, response any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "barrikade-lens/"+c.Version)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	httpResponse, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return httpError{status: httpResponse.StatusCode}
	}
	if response == nil {
		return nil
	}
	decoder := json.NewDecoder(httpResponse.Body)
	if err := decoder.Decode(response); err != nil {
		return fmt.Errorf("decode Hub response: %w", err)
	}
	return nil
}

type httpError struct{ status int }

func (e httpError) Error() string { return fmt.Sprintf("Hub request failed with HTTP %d", e.status) }

func validateHubURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid Hub URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("Hub URL must not contain credentials, a query, or a fragment")
	}
	if u.Scheme != "https" {
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if u.Scheme != "http" || !(host == "localhost" || ip != nil && ip.IsLoopback()) {
			return "", fmt.Errorf("Hub URL must use HTTPS (HTTP is accepted only for loopback quickstarts)")
		}
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}
