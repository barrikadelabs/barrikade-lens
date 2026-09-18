package githubapp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRepositoriesPaginatesInstallationAccess(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if got := r.Header.Get("Authorization"); got != "Bearer installation-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"repositories":[`))
			for i := 0; i < 100; i++ {
				if i > 0 {
					_, _ = w.Write([]byte(","))
				}
				_, _ = w.Write([]byte(`{"name":"repo-` + strconv.Itoa(i) + `","default_branch":"main","owner":{"login":"acme"}}`))
			}
			_, _ = w.Write([]byte(`]}`))
			return
		}
		_, _ = w.Write([]byte(`{"repositories":[{"name":"last","default_branch":"main","owner":{"login":"acme"}}]}`))
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), APIBase: server.URL, Version: "test"}
	repositories, err := client.Repositories(t.Context(), "installation-token")
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 101 || pages != 2 || repositories[100].Name != "last" {
		t.Fatalf("expected 101 repositories over two pages, got %d over %d pages", len(repositories), pages)
	}
}

func TestExtractTarGzDropsRootAndRejectsTraversal(t *testing.T) {
	archive := tarball(t, map[string]string{"repo-sha/package.json": "{}", "repo-sha/agents/a.yaml": "name: a"})
	destination := t.TempDir()
	if err := ExtractTarGz(bytes.NewReader(archive), destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "agents", "a.yaml")); err != nil {
		t.Fatal(err)
	}
	bad := tarball(t, map[string]string{"repo-sha/../../escape": "bad"})
	if err := ExtractTarGz(bytes.NewReader(bad), t.TempDir()); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	writer := tar.NewWriter(gzipWriter)
	for name, content := range files {
		data := []byte(content)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
