package ard

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestProviderUsesConditionalRequestsAndNeverFetchesArtifacts(t *testing.T) {
	requests := 0
	provider := &Provider{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://catalog.example.test/ai-catalog.json" {
			t.Fatalf("provider called a referenced artifact: %s", request.URL)
		}
		if request.Header.Get("If-None-Match") != `"v1"` || request.Header.Get("If-Modified-Since") == "" {
			t.Fatal("conditional request state was not forwarded")
		}
		body := `{"specVersion":"1.0","entries":[{"identifier":"urn:air:example.test:agent:one","displayName":"One","type":"application/a2a-agent-card+json","url":"https://agent.example.test/card.json"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Etag": {`"v2"`}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}}
	document, err := provider.Fetch(context.Background(), "https://catalog.example.test/ai-catalog.json", FetchState{
		ETag: `"v1"`, LastModified: "Tue, 28 Jul 2026 00:00:00 GMT",
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(document.Result.Catalog.Entries) != 1 || document.ETag != `"v2"` {
		t.Fatalf("unexpected fetch result: requests=%d document=%#v", requests, document)
	}
}

func TestProviderRejectsUnsafeCatalogTargets(t *testing.T) {
	for _, target := range []string{
		"http://example.test/catalog.json",
		"https://user:password@example.test/catalog.json",
		"https://example.test/catalog.json?token=secret",
		"https://example.test/catalog.json#fragment",
		"https://169.254.169.254/latest/meta-data",
	} {
		if _, err := ValidateCatalogURL(target); err == nil {
			t.Fatalf("unsafe catalog URL was accepted: %s", target)
		}
	}
	provider := &Provider{}
	for _, address := range []string{"127.0.0.1:443", "169.254.169.254:443"} {
		if _, err := provider.restrictedDialer()(context.Background(), "tcp", address); err == nil {
			t.Fatalf("restricted address was accepted: %s", address)
		}
	}
}

func TestCatalogURLNormalizationProducesStableSeeds(t *testing.T) {
	left, err := ValidateCatalogURL("HTTPS://Catalog.Example.Test.:443")
	if err != nil {
		t.Fatal(err)
	}
	right, err := ValidateCatalogURL("https://catalog.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if left != right || left != "https://catalog.example.test/" {
		t.Fatalf("equivalent catalog URLs did not normalize identically: %q %q", left, right)
	}
}
