package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

type Config struct {
	AllowedHosts []string
	Timeout      time.Duration
	MaxBytes     int64
}
type Result struct {
	Kind        discovery.EntityKind
	Name        string
	Endpoint    string
	Host        string
	ContentHash string
	Attributes  map[string]any
}

func Handshake(ctx context.Context, raw string, config Config) (Result, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Result{}, fmt.Errorf("probe target must be an absolute HTTP URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Result{}, fmt.Errorf("credential-bearing or parameterized probe URLs are blocked")
	}
	host := strings.ToLower(parsed.Hostname())
	if discovery.IsCloudMetadataHost(host) {
		return Result{}, fmt.Errorf("cloud metadata targets are blocked")
	}
	if !allowed(host, config.AllowedHosts) {
		return Result{}, fmt.Errorf("probe host is not allowlisted")
	}
	if config.Timeout <= 0 || config.Timeout > 5*time.Second {
		config.Timeout = 3 * time.Second
	}
	if config.MaxBytes <= 0 || config.MaxBytes > 2<<20 {
		config.MaxBytes = 1 << 20
	}
	client, err := clientFor(ctx, parsed, config.Timeout)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "barrikade-lens/probe")
	response, err := client.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("metadata handshake returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, config.MaxBytes+1))
	if err != nil {
		return Result{}, err
	}
	if int64(len(data)) > config.MaxBytes {
		return Result{}, fmt.Errorf("metadata response exceeded size limit")
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return Result{}, fmt.Errorf("metadata response is not JSON")
	}
	sanitized, _ := discovery.SanitizeURL(parsed.String())
	result := Result{Kind: discovery.KindAPIService, Name: host, Endpoint: sanitized, Host: host, ContentHash: discovery.ContentHash(data), Attributes: map[string]any{"running_at_scan": true, "transport": "http"}}
	if version, ok := document["openapi"].(string); ok {
		result.Kind = discovery.KindAPIService
		result.Attributes["openapi_version"] = version
		if info, ok := document["info"].(map[string]any); ok {
			if title, ok := info["title"].(string); ok && len(title) <= 500 {
				result.Name = title
			}
		}
	} else if version, ok := document["arazzo"].(string); ok && strings.HasPrefix(version, "1.") {
		result.Kind = discovery.KindWorkflow
		result.Attributes["arazzo_version"] = version
	} else if _, hasProtocol := document["protocolVersion"]; hasProtocol {
		result.Kind = discovery.KindMCPServer
		if name := safeName(document); name != "" {
			result.Name = name
		}
	} else if _, hasCapabilities := document["capabilities"]; hasCapabilities {
		result.Kind = discovery.KindAgent
		if name := safeName(document); name != "" {
			result.Name = name
		}
	} else if _, hasModels := document["data"]; hasModels {
		result.Kind = discovery.KindModelServer
		result.Attributes["model_count"] = arrayLength(document["data"])
	}
	result.Attributes["endpoint"] = result.Endpoint
	result.Attributes["host"] = host
	return result, nil
}

func Apply(snapshot *discovery.Snapshot, result Result) {
	evidenceID := discovery.EvidenceID(snapshot.SourceID, "active.metadata", "api_document", result.Endpoint, result.ContentHash)
	id := discovery.StableID(snapshot.OrganizationID, result.Kind, "active:"+result.Endpoint)
	for _, entity := range snapshot.Entities {
		if entity.ID == id {
			return
		}
	}
	snapshot.Evidence = append(snapshot.Evidence, discovery.Evidence{ID: evidenceID, DetectorID: "active.metadata", DetectorVersion: "1", Method: "api_document", Family: "active_handshake", Specificity: "high", Locator: result.Endpoint, ContentHash: result.ContentHash, ObservedAt: snapshot.ObservedAt})
	snapshot.Entities = append(snapshot.Entities, discovery.Entity{ID: id, Kind: result.Kind, CanonicalKey: "active:" + result.Endpoint, Name: result.Name, Attributes: result.Attributes, Confidence: discovery.ConfidenceConfirmed, EvidenceRefs: []string{evidenceID}, Provenance: []string{"active-metadata-handshake"}})
	snapshot.Normalize()
}

func clientFor(ctx context.Context, target *url.URL, timeout time.Duration) (*http.Client, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, target.Hostname())
	if err != nil {
		return nil, err
	}
	allowedIPs := []net.IP{}
	for _, address := range addresses {
		if discovery.IsCloudMetadataHost(address.IP.String()) || address.IP.IsLinkLocalUnicast() || address.IP.IsUnspecified() {
			continue
		}
		allowedIPs = append(allowedIPs, address.IP)
	}
	if len(allowedIPs) == 0 {
		return nil, fmt.Errorf("target resolves only to blocked addresses")
	}
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: target.Hostname()}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, ip := range allowedIPs {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
			last = err
		}
		return nil, last
	}}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func allowed(host string, allowlist []string) bool {
	for _, item := range allowlist {
		item = strings.ToLower(strings.TrimSpace(item))
		if host == item {
			return true
		}
		if strings.HasPrefix(item, "*.") && strings.HasSuffix(host, strings.TrimPrefix(item, "*")) {
			return true
		}
	}
	return false
}
func safeName(document map[string]any) string {
	if name, ok := document["name"].(string); ok && len(name) <= 500 {
		return strings.TrimSpace(name)
	}
	if info, ok := document["serverInfo"].(map[string]any); ok {
		if name, ok := info["name"].(string); ok && len(name) <= 500 {
			return strings.TrimSpace(name)
		}
	}
	return ""
}
func arrayLength(value any) int {
	if list, ok := value.([]any); ok {
		return len(list)
	}
	return 0
}
