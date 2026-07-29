// Package ard parses Agentic Resource Discovery catalogs into privacy-reduced
// declarations. It deliberately does not resolve or execute referenced
// artifacts.
package ard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
)

const (
	Format              = "ard"
	DefaultRefresh      = 6 * time.Hour
	MaxCatalogBytes     = 8 << 20
	MaxEntriesPerSource = 25_000
	MaxCatalogs         = 100
	MaxDepth            = 3
)

var (
	identifierPattern = regexp.MustCompile(`^urn:air:([a-z0-9](?:[a-z0-9.-]*[a-z0-9])?):([A-Za-z0-9][A-Za-z0-9._~-]*(?::[A-Za-z0-9][A-Za-z0-9._~-]*)*)$`)
	domainPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])$`)
)

type Host struct {
	DisplayName      string         `json:"displayName"`
	Identifier       string         `json:"identifier,omitempty"`
	DocumentationURL string         `json:"documentationUrl,omitempty"`
	LogoURL          string         `json:"logoUrl,omitempty"`
	TrustManifest    *TrustManifest `json:"trustManifest,omitempty"`
}

type TrustManifest struct {
	Identity     string        `json:"identity"`
	IdentityType string        `json:"identityType,omitempty"`
	Attestations []Attestation `json:"attestations,omitempty"`
	Provenance   []Provenance  `json:"provenance,omitempty"`
	Signature    string        `json:"signature,omitempty"`
}

type Attestation struct {
	Type   string `json:"type"`
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
}

type Provenance struct {
	Relation     string `json:"relation"`
	SourceID     string `json:"sourceId"`
	SourceDigest string `json:"sourceDigest,omitempty"`
}

type Entry struct {
	Identifier             string
	DisplayName            string
	MediaType              string
	Description            string
	Tags                   []string
	Capabilities           []string
	Version                string
	UpdatedAt              string
	ArtifactURL            string
	ArtifactFingerprint    string
	ProtocolIdentity       string
	RepositoryURL          string
	RepositoryPath         string
	Delivery               string
	MappedKind             string
	PublisherDomain        string
	TrustIdentity          string
	TrustIdentityType      string
	TrustIdentityAlignment string
	SignatureStatus        string
	SignatureDigest        string
	AttestationTypes       []string
	InlineCatalog          *Catalog
}

type Catalog struct {
	SpecVersion string
	Host        Host
	Entries     []Entry
}

type Result struct {
	Catalog  Catalog
	Warnings []string
}

type rawCatalog struct {
	SpecVersion string      `json:"specVersion"`
	Host        *Host       `json:"host"`
	Entries     *[]rawEntry `json:"entries"`
}

type rawEntry struct {
	Identifier            string          `json:"identifier"`
	DisplayName           string          `json:"displayName"`
	Type                  string          `json:"type"`
	URL                   *string         `json:"url"`
	Data                  json.RawMessage `json:"data"`
	Description           string          `json:"description"`
	Tags                  []string        `json:"tags"`
	Capabilities          []string        `json:"capabilities"`
	RepresentativeQueries []string        `json:"representativeQueries"`
	Version               string          `json:"version"`
	UpdatedAt             string          `json:"updatedAt"`
	Metadata              json.RawMessage `json:"metadata"`
	TrustManifest         *TrustManifest  `json:"trustManifest"`
}

func Parse(data []byte) (Result, error) {
	if len(data) == 0 || len(data) > MaxCatalogBytes {
		return Result{}, fmt.Errorf("ARD catalog must be between 1 byte and %d bytes", MaxCatalogBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw rawCatalog
	if err := decoder.Decode(&raw); err != nil {
		return Result{}, fmt.Errorf("parse ARD catalog: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Result{}, fmt.Errorf("parse ARD catalog: trailing JSON content")
	}
	if !strings.HasPrefix(strings.TrimSpace(raw.SpecVersion), "1.") {
		return Result{}, fmt.Errorf("unsupported ARD specVersion %q; expected major version 1", raw.SpecVersion)
	}
	if raw.Entries == nil {
		return Result{}, fmt.Errorf("ARD catalog entries are required")
	}
	entries := *raw.Entries
	if len(entries) > MaxEntriesPerSource {
		return Result{}, fmt.Errorf("ARD catalog contains %d entries; limit is %d", len(entries), MaxEntriesPerSource)
	}
	host := Host{}
	if raw.Host != nil {
		if strings.TrimSpace(raw.Host.DisplayName) == "" {
			return Result{}, fmt.Errorf("ARD catalog host.displayName is required when host is present")
		}
		host = sanitizeHost(*raw.Host)
	}
	result := Result{Catalog: Catalog{SpecVersion: clean(raw.SpecVersion, 32), Host: host}}
	seen := map[string]struct{}{}
	for index, item := range entries {
		entry, warning, err := reduceEntry(item)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("entry %d: %v", index+1, err))
			continue
		}
		if _, exists := seen[entry.Identifier]; exists {
			result.Warnings = append(result.Warnings, fmt.Sprintf("entry %d: duplicate identifier %s", index+1, entry.Identifier))
			continue
		}
		seen[entry.Identifier] = struct{}{}
		if warning != "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s", entry.Identifier, warning))
		}
		result.Catalog.Entries = append(result.Catalog.Entries, entry)
	}
	if len(entries) > 0 && len(result.Catalog.Entries) == 0 {
		return result, fmt.Errorf("ARD catalog contains no valid entries")
	}
	sort.Strings(result.Warnings)
	return result, nil
}

func reduceEntry(raw rawEntry) (Entry, string, error) {
	identifier := strings.TrimSpace(raw.Identifier)
	match := identifierPattern.FindStringSubmatch(identifier)
	if len(match) != 3 || !validPublisherDomain(match[1]) {
		return Entry{}, "", fmt.Errorf("identifier must use urn:air:<publisher-domain>:<resource>")
	}
	if strings.TrimSpace(raw.DisplayName) == "" {
		return Entry{}, "", fmt.Errorf("displayName is required")
	}
	mediaType, err := NormalizeMediaType(raw.Type)
	if err != nil {
		return Entry{}, "", err
	}
	hasURL := raw.URL != nil && strings.TrimSpace(*raw.URL) != ""
	hasData := len(bytes.TrimSpace(raw.Data)) > 0 && string(bytes.TrimSpace(raw.Data)) != "null"
	if hasURL == hasData {
		return Entry{}, "", fmt.Errorf("exactly one of url or data is required")
	}
	entry := Entry{
		Identifier: identifier, DisplayName: clean(raw.DisplayName, 500), MediaType: mediaType,
		Description: clean(raw.Description, 1000), Tags: cleanList(raw.Tags, 100, 128),
		Capabilities: cleanList(raw.Capabilities, 100, 128), Version: clean(raw.Version, 128),
		UpdatedAt: clean(raw.UpdatedAt, 64), PublisherDomain: strings.ToLower(match[1]),
		MappedKind: mapMediaType(mediaType), SignatureStatus: "absent", TrustIdentityAlignment: "absent",
	}
	if entry.UpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, entry.UpdatedAt); err != nil {
			return Entry{}, "", fmt.Errorf("updatedAt must be RFC3339")
		}
	}
	if hasURL {
		sanitized, err := credentialFreeHTTPS(*raw.URL)
		if err != nil {
			return Entry{}, "", fmt.Errorf("url: %w", err)
		}
		entry.Delivery = "url"
		entry.ArtifactURL = sanitized
		entry.RepositoryURL, entry.RepositoryPath = repositoryReference(sanitized)
	} else {
		entry.Delivery = "inline"
		var inline map[string]any
		if json.Unmarshal(raw.Data, &inline) != nil || inline == nil {
			return Entry{}, "", fmt.Errorf("data must be an embedded JSON object")
		}
		entry.ArtifactFingerprint = discovery.ContentHash(raw.Data)
		entry.ProtocolIdentity = inlineProtocolIdentity(mediaType, inline)
		if mediaType == "application/ai-catalog+json" {
			nested, err := Parse(raw.Data)
			if err != nil {
				return Entry{}, "", fmt.Errorf("inline catalog: %w", err)
			}
			entry.InlineCatalog = &nested.Catalog
		}
		entry.Capabilities = mergeLists(entry.Capabilities, inlineCapabilityNames(mediaType, inline))
	}
	if raw.TrustManifest != nil {
		reduceTrust(&entry, raw.TrustManifest)
	}
	warning := ""
	if entry.MappedKind == "unclassified" {
		warning = "media type is retained but not yet mapped to a Lens resource kind"
	}
	return entry, warning, nil
}

func reduceTrust(entry *Entry, manifest *TrustManifest) {
	rawIdentity := strings.TrimSpace(manifest.Identity)
	entry.TrustIdentity = safeIdentity(rawIdentity)
	entry.TrustIdentityType = clean(manifest.IdentityType, 64)
	entry.TrustIdentityAlignment = identityAlignment(entry.PublisherDomain, rawIdentity)
	if manifest.Signature != "" {
		entry.SignatureStatus = "malformed"
		parts := strings.Split(manifest.Signature, ".")
		if len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" {
			entry.SignatureStatus = "present_unverified"
		}
		entry.SignatureDigest = discovery.ContentHash([]byte(manifest.Signature))
	}
	values := make([]string, 0, len(manifest.Attestations))
	for _, attestation := range manifest.Attestations {
		if value := clean(attestation.Type, 128); value != "" {
			values = append(values, value)
		}
	}
	entry.AttestationTypes = cleanList(values, 100, 128)
}

func identityAlignment(publisher, identity string) string {
	if strings.TrimSpace(identity) == "" {
		return "absent"
	}
	parsed, err := url.Parse(identity)
	if err != nil {
		return "unresolved"
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "did" && strings.HasPrefix(identity, "did:web:") {
		host = strings.ToLower(strings.Split(strings.TrimPrefix(identity, "did:web:"), ":")[0])
	}
	if host == "" {
		return "unresolved"
	}
	if host == publisher || strings.HasSuffix(host, "."+publisher) {
		return "aligned"
	}
	return "misaligned"
}

func inlineProtocolIdentity(mediaType string, document map[string]any) string {
	for _, key := range []string{"identifier", "protocolIdentifier", "protocol_identity"} {
		value, _ := document[key].(string)
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "urn:") {
			return clean(value, 500)
		}
		if parsed, err := url.Parse(value); err == nil && parsed.IsAbs() && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
			return clean(parsed.String(), 500)
		}
	}
	if mediaType == "application/a2a-agent-card+json" || mediaType == "application/mcp-server-card+json" {
		for _, key := range []string{"url", "endpoint"} {
			if value, _ := document[key].(string); value != "" {
				if sanitized, err := credentialFreeHTTPS(value); err == nil {
					return sanitized
				}
			}
		}
	}
	if strings.Contains(mediaType, "openapi") {
		if servers, ok := document["servers"].([]any); ok {
			for _, raw := range servers {
				server, _ := raw.(map[string]any)
				if value, _ := server["url"].(string); value != "" {
					if sanitized, err := credentialFreeHTTPS(value); err == nil {
						return sanitized
					}
				}
			}
		}
	}
	return ""
}

func repositoryReference(rawURL string) (string, string) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" || parts[0] == "" || parts[1] == "" || parts[4] == "" {
		return "", ""
	}
	return "https://github.com/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), strings.Join(parts[4:], "/")
}

func sanitizeHost(host Host) Host {
	result := Host{DisplayName: clean(host.DisplayName, 500), Identifier: safeIdentity(host.Identifier)}
	if value, err := credentialFreeHTTPS(host.DocumentationURL); err == nil {
		result.DocumentationURL = value
	}
	if value, err := credentialFreeHTTPS(host.LogoURL); err == nil {
		result.LogoURL = value
	}
	return result
}

func safeIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "?#\r\n\x00") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "did":
		if strings.HasPrefix(strings.ToLower(raw), "did:") {
			return clean(raw, 500)
		}
	case "spiffe":
		if parsed.Host != "" {
			return clean(parsed.String(), 500)
		}
	case "https":
		value, err := credentialFreeHTTPS(raw)
		if err == nil {
			return clean(value, 500)
		}
	}
	return ""
}

func credentialFreeHTTPS(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.Opaque != "" {
		return "", fmt.Errorf("must be an absolute HTTPS URL")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || discovery.IsCloudMetadataHost(host) {
		return "", fmt.Errorf("must not contain credentials, query parameters, fragments, or metadata targets")
	}
	port := parsed.Port()
	if port == "" || port == "443" {
		parsed.Host = host
		if strings.Contains(host, ":") {
			parsed.Host = "[" + host + "]"
		}
	} else {
		parsed.Host = net.JoinHostPort(host, port)
	}
	parsed.Scheme = "https"
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

// ValidateCatalogURL applies the same credential and metadata-safe URL policy
// used by the network provider without performing a request.
func ValidateCatalogURL(raw string) (string, error) {
	return credentialFreeHTTPS(raw)
}

func ValidIdentifier(identifier string) bool {
	match := identifierPattern.FindStringSubmatch(strings.TrimSpace(identifier))
	return len(match) == 3 && validPublisherDomain(match[1])
}

func ValidPublisherDomain(domain string) bool {
	return validPublisherDomain(strings.ToLower(strings.TrimSpace(domain)))
}

// NormalizeMediaType accepts current and future syntactically valid media
// types without requiring Lens to understand the referenced artifact.
func NormalizeMediaType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", fmt.Errorf("type is required")
	}
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil || !strings.Contains(parsed, "/") {
		return "", fmt.Errorf("type must be a valid media type")
	}
	return parsed, nil
}

func validPublisherDomain(domain string) bool {
	if len(domain) > 253 || !strings.Contains(domain, ".") || !domainPattern.MatchString(domain) {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func mapMediaType(mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "application/a2a-agent-card+json":
		return "agent"
	case "application/mcp-server-card+json":
		return "mcp_server"
	case "application/ai-skill", "application/ai-skill+json", "application/ai-skill+md":
		return "skill"
	case "application/openapi+json", "application/openapi+yaml", "application/vnd.oai.openapi+json", "application/vnd.oai.openapi":
		return "api_service"
	case "application/arazzo+json", "application/arazzo+yaml":
		return "workflow"
	case "application/ai-catalog+json":
		return "catalog"
	case "application/ai-registry", "application/ai-registry+json":
		return "registry"
	default:
		return "unclassified"
	}
}

func inlineCapabilityNames(mediaType string, document map[string]any) []string {
	result := []string{}
	for _, key := range []string{"tools", "skills", "capabilities"} {
		switch values := document[key].(type) {
		case []any:
			for _, raw := range values {
				if item, ok := raw.(map[string]any); ok {
					for _, nameKey := range []string{"name", "id"} {
						if name, ok := item[nameKey].(string); ok && strings.TrimSpace(name) != "" {
							result = append(result, name)
							break
						}
					}
				}
			}
		case map[string]any:
			for name := range values {
				result = append(result, name)
			}
		}
	}
	if strings.Contains(mediaType, "openapi") {
		if paths, ok := document["paths"].(map[string]any); ok {
			for path, rawPath := range paths {
				operations, _ := rawPath.(map[string]any)
				for method, rawOperation := range operations {
					if !isHTTPMethod(method) {
						continue
					}
					operation, _ := rawOperation.(map[string]any)
					name, _ := operation["operationId"].(string)
					if strings.TrimSpace(name) == "" {
						name = strings.ToUpper(method) + " " + path
					}
					result = append(result, name)
				}
			}
		}
	}
	if strings.Contains(mediaType, "arazzo") {
		if workflows, ok := document["workflows"].([]any); ok {
			for _, raw := range workflows {
				workflow, _ := raw.(map[string]any)
				if name, _ := workflow["workflowId"].(string); strings.TrimSpace(name) != "" {
					result = append(result, name)
				}
			}
		}
	}
	return cleanList(result, 100, 128)
}

func isHTTPMethod(value string) bool {
	switch strings.ToLower(value) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	default:
		return false
	}
}

func clean(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if utf8.RuneCountInString(value) > limit {
		value = string([]rune(value)[:limit])
	}
	return value
}

func cleanList(values []string, maximum, length int) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = clean(value, length)
		key := strings.ToLower(value)
		if value == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	sort.Strings(result)
	return result
}

func mergeLists(left, right []string) []string {
	return cleanList(append(append([]string{}, left...), right...), 100, 128)
}
