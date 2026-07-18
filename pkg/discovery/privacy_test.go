package discovery

import (
	"net/url"
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	got, err := SanitizeURL("https://user:pass@example.com/v1?api_key=secret#fragment")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/v1" {
		t.Fatalf("unexpected sanitized URL: %s", got)
	}
}

func FuzzSanitizeURLNeverRetainsCredentialsOrParameters(f *testing.F) {
	f.Add("private-value")
	f.Add("spaces and symbols / ? #")
	f.Fuzz(func(t *testing.T, secret string) {
		if secret == "" {
			return
		}
		raw := "https://user:" + url.PathEscape(secret) + "@api.example.test/v1?api_key=" + url.QueryEscape(secret) + "#" + url.PathEscape(secret)
		sanitized, err := SanitizeURL(raw)
		if err != nil {
			return
		}
		parsed, err := url.Parse(sanitized)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatalf("sanitized URL retained private material: %q", sanitized)
		}
	})
}

func TestSafeLocatorHashesAbsolutePaths(t *testing.T) {
	first := SafeLocator("org-salt", "/Users/alice/.config/tool/config.json", "")
	second := SafeLocator("org-salt", "/Users/alice/.config/tool/config.json", "")
	if first != second || first == "/Users/alice/.config/tool/config.json" {
		t.Fatalf("absolute path was not deterministically hidden: %q", first)
	}
	if got := SafeLocator("org-salt", "/repo", "/repo/agents/example.yaml"); got != "agents/example.yaml" {
		t.Fatalf("repository-relative path changed: %q", got)
	}
}

func TestCloudMetadataHosts(t *testing.T) {
	for _, host := range []string{"169.254.169.254", "metadata.google.internal", "100.100.100.200"} {
		if !IsCloudMetadataHost(host) {
			t.Errorf("expected %q to be blocked", host)
		}
	}
}
