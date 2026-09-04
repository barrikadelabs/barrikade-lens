package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"gopkg.in/yaml.v3"
)

const PublicCatalogManifest = "https://raw.githubusercontent.com/jentic/jentic-public-apis/refs/heads/main/apis/openapi/apis.json"

type State struct {
	ETag         string
	Modified     time.Time
	SourceCommit string
}
type Index struct {
	Name         string
	Entries      []Entry
	ETag         string
	Modified     time.Time
	SourceCommit string
	NotModified  bool
}
type Entry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
	APIFamily  string `json:"api_family,omitempty"`
	Version    string `json:"version,omitempty"`
	Reference  string `json:"reference"`
	MatchHost  string `json:"-"`
}
type Match struct {
	Entry      Entry
	Confidence string
	Exact      bool
	Reason     string
}
type Document struct {
	API       API
	OpenAPI   map[string]any
	Arazzo    []map[string]any
	ETag      string
	SourceSHA string
}
type API struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	BaseURL          string   `json:"base_url,omitempty"`
	Host             string   `json:"host,omitempty"`
	Version          string   `json:"version,omitempty"`
	OpenAPIReference string   `json:"openapi_reference,omitempty"`
	ArazzoReferences []string `json:"arazzo_references,omitempty"`
}

type Provider interface {
	ID() string
	DisplayName() string
	Refresh(context.Context, State) (Index, error)
	Match(Index, string, string) []Match
	Fetch(context.Context, Entry, State) (Document, error)
}

type OAKProvider struct {
	ProviderID  string
	Name        string
	ManifestURL string
	Client      *http.Client
}

func (p *OAKProvider) ID() string {
	if p.ProviderID != "" {
		return p.ProviderID
	}
	return "public-api-catalog"
}
func (p *OAKProvider) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return "Public API Catalog"
}

type oakIndex struct {
	Name    string `json:"name"`
	Include []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"include"`
}
type oakAPI struct {
	AID         string `json:"aid"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BaseURL     string `json:"baseURL"`
	Version     string `json:"version"`
	Properties  []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"properties"`
}

type oakDocument struct {
	AID         string   `json:"aid"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	APIs        []oakAPI `json:"apis"`
}

func (p *OAKProvider) Refresh(ctx context.Context, state State) (Index, error) {
	manifest := p.ManifestURL
	if manifest == "" {
		manifest = PublicCatalogManifest
	}
	data, headers, status, err := p.fetchHTTP(ctx, manifest, state)
	if err != nil {
		return Index{}, err
	}
	if status == http.StatusNotModified {
		return Index{ETag: state.ETag, Modified: state.Modified, SourceCommit: state.SourceCommit, NotModified: true}, nil
	}
	var parsed oakIndex
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Index{}, fmt.Errorf("parse OAK manifest: %w", err)
	}
	index := Index{Name: parsed.Name, ETag: headers.Get("ETag"), Modified: parseHTTPTime(headers.Get("Last-Modified"))}
	for _, include := range parsed.Include {
		providerID := providerFromReference(include.URL, include.Name)
		if providerID == "" {
			continue
		}
		family, version := entryDescriptor(include.Name)
		index.Entries = append(index.Entries, Entry{ID: entryID(include.URL), Name: include.Name, ProviderID: providerID, APIFamily: family, Version: version, Reference: include.URL})
	}
	return index, nil
}

func (p *OAKProvider) Match(index Index, host, providerIdentifier string) []Match {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	providerIdentifier = strings.ToLower(strings.TrimSpace(providerIdentifier))
	matches := []Match{}
	for _, entry := range index.Entries {
		provider := strings.ToLower(entry.ProviderID)
		match := Match{Entry: entry}
		switch {
		case host != "" && host == provider:
			match.Confidence = "possible"
			match.Reason = "provider host suggestion"
		case host != "" && strings.HasSuffix(host, "."+provider):
			match.Confidence = "possible"
			match.Reason = "provider domain"
		case providerIdentifier != "" && (providerIdentifier == strings.ToLower(entry.ID) || providerIdentifier == strings.ToLower(entry.APIFamily) || providerIdentifier == strings.ToLower(entry.APIFamily+"@"+entry.Version)):
			match.Confidence = "possible"
			match.Reason = "API family suggestion"
		case providerIdentifier != "" && strings.Contains(strings.ToLower(entry.Name), providerIdentifier):
			match.Confidence = "possible"
			match.Reason = "catalog name suggestion"
		default:
			continue
		}
		match.Entry.MatchHost = host
		matches = append(matches, match)
	}
	// A provider-domain match is safe to auto-link only when it identifies one
	// catalogue entry. Provider umbrellas and multi-version APIs stay suggestions.
	if len(matches) == 1 && (matches[0].Reason == "provider host suggestion" || matches[0].Reason == "provider domain" || matches[0].Reason == "API family suggestion") {
		matches[0].Exact = true
		matches[0].Confidence = "confirmed"
		matches[0].Reason = "unique API family/version"
	}
	sort.Slice(matches, func(i, j int) bool { return matchRank(matches[i]) > matchRank(matches[j]) })
	if len(matches) > 20 {
		return matches[:20]
	}
	return matches
}

func (p *OAKProvider) Fetch(ctx context.Context, entry Entry, state State) (Document, error) {
	data, headers, status, err := p.fetchHTTP(ctx, entry.Reference, state)
	if err != nil {
		return Document{}, err
	}
	if status == http.StatusNotModified {
		return Document{ETag: state.ETag, SourceSHA: state.SourceCommit}, nil
	}
	var parsed oakDocument
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Document{}, fmt.Errorf("parse OAK API document: %w", err)
	}
	if len(parsed.APIs) == 0 {
		return Document{}, fmt.Errorf("catalog entry contains no APIs")
	}
	selected, err := selectOAKAPI(parsed, entry.MatchHost)
	if err != nil {
		return Document{}, err
	}
	api := API{ID: selected.AID, Name: selected.Name, Description: selected.Description, Version: selected.Version}
	if api.ID == "" {
		api.ID = parsed.AID
	}
	if api.Name == "" {
		api.Name = parsed.Name
	}
	if api.Description == "" {
		api.Description = parsed.Description
	}
	if selected.BaseURL != "" {
		sanitized, err := discovery.SanitizeURL(selected.BaseURL)
		if err == nil {
			api.BaseURL = sanitized
			api.Host = discovery.URLHost(sanitized)
		}
	}
	document := Document{API: api, ETag: headers.Get("ETag")}
	for _, property := range selected.Properties {
		switch strings.ToLower(property.Type) {
		case "openapi":
			api.OpenAPIReference = property.URL
			spec, _, _, err := p.fetchHTTP(ctx, property.URL, State{})
			if err == nil {
				document.OpenAPI, _ = parseStructured(spec)
				deriveAPIEndpoint(&api, document.OpenAPI)
			}
		case "arazzo":
			api.ArazzoReferences = append(api.ArazzoReferences, property.URL)
			workflow, _, _, err := p.fetchHTTP(ctx, property.URL, State{})
			if err == nil {
				if parsedWorkflow, parseErr := parseStructured(workflow); parseErr == nil {
					document.Arazzo = append(document.Arazzo, parsedWorkflow)
				}
			}
		}
	}
	document.API = api
	return document, nil
}

func (p *OAKProvider) fetchHTTP(ctx context.Context, raw string, state State) ([]byte, http.Header, int, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || discovery.IsCloudMetadataHost(u.Hostname()) {
		return nil, nil, 0, fmt.Errorf("catalog URL must be credential-free HTTPS")
	}
	client := p.Client
	if client == nil {
		transport := &http.Transport{DialContext: restrictedCatalogDialer()}
		client = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" || discovery.IsCloudMetadataHost(request.URL.Hostname()) {
				return fmt.Errorf("catalog redirect is not a credential-free public HTTPS URL")
			}
			return nil
		}}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, 0, err
	}
	request.Header.Set("Accept", "application/json, application/yaml, text/yaml")
	request.Header.Set("User-Agent", "barrikade-lens-hub/catalog")
	if state.ETag != "" {
		request.Header.Set("If-None-Match", state.ETag)
	}
	if !state.Modified.IsZero() {
		request.Header.Set("If-Modified-Since", state.Modified.Format(http.TimeFormat))
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return nil, response.Header, response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.Header, response.StatusCode, fmt.Errorf("catalog returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, response.Header, response.StatusCode, err
	}
	if len(data) >= 16<<20 {
		return nil, response.Header, response.StatusCode, fmt.Errorf("catalog document exceeds size limit")
	}
	return data, response.Header, response.StatusCode, nil
}

type FileProvider struct {
	ProviderID   string
	Name         string
	ManifestPath string
	Root         string
}

type DirectoryProvider struct {
	ProviderID string
	Name       string
	Root       string
	Manifest   string
}

func (p *DirectoryProvider) delegate() *FileProvider {
	manifest := p.Manifest
	if manifest == "" {
		manifest = "apis.json"
	}
	return &FileProvider{ProviderID: p.ProviderID, Name: p.Name, ManifestPath: filepath.Join(p.Root, filepath.FromSlash(manifest)), Root: p.Root}
}
func (p *DirectoryProvider) ID() string          { return p.delegate().ID() }
func (p *DirectoryProvider) DisplayName() string { return p.delegate().DisplayName() }
func (p *DirectoryProvider) Refresh(ctx context.Context, state State) (Index, error) {
	return p.delegate().Refresh(ctx, state)
}
func (p *DirectoryProvider) Match(index Index, host, provider string) []Match {
	return p.delegate().Match(index, host, provider)
}
func (p *DirectoryProvider) Fetch(ctx context.Context, entry Entry, state State) (Document, error) {
	return p.delegate().Fetch(ctx, entry, state)
}

// GitProvider reads the compact manifest over HTTPS and obtains commit
// provenance with git ls-remote. It never clones the catalog repository.
type GitProvider struct {
	OAKProvider
	RepositoryURL string
	Ref           string
}

func (p *GitProvider) Refresh(ctx context.Context, state State) (Index, error) {
	index, err := p.OAKProvider.Refresh(ctx, state)
	if err != nil {
		return Index{}, err
	}
	ref := p.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if p.RepositoryURL != "" {
		output, gitErr := exec.CommandContext(ctx, "git", "ls-remote", "--exit-code", p.RepositoryURL, ref).Output()
		if gitErr == nil {
			fields := strings.Fields(string(output))
			if len(fields) > 0 {
				index.SourceCommit = fields[0]
			}
		}
	}
	return index, nil
}

func (p *FileProvider) ID() string {
	if p.ProviderID != "" {
		return p.ProviderID
	}
	return "file-catalog"
}
func (p *FileProvider) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return "Local API Catalog"
}
func (p *FileProvider) Refresh(ctx context.Context, state State) (Index, error) {
	data, err := os.ReadFile(p.ManifestPath)
	if err != nil {
		return Index{}, err
	}
	var parsed oakIndex
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Index{}, err
	}
	index := Index{Name: parsed.Name}
	for _, include := range parsed.Include {
		providerID := providerFromReference(include.URL, include.Name)
		reference := include.URL
		if parsedURL, parseErr := url.Parse(reference); parseErr == nil && parsedURL.Host != "" {
			const marker = "/apis/openapi/"
			if index := strings.Index(parsedURL.Path, marker); index >= 0 {
				reference = filepath.Join(filepath.Dir(p.ManifestPath), filepath.FromSlash(parsedURL.Path[index+len(marker):]))
			}
		} else if !filepath.IsAbs(reference) {
			reference = filepath.Join(filepath.Dir(p.ManifestPath), filepath.FromSlash(reference))
		}
		index.Entries = append(index.Entries, Entry{ID: entryID(reference), Name: include.Name, ProviderID: providerID, Reference: reference})
		index.Entries[len(index.Entries)-1].APIFamily, index.Entries[len(index.Entries)-1].Version = entryDescriptor(include.Name)
	}
	return index, nil
}
func (p *FileProvider) Match(index Index, host, provider string) []Match {
	return (&OAKProvider{}).Match(index, host, provider)
}
func (p *FileProvider) Fetch(ctx context.Context, entry Entry, state State) (Document, error) {
	data, err := os.ReadFile(entry.Reference)
	if err != nil {
		return Document{}, err
	}
	var parsed oakDocument
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Document{}, err
	}
	if len(parsed.APIs) == 0 {
		return Document{}, fmt.Errorf("catalog entry contains no APIs")
	}
	item, err := selectOAKAPI(parsed, entry.MatchHost)
	if err != nil {
		return Document{}, err
	}
	api := API{ID: item.AID, Name: item.Name, Description: item.Description, Version: item.Version}
	if sanitized, err := discovery.SanitizeURL(item.BaseURL); err == nil {
		api.BaseURL = sanitized
		api.Host = discovery.URLHost(sanitized)
	}
	document := Document{API: api}
	for _, property := range item.Properties {
		path := property.URL
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(entry.Reference), filepath.FromSlash(path))
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsedBody, _ := parseStructured(body)
		if strings.EqualFold(property.Type, "OpenAPI") {
			api.OpenAPIReference = property.URL
			document.OpenAPI = parsedBody
			deriveAPIEndpoint(&api, document.OpenAPI)
		} else if strings.EqualFold(property.Type, "Arazzo") {
			api.ArazzoReferences = append(api.ArazzoReferences, property.URL)
			document.Arazzo = append(document.Arazzo, parsedBody)
		}
	}
	document.API = api
	return document, nil
}

func selectOAKAPI(document oakDocument, matchHost string) (oakAPI, error) {
	if len(document.APIs) == 0 {
		return oakAPI{}, fmt.Errorf("catalog entry contains no APIs")
	}
	matchHost = strings.ToLower(strings.TrimSuffix(matchHost, "."))
	if matchHost == "" {
		return document.APIs[0], nil
	}
	for _, candidate := range document.APIs {
		sanitized, err := discovery.SanitizeURL(candidate.BaseURL)
		if err != nil {
			continue
		}
		host := strings.ToLower(discovery.URLHost(sanitized))
		if host == matchHost {
			return candidate, nil
		}
	}
	return oakAPI{}, fmt.Errorf("catalog umbrella entry contains no API for discovered host")
}

func deriveAPIEndpoint(api *API, document map[string]any) {
	if api.BaseURL != "" || len(document) == 0 {
		return
	}
	servers, _ := document["servers"].([]any)
	for _, raw := range servers {
		server, _ := raw.(map[string]any)
		value, _ := server["url"].(string)
		if strings.Contains(value, "{") {
			continue
		}
		sanitized, err := discovery.SanitizeURL(value)
		if err == nil {
			api.BaseURL = sanitized
			api.Host = discovery.URLHost(sanitized)
			return
		}
	}
}

func restrictedCatalogDialer() func(context.Context, string, string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || discovery.IsCloudMetadataHost(host) {
			return nil, fmt.Errorf("catalog target is blocked")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, candidate := range addresses {
			ip := candidate.IP
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			last = dialErr
		}
		if last == nil {
			last = fmt.Errorf("catalog host resolves only to blocked addresses")
		}
		return nil, last
	}
}

func parseStructured(data []byte) (map[string]any, error) {
	var value map[string]any
	if json.Unmarshal(data, &value) == nil {
		return value, nil
	}
	if err := yaml.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}
func providerFromReference(reference, name string) string {
	if parsed, err := url.Parse(reference); err == nil {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i, part := range parts {
			if part == "openapi" && i+1 < len(parts) {
				return strings.ToLower(parts[i+1])
			}
		}
	}
	if index := strings.Index(name, ":"); index > 0 {
		return strings.ToLower(strings.TrimSpace(name[:index]))
	}
	return ""
}
func entryDescriptor(name string) (string, string) {
	descriptor := strings.TrimSpace(strings.SplitN(name, " - ", 2)[0])
	if colon := strings.Index(descriptor, ":"); colon >= 0 {
		descriptor = descriptor[colon+1:]
	}
	family, version := descriptor, ""
	if at := strings.LastIndex(descriptor, "@"); at >= 0 {
		family, version = descriptor[:at], descriptor[at+1:]
	}
	return strings.TrimSpace(family), strings.TrimSpace(version)
}
func entryID(reference string) string      { return discovery.ContentHash([]byte(reference)) }
func parseHTTPTime(value string) time.Time { parsed, _ := http.ParseTime(value); return parsed }
func matchRank(match Match) int {
	if match.Exact {
		return 3
	}
	if match.Confidence == "likely" {
		return 2
	}
	return 1
}
