package githubapp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Client struct {
	AppID      string
	PrivateKey *rsa.PrivateKey
	HTTP       *http.Client
	APIBase    string
	Version    string
}
type Repository struct {
	Owner         string
	Name          string
	DefaultBranch string
}

func New(appID string, keyPEM []byte, version string) (*Client, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("GitHub App private key is not PEM")
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = parsed
	} else {
		value, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes)
		if parseErr != nil {
			return nil, fmt.Errorf("parse GitHub App private key: %w", parseErr)
		}
		var ok bool
		key, ok = value.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("GitHub App private key is not RSA")
		}
	}
	return &Client{AppID: appID, PrivateKey: key, HTTP: &http.Client{Timeout: 60 * time.Second}, APIBase: "https://api.github.com", Version: version}, nil
}

func (c *Client) InstallationToken(ctx context.Context, installationID int64) (string, time.Time, error) {
	appToken, err := c.appJWT()
	if err != nil {
		return "", time.Time{}, err
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimSuffix(c.APIBase, "/"), installationID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	c.headers(request, appToken)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return "", time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("GitHub installation token returned HTTP %d", response.StatusCode)
	}
	var value struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value); err != nil {
		return "", time.Time{}, err
	}
	if value.Token == "" {
		return "", time.Time{}, fmt.Errorf("GitHub returned an empty installation token")
	}
	return value.Token, value.ExpiresAt, nil
}

func (c *Client) DownloadRepository(ctx context.Context, token, owner, repository, commit, destination string) error {
	if !safeSegment(owner) || !safeSegment(repository) || !validCommit(commit) {
		return fmt.Errorf("invalid repository coordinates")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", strings.TrimSuffix(c.APIBase, "/"), url.PathEscape(owner), url.PathEscape(repository), commit)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.headers(request, token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub repository archive returned HTTP %d", response.StatusCode)
	}
	return ExtractTarGz(io.LimitReader(response.Body, 512<<20), destination)
}

func (c *Client) Repositories(ctx context.Context, token string) ([]Repository, error) {
	result := []Repository{}
	for page := 1; ; page++ {
		endpoint := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", strings.TrimSuffix(c.APIBase, "/"), page)
		var response struct {
			Repositories []struct {
				Name          string `json:"name"`
				DefaultBranch string `json:"default_branch"`
				Owner         struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		if err := c.getJSON(ctx, endpoint, token, &response); err != nil {
			return nil, err
		}
		for _, repository := range response.Repositories {
			result = append(result, Repository{Owner: repository.Owner.Login, Name: repository.Name, DefaultBranch: repository.DefaultBranch})
		}
		if len(response.Repositories) < 100 {
			break
		}
	}
	return result, nil
}

func (c *Client) HeadCommit(ctx context.Context, token string, repository Repository) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/commits/%s", strings.TrimSuffix(c.APIBase, "/"), url.PathEscape(repository.Owner), url.PathEscape(repository.Name), url.PathEscape(repository.DefaultBranch))
	var response struct {
		SHA string `json:"sha"`
	}
	if err := c.getJSON(ctx, endpoint, token, &response); err != nil {
		return "", err
	}
	if !validCommit(response.SHA) {
		return "", fmt.Errorf("GitHub returned an invalid commit SHA")
	}
	return response.SHA, nil
}
func (c *Client) getJSON(ctx context.Context, endpoint, token string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.headers(request, token)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target)
}

func (c *Client) appJWT() (string, error) {
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{Issuer: c.AppID, IssuedAt: jwt.NewNumericDate(now.Add(-30 * time.Second)), ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute))}
	return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(c.PrivateKey)
}
func (c *Client) headers(request *http.Request, token string) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "barrikade-lens/"+c.Version)
}

func ExtractTarGz(reader io.Reader, destination string) error {
	gzipReader, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	files := 0
	var total int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		files++
		if files > 100000 {
			return fmt.Errorf("repository archive contains too many files")
		}
		parts := strings.Split(filepath.ToSlash(header.Name), "/")
		if len(parts) < 2 {
			continue
		}
		relative := filepath.Clean(filepath.Join(parts[1:]...))
		if relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("repository archive contains an unsafe path")
		}
		target := filepath.Join(destination, relative)
		if !strings.HasPrefix(target, filepath.Clean(destination)+string(filepath.Separator)) {
			return fmt.Errorf("repository archive escaped destination")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if total > 256<<20 {
				return fmt.Errorf("repository archive exceeds extracted size limit")
			}
			if header.Size > 16<<20 {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, archive, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			continue
		}
	}
	return nil
}

func safeSegment(value string) bool {
	return value != "" && !strings.ContainsAny(value, "/\\") && value != "." && value != ".."
}
func validCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}
