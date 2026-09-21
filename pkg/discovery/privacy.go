package discovery

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var sensitiveKey = regexp.MustCompile(`(?i)(^|[_-])(secret|token|password|passwd|credential|private[_-]?key|api[_-]?key|authorization|cookie|prompt|content|command[_-]?(line|args?))($|[_-])`)

func IsSensitiveKey(key string) bool {
	key = strings.TrimSpace(key)
	if strings.HasSuffix(strings.ToLower(key), "_present") || strings.HasSuffix(strings.ToLower(key), "_count") {
		return false
	}
	return sensitiveKey.MatchString(key)
}

func SanitizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String(), nil
}

// NormalizeRepositoryURL returns a credential-free repository identity shared
// by repository and deployment collectors. Display names are intentionally not
// part of this correlation key.
func NormalizeRepositoryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(strings.TrimPrefix(raw, "git@"), ":", 2)
		if len(parts) == 2 {
			raw = "https://" + parts[0] + "/" + parts[1]
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimSuffix(u.Path, ".git")
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ssh" {
		return ""
	}
	return u.String()
}

func URLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	return strings.TrimSuffix(host, ".")
}

func IsCloudMetadataHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "metadata.google.internal" || host == "metadata.azure.internal" || host == "100.100.100.200" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(net.ParseIP("169.254.169.254")) || ip.IsLinkLocalUnicast())
}

func SafeLocator(orgID, root, path string) string {
	if root != "" {
		if relative, err := filepath.Rel(root, path); err == nil && relative != "." && !strings.HasPrefix(relative, "..") {
			return filepath.ToSlash(relative)
		}
	}
	return HashLocator(orgID, filepath.Clean(path))
}

func SafeEnvKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
