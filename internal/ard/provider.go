package ard

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barrikadelabs/barrikade-lens/pkg/discovery"
	"golang.org/x/net/publicsuffix"
)

type FetchState struct {
	ETag         string
	LastModified string
}

type Document struct {
	URL          string
	Result       Result
	ETag         string
	LastModified string
	NotModified  bool
	ContentHash  string
}

type ResourceDeclarationProvider interface {
	Format() string
	Fetch(context.Context, string, FetchState) (Document, error)
}

type Provider struct {
	Client              *http.Client
	AllowedPrivateHosts map[string]bool
}

func (p *Provider) Format() string { return Format }

func (p *Provider) Fetch(ctx context.Context, rawURL string, state FetchState) (Document, error) {
	sanitized, err := credentialFreeHTTPS(rawURL)
	if err != nil {
		return Document{}, err
	}
	client := p.Client
	if client == nil {
		transport := &http.Transport{DialContext: p.restrictedDialer()}
		client = &http.Client{
			Transport: transport, Timeout: 20 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many catalog redirects")
				}
				if _, err := credentialFreeHTTPS(request.URL.String()); err != nil {
					return err
				}
				if registrableSite(request.URL.Hostname()) != registrableSite(via[0].URL.Hostname()) {
					return fmt.Errorf("catalog redirect left the configured site")
				}
				return nil
			},
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sanitized, nil)
	if err != nil {
		return Document{}, err
	}
	request.Header.Set("Accept", "application/ai-catalog+json, application/json")
	request.Header.Set("User-Agent", "barrikade-lens-hub/ard")
	if state.ETag != "" {
		request.Header.Set("If-None-Match", state.ETag)
	}
	if state.LastModified != "" {
		request.Header.Set("If-Modified-Since", state.LastModified)
	}
	response, err := client.Do(request)
	if err != nil {
		return Document{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return Document{URL: sanitized, ETag: state.ETag, LastModified: state.LastModified, NotModified: true}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Document{}, fmt.Errorf("ARD catalog returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxCatalogBytes+1))
	if err != nil {
		return Document{}, err
	}
	if len(body) > MaxCatalogBytes {
		return Document{}, fmt.Errorf("ARD catalog exceeds %d bytes", MaxCatalogBytes)
	}
	result, err := Parse(body)
	if err != nil {
		return Document{}, err
	}
	return Document{
		URL: sanitized, Result: result, ETag: response.Header.Get("ETag"),
		LastModified: response.Header.Get("Last-Modified"), ContentHash: discovery.ContentHash(body),
	}, nil
}

func (p *Provider) restrictedDialer() func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		normalized := strings.ToLower(strings.TrimSuffix(host, "."))
		allowedPrivate := p.AllowedPrivateHosts[normalized]
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, item := range addresses {
			if discovery.IsCloudMetadataHost(item.IP.String()) {
				return nil, fmt.Errorf("catalog target resolves to cloud metadata")
			}
			if !allowedPrivate && (item.IP.IsLoopback() || item.IP.IsPrivate() || item.IP.IsLinkLocalUnicast() || item.IP.IsUnspecified()) {
				return nil, fmt.Errorf("catalog target resolves to a restricted address")
			}
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("catalog target has no usable address")
		}
		// Dial the address that was just validated. Dialing the hostname again
		// would introduce a second DNS lookup and reopen a rebinding window.
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
}

func registrableSite(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return site
}

func SameSite(left, right string) bool {
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	return leftErr == nil && rightErr == nil && registrableSite(leftURL.Hostname()) == registrableSite(rightURL.Hostname())
}
